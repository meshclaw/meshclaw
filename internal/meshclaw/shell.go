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

// Intent patterns for natural language understanding
var intentPatterns = map[string][]string{
	"exit":   {"exit", "quit", "bye", "q", "종료", "나가기"},
	"help":   {"help", "?", "h", "도움", "도움말"},
	"nodes":  {"nodes", "node", "cluster", "servers", "노드", "서버", "클러스터"},
	"agents": {"agents", "agent", "list", "에이전트", "목록"},
	"ps":     {"ps", "status", "running", "상태", "실행중"},
	"news":   {"news", "뉴스", "headlines", "what's new", "what's happening"},
	"system": {"system", "health", "시스템", "헬스", "check"},
	"hello":  {"hello", "test", "ping", "테스트"},
}

// ParseInput parses natural language input into a command
func ParseInput(input string) *ShellCommand {
	rawInput := strings.TrimSpace(input)
	input = strings.ToLower(rawInput)
	words := strings.Fields(input)

	if len(words) == 0 {
		return nil
	}

	cmd := &ShellCommand{Raw: rawInput}

	// Check for question patterns first
	if isQuestion(input) {
		return parseQuestion(input, words, cmd)
	}

	// Direct command mappings
	switch words[0] {
	case "exit", "quit", "bye", "q", "종료":
		cmd.Action = "exit"
		return cmd

	case "help", "?", "h", "도움":
		cmd.Action = "help"
		return cmd

	case "nodes", "node", "cluster", "servers", "노드":
		cmd.Action = "nodes"
		return cmd

	case "agents", "agent", "list", "에이전트":
		cmd.Action = "agents"
		return cmd

	case "ps", "status", "running", "상태":
		cmd.Action = "ps"
		return cmd

	case "show", "보여줘", "보여":
		return parseShowCommand(words, cmd)

	case "check", "체크", "확인":
		return parseCheckCommand(words, cmd)
	}

	// Pattern matching for run commands
	return parseRunCommand(input, words)
}

// isQuestion checks if input looks like a question
func isQuestion(input string) bool {
	return strings.HasPrefix(input, "what") ||
		strings.HasPrefix(input, "how") ||
		strings.HasPrefix(input, "which") ||
		strings.HasPrefix(input, "뭐") ||
		strings.HasPrefix(input, "어떤") ||
		strings.HasSuffix(input, "?")
}

// parseQuestion handles question-like inputs
func parseQuestion(input string, words []string, cmd *ShellCommand) *ShellCommand {
	// "what's new" / "what's happening" → news
	if strings.Contains(input, "new") || strings.Contains(input, "happening") ||
		strings.Contains(input, "news") || strings.Contains(input, "뉴스") {
		cmd.Action = "run"
		cmd.Args = []string{"news"}
		return parseNodeModifiers(words, cmd)
	}

	// "how are the nodes" / "which nodes" → nodes
	if strings.Contains(input, "node") || strings.Contains(input, "노드") ||
		strings.Contains(input, "server") {
		cmd.Action = "nodes"
		return cmd
	}

	// "what's running" → ps
	if strings.Contains(input, "running") || strings.Contains(input, "실행") {
		cmd.Action = "ps"
		return cmd
	}

	// Default: treat as unknown
	cmd.Action = "unknown"
	return cmd
}

// parseShowCommand handles "show X" patterns
func parseShowCommand(words []string, cmd *ShellCommand) *ShellCommand {
	for _, w := range words[1:] {
		if containsAny([]string{w}, []string{"nodes", "node", "노드", "서버"}) {
			cmd.Action = "nodes"
			return cmd
		}
		if containsAny([]string{w}, []string{"agents", "에이전트"}) {
			cmd.Action = "agents"
			return cmd
		}
		if containsAny([]string{w}, []string{"news", "뉴스"}) {
			cmd.Action = "run"
			cmd.Args = []string{"news"}
			return parseNodeModifiers(words, cmd)
		}
	}
	cmd.Action = "nodes" // default: show nodes
	return cmd
}

// parseCheckCommand handles "check X" patterns
func parseCheckCommand(words []string, cmd *ShellCommand) *ShellCommand {
	cmd.Action = "run"
	cmd.Args = []string{"system"}
	return parseNodeModifiers(words, cmd)
}

// parseNodeModifiers extracts --on and --gpu from words
func parseNodeModifiers(words []string, cmd *ShellCommand) *ShellCommand {
	// Check for GPU requirement
	if containsAny(words, []string{"gpu", "cuda", "nvidia", "ml", "ai", "model"}) {
		cmd.Args = append(cmd.Args, "--gpu")
	}

	// Check for specific node
	for i, w := range words {
		if (w == "on" || w == "at" || w == "에서") && i+1 < len(words) {
			node := words[i+1]
			if strings.HasPrefix(node, "v") || strings.HasPrefix(node, "g") ||
				strings.HasPrefix(node, "d") || strings.HasPrefix(node, "s") ||
				strings.HasPrefix(node, "n") || strings.HasPrefix(node, "m") {
				cmd.Args = append(cmd.Args, "--on="+node)
			}
		}
	}
	return cmd
}

func parseRunCommand(input string, words []string) *ShellCommand {
	cmd := &ShellCommand{Raw: input, Action: "run"}

	// Korean agent name mappings
	koreanAgents := map[string]string{
		"뉴스": "news", "시스템": "system", "헬로": "hello",
		"테스트": "hello", "건강": "system", "체크": "system",
	}

	// Direct agent names
	agentNames := []string{"news", "system", "hello", "ml", "test"}
	for _, name := range agentNames {
		if containsWord(words, name) {
			cmd.Args = append(cmd.Args, name)
			break
		}
	}

	// Check Korean names
	if len(cmd.Args) == 0 {
		for _, w := range words {
			if agent, ok := koreanAgents[w]; ok {
				cmd.Args = append(cmd.Args, agent)
				break
			}
		}
	}

	// If no agent found, check for "run X" / "실행 X" pattern
	if len(cmd.Args) == 0 {
		for i, w := range words {
			if (w == "run" || w == "실행") && i+1 < len(words) {
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

	return parseNodeModifiers(words, cmd)
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
    ps / status       Show running agents

  Modifiers:
    ... on g1         Run on specific node
    ... with gpu      Run on GPU node

  Natural Language:
    "what's new"           → news agent
    "show me the nodes"    → cluster status
    "check system on g1"   → system check on g1
    "뉴스"                  → news agent (Korean)
    "노드 보여줘"           → cluster status

  Examples:
    > news
    > system on g1
    > what's happening?
    > show nodes
    > check health on d1

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
