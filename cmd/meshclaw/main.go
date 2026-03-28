package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/common"
	"github.com/meshclaw/meshclaw/internal/meshclaw"
)

const Version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		cmdHelp(nil)
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	commands := map[string]func([]string){
		"init":      cmdInit,
		"start":     cmdStart,
		"up":        cmdStart, // alias
		"stop":      cmdStop,
		"down":      cmdStop, // alias
		"restart":   cmdRestart,
		"ps":        cmdPS,
		"list":      cmdPS, // alias
		"logs":      cmdLogs,
		"ask":       cmdAsk,
		"chat":      cmdChat,
		"webchat":   cmdWebChat,
		"run":       cmdRun,
		"batch":     cmdBatch, // run batch agent locally
		"_daemon":   cmdDaemon, // internal: run agent loop
		"nodes":     cmdNodes,
		"agents":    cmdAgentsList, // list available agents
		"remote-up": cmdRemoteUp,
		"sync-keys": cmdSyncKeys,
		"templates": cmdTemplates,
		"help":      cmdHelp,
		"-h":        cmdHelp,
		"--help":    cmdHelp,
		"--version": cmdVersion,
		"-v":        cmdVersion,
		"version":   cmdVersion,
	}

	if fn, ok := commands[cmd]; ok {
		fn(args)
	} else {
		fmt.Printf("Unknown command: %s\n", cmd)
		cmdHelp(nil)
	}
}

func cmdVersion(args []string) {
	fmt.Printf("meshclaw v%s (Go)\n", Version)
}

func cmdInit(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw init <name> [--template <template>]")
		fmt.Println()
		fmt.Println("Templates:")
		for _, name := range meshclaw.ListTemplates() {
			tmpl := meshclaw.GetTemplate(name)
			fmt.Printf("  %-16s %s\n", name, tmpl.Description)
		}
		os.Exit(1)
	}

	name := args[0]
	template := "assistant"

	// Parse --template flag
	for i, arg := range args {
		if arg == "--template" || arg == "-t" {
			if i+1 < len(args) {
				template = args[i+1]
			}
		}
	}

	configPath, err := meshclaw.InitFromTemplate(template, name, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s✓%s Created agent '%s' from template '%s'\n",
		common.Green, common.Reset, name, template)
	fmt.Printf("  Config: %s\n", configPath)
	fmt.Println()
	fmt.Printf("  Start with: meshclaw start %s\n", name)
	fmt.Printf("  Chat with:  meshclaw chat %s\n", name)
}

func cmdStart(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw start <name|config.json>")
		os.Exit(1)
	}

	target := args[0]
	foreground := false

	for _, arg := range args[1:] {
		if arg == "--foreground" || arg == "-f" {
			foreground = true
		}
	}

	// Find config path
	configPath := target
	if !strings.HasSuffix(target, ".json") {
		// Look in agents directory
		configPath = meshclaw.AgentsDir() + "/" + target + "/config.json"
	}

	state, err := meshclaw.StartAgent(configPath, foreground)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if !foreground {
		fmt.Printf("%s✓%s Started agent '%s' (PID %d)\n",
			common.Green, common.Reset, state.Name, state.PID)
		fmt.Printf("  Socket: %s\n", state.Socket)
		fmt.Println()
		fmt.Printf("  Chat:  meshclaw chat %s\n", state.Name)
		fmt.Printf("  Logs:  meshclaw logs %s\n", state.Name)
		fmt.Printf("  Stop:  meshclaw stop %s\n", state.Name)
	}
}

func cmdStop(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw stop <name>")
		os.Exit(1)
	}

	name := args[0]

	if err := meshclaw.StopAgent(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s✓%s Stopped agent '%s'\n", common.Green, common.Reset, name)
}

func cmdRestart(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw restart <name>")
		os.Exit(1)
	}

	name := args[0]

	// Stop if running
	meshclaw.StopAgent(name)
	time.Sleep(500 * time.Millisecond)

	// Find config and start
	configPath := meshclaw.AgentsDir() + "/" + name + "/config.json"
	state, err := meshclaw.StartAgent(configPath, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s✓%s Restarted agent '%s' (PID %d)\n",
		common.Green, common.Reset, state.Name, state.PID)
}

func cmdPS(args []string) {
	agents, err := meshclaw.ListAgents()
	if err != nil || len(agents) == 0 {
		fmt.Println("No agents found.")
		fmt.Println("  Create one with: meshclaw init <name>")
		return
	}

	fmt.Println()
	fmt.Printf("  %s%-16s  %-8s  %-6s  %s%s\n",
		common.Dim, "NAME", "STATUS", "PID", "SOCKET", common.Reset)
	fmt.Printf("  %s%s%s\n", common.Dim, strings.Repeat("-", 60), common.Reset)

	// Sort by name
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	for _, w := range agents {
		var dot, statusCol string
		if w.Status == "running" {
			dot = common.Green + "●" + common.Reset
			statusCol = common.Green + w.Status + common.Reset
		} else {
			dot = common.Red + "○" + common.Reset
			statusCol = common.Dim + w.Status + common.Reset
		}

		pid := "-"
		if w.PID > 0 && w.Status == "running" {
			pid = fmt.Sprintf("%d", w.PID)
		}

		fmt.Printf("  %s %-16s  %-8s  %-6s  %s\n",
			dot, w.Name, statusCol, pid, w.Socket)
	}
	fmt.Println()
}

func cmdLogs(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw logs <name> [lines]")
		os.Exit(1)
	}

	name := args[0]
	lines := 50

	if len(args) > 1 {
		fmt.Sscanf(args[1], "%d", &lines)
	}

	logs, err := meshclaw.GetLogs(name, lines)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(logs)
}

func cmdAsk(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: meshclaw ask <name> <message>")
		os.Exit(1)
	}

	name := args[0]
	message := strings.Join(args[1:], " ")

	response, err := meshclaw.AskAgent(name, message, 60*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(response)
}

func cmdChat(args []string) {
	// No args = system shell
	if len(args) < 1 {
		meshclaw.RunShell()
		return
	}

	name := args[0]

	// Check if worker is running
	state, err := meshclaw.LoadState(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Agent not found. Start it with: meshclaw start %s\n", name, name)
		os.Exit(1)
	}

	if !meshclaw.IsProcessRunning(state.PID) {
		fmt.Fprintf(os.Stderr, "Worker '%s' is not running. Start it with: meshclaw start %s\n", name, name)
		os.Exit(1)
	}

	fmt.Printf("%sChat with %s%s (Ctrl+C to exit)\n\n", common.Cyan, name, common.Reset)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(common.Green + "you> " + common.Reset)
		if !scanner.Scan() {
			break
		}

		message := strings.TrimSpace(scanner.Text())
		if message == "" {
			continue
		}

		if message == "exit" || message == "quit" {
			break
		}

		response, err := meshclaw.AskAgent(name, message, 120*time.Second)
		if err != nil {
			fmt.Printf("%sError: %v%s\n", common.Red, err, common.Reset)
			continue
		}

		fmt.Printf("%s%s%s> %s\n\n", common.Cyan, name, common.Reset, response)
	}
}

func cmdRun(args []string) {
	// Distributed agent execution
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw run <agent> [options]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  --on=<node>    Run on specific node")
		fmt.Println("  --gpu          Require GPU")
		fmt.Println("  --dry-run      Show selected node without running")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  meshclaw run news-agent")
		fmt.Println("  meshclaw run ml-agent --gpu")
		fmt.Println("  meshclaw run my-agent --on=g1")
		os.Exit(1)
	}

	agentName := args[0]
	req := meshclaw.NodeRequirement{}
	dryRun := false

	// Parse options
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "--on=") {
			req.Prefer = strings.TrimPrefix(arg, "--on=")
		} else if arg == "--gpu" {
			req.GPU = true
		} else if arg == "--dry-run" {
			dryRun = true
		}
	}

	// Select best nodes
	candidates, err := meshclaw.SelectNode(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if dryRun {
		fmt.Printf("%sSelected nodes for '%s':%s\n\n", common.Cyan, agentName, common.Reset)
		for i, c := range candidates {
			if i >= 5 {
				break
			}
			marker := " "
			if i == 0 {
				marker = common.Green + "→" + common.Reset
			}
			fmt.Printf("  %s %-8s  score=%.1f  cpu=%d%%  mem=%d%%  load=%.2f\n",
				marker, c.Peer.NodeName, c.Score,
				c.Peer.Stats.CPUPct, c.Peer.Stats.MemPct, c.Peer.Stats.LoadValue)
		}
		fmt.Println()
		return
	}

	// Run with streaming output
	fmt.Printf("%sRunning '%s' on cluster...%s\n", common.Cyan, agentName, common.Reset)

	var selectedNode string
	result, err := meshclaw.RunAgentStream(agentName, req, func(node, line string) {
		if selectedNode == "" {
			selectedNode = node
			fmt.Printf("%s✓%s Agent running on %s%s%s\n",
				common.Green, common.Reset, common.Cyan, node, common.Reset)
		}
		fmt.Printf("  %s[%s]%s %s\n", common.Dim, node, common.Reset, line)
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "%sError: %v%s\n", common.Red, err, common.Reset)
		os.Exit(1)
	}

	if !result.Success {
		fmt.Fprintf(os.Stderr, "%s✗%s Failed on %s: %s\n",
			common.Red, common.Reset, result.Node, result.Error)
		os.Exit(1)
	}
}

func cmdDaemon(args []string) {
	// Internal command: run agent loop in foreground (used by daemon)
	if len(args) < 1 {
		os.Exit(1)
	}

	configPath := args[0]
	cfg, err := meshclaw.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	socketPath := meshclaw.SocketPath(cfg.Name)
	meshclaw.RunAgentLoop(cfg, socketPath)
}

func cmdNodes(args []string) {
	stats, err := meshclaw.GetNodeStats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("  %s%-10s %-15s %5s %5s %5s %4s %s%s\n",
		common.Dim, "NODE", "IP", "CPU", "MEM", "LOAD", "GPU", "STATUS", common.Reset)
	fmt.Printf("  %s%s%s\n", common.Dim, strings.Repeat("-", 60), common.Reset)

	for _, s := range stats {
		var dot, status string
		if s.Online {
			dot = common.Green + "●" + common.Reset
			status = "ready"
			if s.Reserved {
				dot = common.Yellow + "●" + common.Reset
				status = "busy"
			}
		} else {
			dot = common.Red + "○" + common.Reset
			status = "offline"
		}

		gpu := "-"
		if s.GPU > 0 {
			gpu = fmt.Sprintf("%d", s.GPU)
		}

		fmt.Printf("  %s %-10s %-15s %4d%% %4d%% %5.2f %4s %s\n",
			dot, s.Name, s.IP, s.CPU, s.Mem, s.Load, gpu, status)
	}
	fmt.Println()
}

func cmdWebChat(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw webchat <name> [--port <port>]")
		os.Exit(1)
	}

	name := args[0]
	port := 8080

	// Parse --port flag
	for i, arg := range args {
		if (arg == "--port" || arg == "-p") && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &port)
		}
	}

	// Load config
	configPath := meshclaw.AgentsDir() + "/" + name + "/config.json"
	cfg, err := meshclaw.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Override webchat config
	cfg.WebChat = &meshclaw.WebChatConfig{
		Port: port,
		Host: "0.0.0.0",
	}

	fmt.Printf("%sStarting webchat for %s on port %d%s\n",
		common.Cyan, name, port, common.Reset)
	fmt.Printf("Open: http://localhost:%d\n\n", port)

	server := meshclaw.NewWebChatServer(cfg)
	if err := server.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func cmdRemoteUp(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: meshclaw remote-up <host> <template|name>")
		fmt.Println()
		fmt.Println("Deploy a worker to a remote server via SSH.")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  meshclaw remote-up 192.168.1.100 system-monitor")
		fmt.Println("  meshclaw remote-up root@server.local assistant")
		os.Exit(1)
	}

	host := args[0]
	template := args[1]

	fmt.Printf("%sDeploying %s to %s...%s\n", common.Cyan, template, host, common.Reset)

	// Check if meshclaw is installed on remote
	checkCmd := "which meshclaw || echo 'not found'"
	result := sshExec(host, checkCmd)
	if strings.Contains(result, "not found") {
		fmt.Println("Installing meshclaw on remote...")
		sshExec(host, "curl -sL https://raw.githubusercontent.com/meshclaw/meshclaw/main/install.sh | bash")
	}

	// Initialize worker on remote
	fmt.Printf("Initializing %s...\n", template)
	sshExec(host, fmt.Sprintf("meshclaw init %s --template %s", template, template))

	// Start worker on remote
	fmt.Printf("Starting %s...\n", template)
	sshExec(host, fmt.Sprintf("meshclaw start %s", template))

	fmt.Printf("%s✓%s Deployed %s to %s\n", common.Green, common.Reset, template, host)
	fmt.Printf("  Check: ssh %s 'meshclaw ps'\n", host)
	fmt.Printf("  Ask:   ssh %s 'meshclaw ask %s \"status\"'\n", host, template)
}

func sshExec(host, cmd string) string {
	sshCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no %s '%s'", host, cmd)
	out, _ := execCommand("sh", "-c", sshCmd)
	return out
}

func execCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func cmdTemplates(args []string) {
	fmt.Println()
	fmt.Printf("  %sAvailable Templates%s\n", common.Cyan, common.Reset)
	fmt.Println()

	templates := meshclaw.ListTemplates()
	sort.Strings(templates)

	for _, name := range templates {
		tmpl := meshclaw.GetTemplate(name)
		fmt.Printf("  %-16s %s\n", name, tmpl.Description)
	}
	fmt.Println()
	fmt.Println("  Usage: meshclaw init <name> --template <template>")
	fmt.Println()
}

func cmdHelp(args []string) {
	fmt.Println()
	fmt.Printf("  %smeshclaw%s - AI Worker Runtime (Go)\n", common.Cyan, common.Reset)
	fmt.Println()
	fmt.Println("  Run AI assistants anywhere. No cloud dependency, no Docker required.")
	fmt.Println()
	fmt.Println("  Usage: meshclaw [command] [options]")
	fmt.Println()
	fmt.Println("  Commands:")
	fmt.Println("    init <name>           Create agent from template")
	fmt.Println("    start <name>          Start agent locally")
	fmt.Println("    stop <name>           Stop agent")
	fmt.Println("    restart <name>        Restart agent")
	fmt.Println("    ps                    List local agents")
	fmt.Println("    logs <name>           View agent logs")
	fmt.Println("    ask <name> <msg>      Send message, get response")
	fmt.Println("    chat <name>           Interactive chat")
	fmt.Println("    webchat <name>        Web UI for chat")
	fmt.Println()
	fmt.Println("  Distributed:")
	fmt.Println("    run <agent>           Run agent on best node")
	fmt.Println("    run <agent> --gpu     Run on node with GPU")
	fmt.Println("    run <agent> --on=g1   Run on specific node")
	fmt.Println("    nodes                 Show cluster node stats")
	fmt.Println()
	fmt.Println("  Other:")
	fmt.Println("    remote-up <host> <t>  Deploy to remote server")
	fmt.Println("    templates             List available templates")
	fmt.Println("    help                  Show this help")
	fmt.Println()
	fmt.Println("  Quick Start:")
	fmt.Println("    export ANTHROPIC_API_KEY=sk-ant-...")
	fmt.Println("    meshclaw init my-bot")
	fmt.Println("    meshclaw start my-bot")
	fmt.Println("    meshclaw chat my-bot")
	fmt.Println()
	fmt.Println("  Templates:")
	for _, name := range meshclaw.ListTemplates() {
		tmpl := meshclaw.GetTemplate(name)
		fmt.Printf("    %-16s %s\n", name, tmpl.Description)
	}
	fmt.Println()
}

func cmdBatch(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw batch <agent>")
		fmt.Println()
		fmt.Println("Run a batch agent locally and exit.")
		fmt.Println()
		fmt.Println("Available agents:")
		for _, name := range meshclaw.ListBatchAgents() {
			agent := meshclaw.BuiltinBatchAgents[name]
			fmt.Printf("  %-12s %s\n", name, agent.Description)
		}
		os.Exit(1)
	}

	agentName := args[0]

	output, err := meshclaw.RunBatchAgent(agentName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sError: %v%s\n", common.Red, err, common.Reset)
		os.Exit(1)
	}

	fmt.Println(output)
}

func cmdSyncKeys(args []string) {
	// Get API key from local keychain (never print key!)
	apiKey := ""

	// 1. Try macOS Keychain
	if out, err := exec.Command("security", "find-generic-password", "-s", "anthropic-api-key", "-w").Output(); err == nil {
		apiKey = strings.TrimSpace(string(out))
	}

	// 2. Fallback to environment
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "%sError: No API key found in keychain or env%s\n", common.Red, common.Reset)
		fmt.Println("Set key: security add-generic-password -a $USER -s anthropic-api-key -w 'sk-ant-...'")
		os.Exit(1)
	}

	// Validate key format (never show actual key)
	if !strings.HasPrefix(apiKey, "sk-ant-") {
		fmt.Fprintf(os.Stderr, "%sWarning: Key doesn't look like Anthropic key%s\n", common.Yellow, common.Reset)
	}

	fmt.Printf("%sSyncing API key to cluster...%s\n", common.Cyan, common.Reset)
	fmt.Printf("  Key: %s...%s (masked)\n", apiKey[:12], apiKey[len(apiKey)-4:])

	// Get all nodes via mpop
	peers, err := meshclaw.GetClusterNodes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sError getting nodes: %v%s\n", common.Red, err, common.Reset)
		os.Exit(1)
	}

	if len(peers) == 0 {
		fmt.Println("No nodes found. Is mpop running?")
		os.Exit(1)
	}

	// Sync to each node (parallel)
	type result struct {
		node    string
		success bool
		err     error
	}
	results := make(chan result, len(peers))

	for _, peer := range peers {
		go func(nodeName, ip string) {
			// Create ~/.meshclaw/env with key
			// Safe: create dir, write key only to dedicated file
			cmd := fmt.Sprintf("mkdir -p ~/.meshclaw && echo 'ANTHROPIC_API_KEY=%s' > ~/.meshclaw/env && chmod 600 ~/.meshclaw/env", apiKey)

			// Use vssh exec (handles SSH config fallback automatically)
			_, err := execCommand("vssh", "exec", nodeName, cmd)

			results <- result{node: nodeName, success: err == nil, err: err}
		}(peer.Name, peer.IP)
	}

	// Collect results
	success := 0
	failed := 0
	for i := 0; i < len(peers); i++ {
		r := <-results
		if r.success {
			fmt.Printf("  %s✓%s %s\n", common.Green, common.Reset, r.node)
			success++
		} else {
			fmt.Printf("  %s✗%s %s (error)\n", common.Red, common.Reset, r.node)
			failed++
		}
	}

	fmt.Println()
	fmt.Printf("Synced to %d/%d nodes\n", success, len(peers))
	if failed > 0 {
		fmt.Printf("%s%d nodes failed%s\n", common.Yellow, failed, common.Reset)
	}
}

func cmdAgentsList(args []string) {
	fmt.Println()
	fmt.Printf("  %sBatch Agents%s (run once, exit)\n", common.Cyan, common.Reset)
	fmt.Println()
	for _, name := range meshclaw.ListBatchAgents() {
		agent := meshclaw.BuiltinBatchAgents[name]
		fmt.Printf("  %-12s %s\n", name, agent.Description)
	}
	fmt.Println()
	fmt.Printf("  %sService Agents%s (long-running)\n", common.Cyan, common.Reset)
	fmt.Println()
	for _, name := range meshclaw.ListTemplates() {
		tmpl := meshclaw.GetTemplate(name)
		fmt.Printf("  %-12s %s\n", name, tmpl.Description)
	}
	fmt.Println()
	fmt.Println("  Run batch:   meshclaw run <agent>")
	fmt.Println("  Run local:   meshclaw batch <agent>")
	fmt.Println("  Start svc:   meshclaw start <agent>")
	fmt.Println()
}
