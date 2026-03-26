package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/common"
	"github.com/meshclaw/meshclaw/internal/mpop"
	"github.com/meshclaw/meshclaw/internal/wire"
)

const Version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		cmdDashboard(nil)
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	// Commands that work without config
	noConfigCmds := map[string]bool{
		"help": true, "-h": true, "--help": true,
		"--version": true, "-v": true, "version": true,
		"init": true, "config": true,
	}

	// Handle version
	if cmd == "--version" || cmd == "-v" || cmd == "version" {
		fmt.Printf("mpop v%s (Go)\n", Version)
		return
	}

	// Initialize peer discovery for commands that need it
	if !noConfigCmds[cmd] {
		if mpop.EnsureConfig() {
			fmt.Printf("  %s Created %s%s with sample servers. Edit to add your infrastructure.\n\n",
				common.Cyan+"✓"+common.Reset, mpop.ConfigPath(), common.Reset)
		}
		mpop.DiscoverPeers(false)
	}

	commands := map[string]func([]string){
		"dashboard": cmdDashboard,
		"status":    cmdDashboard, // alias
		"peers":     cmdPeers,
		"exec":      cmdExec,
		"servers":   cmdServers,
		"info":      cmdInfo,
		"config":    cmdConfig,
		"init":      cmdInit,
		"vpn":       cmdVPN,
		"network":   cmdNetwork,
		"help":      cmdHelp,
		"-h":        cmdHelp,
		"--help":    cmdHelp,
	}

	if fn, ok := commands[cmd]; ok {
		fn(args)
	} else {
		fmt.Printf("  Unknown command: %s\n", cmd)
		cmdHelp(nil)
	}
}

func cmdDashboard(args []string) {
	statuses := mpop.GetAllServerStatus(5 * time.Second)
	if len(statuses) == 0 {
		fmt.Println()
		fmt.Println("  No servers configured or discovered.")
		fmt.Println("  Run 'mpop init' to create a configuration.")
		fmt.Println()
		return
	}
	mpop.PrintDashboard(statuses)
}

func cmdPeers(args []string) {
	fmt.Println()
	fmt.Printf("  %smpop peers%s\n", common.Cyan, common.Reset)
	fmt.Println()

	cfg, _ := wire.LoadConfig()
	if cfg == nil {
		fmt.Println("  wire not configured")
		return
	}

	peers, err := wire.GetPeers(cfg.ServerURL)
	if err != nil {
		fmt.Printf("  Failed to get peers: %v\n", err)
		return
	}

	// Sort by name
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].NodeName < peers[j].NodeName
	})

	now := time.Now()
	for _, p := range peers {
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

		var dot, nameCol string
		if online {
			dot = common.Green + "●" + common.Reset
			nameCol = common.Green + fmt.Sprintf("%-16s", p.NodeName) + common.Reset
		} else {
			dot = common.Red + "○" + common.Reset
			nameCol = common.Dim + fmt.Sprintf("%-16s", p.NodeName) + common.Reset
		}
		fmt.Printf("  %s %s  %s\n", dot, nameCol, p.VpnIP)
	}
	fmt.Println()
}

func cmdExec(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: mpop exec <server|all> <command...>")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  mpop exec node1 uname -a")
		fmt.Println("  mpop exec all uptime")
		os.Exit(1)
	}

	target := args[0]
	cmd := strings.Join(args[1:], " ")
	timeout := 30 * time.Second

	if target == "all" {
		servers := mpop.GetServers()
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)

		results := mpop.ParallelExec(names, cmd, timeout)
		for _, name := range names {
			output := results[name]
			fmt.Printf("\n%s--- %s ---%s\n", common.Cyan, name, common.Reset)
			fmt.Println(output)
		}
		return
	}

	result, err := mpop.RemoteExec(target, cmd, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
}

func cmdServers(args []string) {
	fmt.Println()
	fmt.Printf("  %sConfigured Servers%s\n", common.Cyan, common.Reset)
	fmt.Println()

	cfg, err := mpop.LoadConfig()
	if err != nil {
		fmt.Println("  No configuration found. Run 'mpop init'")
		return
	}

	// Sort by name
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		srv := cfg.Servers[name]
		ip := srv.IP
		if ip == "" {
			ip = srv.TailscaleIP
		}
		if ip == "" {
			ip = srv.PublicIP
		}

		role := srv.Role
		if role == "" {
			role = "-"
		}

		fmt.Printf("  %-12s  %-15s  %s\n", name, ip, role)
	}
	fmt.Println()
}

func cmdInfo(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: mpop info <server>")
		os.Exit(1)
	}

	name := args[0]
	servers := mpop.GetServers()
	ip, ok := servers[name]
	if !ok {
		fmt.Printf("Server not found: %s\n", name)
		os.Exit(1)
	}

	status := mpop.GetServerStatus(name, ip, 10*time.Second)

	fmt.Println()
	fmt.Printf("  %s%s%s (%s)\n", common.Cyan, name, common.Reset, ip)
	fmt.Println()
	if status.Online {
		fmt.Printf("  Status:  %s●%s Online\n", common.Green, common.Reset)
		fmt.Printf("  Load:    %s\n", status.Load)
		fmt.Printf("  Memory:  %s\n", status.Memory)
		fmt.Printf("  Disk:    %s\n", status.Disk)
		fmt.Printf("  Uptime:  %s\n", status.Uptime)
	} else {
		fmt.Printf("  Status:  %s○%s Offline\n", common.Red, common.Reset)
	}
	if status.Role != "" {
		fmt.Printf("  Role:    %s\n", status.Role)
	}
	fmt.Println()
}

func cmdConfig(args []string) {
	if len(args) < 1 {
		// Show current config
		cfg, err := mpop.LoadConfig()
		if err != nil {
			fmt.Println("No configuration found. Run 'mpop init'")
			return
		}

		fmt.Println()
		fmt.Printf("  %sConfiguration%s: %s\n", common.Cyan, common.Reset, mpop.ConfigPath())
		fmt.Println()
		fmt.Printf("  VPN:        %s\n", cfg.Connection.VPN)
		fmt.Printf("  SSH Method: %s\n", cfg.Connection.SSHMethod)
		fmt.Printf("  Servers:    %d\n", len(cfg.Servers))
		if len(cfg.Relays) > 0 {
			fmt.Printf("  Relays:     %s\n", strings.Join(cfg.Relays, ", "))
		}
		fmt.Println()
		return
	}

	// config set <key> <value>
	if args[0] == "set" && len(args) >= 3 {
		key := args[1]
		value := args[2]

		cfg, err := mpop.LoadConfig()
		if err != nil {
			cfg = &mpop.Config{}
		}

		switch key {
		case "vpn":
			cfg.Connection.VPN = value
		case "ssh_method":
			cfg.Connection.SSHMethod = value
		case "language":
			cfg.Language = value
		default:
			fmt.Printf("Unknown config key: %s\n", key)
			return
		}

		if err := mpop.SaveConfig(cfg); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
			return
		}
		fmt.Printf("Set %s = %s\n", key, value)
		return
	}

	fmt.Println("Usage: mpop config [set <key> <value>]")
}

func cmdInit(args []string) {
	path := mpop.ConfigPath()

	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Configuration already exists: %s\n", path)
		fmt.Println("Edit it manually or delete it to reinitialize.")
		return
	}

	mpop.EnsureConfig()
	fmt.Println()
	fmt.Printf("  %s✓%s Created %s\n", common.Green, common.Reset, path)
	fmt.Println()
	fmt.Println("  Edit the file to add your servers:")
	fmt.Println("    \"servers\": {")
	fmt.Println("      \"node1\": {\"ip\": \"10.98.x.x\", \"user\": \"root\"},")
	fmt.Println("      \"node2\": {\"ip\": \"10.98.x.x\", \"user\": \"root\"}")
	fmt.Println("    }")
	fmt.Println()
}

func cmdVPN(args []string) {
	fmt.Println()
	fmt.Printf("  %sVPN Status%s\n", common.Cyan, common.Reset)
	fmt.Println()

	vpnIP := mpop.GetVpnIP()
	vpnType := mpop.GetVPNType()

	fmt.Printf("  Type:     %s\n", vpnType)
	fmt.Printf("  Local IP: %s\n", vpnIP)

	// Get wire status if available
	if vpnType == "wire" {
		cfg, _ := wire.LoadConfig()
		if cfg != nil {
			fmt.Printf("  Server:   %s\n", cfg.ServerURL)
			fmt.Printf("  Node:     %s\n", cfg.NodeName)
			if cfg.NodeID != "" {
				fmt.Printf("  ID:       %s\n", cfg.NodeID[:16]+"...")
			}
		}
	}
	fmt.Println()
}

func cmdNetwork(args []string) {
	cmdVPN(args) // Same as vpn for now
}

func cmdHelp(args []string) {
	fmt.Println()
	fmt.Printf("  %smpop%s - MeshPOP Network Operations CLI (Go)\n", common.Cyan, common.Reset)
	fmt.Println()
	fmt.Println("  Usage: mpop [command] [options]")
	fmt.Println()
	fmt.Println("  Commands:")
	fmt.Println("    dashboard       Show server status dashboard (default)")
	fmt.Println("    peers           List VPN peers")
	fmt.Println("    exec <srv> <cmd>  Execute command on server")
	fmt.Println("    servers         List configured servers")
	fmt.Println("    info <server>   Show server details")
	fmt.Println("    vpn             Show VPN status")
	fmt.Println("    config          Show/set configuration")
	fmt.Println("    init            Create configuration")
	fmt.Println("    help            Show this help")
	fmt.Println()
	fmt.Println("  Examples:")
	fmt.Println("    mpop                        Show dashboard")
	fmt.Println("    mpop exec node1 uname -a    Run command on node1")
	fmt.Println("    mpop exec all uptime        Run on all servers")
	fmt.Println("    mpop peers                  Show VPN peers")
	fmt.Println()
	fmt.Println("  Environment:")
	fmt.Println("    WIRE_SERVER_URL    Wire coordinator URL")
	fmt.Println("    VSSH_SECRET        vssh authentication secret")
	fmt.Println()
}
