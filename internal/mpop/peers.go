package mpop

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/meshclaw/meshclaw/internal/wire"
)

const (
	WireAPIPort = 8790
	CacheTTL    = 300 // 5 minutes
)

// PeerStats contains system stats reported by workers
type PeerStats struct {
	Load      string  `json:"load,omitempty"`
	LoadValue float64 `json:"load_value,omitempty"`
	MemPct    int     `json:"mem_pct,omitempty"`
	DiskPct   int     `json:"disk_pct,omitempty"`
	Uptime    string  `json:"uptime,omitempty"`
	UpdatedAt int64   `json:"updated_at,omitempty"`

	// Extended stats from worker
	Hostname    string `json:"hostname,omitempty"`
	OS          string `json:"os,omitempty"`
	Arch        string `json:"arch,omitempty"`
	CPUCores    int    `json:"cpu_cores,omitempty"`
	CPUPct      int    `json:"cpu_pct,omitempty"`
	MemTotal    int64  `json:"mem_total,omitempty"`
	MemUsed     int64  `json:"mem_used,omitempty"`
	SwapTotal   int64  `json:"swap_total,omitempty"`
	SwapUsed    int64  `json:"swap_used,omitempty"`
	DiskTotal   int64  `json:"disk_total,omitempty"`
	DiskUsed    int64  `json:"disk_used,omitempty"`
	NetRX       int64  `json:"net_rx,omitempty"`
	NetTX       int64  `json:"net_tx,omitempty"`
	Procs       int    `json:"procs,omitempty"`
	Connections int    `json:"connections,omitempty"`
	DockerCount int    `json:"docker_count,omitempty"`
	GPUCount    int    `json:"gpu_count,omitempty"`
	GPUMemUsed  int    `json:"gpu_mem_used,omitempty"`
	GPUMemTotal int    `json:"gpu_mem_total,omitempty"`
	GPUUtil     int    `json:"gpu_util,omitempty"`
	IOWait      int    `json:"io_wait,omitempty"`
	TopProcess  string `json:"top_process,omitempty"`
}

// Peer represents a network peer
type Peer struct {
	NodeID      string      `json:"node_id"`
	NodeName    string      `json:"node_name"`
	WgPublicKey string      `json:"wg_public_key"`
	PublicIP    string      `json:"public_ip"`
	LanIP       string      `json:"lan_ip"`
	VpnIP       string      `json:"vpn_ip"`
	Port        int         `json:"port"`
	NatPort     int         `json:"nat_port"`
	LastSeen    interface{} `json:"last_seen"`
	Stats       *PeerStats  `json:"stats,omitempty"`
}

// PeersResponse from wire coordinator
type PeersResponse struct {
	Peers []Peer `json:"peers"`
}

// GetPeerStats fetches stats for a specific peer from coordinator
func GetPeerStats(name string) (*Peer, error) {
	peers, err := GetPeersWithStats()
	if err != nil {
		return nil, err
	}
	for _, p := range peers {
		if p.NodeName == name {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("peer not found: %s", name)
}

// GetPeersWithStats fetches peers with stats from coordinator
func GetPeersWithStats() ([]Peer, error) {
	// Try wire config first
	wireCfg, err := wire.LoadConfig()
	if err == nil && wireCfg.ServerURL != "" {
		if peers, err := fetchPeersWithStatsFromURL(wireCfg.ServerURL + "/peers"); err == nil {
			return peers, nil
		}
	}

	// Fallback to mpop config relays
	relays := GetRelays()
	if len(relays) == 0 {
		relays = getWireRelays()
	}

	for _, relayIP := range relays {
		url := fmt.Sprintf("http://%s:%d/peers", relayIP, WireAPIPort)
		if peers, err := fetchPeersWithStatsFromURL(url); err == nil {
			return peers, nil
		}
	}

	return nil, fmt.Errorf("no coordinator available")
}

func fetchPeersWithStatsFromURL(url string) ([]Peer, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result PeersResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Peers, nil
}

var (
	serversCache     map[string]string
	serversCacheTime time.Time
	serversMutex     sync.RWMutex
)

// DiscoverPeers discovers VPN peers from wire or tailscale
func DiscoverPeers(force bool) map[string]string {
	serversMutex.Lock()
	defer serversMutex.Unlock()

	now := time.Now()
	if !force && serversCache != nil && now.Sub(serversCacheTime) < CacheTTL*time.Second {
		return serversCache
	}

	// Try wire first, then tailscale
	servers := fetchWirePeers()
	if len(servers) == 0 {
		servers = fetchTailscalePeers()
	}
	if servers == nil {
		servers = make(map[string]string)
	}

	// Merge config servers
	cfg, _ := LoadConfig()
	if cfg != nil {
		for name, srv := range cfg.Servers {
			if _, exists := servers[name]; !exists && srv.IP != "" {
				servers[name] = srv.IP
			}
		}
	}

	serversCache = servers
	serversCacheTime = now
	return servers
}

// GetServers returns discovered servers (lazy init)
func GetServers() map[string]string {
	serversMutex.RLock()
	if serversCache != nil {
		defer serversMutex.RUnlock()
		return serversCache
	}
	serversMutex.RUnlock()
	return DiscoverPeers(false)
}

func fetchWirePeers() map[string]string {
	// Try wire config first (most reliable)
	wireCfg, err := wire.LoadConfig()
	if err == nil && wireCfg.ServerURL != "" {
		if servers := fetchPeersFromURL(wireCfg.ServerURL + "/peers"); servers != nil {
			return servers
		}
	}

	// Fallback to mpop config relays
	relays := GetRelays()
	if len(relays) == 0 {
		relays = getWireRelays()
	}

	for _, relayIP := range relays {
		url := fmt.Sprintf("http://%s:%d/peers", relayIP, WireAPIPort)
		if servers := fetchPeersFromURL(url); servers != nil {
			return servers
		}
	}

	return nil
}

func fetchPeersFromURL(url string) map[string]string {
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var result PeersResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil
	}

	servers := make(map[string]string)
	for _, peer := range result.Peers {
		name := peer.NodeName
		if name == "" {
			parts := strings.Split(peer.VpnIP, ".")
			if len(parts) == 4 {
				name = fmt.Sprintf("node-%s", parts[3])
			}
		}
		if name != "" && peer.VpnIP != "" {
			servers[name] = peer.VpnIP
		}
	}
	return servers
}

func getWireRelays() []string {
	// Try loading from wire config
	cfg, err := LoadConfig()
	if err == nil && len(cfg.Relays) > 0 {
		return cfg.Relays
	}
	return nil
}

func fetchTailscalePeers() map[string]string {
	cmd := exec.Command("tailscale", "status", "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var data struct {
		Self struct {
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
		Peer map[string]struct {
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Peer"`
	}

	if err := json.Unmarshal(output, &data); err != nil {
		return nil
	}

	servers := make(map[string]string)

	// Add self
	if len(data.Self.TailscaleIPs) > 0 && data.Self.HostName != "" {
		name := strings.ToLower(data.Self.HostName)
		if len(name) > 8 {
			name = name[:8]
		}
		servers[name] = data.Self.TailscaleIPs[0]
	}

	// Add peers
	for _, peer := range data.Peer {
		if len(peer.TailscaleIPs) > 0 && peer.HostName != "" {
			name := strings.ToLower(peer.HostName)
			if len(name) > 8 {
				name = name[:8]
			}
			servers[name] = peer.TailscaleIPs[0]
		}
	}

	return servers
}

// PingServer checks if a server is reachable
func PingServer(ip string, timeout time.Duration) bool {
	// Try ICMP ping first
	cmd := exec.Command("ping", "-c", "1", "-W", "1", ip)
	if err := cmd.Run(); err == nil {
		return true
	}

	// Fallback to TCP connect on vssh port
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:2222", ip), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// CheckPort checks if a port is open
func CheckPort(ip string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetMyName returns the local machine's name from servers
func GetMyName() string {
	vpnIP := GetVpnIP()
	servers := GetServers()
	for name, ip := range servers {
		if ip == vpnIP {
			return name
		}
	}
	return "local"
}

// GetVpnIP returns the local VPN IP
func GetVpnIP() string {
	cfg, _ := LoadConfig()
	prefix := "10."
	if cfg != nil && cfg.VpnPrefix != "" {
		prefix = cfg.VpnPrefix
	}

	// Try ip addr (Linux)
	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("ip addr show 2>/dev/null | grep -F 'inet %s' | awk '{print $2}' | cut -d/ -f1 | head -1", prefix))
	if output, err := cmd.Output(); err == nil {
		if ip := strings.TrimSpace(string(output)); ip != "" {
			return ip
		}
	}

	// Try ifconfig (macOS)
	cmd = exec.Command("sh", "-c",
		fmt.Sprintf("ifconfig 2>/dev/null | grep -F 'inet %s' | awk '{print $2}' | head -1", prefix))
	if output, err := cmd.Output(); err == nil {
		if ip := strings.TrimSpace(string(output)); ip != "" {
			return ip
		}
	}

	return "N/A"
}
