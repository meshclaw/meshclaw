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
		"remote-up": cmdRemoteUp,
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

	fmt.Printf("%s✓%s Created worker '%s' from template '%s'\n",
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
		// Look in workers directory
		configPath = meshclaw.WorkersDir() + "/" + target + "/config.json"
	}

	state, err := meshclaw.StartWorker(configPath, foreground)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if !foreground {
		fmt.Printf("%s✓%s Started worker '%s' (PID %d)\n",
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

	if err := meshclaw.StopWorker(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s✓%s Stopped worker '%s'\n", common.Green, common.Reset, name)
}

func cmdRestart(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw restart <name>")
		os.Exit(1)
	}

	name := args[0]

	// Stop if running
	meshclaw.StopWorker(name)
	time.Sleep(500 * time.Millisecond)

	// Find config and start
	configPath := meshclaw.WorkersDir() + "/" + name + "/config.json"
	state, err := meshclaw.StartWorker(configPath, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s✓%s Restarted worker '%s' (PID %d)\n",
		common.Green, common.Reset, state.Name, state.PID)
}

func cmdPS(args []string) {
	workers, err := meshclaw.ListWorkers()
	if err != nil || len(workers) == 0 {
		fmt.Println("No workers found.")
		fmt.Println("  Create one with: meshclaw init <name>")
		return
	}

	fmt.Println()
	fmt.Printf("  %s%-16s  %-8s  %-6s  %s%s\n",
		common.Dim, "NAME", "STATUS", "PID", "SOCKET", common.Reset)
	fmt.Printf("  %s%s%s\n", common.Dim, strings.Repeat("-", 60), common.Reset)

	// Sort by name
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].Name < workers[j].Name
	})

	for _, w := range workers {
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

	response, err := meshclaw.AskWorker(name, message, 60*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(response)
}

func cmdChat(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw chat <name>")
		os.Exit(1)
	}

	name := args[0]

	// Check if worker is running
	state, err := meshclaw.LoadState(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Worker '%s' not found. Start it with: meshclaw start %s\n", name, name)
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

		response, err := meshclaw.AskWorker(name, message, 120*time.Second)
		if err != nil {
			fmt.Printf("%sError: %v%s\n", common.Red, err, common.Reset)
			continue
		}

		fmt.Printf("%s%s%s> %s\n\n", common.Cyan, name, common.Reset, response)
	}
}

func cmdRun(args []string) {
	// Internal command: run worker in foreground (used by daemon)
	if len(args) < 1 {
		fmt.Println("Usage: meshclaw run <config.json>")
		os.Exit(1)
	}

	configPath := args[0]
	cfg, err := meshclaw.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	socketPath := meshclaw.SocketPath(cfg.Name)
	meshclaw.RunWorkerLoop(cfg, socketPath)
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
	configPath := meshclaw.WorkersDir() + "/" + name + "/config.json"
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
	fmt.Println("    init <name>           Create worker from template")
	fmt.Println("    start <name>          Start worker (background)")
	fmt.Println("    stop <name>           Stop worker")
	fmt.Println("    restart <name>        Restart worker")
	fmt.Println("    ps                    List workers")
	fmt.Println("    logs <name>           View worker logs")
	fmt.Println("    ask <name> <msg>      Send message, get response")
	fmt.Println("    chat <name>           Interactive chat")
	fmt.Println("    webchat <name>        Web UI for chat")
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
