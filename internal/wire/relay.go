package wire

import (
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/common"
)

// RelayConfig holds relay configuration
type RelayConfig struct {
	RelayNodeID   string // Node ID of relay
	RelayVpnIP    string // VPN IP of relay
	RelayPubKey   string // WireGuard public key of relay
	RelayEndpoint string // Public endpoint of relay
}

// GetRelayNode finds the best relay node from peers (any node with public IP)
func GetRelayNode(peers []Peer, myNodeID string) *RelayConfig {
	// Find any node with public IP that can act as relay
	for _, p := range peers {
		if p.NodeID != myNodeID && p.PublicIP != "" && p.WgPublicKey != "" {
			return &RelayConfig{
				RelayNodeID:   p.NodeID,
				RelayVpnIP:    p.VpnIP,
				RelayPubKey:   p.WgPublicKey,
				RelayEndpoint: fmt.Sprintf("%s:%d", p.PublicIP, getPort(p)),
			}
		}
	}
	return nil
}

func getPort(p Peer) int {
	if p.NatPort > 0 {
		return p.NatPort
	}
	if p.Port > 0 {
		return p.Port
	}
	return DefaultPort
}

// IsReachable checks if a peer's VPN IP is reachable
func IsReachable(vpnIP string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("udp", vpnIP+":51820", timeout)
	if err != nil {
		return false
	}
	conn.Close()

	// Also try ICMP ping
	var cmd string
	if runtime.GOOS == "darwin" {
		cmd = fmt.Sprintf("ping -c 1 -W 1 %s >/dev/null 2>&1", vpnIP)
	} else {
		cmd = fmt.Sprintf("ping -c 1 -W 1 %s >/dev/null 2>&1", vpnIP)
	}
	_, _, code := common.RunShell(cmd)
	return code == 0
}

// SyncPeersWithRelay syncs peers, using relay for unreachable peers
func SyncPeersWithRelay(iface, server, myNodeID string, relayVpnIP string) int {
	peers, err := GetPeers(server)
	if err != nil {
		return 0
	}

	// Get relay config
	relay := GetRelayNode(peers, myNodeID)

	// Get my public IP from peers list
	var myPubIP string
	for _, p := range peers {
		if p.NodeID == myNodeID {
			myPubIP = p.PublicIP
			break
		}
	}

	// First, add the relay node with direct connection
	if relay != nil {
		AddPeer(iface, relay.RelayPubKey, relay.RelayVpnIP, relay.RelayEndpoint)
	}

	// Track which peers need relay
	var relayedPeers []string

	for _, p := range peers {
		if p.NodeID == myNodeID {
			continue
		}
		if p.WgPublicKey == "" || p.VpnIP == "" {
			continue
		}

		// Skip relay node (already added)
		if relay != nil && p.NodeID == relay.RelayNodeID {
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
			endpoint = strings.TrimSpace(lanIP) + ":" + fmt.Sprintf("%d", port)
		} else if p.PublicIP != "" {
			endpoint = strings.TrimSpace(p.PublicIP) + ":" + fmt.Sprintf("%d", natPort)
		}

		// Add peer with direct endpoint first
		AddPeer(iface, p.WgPublicKey, p.VpnIP, endpoint)

		// Check if peer is reachable after short delay
		// (Give WireGuard time to establish connection)
		time.Sleep(100 * time.Millisecond)

		if endpoint != "" && !IsReachable(p.VpnIP, 500*time.Millisecond) {
			// Peer not reachable directly, will route through relay
			if relay != nil && relay.RelayVpnIP != p.VpnIP {
				relayedPeers = append(relayedPeers, p.VpnIP)
			}
		}
	}

	// Update relay node's allowed-ips to include unreachable peers
	if relay != nil && len(relayedPeers) > 0 {
		UpdateRelayAllowedIPs(iface, relay.RelayPubKey, relay.RelayVpnIP, relayedPeers)
	}

	return len(peers)
}

// UpdateRelayAllowedIPs updates relay peer to route additional IPs
func UpdateRelayAllowedIPs(iface, relayPubKey, relayVpnIP string, additionalIPs []string) {
	wg := common.FindBin("wg")

	// Build allowed-ips list: relay's own IP + all relayed peers
	allowedIPs := relayVpnIP + "/32"
	for _, ip := range additionalIPs {
		allowedIPs += "," + ip + "/32"
	}

	common.Run(wg, "set", iface, "peer", relayPubKey, "allowed-ips", allowedIPs)
}

// EnableIPForwarding enables IP forwarding on Linux (for relay nodes)
func EnableIPForwarding() {
	if runtime.GOOS == "linux" {
		common.RunShell("sysctl -w net.ipv4.ip_forward=1")
		common.RunShell("sysctl -w net.ipv6.conf.all.forwarding=1")
	}
}

// SetupRelayRouting sets up iptables for relay
func SetupRelayRouting(iface, subnet string) {
	if runtime.GOOS != "linux" {
		return
	}
	// Enable masquerading for VPN traffic
	common.RunShell(fmt.Sprintf("iptables -t nat -A POSTROUTING -s %s.0.0/16 -o %s -j MASQUERADE 2>/dev/null", subnet, iface))
	// Allow forwarding
	common.RunShell(fmt.Sprintf("iptables -A FORWARD -i %s -j ACCEPT 2>/dev/null", iface))
	common.RunShell(fmt.Sprintf("iptables -A FORWARD -o %s -j ACCEPT 2>/dev/null", iface))
}
