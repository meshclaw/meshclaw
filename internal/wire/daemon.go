package wire

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/common"
)

// DetectLanIP detects local LAN IP (excluding VPN IPs)
func DetectLanIP() string {
	// Get VPN subnet from config to exclude VPN IPs
	vpnSubnet := ""
	if cfg, err := LoadConfig(); err == nil {
		vpnSubnet = cfg.Subnet()
	}

	if runtime.GOOS == "darwin" {
		out, _, code := common.RunShell("ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null")
		if code == 0 {
			ip := strings.TrimSpace(out)
			// Accept 192.168.x.x or 10.x.x.x (excluding VPN subnet)
			if strings.HasPrefix(ip, "192.168.") {
				return ip
			}
			if strings.HasPrefix(ip, "10.") {
				// Exclude VPN subnet
				if vpnSubnet != "" && strings.HasPrefix(ip, vpnSubnet+".") {
					return ""
				}
				return ip
			}
		}
	} else {
		out, _, code := common.RunShell("hostname -I 2>/dev/null | awk '{print $1}'")
		if code == 0 {
			ip := strings.TrimSpace(out)
			// Exclude VPN subnet on Linux too
			if vpnSubnet != "" && strings.HasPrefix(ip, vpnSubnet+".") {
				// Try second IP
				out2, _, code2 := common.RunShell("hostname -I 2>/dev/null | awk '{print $2}'")
				if code2 == 0 {
					return strings.TrimSpace(out2)
				}
				return ""
			}
			return ip
		}
	}
	return ""
}

// SyncPeers syncs peers from coordinator to WireGuard
func SyncPeers(iface, server, myNodeID string) int {
	peers, err := GetPeers(server)
	if err != nil {
		return 0
	}

	// Get my public IP from peers list
	var myPubIP string
	for _, p := range peers {
		if p.NodeID == myNodeID {
			myPubIP = p.PublicIP
			break
		}
	}

	for _, p := range peers {
		if p.NodeID == myNodeID {
			continue
		}
		if p.WgPublicKey == "" || p.VpnIP == "" {
			continue
		}

		// Determine endpoint
		var endpoint string
		lanIP := p.LanIP
		useLan := lanIP != "" && !strings.HasPrefix(lanIP, "127.") && myPubIP != "" && p.PublicIP == myPubIP

		port := p.Port
		if port == 0 {
			port = DefaultPort
		}
		natPort := p.NatPort
		if natPort == 0 {
			natPort = port
		}

		if useLan {
			endpoint = strings.TrimSpace(lanIP) + ":" + strconv.Itoa(port)
		} else if p.PublicIP != "" {
			endpoint = strings.TrimSpace(p.PublicIP) + ":" + strconv.Itoa(natPort)
		}

		AddPeer(iface, p.WgPublicKey, p.VpnIP, endpoint)
	}

	return len(peers)
}

// RunDaemonForeground runs the daemon loop in foreground
func RunDaemonForeground(iface, server string, cfg *Config, pubKey string, natPort int, isRelay bool) {
	// If this is relay node, enable IP forwarding
	if isRelay {
		EnableIPForwarding()
		SetupRelayRouting(iface, cfg.Subnet())
	}

	// Load cached peers
	cachedPeers, _ := LoadPeersCache()

	for {
		lanIP := DetectLanIP()

		// Register with all known coordinators (distributed)
		RegisterWithAll(server, cachedPeers, cfg, pubKey, lanIP, natPort)

		// Get peers from all coordinators and merge
		allPeers := GetPeersFromAll(server, cachedPeers)

		// Update cache
		if len(allPeers) > 0 {
			cachedPeers = allPeers
			SavePeersCache(allPeers)
		}

		// Sync to WireGuard
		SyncPeersToWireGuard(iface, allPeers, cfg.NodeID)

		time.Sleep(RefreshInterval * time.Second)
	}
}

// SyncPeersToWireGuard syncs peer list to WireGuard interface
func SyncPeersToWireGuard(iface string, peers []Peer, myNodeID string) int {
	// Get my public IP from peers list
	var myPubIP string
	for _, p := range peers {
		if p.NodeID == myNodeID {
			myPubIP = p.PublicIP
			break
		}
	}

	count := 0
	for _, p := range peers {
		if p.NodeID == myNodeID {
			continue
		}
		if p.WgPublicKey == "" || p.VpnIP == "" {
			continue
		}

		// Determine endpoint
		var endpoint string
		lanIP := p.LanIP
		useLan := lanIP != "" && !strings.HasPrefix(lanIP, "127.") && myPubIP != "" && p.PublicIP == myPubIP

		port := p.Port
		if port == 0 {
			port = DefaultPort
		}
		natPort := p.NatPort
		if natPort == 0 {
			natPort = port
		}

		if useLan {
			endpoint = strings.TrimSpace(lanIP) + ":" + strconv.Itoa(port)
		} else if p.PublicIP != "" {
			endpoint = strings.TrimSpace(p.PublicIP) + ":" + strconv.Itoa(natPort)
		}

		AddPeer(iface, p.WgPublicKey, p.VpnIP, endpoint)
		count++
	}

	return count
}

// SpawnDaemon spawns daemon as background process
func SpawnDaemon() error {
	return SpawnDaemonForNetwork("default")
}

// SpawnDaemonForNetwork spawns daemon for a specific network
func SpawnDaemonForNetwork(network string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "up", "--foreground", "--network", network)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// SyncPeersForNetwork syncs peers from coordinator for a specific network
func SyncPeersForNetwork(iface, server, myNodeID, network string) int {
	peers, err := GetPeersForNetwork(server, network)
	if err != nil {
		return 0
	}
	return SyncPeersToWireGuard(iface, peers, myNodeID)
}

// RunDaemonForNetwork runs the daemon loop for a specific network
func RunDaemonForNetwork(iface, server string, cfg *Config, nc *NetworkConfig, pubKey string, natPort int, isRelay bool) {
	// If this is relay node, enable IP forwarding
	if isRelay {
		EnableIPForwarding()
		SetupRelayRouting(iface, nc.VpnSubnet)
	}

	network := nc.Network

	for {
		lanIP := DetectLanIP()

		// Create temp config for registration
		regCfg := &Config{
			ServerURL:  cfg.ServerURL,
			NodeName:   cfg.NodeName,
			NodeID:     cfg.NodeID,
			Network:    network,
			AccessKey:  nc.AccessKey,
			VpnIP:      nc.VpnIP,
			ListenPort: nc.ListenPort,
			VpnSubnet:  nc.VpnSubnet,
		}

		// Register with coordinator
		Register(server, regCfg, pubKey, lanIP, natPort)

		// Sync peers for this network
		SyncPeersForNetwork(iface, server, cfg.NodeID, network)

		time.Sleep(RefreshInterval * time.Second)
	}
}
