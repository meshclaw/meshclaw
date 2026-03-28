package wire

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/common"
)

// RelayConfig holds relay configuration
type RelayConfig struct {
	RelayNodeID   string        // Node ID of relay
	RelayVpnIP    string        // VPN IP of relay
	RelayPubKey   string        // WireGuard public key of relay
	RelayEndpoint string        // Public endpoint of relay
	Latency       time.Duration // Measured latency to relay
}

// RelayState tracks current relay and health
type RelayState struct {
	Current       *RelayConfig  // Currently selected relay
	FailCount     int           // Consecutive failures
	LastCheck     time.Time     // Last health check time
	LastSuccess   time.Time     // Last successful connection
}

// Global relay state (per interface would be better, but simple for now)
var currentRelayState = make(map[string]*RelayState) // key: network name

// GetRelayState returns relay state for a network
func GetRelayState(network string) *RelayState {
	if state, ok := currentRelayState[network]; ok {
		return state
	}
	state := &RelayState{}
	currentRelayState[network] = state
	return state
}

// IsRelayHealthy checks if current relay is still working
// Uses WireGuard handshake time to verify actual connectivity
func IsRelayHealthy(relay *RelayConfig, iface string) bool {
	if relay == nil {
		return false
	}

	// Check WireGuard handshake time for this peer
	wg := common.FindBin("wg")
	out, _, code := common.Run(wg, "show", iface, "latest-handshakes")
	if code != 0 {
		return false
	}

	// Parse output: "pubkey\ttimestamp\n"
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}
		if parts[0] == relay.RelayPubKey {
			ts, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return false
			}
			if ts == 0 {
				// No handshake yet
				return false
			}
			handshakeAge := time.Since(time.Unix(ts, 0))
			// Healthy if handshake within last 2.5 minutes
			return handshakeAge < 150*time.Second
		}
	}

	return false
}

// SelectRelayWithStickiness selects relay with stickiness and failover
func SelectRelayWithStickiness(candidates []RelayConfig, network, iface string) *RelayConfig {
	state := GetRelayState(network)

	// If we have a current relay, check if it's still healthy
	if state.Current != nil {
		// Don't check too frequently (every 15s max)
		if time.Since(state.LastCheck) < 15*time.Second {
			return state.Current
		}

		state.LastCheck = time.Now()

		if IsRelayHealthy(state.Current, iface) {
			state.FailCount = 0
			state.LastSuccess = time.Now()
			return state.Current
		}

		// Relay failed - switch immediately
		fmt.Printf("  [relay] %s unhealthy, switching...\n", state.Current.RelayNodeID)
		state.Current = nil
		state.FailCount = 0
	}

	// No current relay or need to select new one
	newRelay := SelectBestRelay(candidates)
	if newRelay != nil {
		fmt.Printf("  [relay] selected %s (%s) latency=%v\n",
			newRelay.RelayNodeID, newRelay.RelayEndpoint, newRelay.Latency)
		state.Current = newRelay
		state.LastCheck = time.Now()
		state.LastSuccess = time.Now()
	}

	return newRelay
}

// GetRelayCandidates returns all nodes that can act as relay
// Only nodes with direct public IP (public_ip == lan_ip) can be relays
// This excludes NAT clients that have public_ip but can't accept incoming
func GetRelayCandidates(peers []Peer, myNodeID string) []RelayConfig {
	var relays []RelayConfig

	for _, p := range peers {
		if p.NodeID == myNodeID {
			continue
		}
		if p.PublicIP == "" || p.WgPublicKey == "" {
			continue
		}
		// Only consider as relay if public_ip == lan_ip (direct public IP, not NAT)
		if p.PublicIP != p.LanIP {
			continue
		}
		relays = append(relays, RelayConfig{
			RelayNodeID:   p.NodeID,
			RelayVpnIP:    p.VpnIP,
			RelayPubKey:   p.WgPublicKey,
			RelayEndpoint: fmt.Sprintf("%s:%d", p.PublicIP, getPort(p)),
			Latency:       0,
		})
	}

	return relays
}

// MeasureLatency measures RTT to a relay endpoint
func MeasureLatency(endpoint string) time.Duration {
	host := strings.Split(endpoint, ":")[0]
	start := time.Now()

	conn, err := net.DialTimeout("udp", endpoint, 2*time.Second)
	if err != nil {
		// Try TCP as fallback
		conn, err = net.DialTimeout("tcp", host+":22", 2*time.Second)
		if err != nil {
			return time.Hour // Very high latency means unreachable
		}
	}
	conn.Close()

	return time.Since(start)
}

// SelectBestRelay selects the best relay from candidates based on latency
func SelectBestRelay(candidates []RelayConfig) *RelayConfig {
	if len(candidates) == 0 {
		return nil
	}

	// Measure latency for each candidate
	for i := range candidates {
		candidates[i].Latency = MeasureLatency(candidates[i].RelayEndpoint)
	}

	// Find best (lowest latency)
	best := &candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].Latency < best.Latency {
			best = &candidates[i]
		}
	}

	// Only return if reachable (latency < 1 hour)
	if best.Latency >= time.Hour {
		return nil
	}

	return best
}

// SelectRelayWithFallback tries to select the best relay, with fallback to others
func SelectRelayWithFallback(candidates []RelayConfig, testFn func(*RelayConfig) bool) *RelayConfig {
	if len(candidates) == 0 {
		return nil
	}

	// Measure latency and sort by it
	for i := range candidates {
		candidates[i].Latency = MeasureLatency(candidates[i].RelayEndpoint)
	}

	// Sort by latency (simple bubble sort, few candidates)
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].Latency < candidates[i].Latency {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	// Try each relay in order of latency
	for i := range candidates {
		if candidates[i].Latency >= time.Hour {
			continue // Skip unreachable
		}
		if testFn == nil || testFn(&candidates[i]) {
			return &candidates[i]
		}
	}

	return nil
}

// GetRelayNode finds the best relay node from peers (legacy, uses new logic)
func GetRelayNode(peers []Peer, myNodeID string) *RelayConfig {
	candidates := GetRelayCandidates(peers, myNodeID)
	return SelectBestRelay(candidates)
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
