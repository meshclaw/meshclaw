package coordinator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	GossipInterval = 30 * time.Second
	GossipTimeout  = 5 * time.Second
	CoordPort      = 8790
	DefaultNetwork = "default"
)

// Network represents a VPN network
type Network struct {
	Name      string `json:"name"`
	Subnet    string `json:"subnet"`     // e.g., "10.99"
	AccessKey string `json:"access_key"` // SHA256 hash for authentication
	CreatedAt int64  `json:"created_at"`
}

// PeerStats contains system stats reported by workers
type PeerStats struct {
	Load      string  `json:"load,omitempty"`
	LoadValue float64 `json:"load_value,omitempty"`
	MemPct    int     `json:"mem_pct,omitempty"`
	DiskPct   int     `json:"disk_pct,omitempty"`
	Uptime    string  `json:"uptime,omitempty"`
	UpdatedAt int64   `json:"updated_at,omitempty"`
}

// Peer represents a registered peer
type Peer struct {
	Network     string     `json:"network"`
	NodeID      string     `json:"node_id"`
	NodeName    string     `json:"node_name"`
	WgPublicKey string     `json:"wg_public_key"`
	PublicIP    string     `json:"public_ip"`
	LanIP       string     `json:"lan_ip"`
	VpnIP       string     `json:"vpn_ip"`
	Port        int        `json:"port"`
	NatPort     int        `json:"nat_port"`
	LastSeen    int64      `json:"last_seen"`
	Stats       *PeerStats `json:"stats,omitempty"`
}

// Server is the coordinator server
type Server struct {
	port     int
	dataDir  string
	binDir   string
	networks map[string]*Network          // network_name -> Network
	peers    map[string]map[string]*Peer  // network -> node_id -> Peer
	mu       sync.RWMutex
}

// NewServer creates a new coordinator server
func NewServer(port int, dataDir, binDir string) *Server {
	s := &Server{
		port:     port,
		dataDir:  dataDir,
		binDir:   binDir,
		networks: make(map[string]*Network),
		peers:    make(map[string]map[string]*Peer),
	}
	s.loadData()
	s.ensureDefaultNetwork()
	return s
}

func (s *Server) ensureDefaultNetwork() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.networks[DefaultNetwork]; !exists {
		// Allow subnet to be configured via environment variable
		// WIRE_SUBNET=10.99 for old network, default 10.98 for new network
		subnet := os.Getenv("WIRE_SUBNET")
		if subnet == "" {
			subnet = "10.98"
		}
		s.networks[DefaultNetwork] = &Network{
			Name:      DefaultNetwork,
			Subnet:    subnet,
			AccessKey: "",
			CreatedAt: time.Now().Unix(),
		}
		s.peers[DefaultNetwork] = make(map[string]*Peer)
	}
}

// Run starts the server
func (s *Server) Run() error {
	mux := http.NewServeMux()

	// Network management
	mux.HandleFunc("/networks", s.handleNetworks)
	mux.HandleFunc("/networks/create", s.handleNetworkCreate)

	// Peer management (with network support)
	mux.HandleFunc("/peers", s.handlePeers)
	mux.HandleFunc("/register", s.handleRegister)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/sync", s.handleSync)

	// Utility
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/install.sh", s.handleInstallScript)
	mux.HandleFunc("/bin/wire", s.handleBinary("wire"))
	mux.HandleFunc("/bin/vssh", s.handleBinary("vssh"))

	// Initial sync from seed if joining existing cluster
	if seedURL := os.Getenv("WIRE_SEED_URL"); seedURL != "" {
		fmt.Printf("  Syncing from seed: %s\n", seedURL)
		go func() {
			time.Sleep(2 * time.Second)
			s.syncFromSeed(seedURL)
		}()
	}

	// Start gossip loop
	go s.gossipLoop()

	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("Coordinator listening on %s (multi-network, gossip enabled)\n", addr)
	fmt.Printf("  Install: curl -sL http://<this-ip>:%d/install.sh | bash\n", s.port)
	return http.ListenAndServe(addr, mux)
}

// syncFromSeed fetches initial peers from seed coordinator
func (s *Server) syncFromSeed(seedURL string) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(seedURL + "/peers")
	if err != nil {
		fmt.Printf("  Seed sync failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result struct {
		Peers []*Peer `json:"peers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("  Seed sync decode failed: %v\n", err)
		return
	}

	s.mergeData(nil, result.Peers)
	fmt.Printf("  Synced %d peers from seed\n", len(result.Peers))
}

// handleNetworks lists all networks
func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	networks := make([]*Network, 0, len(s.networks))
	for _, n := range s.networks {
		// Don't expose access_key
		networks = append(networks, &Network{
			Name:      n.Name,
			Subnet:    n.Subnet,
			CreatedAt: n.CreatedAt,
		})
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"networks": networks,
	})
}

// handleNetworkCreate creates a new network
func (s *Server) handleNetworkCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name   string `json:"name"`
		Subnet string `json:"subnet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json")
		return
	}

	if req.Name == "" {
		jsonError(w, "name required")
		return
	}
	if req.Subnet == "" {
		jsonError(w, "subnet required (e.g., '10.98')")
		return
	}

	// Generate access key
	h := sha256.Sum256([]byte(req.Name + "-" + fmt.Sprintf("%d", time.Now().UnixNano())))
	accessKey := hex.EncodeToString(h[:16])

	s.mu.Lock()
	if _, exists := s.networks[req.Name]; exists {
		s.mu.Unlock()
		jsonError(w, "network already exists")
		return
	}
	s.networks[req.Name] = &Network{
		Name:      req.Name,
		Subnet:    req.Subnet,
		AccessKey: accessKey,
		CreatedAt: time.Now().Unix(),
	}
	s.peers[req.Name] = make(map[string]*Peer)
	s.mu.Unlock()

	s.saveData()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"name":       req.Name,
		"subnet":     req.Subnet,
		"access_key": accessKey,
	})
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	network := r.URL.Query().Get("network")
	if network == "" {
		network = DefaultNetwork
	}

	s.mu.RLock()
	networkPeers, exists := s.peers[network]
	if !exists {
		s.mu.RUnlock()
		jsonError(w, "network not found")
		return
	}
	peers := make([]*Peer, 0, len(networkPeers))
	for _, p := range networkPeers {
		peers = append(peers, p)
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"network": network,
		"peers":   peers,
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Network     string `json:"network"`
		AccessKey   string `json:"access_key"`
		NodeID      string `json:"node_id"`
		NodeName    string `json:"node_name"`
		WgPublicKey string `json:"wg_public_key"`
		LanIP       string `json:"lan_ip"`
		VpnIP       string `json:"vpn_ip"`
		Port        int    `json:"port"`
		NatPort     int    `json:"nat_port"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json")
		return
	}

	if req.NodeID == "" {
		jsonError(w, "node_id required")
		return
	}

	network := req.Network
	if network == "" {
		network = DefaultNetwork
	}

	// Verify network exists and access key (if required)
	s.mu.RLock()
	net, exists := s.networks[network]
	s.mu.RUnlock()

	if !exists {
		jsonError(w, "network not found")
		return
	}
	if net.AccessKey != "" && req.AccessKey != net.AccessKey {
		jsonError(w, "invalid access key")
		return
	}

	publicIP := getClientIP(r)

	newPeer := &Peer{
		Network:     network,
		NodeID:      req.NodeID,
		NodeName:    req.NodeName,
		WgPublicKey: req.WgPublicKey,
		PublicIP:    publicIP,
		LanIP:       req.LanIP,
		VpnIP:       req.VpnIP,
		Port:        req.Port,
		NatPort:     req.NatPort,
		LastSeen:    time.Now().Unix(),
	}

	s.mu.Lock()
	if s.peers[network] == nil {
		s.peers[network] = make(map[string]*Peer)
	}
	s.peers[network][req.NodeID] = newPeer
	s.mu.Unlock()

	s.saveData()
	go s.gossipOnce()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        true,
		"network":   network,
		"public_ip": publicIP,
	})
}

// handleStats receives stats from workers
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Network   string  `json:"network"`
		NodeID    string  `json:"node_id"`
		Load      string  `json:"load"`
		LoadValue float64 `json:"load_value"`
		MemPct    int     `json:"mem_pct"`
		DiskPct   int     `json:"disk_pct"`
		Uptime    string  `json:"uptime"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json")
		return
	}

	if req.NodeID == "" {
		jsonError(w, "node_id required")
		return
	}

	network := req.Network
	if network == "" {
		network = DefaultNetwork
	}

	s.mu.Lock()
	if peers, ok := s.peers[network]; ok {
		if peer, ok := peers[req.NodeID]; ok {
			peer.Stats = &PeerStats{
				Load:      req.Load,
				LoadValue: req.LoadValue,
				MemPct:    req.MemPct,
				DiskPct:   req.DiskPct,
				Uptime:    req.Uptime,
				UpdatedAt: time.Now().Unix(),
			}
			peer.LastSeen = time.Now().Unix()
		}
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	totalPeers := 0
	for _, np := range s.peers {
		totalPeers += len(np)
	}
	networkCount := len(s.networks)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"networks": networkCount,
		"peers":    totalPeers,
	})
}

// handleSync receives gossip from other coordinators
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var incoming struct {
		Networks []*Network `json:"networks"`
		Peers    []*Peer    `json:"peers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		jsonError(w, "invalid json")
		return
	}

	updated := s.mergeData(incoming.Networks, incoming.Peers)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"updated": updated,
	})
}

func (s *Server) mergeData(networks []*Network, peers []*Peer) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get local subnet to filter incoming peers
	localSubnet := ""
	if defaultNet := s.networks[DefaultNetwork]; defaultNet != nil {
		localSubnet = defaultNet.Subnet
	}

	updated := 0

	// Merge networks (don't overwrite existing)
	for _, n := range networks {
		if _, exists := s.networks[n.Name]; !exists {
			s.networks[n.Name] = n
			s.peers[n.Name] = make(map[string]*Peer)
			updated++
		}
	}

	// Merge peers - only accept peers in the same subnet as this coordinator
	for _, p := range peers {
		if p.Network == "" {
			p.Network = DefaultNetwork
		}
		if s.peers[p.Network] == nil {
			continue // Network doesn't exist locally
		}
		// Skip peers from different subnets to prevent cross-network sync
		if localSubnet != "" && p.VpnIP != "" && !strings.HasPrefix(p.VpnIP, localSubnet+".") {
			continue
		}
		existing, exists := s.peers[p.Network][p.NodeID]
		if !exists || p.LastSeen > existing.LastSeen {
			// Preserve newer stats when merging
			if exists && existing.Stats != nil && (p.Stats == nil || (existing.Stats.UpdatedAt > p.Stats.UpdatedAt)) {
				p.Stats = existing.Stats
			}
			s.peers[p.Network][p.NodeID] = p
			updated++
		} else if exists && p.Stats != nil && (existing.Stats == nil || p.Stats.UpdatedAt > existing.Stats.UpdatedAt) {
			// Update stats even if peer info hasn't changed
			existing.Stats = p.Stats
			updated++
		}
	}

	if updated > 0 {
		go s.saveData()
	}
	return updated
}

func (s *Server) gossipLoop() {
	time.Sleep(5 * time.Second)
	for {
		s.gossipOnce()
		time.Sleep(GossipInterval)
	}
}

func (s *Server) gossipOnce() {
	s.mu.RLock()

	// Get the default network's subnet for this coordinator
	defaultNet := s.networks[DefaultNetwork]
	if defaultNet == nil {
		s.mu.RUnlock()
		return
	}
	localSubnet := defaultNet.Subnet // e.g., "10.98"

	networks := make([]*Network, 0, len(s.networks))
	allPeers := make([]*Peer, 0)
	coordURLs := make(map[string]bool)

	for _, n := range s.networks {
		networks = append(networks, n)
	}
	for _, networkPeers := range s.peers {
		for _, p := range networkPeers {
			allPeers = append(allPeers, p)
			// Only gossip with peers in the SAME subnet as this coordinator
			// This prevents cross-network gossip between separate VPNs
			if p.VpnIP != "" && strings.HasPrefix(p.VpnIP, localSubnet+".") {
				coordURLs[fmt.Sprintf("http://%s:%d", p.VpnIP, CoordPort)] = true
			}
		}
	}
	s.mu.RUnlock()

	if len(allPeers) == 0 {
		return
	}

	var wg sync.WaitGroup
	for url := range coordURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			s.sendGossip(u, networks, allPeers)
		}(url)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(GossipTimeout):
	}
}

func (s *Server) sendGossip(coordURL string, networks []*Network, peers []*Peer) {
	client := &http.Client{Timeout: GossipTimeout}

	data, _ := json.Marshal(map[string]interface{}{
		"networks": networks,
		"peers":    peers,
	})

	resp, err := client.Post(coordURL+"/sync", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return
	}
	resp.Body.Close()

	s.fetchAndMerge(coordURL)
}

func (s *Server) fetchAndMerge(coordURL string) {
	client := &http.Client{Timeout: GossipTimeout}

	// Fetch networks
	resp, err := client.Get(coordURL + "/networks")
	if err != nil {
		return
	}
	var netResult struct {
		Networks []*Network `json:"networks"`
	}
	json.NewDecoder(resp.Body).Decode(&netResult)
	resp.Body.Close()

	// Fetch peers for each network
	var allPeers []*Peer
	for _, n := range netResult.Networks {
		resp, err := client.Get(coordURL + "/peers?network=" + n.Name)
		if err != nil {
			continue
		}
		var peerResult struct {
			Peers []*Peer `json:"peers"`
		}
		json.NewDecoder(resp.Body).Decode(&peerResult)
		resp.Body.Close()
		allPeers = append(allPeers, peerResult.Peers...)
	}

	s.mergeData(netResult.Networks, allPeers)
}

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = r.Header.Get("Host")
	}
	if !strings.Contains(host, ":") {
		host = host + ":8790"
	}
	coordURL := fmt.Sprintf("http://%s", host)
	network := r.URL.Query().Get("network")
	if network == "" {
		network = DefaultNetwork
	}
	nodeName := r.URL.Query().Get("node_name")

	script := generateInstallScript(coordURL, network, nodeName)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(script))
}

func (s *Server) handleBinary(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		binPath := s.binDir + "/" + name
		data, err := os.ReadFile(binPath)
		if err != nil {
			http.Error(w, "binary not found: "+name, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", name))
		w.Write(data)
	}
}

func generateInstallScript(coordURL, network, nodeName string) string {
	nodeNameEnv := ""
	if nodeName != "" {
		nodeNameEnv = fmt.Sprintf("Environment=WIRE_NODE_NAME=%s\n", nodeName)
	}
	return fmt.Sprintf(`#!/bin/bash
set -e

COORD_URL="%s"
NETWORK="%s"
NODE_NAME="%s"
BIN_DIR="/usr/local/bin"

echo "Installing wire mesh VPN (distributed)..."
echo "Coordinator: $COORD_URL"
echo "Network: $NETWORK"
[ -n "$NODE_NAME" ] && echo "Node Name: $NODE_NAME"

# Download binaries
echo "Downloading binaries..."
curl -sL "$COORD_URL/bin/wire" -o "$BIN_DIR/wire"
curl -sL "$COORD_URL/bin/vssh" -o "$BIN_DIR/vssh"
chmod +x "$BIN_DIR/wire" "$BIN_DIR/vssh"

mkdir -p /var/lib/wire/bin
cp "$BIN_DIR/wire" "$BIN_DIR/vssh" /var/lib/wire/bin/

echo "Creating systemd services..."

cat > /etc/systemd/system/wire-coordinator.service << 'EOF'
[Unit]
Description=Wire Coordinator (Gossip)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/wire coordinator
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/wire.service << EOF
[Unit]
Description=Wire VPN
After=wire-coordinator.service
Wants=wire-coordinator.service

[Service]
Type=simple
Environment=WIRE_SERVER_URL=%s
Environment=WIRE_NETWORK=%s
%sExecStart=/usr/local/bin/wire up --foreground --network %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/vssh.service << EOF
[Unit]
Description=VSSH Server
After=wire.service
Wants=wire.service

[Service]
Type=simple
Environment=WIRE_SERVER_URL=%s
ExecStart=/usr/local/bin/vssh server
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable wire-coordinator wire vssh
systemctl start wire-coordinator
sleep 1
systemctl start wire
sleep 2
systemctl start vssh

echo ""
echo "Done! Network '$NETWORK' joined."
echo "  wire status    - check VPN status"
echo "  vssh status    - check vssh status"
echo ""
`, coordURL, network, nodeName, coordURL, network, nodeNameEnv, network, coordURL)
}

func (s *Server) loadData() {
	// Load networks
	netFile := filepath.Join(s.dataDir, "networks.json")
	if data, err := os.ReadFile(netFile); err == nil {
		var networks []*Network
		if json.Unmarshal(data, &networks) == nil {
			for _, n := range networks {
				s.networks[n.Name] = n
				s.peers[n.Name] = make(map[string]*Peer)
			}
		}
	}

	// Load peers
	peerFile := filepath.Join(s.dataDir, "peers.json")
	if data, err := os.ReadFile(peerFile); err == nil {
		var peers []*Peer
		if json.Unmarshal(data, &peers) == nil {
			for _, p := range peers {
				if p.Network == "" {
					p.Network = DefaultNetwork
				}
				if s.peers[p.Network] == nil {
					s.peers[p.Network] = make(map[string]*Peer)
				}
				s.peers[p.Network][p.NodeID] = p
			}
		}
	}
}

func (s *Server) saveData() {
	os.MkdirAll(s.dataDir, 0755)

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Save networks
	networks := make([]*Network, 0, len(s.networks))
	for _, n := range s.networks {
		networks = append(networks, n)
	}
	if data, err := json.MarshalIndent(networks, "", "  "); err == nil {
		os.WriteFile(filepath.Join(s.dataDir, "networks.json"), data, 0644)
	}

	// Save peers
	var allPeers []*Peer
	for _, np := range s.peers {
		for _, p := range np {
			allPeers = append(allPeers, p)
		}
	}
	if data, err := json.MarshalIndent(allPeers, "", "  "); err == nil {
		os.WriteFile(filepath.Join(s.dataDir, "peers.json"), data, 0644)
	}
}

func jsonError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
