package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/common"
	"github.com/meshclaw/meshclaw/internal/coordinator"
	"github.com/meshclaw/meshclaw/internal/wire"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "install":
		joinURL := ""
		nodeName := ""
		for i, arg := range os.Args[2:] {
			if arg == "--join" || arg == "-j" {
				if i+1 < len(os.Args[2:]) {
					joinURL = os.Args[2:][i+1]
				}
			}
			if strings.HasPrefix(arg, "--join=") {
				joinURL = strings.TrimPrefix(arg, "--join=")
			}
			if arg == "--name" {
				if i+1 < len(os.Args[2:]) {
					nodeName = os.Args[2:][i+1]
				}
			}
			if strings.HasPrefix(arg, "--name=") {
				nodeName = strings.TrimPrefix(arg, "--name=")
			}
		}
		cmdInstall(joinURL, nodeName)
	case "up":
		foreground := false
		isRelay := false
		network := os.Getenv("WIRE_NETWORK")
		if network == "" {
			network = "default"
		}
		for i, arg := range os.Args[2:] {
			if arg == "--foreground" || arg == "-f" {
				foreground = true
			}
			if arg == "--relay" || arg == "-r" {
				isRelay = true
			}
			if arg == "--network" || arg == "-n" {
				if i+1 < len(os.Args[2:]) {
					network = os.Args[2:][i+1]
				}
			}
			if strings.HasPrefix(arg, "--network=") {
				network = strings.TrimPrefix(arg, "--network=")
			}
		}
		cmdUp(foreground, isRelay, network)
	case "down":
		cmdDown()
	case "status":
		network := ""
		for i, arg := range os.Args[2:] {
			if arg == "--network" || arg == "-n" {
				if i+1 < len(os.Args[2:]) {
					network = os.Args[2:][i+1]
				}
			}
			if strings.HasPrefix(arg, "--network=") {
				network = strings.TrimPrefix(arg, "--network=")
			}
		}
		cmdStatus(network)
	case "coordinator", "coord":
		cmdCoordinator()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("wire - WireGuard mesh VPN")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  wire install               First node: start coordinator + VPN")
	fmt.Println("  wire install --join URL    Join existing mesh")
	fmt.Println("  wire up [options]          Start VPN (manual)")
	fmt.Println("  wire down                  Stop VPN")
	fmt.Println("  wire status                Show all networks")
	fmt.Println("  wire status -n NAME        Show specific network")
	fmt.Println("  wire coordinator           Start coordinator server")
	fmt.Println()
	fmt.Println("Install Options:")
	fmt.Println("  -j, --join URL             Join existing coordinator")
	fmt.Println("  --name NAME                Set node name (default: hostname)")
	fmt.Println()
	fmt.Println("Up Options:")
	fmt.Println("  -f, --foreground           Run in foreground")
	fmt.Println("  -r, --relay                Enable relay mode (IP forwarding)")
	fmt.Println("  -n, --network NAME         Join specific network")
	fmt.Println()
	fmt.Println("Environment:")
	fmt.Println("  WIRE_SERVER_URL            Coordinator URL")
	fmt.Println("  WIRE_NODE_NAME             Node name override")
	fmt.Println("  WIRE_NETWORK               Network name (default: 'default')")
	fmt.Println("  WIRE_ACCESS_KEY            Access key for private networks")
	fmt.Println("  COORDINATOR_PORT           Coordinator port (default: 8790)")
	fmt.Println("  COORDINATOR_DATA           Data directory (default: /var/lib/wire)")
	fmt.Println()
	fmt.Println("Quick Start:")
	fmt.Println("  # First node (coordinator):")
	fmt.Println("  wire install")
	fmt.Println()
	fmt.Println("  # Additional nodes:")
	fmt.Println("  wire install --join http://<coordinator-ip>:8790")
	fmt.Println()
	fmt.Println("Multi-Network:")
	fmt.Println("  wire up --network prod")
	fmt.Println("  wire up --network dev")
	fmt.Println("  wire status")
	fmt.Println()
}

func cmdUp(foreground, isRelay bool, network string) {
	if !common.IsRoot() {
		fmt.Fprintln(os.Stderr, "Must run as root")
		os.Exit(1)
	}

	if network == "" {
		network = "default"
	}

	// Load or create base config
	cfg, err := wire.LoadConfig()
	if err != nil {
		cfg = &wire.Config{
			ServerURL: os.Getenv("WIRE_SERVER_URL"),
			Networks:  make(map[string]*wire.NetworkConfig),
		}
		if cfg.ServerURL == "" {
			fmt.Fprintln(os.Stderr, "WIRE_SERVER_URL not set and no config found")
			os.Exit(1)
		}
	}

	// Get hostname for node name (WIRE_NODE_NAME env var takes priority)
	if cfg.NodeName == "" {
		if name := os.Getenv("WIRE_NODE_NAME"); name != "" {
			cfg.NodeName = name
		} else {
			hostname, _ := os.Hostname()
			cfg.NodeName = strings.Split(hostname, ".")[0]
		}
	}

	// Generate node ID if needed
	if cfg.NodeID == "" {
		cfg.NodeID = wire.GenerateNodeID()
	}

	// Get or create network-specific config
	nc := cfg.GetNetworkConfig(network)

	// Always fetch network info to check if subnet has changed
	netInfo, err := wire.GetNetworkInfo(cfg.ServerURL, network)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get network info: %v\n", err)
		os.Exit(1)
	}

	if nc == nil {
		nc = &wire.NetworkConfig{
			Network:    network,
			AccessKey:  os.Getenv("WIRE_ACCESS_KEY"),
			VpnSubnet:  netInfo.Subnet,
			Interface:  cfg.NextInterface(),
			ListenPort: cfg.NextPort(),
		}
		nc.VpnIP = wire.GenerateVpnIPForNetwork(cfg.NodeID, network, nc.VpnSubnet)
		cfg.SetNetworkConfig(nc)
	} else if nc.VpnSubnet != netInfo.Subnet {
		// Subnet changed on coordinator - regenerate VPN IP
		fmt.Printf("  Subnet changed from %s to %s, regenerating VPN IP\n", nc.VpnSubnet, netInfo.Subnet)
		nc.VpnSubnet = netInfo.Subnet
		nc.VpnIP = wire.GenerateVpnIPForNetwork(cfg.NodeID, network, nc.VpnSubnet)
		cfg.SetNetworkConfig(nc)
	}

	// Get or create private key (shared across networks)
	privateKey := wire.GetOrCreatePrivateKey()
	pubKey := wire.GetPublicKey(privateKey)

	// Setup interface for this network
	iface, err := wire.SetupInterface(nc.Interface, nc.VpnIP, privateKey, nc.ListenPort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to setup interface: %v\n", err)
		os.Exit(1)
	}

	// For registration, create a temporary config with network info
	regCfg := &wire.Config{
		ServerURL:  cfg.ServerURL,
		NodeName:   cfg.NodeName,
		NodeID:     cfg.NodeID,
		Network:    network,
		AccessKey:  nc.AccessKey,
		VpnIP:      nc.VpnIP,
		ListenPort: nc.ListenPort,
		VpnSubnet:  nc.VpnSubnet,
	}

	// Initial registration
	lanIP := wire.DetectLanIP()
	if err := wire.Register(cfg.ServerURL, regCfg, pubKey, lanIP, nc.ListenPort); err != nil {
		fmt.Fprintf(os.Stderr, "Registration failed: %v\n", err)
	}

	// Sync peers for this network
	peerCount := wire.SyncPeersForNetwork(iface, cfg.ServerURL, cfg.NodeID, network)

	// Save config
	wire.SaveConfig(cfg)

	// Print success
	relayStr := ""
	if isRelay {
		relayStr = " [RELAY]"
	}
	fmt.Printf("%s✓ wire up: %s%s%s (%s) ↔ %s [%s]%s\n",
		common.Green, common.Cyan, cfg.NodeName, common.Reset, nc.VpnIP, cfg.ServerURL, network, relayStr)
	fmt.Printf("  interface: %s  port: %d  peers: %d\n", iface, nc.ListenPort, peerCount)

	if foreground {
		fmt.Printf("  daemon running in foreground (heartbeat every %ds)\n", wire.RefreshInterval)
		wire.RunDaemonForNetwork(iface, cfg.ServerURL, cfg, nc, pubKey, nc.ListenPort, isRelay)
	} else {
		fmt.Printf("  daemon running in background (heartbeat every %ds)\n", wire.RefreshInterval)
		if err := wire.SpawnDaemonForNetwork(network); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to spawn daemon: %v\n", err)
		}
	}
}

func cmdDown() {
	if !common.IsRoot() {
		fmt.Fprintln(os.Stderr, "Must run as root")
		os.Exit(1)
	}

	// Get subnet from config
	subnet := ""
	if cfg, err := wire.LoadConfig(); err == nil {
		subnet = cfg.Subnet()
	}

	// Find and remove interface
	iface := wire.FindExistingInterface()
	if iface == "" {
		iface = wire.DefaultInterface
	}

	wire.TeardownInterface(iface, subnet)
	fmt.Printf("  Interface %s removed\n", iface)
}

func cmdCoordinator() {
	port := 8790
	if p := os.Getenv("COORDINATOR_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}

	dataDir := "/var/lib/wire"
	if d := os.Getenv("COORDINATOR_DATA"); d != "" {
		dataDir = d
	}

	binDir := "/var/lib/wire/bin"
	if d := os.Getenv("COORDINATOR_BIN"); d != "" {
		binDir = d
	}

	// Ensure directories exist
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(binDir, 0755)

	server := coordinator.NewServer(port, dataDir, binDir)
	if err := server.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Coordinator error: %v\n", err)
		os.Exit(1)
	}
}

func cmdInstall(joinURL, nodeName string) {
	if !common.IsRoot() {
		fmt.Fprintln(os.Stderr, "Must run as root")
		os.Exit(1)
	}

	// Detect node name
	if nodeName == "" {
		hostname, _ := os.Hostname()
		nodeName = strings.Split(hostname, ".")[0]
	}

	// Detect public IP
	publicIP := detectPublicIP()

	// Determine if we should start coordinator
	isFirstNode := joinURL == ""
	var coordURL string

	if isFirstNode {
		// First node - start coordinator
		if publicIP == "" {
			fmt.Fprintln(os.Stderr, "Error: First node needs a public IP")
			os.Exit(1)
		}
		coordURL = fmt.Sprintf("http://%s:8790", publicIP)
		fmt.Printf("%s✓ First node detected%s\n", common.Green, common.Reset)
		fmt.Printf("  Starting coordinator: %s\n", coordURL)
	} else {
		// Join existing mesh
		coordURL = strings.TrimSuffix(joinURL, "/")
		fmt.Printf("  Joining mesh: %s\n", coordURL)

		// Verify coordinator is reachable
		if !testCoordinator(coordURL) {
			fmt.Fprintf(os.Stderr, "Error: Cannot reach coordinator at %s\n", coordURL)
			os.Exit(1)
		}
	}

	// Create systemd services
	fmt.Println("  Creating systemd services...")
	seedURL := ""
	if !isFirstNode {
		seedURL = coordURL // Use join URL as seed for gossip sync
	}
	createSystemdServices(coordURL, nodeName, seedURL)

	// Configure firewall
	configureFirewall()

	// Start services - ALL nodes run coordinator (distributed mesh)
	fmt.Println("  Starting services...")
	runCmd("systemctl", "daemon-reload")
	runCmd("systemctl", "enable", "wire-coordinator", "wire", "--quiet")
	runCmd("systemctl", "start", "wire-coordinator")
	time.Sleep(2 * time.Second)
	runCmd("systemctl", "start", "wire")

	// Wait for VPN to be ready
	time.Sleep(2 * time.Second)

	// Get status
	cfg, _ := wire.LoadConfig()
	var vpnIP string
	if cfg != nil {
		if nc := cfg.GetNetworkConfig("default"); nc != nil {
			vpnIP = nc.VpnIP
		}
	}

	fmt.Println()
	fmt.Printf("%s✓ wire installed%s\n", common.Green, common.Reset)
	fmt.Printf("  Node: %s%s%s\n", common.Cyan, nodeName, common.Reset)
	if vpnIP != "" {
		fmt.Printf("  VPN IP: %s\n", vpnIP)
	}
	fmt.Printf("  Coordinator: %s\n", coordURL)
	if isFirstNode {
		fmt.Println()
		fmt.Println("  Join other nodes with:")
		fmt.Printf("    wire install --join %s\n", coordURL)
	}
	fmt.Println()
}

func detectPublicIP() string {
	// Try to detect public IP via external service
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://ifconfig.me/ip")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	return strings.TrimSpace(string(buf[:n]))
}

func testCoordinator(url string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func createSystemdServices(coordURL, nodeName string, seedURL string) {
	// Wire coordinator service - ALL nodes run coordinator (distributed mesh)
	var coordService string
	if seedURL != "" {
		// Joining node - connect to seed for initial sync
		coordService = fmt.Sprintf(`[Unit]
Description=Wire Coordinator (Gossip)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=WIRE_SEED_URL=%s
ExecStart=/usr/local/bin/wire coordinator
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, seedURL)
	} else {
		// First node - no seed
		coordService = `[Unit]
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
`
	}
	os.WriteFile("/etc/systemd/system/wire-coordinator.service", []byte(coordService), 0644)

	// Wire VPN service - always depends on local coordinator
	wireService := fmt.Sprintf(`[Unit]
Description=Wire VPN
After=wire-coordinator.service network-online.target
Wants=wire-coordinator.service network-online.target

[Service]
Type=simple
Environment=WIRE_SERVER_URL=%s
Environment=WIRE_NODE_NAME=%s
ExecStart=/usr/local/bin/wire up --foreground
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, coordURL, nodeName)
	os.WriteFile("/etc/systemd/system/wire.service", []byte(wireService), 0644)

	// Template service for additional networks
	templateService := fmt.Sprintf(`[Unit]
Description=Wire VPN Network %%i
After=wire.service

[Service]
Type=simple
Environment=WIRE_SERVER_URL=%s
Environment=WIRE_NODE_NAME=%s
ExecStart=/usr/local/bin/wire up --foreground --network %%i
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, coordURL, nodeName)
	os.WriteFile("/etc/systemd/system/wire@.service", []byte(templateService), 0644)
}

func configureFirewall() {
	// Check if ufw is available
	if _, err := exec.LookPath("ufw"); err == nil {
		runCmdQuiet("ufw", "allow", "51820/udp")
		runCmdQuiet("ufw", "allow", "8790/tcp")
	}
	// Check if firewall-cmd is available (CentOS/RHEL)
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		runCmdQuiet("firewall-cmd", "--permanent", "--add-port=51820/udp")
		runCmdQuiet("firewall-cmd", "--permanent", "--add-port=8790/tcp")
		runCmdQuiet("firewall-cmd", "--reload")
	}
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCmdQuiet(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func cmdStatus(filterNetwork string) {
	cfg, err := wire.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wire not configured")
		os.Exit(1)
	}

	// Get all networks
	networks := cfg.ActiveNetworks()
	if len(networks) == 0 {
		networks = []string{"default"}
	}

	// Filter if specific network requested
	if filterNetwork != "" {
		networks = []string{filterNetwork}
	}

	fmt.Println()

	for _, network := range networks {
		peers, err := wire.GetPeersForNetwork(cfg.ServerURL, network)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Failed to get peers for %s: %v\n", network, err)
			continue
		}

		// Sort peers by node name
		sort.Slice(peers, func(i, j int) bool {
			return peers[i].NodeName < peers[j].NodeName
		})

		// Count online/offline
		now := time.Now()
		online := 0
		offline := 0

		type peerInfo struct {
			name     string
			vpnIP    string
			pubIP    string
			lastSeen string
			isOnline bool
		}
		var infos []peerInfo

		for _, p := range peers {
			isOnline := false
			lastSeenStr := ""

			var lastSeenTime time.Time
			switch v := p.LastSeen.(type) {
			case string:
				if v != "" {
					lastSeenTime, _ = time.Parse(time.RFC3339, v)
				}
			case float64:
				lastSeenTime = time.Unix(int64(v), 0)
			}

			if !lastSeenTime.IsZero() {
				age := now.Sub(lastSeenTime)
				if age < 90*time.Second {
					isOnline = true
					online++
				} else {
					offline++
				}
				if age < time.Minute {
					lastSeenStr = fmt.Sprintf("%ds ago", int(age.Seconds()))
				} else {
					lastSeenStr = fmt.Sprintf("%dm ago", int(age.Minutes()))
				}
			} else {
				offline++
			}

			infos = append(infos, peerInfo{
				name:     p.NodeName,
				vpnIP:    p.VpnIP,
				pubIP:    p.PublicIP,
				lastSeen: lastSeenStr,
				isOnline: isOnline,
			})
		}

		// Print network header
		nc := cfg.GetNetworkConfig(network)
		iface := "wire0"
		if nc != nil {
			iface = nc.Interface
		}
		fmt.Printf("  %snetwork: %s%s  │  %s  │  %s\n",
			common.Cyan, network, common.Reset, iface, cfg.ServerURL)
		fmt.Printf("  %s\n", strings.Repeat("─", 50))
		fmt.Printf("  %s%d online%s / %s%d offline%s / %d total\n\n",
			common.Green, online, common.Reset,
			common.Yellow, offline, common.Reset,
			len(peers))

		// Print peers
		for _, info := range infos {
			var dot, nameCol string
			if info.isOnline {
				dot = common.Green + "●" + common.Reset
				nameCol = common.Green + fmt.Sprintf("%-16s", info.name) + common.Reset
			} else {
				dot = common.Red + "○" + common.Reset
				nameCol = common.Dim + fmt.Sprintf("%-16s", info.name) + common.Reset
			}
			fmt.Printf("  %s %s  %-18s %-20s %s\n", dot, nameCol, info.vpnIP, info.pubIP, info.lastSeen)
		}
		fmt.Println()
	}
}

// Helper to pretty print JSON (for debugging)
func prettyJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
