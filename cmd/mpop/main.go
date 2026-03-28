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
		"details":   cmdDetails,
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

func cmdDetails(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: mpop details <server>")
		os.Exit(1)
	}

	name := args[0]
	servers := mpop.GetServers()
	ip, ok := servers[name]
	if !ok {
		fmt.Printf("Server not found: %s\n", name)
		os.Exit(1)
	}

	timeout := 15 * time.Second

	fmt.Println()
	fmt.Printf("  %s%s%s (%s)\n", common.Cyan, name, common.Reset, ip)
	fmt.Println()

	// System info
	fmt.Printf("  %s== System ==%s\n", common.Yellow, common.Reset)
	sysCmd := `hostname -f 2>/dev/null || hostname; uname -sr; cat /etc/os-release 2>/dev/null | grep -E "^(PRETTY_NAME|VERSION_ID)" | head -2 | cut -d= -f2 | tr -d '"' || sw_vers 2>/dev/null | grep -E "ProductName|ProductVersion" | awk '{print $2,$3}'`
	if out, err := mpop.RemoteExec(name, sysCmd, timeout); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
	}
	fmt.Println()

	// CPU
	fmt.Printf("  %s== CPU ==%s\n", common.Yellow, common.Reset)
	cpuCmd := `nproc 2>/dev/null || sysctl -n hw.ncpu; cat /proc/loadavg 2>/dev/null | awk '{print "Load: "$1" "$2" "$3}' || sysctl -n vm.loadavg 2>/dev/null | tr -d '{}' | awk '{print "Load: "$1" "$2" "$3}'`
	if out, err := mpop.RemoteExec(name, cpuCmd, timeout); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) >= 1 {
			fmt.Printf("  Cores: %s\n", lines[0])
		}
		if len(lines) >= 2 {
			fmt.Printf("  %s\n", lines[1])
		}
	}
	fmt.Println()

	// Memory
	fmt.Printf("  %s== Memory ==%s\n", common.Yellow, common.Reset)
	memCmd := `free -h 2>/dev/null | grep -E "Mem|Swap" || vm_stat 2>/dev/null | head -5`
	if out, err := mpop.RemoteExec(name, memCmd, timeout); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fmt.Printf("  %s\n", line)
		}
	}
	fmt.Println()

	// Disk
	fmt.Printf("  %s== Disk ==%s\n", common.Yellow, common.Reset)
	diskCmd := `df -h / /home 2>/dev/null | grep -v "^Filesystem" | awk '{print $6": "$3"/"$2" ("$5")"}'`
	if out, err := mpop.RemoteExec(name, diskCmd, timeout); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
	}
	fmt.Println()

	// Network
	fmt.Printf("  %s== Network ==%s\n", common.Yellow, common.Reset)
	netCmd := `ip -4 addr show 2>/dev/null | grep inet | grep -v "127.0.0.1" | awk '{print $NF": "$2}' || ifconfig 2>/dev/null | grep -E "^[a-z]|inet " | grep -B1 "inet " | grep -v "127.0.0.1" | paste - - | awk '{print $1" "$6}'`
	if out, err := mpop.RemoteExec(name, netCmd, timeout); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
	}
	fmt.Println()

	// Top processes
	fmt.Printf("  %s== Top Processes ==%s\n", common.Yellow, common.Reset)
	procCmd := `ps aux --sort=-%cpu 2>/dev/null | head -6 | tail -5 | awk '{printf "%-6s %-5s %-5s %s\n", $1, $3"%", $4"%", $11}' || ps aux -r 2>/dev/null | head -6 | tail -5 | awk '{printf "%-6s %-5s %-5s %s\n", $1, $3"%", $4"%", $11}'`
	if out, err := mpop.RemoteExec(name, procCmd, timeout); err == nil {
		fmt.Printf("  %sUSER   CPU   MEM   CMD%s\n", common.Dim, common.Reset)
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
	}
	fmt.Println()

	// Services
	fmt.Printf("  %s== Services ==%s\n", common.Yellow, common.Reset)
	svcCmd := `systemctl list-units --type=service --state=running 2>/dev/null | grep -E "wire|vssh|worker|docker|nginx|mysql|postgres|redis|mongo" | awk '{print $1": "$4}' | head -10 || launchctl list 2>/dev/null | grep -E "meshclaw|wire|vssh" | awk '{print $3": running"}'`
	if out, err := mpop.RemoteExec(name, svcCmd, timeout); err == nil && strings.TrimSpace(out) != "" {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
	} else {
		fmt.Printf("  %s(no monitored services)%s\n", common.Dim, common.Reset)
	}
	fmt.Println()

	// Docker containers
	fmt.Printf("  %s== Docker ==%s\n", common.Yellow, common.Reset)
	dockerCmd := `docker ps --format "{{.Names}}: {{.Status}}" 2>/dev/null | head -10`
	if out, err := mpop.RemoteExec(name, dockerCmd, timeout); err == nil && strings.TrimSpace(out) != "" {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
	} else {
		fmt.Printf("  %s(no containers)%s\n", common.Dim, common.Reset)
	}
	fmt.Println()

	// Listening ports
	fmt.Printf("  %s== Listening Ports ==%s\n", common.Yellow, common.Reset)
	portCmd := `ss -tlnp 2>/dev/null | grep LISTEN | awk '{print $4}' | sed 's/.*://' | sort -n | uniq | head -15 | tr '\n' ' ' || netstat -tlnp 2>/dev/null | grep LISTEN | awk '{print $4}' | sed 's/.*://' | sort -n | uniq | head -15 | tr '\n' ' '`
	if out, err := mpop.RemoteExec(name, portCmd, timeout); err == nil && strings.TrimSpace(out) != "" {
		fmt.Printf("  %s\n", strings.TrimSpace(out))
	}
	fmt.Println()

	// GPU (if available)
	gpuCmd := `nvidia-smi --query-gpu=name,memory.used,memory.total,utilization.gpu --format=csv,noheader 2>/dev/null | head -4`
	if out, err := mpop.RemoteExec(name, gpuCmd, timeout); err == nil && strings.TrimSpace(out) != "" {
		fmt.Printf("  %s== GPU ==%s\n", common.Yellow, common.Reset)
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
		fmt.Println()
	}

	// Recent logins
	fmt.Printf("  %s== Recent Logins ==%s\n", common.Yellow, common.Reset)
	loginCmd := `last -n 5 2>/dev/null | head -5 | awk '{print $1" "$3" "$4" "$5" "$6}'`
	if out, err := mpop.RemoteExec(name, loginCmd, timeout); err == nil && strings.TrimSpace(out) != "" {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
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
