package meshclaw

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ShellCommand represents a parsed command
type ShellCommand struct {
	Action string   // run, nodes, status, help, etc.
	Args   []string // additional arguments
	Raw    string   // original input
}

// ParseInput parses natural language input into a command
func ParseInput(input string) *ShellCommand {
	input = strings.TrimSpace(strings.ToLower(input))
	words := strings.Fields(input)

	if len(words) == 0 {
		return nil
	}

	cmd := &ShellCommand{Raw: input}

	// Direct command mappings
	switch words[0] {
	case "exit", "quit", "bye", "q":
		cmd.Action = "exit"
		return cmd

	case "help", "?", "h":
		cmd.Action = "help"
		return cmd

	case "nodes", "node", "cluster", "servers":
		cmd.Action = "nodes"
		return cmd

	case "agents", "agent", "list":
		cmd.Action = "agents"
		return cmd

	case "ps", "status", "running":
		cmd.Action = "ps"
		return cmd
	}

	// Pattern matching for run commands
	return parseRunCommand(input, words)
}

func parseRunCommand(input string, words []string) *ShellCommand {
	cmd := &ShellCommand{Raw: input, Action: "run"}

	// Direct agent names
	agentNames := []string{"news", "system", "hello", "ml", "test"}
	for _, name := range agentNames {
		if containsWord(words, name) {
			cmd.Args = append(cmd.Args, name)
			break
		}
	}

	// If no agent found, check for "run X" pattern
	if len(cmd.Args) == 0 {
		for i, w := range words {
			if w == "run" && i+1 < len(words) {
				cmd.Args = append(cmd.Args, words[i+1])
				break
			}
		}
	}

	// Still no agent? Unknown command
	if len(cmd.Args) == 0 {
		cmd.Action = "unknown"
		return cmd
	}

	// Check for GPU requirement
	if containsAny(words, []string{"gpu", "cuda", "nvidia", "ml", "ai", "model"}) {
		cmd.Args = append(cmd.Args, "--gpu")
	}

	// Check for specific node
	for i, w := range words {
		if (w == "on" || w == "at") && i+1 < len(words) {
			node := words[i+1]
			// Common node prefixes
			if strings.HasPrefix(node, "v") || strings.HasPrefix(node, "g") ||
				strings.HasPrefix(node, "d") || strings.HasPrefix(node, "s") ||
				strings.HasPrefix(node, "n") {
				cmd.Args = append(cmd.Args, "--on="+node)
			}
		}
	}

	return cmd
}

func containsWord(words []string, target string) bool {
	for _, w := range words {
		if w == target {
			return true
		}
	}
	return false
}

func containsAny(words []string, targets []string) bool {
	for _, w := range words {
		for _, t := range targets {
			if w == t {
				return true
			}
		}
	}
	return false
}

// ExecuteCommand executes a parsed command
func ExecuteCommand(cmd *ShellCommand) (string, error) {
	switch cmd.Action {
	case "exit":
		return "", fmt.Errorf("exit")

	case "help":
		return shellHelp(), nil

	case "nodes":
		return runMeshclaw("nodes")

	case "agents":
		return runMeshclaw("agents")

	case "ps":
		return runMeshclaw("ps")

	case "run":
		args := append([]string{"run"}, cmd.Args...)
		return runMeshclaw(args...)

	case "unknown":
		return fmt.Sprintf("Unknown command: %s\nType 'help' for available commands.", cmd.Raw), nil

	default:
		return "", fmt.Errorf("unhandled action: %s", cmd.Action)
	}
}

func runMeshclaw(args ...string) (string, error) {
	exe, _ := os.Executable()
	c := exec.Command(exe, args...)
	c.Env = os.Environ()
	output, err := c.CombinedOutput()
	return string(output), err
}

func shellHelp() string {
	return `
  meshclaw shell - Natural language interface

  Commands:
    news              Run news agent
    system            Run system health check
    hello             Run hello test agent
    nodes             Show cluster nodes
    agents            List available agents
    ps                Show running agents

  Modifiers:
    ... on g1         Run on specific node
    ... with gpu      Run on GPU node

  Examples:
    > news
    > system on g1
    > run hello with gpu
    > nodes
    > help

  Type 'exit' to quit.
`
}

// RunShell starts the interactive shell
func RunShell() {
	fmt.Println()
	fmt.Println("  \033[36mmeshclaw\033[0m - AI Agent Platform")
	fmt.Println("  Type 'help' for commands, 'exit' to quit.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("\033[32m>\033[0m ")

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		cmd := ParseInput(input)
		if cmd == nil {
			continue
		}

		output, err := ExecuteCommand(cmd)
		if err != nil {
			if err.Error() == "exit" {
				fmt.Println("Bye!")
				break
			}
			fmt.Printf("\033[91mError: %v\033[0m\n", err)
			continue
		}

		fmt.Print(output)
	}
}
