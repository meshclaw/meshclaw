package net

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Peer represents a discovered network peer
type Peer struct {
	Name     string         `json:"name"`
	IP       string         `json:"ip"`
	Mode     ConnectionMode `json:"mode"`
	Online   bool           `json:"online"`
	LastSeen time.Time      `json:"last_seen"`
}

var (
	peersCache     map[string]*Peer
	peersCacheTime time.Time
	peersCacheMu   sync.RWMutex
	peersCacheTTL  = 5 * time.Minute
)

// DiscoverPeers finds all available peers across all network modes
func DiscoverPeers(force bool) map[string]*Peer {
	peersCacheMu.Lock()
	defer peersCacheMu.Unlock()

	// Return cached if fresh
	if !force && peersCache != nil && time.Since(peersCacheTime) < peersCacheTTL {
		return peersCache
	}

	peers := make(map[string]*Peer)
	caps := Detect()

	// Discover from wire
	if caps["wire"] {
		wirePeers := discoverWirePeers()
		for name, p := range wirePeers {
			peers[name] = p
		}
	}

	// Discover from tailscale
	if caps["tailscale"] {
		tsPeers := discoverTailscalePeers()
		for name, p := range tsPeers {
			// Don't overwrite wire peers
			if _, exists := peers[name]; !exists {
				peers[name] = p
			}
		}
	}

	// Load from config file if exists
	configPeers := loadConfigPeers()
	for name, p := range configPeers {
		if _, exists := peers[name]; !exists {
			peers[name] = p
		}
	}

	// Add local machine
	hostname, _ := os.Hostname()
	if hostname != "" {
		shortName := strings.ToLower(hostname)
		if idx := strings.Index(shortName, "."); idx > 0 {
			shortName = shortName[:idx]
		}
		if len(shortName) > 8 {
			shortName = shortName[:8]
		}
		peers[shortName] = &Peer{
			Name:     shortName,
			IP:       "127.0.0.1",
			Mode:     ModeLocal,
			Online:   true,
			LastSeen: time.Now(),
		}
	}

	peersCache = peers
	peersCacheTime = time.Now()
	return peers
}

func discoverWirePeers() map[string]*Peer {
	peers := make(map[string]*Peer)

	cmd := exec.Command("wire", "peers", "--json")
	output, err := cmd.Output()
	if err != nil {
		return peers
	}

	var data struct {
		Peers []struct {
			NodeName string `json:"node_name"`
			VpnIP    string `json:"vpn_ip"`
			LastSeen interface{} `json:"last_seen"`
		} `json:"peers"`
	}

	if err := json.Unmarshal(output, &data); err != nil {
		// Try line-based parsing
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				name := strings.ToLower(parts[0])
				if len(name) > 8 {
					name = name[:8]
				}
				peers[name] = &Peer{
					Name:   name,
					IP:     parts[1],
					Mode:   ModeWire,
					Online: true,
				}
			}
		}
		return peers
	}

	now := time.Now()
	for _, p := range data.Peers {
		name := strings.ToLower(p.NodeName)
		if len(name) > 8 {
			name = name[:8]
		}
		if name == "" {
			continue
		}

		online := false
		switch v := p.LastSeen.(type) {
		case string:
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				online = now.Sub(t) < 90*time.Second
			}
		case float64:
			t := time.Unix(int64(v), 0)
			online = now.Sub(t) < 90*time.Second
		}

		peers[name] = &Peer{
			Name:   name,
			IP:     p.VpnIP,
			Mode:   ModeWire,
			Online: online,
		}
	}

	return peers
}

func discoverTailscalePeers() map[string]*Peer {
	peers := make(map[string]*Peer)

	cmd := exec.Command("tailscale", "status", "--json")
	output, err := cmd.Output()
	if err != nil {
		return peers
	}

	var data struct {
		Self struct {
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
		} `json:"Self"`
		Peer map[string]struct {
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
			LastSeen     string   `json:"LastSeen"`
		} `json:"Peer"`
	}

	if err := json.Unmarshal(output, &data); err != nil {
		return peers
	}

	// Add self
	if len(data.Self.TailscaleIPs) > 0 && data.Self.HostName != "" {
		name := strings.ToLower(data.Self.HostName)
		if len(name) > 8 {
			name = name[:8]
		}
		peers[name] = &Peer{
			Name:   name,
			IP:     data.Self.TailscaleIPs[0],
			Mode:   ModeTailscale,
			Online: true,
		}
	}

	// Add peers
	for _, p := range data.Peer {
		if len(p.TailscaleIPs) > 0 && p.HostName != "" {
			name := strings.ToLower(p.HostName)
			if len(name) > 8 {
				name = name[:8]
			}
			peers[name] = &Peer{
				Name:   name,
				IP:     p.TailscaleIPs[0],
				Mode:   ModeTailscale,
				Online: p.Online,
			}
		}
	}

	return peers
}

func loadConfigPeers() map[string]*Peer {
	peers := make(map[string]*Peer)

	// Try loading from ~/.mpop/config.json
	home := os.Getenv("HOME")
	configPath := home + "/.mpop/config.json"

	data, err := os.ReadFile(configPath)
	if err != nil {
		return peers
	}

	var config struct {
		Servers map[string]struct {
			IP          string `json:"ip"`
			TailscaleIP string `json:"tailscale_ip"`
			PublicIP    string `json:"public_ip"`
		} `json:"servers"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return peers
	}

	for name, srv := range config.Servers {
		ip := srv.IP
		mode := ModeSSH

		// Determine best IP and mode
		caps := Detect()
		if caps["wire"] && strings.HasPrefix(ip, "10.") {
			mode = ModeWire
		} else if caps["tailscale"] && srv.TailscaleIP != "" {
			ip = srv.TailscaleIP
			mode = ModeTailscale
		} else if srv.PublicIP != "" && ip == "" {
			ip = srv.PublicIP
		}

		if ip != "" {
			shortName := name
			if len(shortName) > 8 {
				shortName = shortName[:8]
			}
			peers[shortName] = &Peer{
				Name: shortName,
				IP:   ip,
				Mode: mode,
			}
		}
	}

	return peers
}

// ListPeers returns a sorted list of discovered peers
func ListPeers() []*Peer {
	peers := DiscoverPeers(false)
	result := make([]*Peer, 0, len(peers))
	for _, p := range peers {
		result = append(result, p)
	}
	return result
}

// GetPeer returns a specific peer by name
func GetPeer(name string) *Peer {
	peers := DiscoverPeers(false)
	// Try exact match first
	if p, ok := peers[name]; ok {
		return p
	}
	// Try prefix match
	name = strings.ToLower(name)
	for peerName, p := range peers {
		if strings.HasPrefix(strings.ToLower(peerName), name) {
			return p
		}
	}
	return nil
}

// RefreshPeers forces a refresh of the peer list
func RefreshPeers() map[string]*Peer {
	return DiscoverPeers(true)
}

// PrintNetworkStatus prints a summary of network status
func PrintNetworkStatus() {
	caps := Detect()
	mode := GetMode()

	fmt.Println()
	fmt.Printf("  Network Status\n")
	fmt.Println()

	// Capabilities
	fmt.Printf("  Mode: %s\n", mode)
	fmt.Printf("  Capabilities:\n")

	if caps["wire"] {
		fmt.Printf("    wire:      [OK] VPN mesh active\n")
	} else {
		fmt.Printf("    wire:      [--] not available\n")
	}

	if caps["tailscale"] {
		fmt.Printf("    tailscale: [OK] connected\n")
	} else {
		fmt.Printf("    tailscale: [--] not available\n")
	}

	if caps["vssh"] {
		fmt.Printf("    vssh:      [OK] secure shell\n")
	} else {
		fmt.Printf("    vssh:      [--] using SSH fallback\n")
	}

	fmt.Printf("    ssh:       [OK] always available\n")
	fmt.Println()

	// Connection fallback chain
	fmt.Printf("  Fallback chain: ")
	chain := []string{}
	if caps["wire"] {
		chain = append(chain, "wire")
	}
	if caps["tailscale"] {
		chain = append(chain, "tailscale")
	}
	chain = append(chain, "ssh")
	fmt.Printf("%s\n", strings.Join(chain, " -> "))
	fmt.Println()
}
