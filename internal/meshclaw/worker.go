package meshclaw

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StartWorker starts a worker process
func StartWorker(configPath string, foreground bool) (*WorkerState, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.Name == "" {
		// Use config filename as name
		cfg.Name = strings.TrimSuffix(filepath.Base(configPath), filepath.Ext(configPath))
	}

	// Check if already running
	if state, err := LoadState(cfg.Name); err == nil {
		if IsProcessRunning(state.PID) {
			return nil, fmt.Errorf("worker %s is already running (PID %d)", cfg.Name, state.PID)
		}
	}

	// Create work directory
	workDir := filepath.Join(WorkersDir(), cfg.Name)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, err
	}

	// Start the worker process
	logFile := filepath.Join(workDir, "worker.log")
	socketPath := SocketPath(cfg.Name)

	// Remove old socket if exists
	os.Remove(socketPath)

	if foreground {
		// Run in foreground
		return runWorkerForeground(cfg, configPath, socketPath, logFile)
	}

	// Run as daemon
	return runWorkerDaemon(cfg, configPath, socketPath, logFile)
}

func runWorkerDaemon(cfg *Config, configPath, socketPath, logFile string) (*WorkerState, error) {
	// Get the meshclaw binary path
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}

	// Start worker process
	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(exe, "run", configPath)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session (detach)
	}

	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}

	state := &WorkerState{
		Name:      cfg.Name,
		PID:       cmd.Process.Pid,
		Socket:    socketPath,
		Status:    "running",
		StartTime: time.Now().Unix(),
		Config:    configPath,
	}

	if err := SaveState(state); err != nil {
		return nil, err
	}

	// Detach the process
	cmd.Process.Release()
	log.Close()

	return state, nil
}

func runWorkerForeground(cfg *Config, configPath, socketPath, logFile string) (*WorkerState, error) {
	state := &WorkerState{
		Name:      cfg.Name,
		PID:       os.Getpid(),
		Socket:    socketPath,
		Status:    "running",
		StartTime: time.Now().Unix(),
		Config:    configPath,
	}

	if err := SaveState(state); err != nil {
		return nil, err
	}

	// Run the worker loop
	RunWorkerLoop(cfg, socketPath)
	return state, nil
}

// RunWorkerLoop runs the main worker loop
func RunWorkerLoop(cfg *Config, socketPath string) {
	// Create Unix socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create socket: %v\n", err)
		return
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	fmt.Printf("[%s] Worker started, listening on %s\n", cfg.Name, socketPath)

	// Handle connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn, cfg)
	}
}

func handleConnection(conn net.Conn, cfg *Config) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	message, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return
	}

	message = strings.TrimSpace(message)
	if message == "" {
		return
	}

	// Process the message with LLM
	response := processMessage(cfg, message)
	conn.Write([]byte(response + "\n"))
}

func processMessage(cfg *Config, message string) string {
	// Call LLM based on provider
	switch cfg.Provider {
	case "anthropic":
		return callAnthropic(cfg, message)
	case "ollama":
		return callOllama(cfg, message)
	default:
		return "Unknown provider: " + cfg.Provider
	}
}

func callAnthropic(cfg *Config, message string) string {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return "ERROR: ANTHROPIC_API_KEY not set"
	}

	// Build request
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful assistant."
	}

	reqBody := map[string]interface{}{
		"model":      cfg.Model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": message},
		},
	}

	// Add tools if configured
	if len(cfg.Tools) > 0 {
		tools := []map[string]interface{}{}
		for _, t := range cfg.Tools {
			if t == "bash" {
				tools = append(tools, map[string]interface{}{
					"name":        "bash",
					"description": "Run a bash command and get the output",
					"input_schema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"command": map[string]interface{}{
								"type":        "string",
								"description": "The command to run",
							},
						},
						"required": []string{"command"},
					},
				})
			}
		}
		if len(tools) > 0 {
			reqBody["tools"] = tools
		}
	}

	data, _ := json.Marshal(reqBody)

	cmd := exec.Command("curl", "-s",
		"https://api.anthropic.com/v1/messages",
		"-H", "Content-Type: application/json",
		"-H", "x-api-key: "+apiKey,
		"-H", "anthropic-version: 2023-06-01",
		"-d", string(data))

	output, err := cmd.Output()
	if err != nil {
		return "ERROR: API call failed: " + err.Error()
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(output, &resp); err != nil {
		return "ERROR: Failed to parse response"
	}

	if resp.Error.Message != "" {
		return "ERROR: " + resp.Error.Message
	}

	for _, c := range resp.Content {
		if c.Type == "text" {
			return c.Text
		}
	}

	return "(no response)"
}

func callOllama(cfg *Config, message string) string {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "localhost:11434"
	}

	model := cfg.Model
	if model == "" {
		model = "llama3"
	}

	reqBody := map[string]interface{}{
		"model":  model,
		"prompt": message,
		"stream": false,
	}

	data, _ := json.Marshal(reqBody)

	cmd := exec.Command("curl", "-s",
		fmt.Sprintf("http://%s/api/generate", host),
		"-d", string(data))

	output, err := cmd.Output()
	if err != nil {
		return "ERROR: Ollama call failed: " + err.Error()
	}

	var resp struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}

	if err := json.Unmarshal(output, &resp); err != nil {
		return "ERROR: Failed to parse response"
	}

	if resp.Error != "" {
		return "ERROR: " + resp.Error
	}

	return resp.Response
}

// StopWorker stops a worker process
func StopWorker(name string) error {
	state, err := LoadState(name)
	if err != nil {
		return fmt.Errorf("worker %s not found", name)
	}

	if !IsProcessRunning(state.PID) {
		DeleteState(name)
		return fmt.Errorf("worker %s is not running", name)
	}

	// Send SIGTERM
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return err
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return err
	}

	// Wait for process to exit
	time.Sleep(500 * time.Millisecond)

	// Force kill if still running
	if IsProcessRunning(state.PID) {
		process.Signal(syscall.SIGKILL)
	}

	DeleteState(name)
	os.Remove(state.Socket)

	return nil
}

// ListWorkers lists all workers
func ListWorkers() ([]*WorkerState, error) {
	dir := StateDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var workers []*WorkerState
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".json")
		state, err := LoadState(name)
		if err != nil {
			continue
		}

		// Update status
		if IsProcessRunning(state.PID) {
			state.Status = "running"
		} else {
			state.Status = "stopped"
		}

		workers = append(workers, state)
	}

	return workers, nil
}

// AskWorker sends a message to a worker and gets response
func AskWorker(name, message string, timeout time.Duration) (string, error) {
	state, err := LoadState(name)
	if err != nil {
		return "", fmt.Errorf("worker %s not found", name)
	}

	if !IsProcessRunning(state.PID) {
		return "", fmt.Errorf("worker %s is not running", name)
	}

	// Connect to socket
	conn, err := net.DialTimeout("unix", state.Socket, timeout)
	if err != nil {
		return "", fmt.Errorf("failed to connect to worker: %w", err)
	}
	defer conn.Close()

	// Send message
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(message + "\n")); err != nil {
		return "", err
	}

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}

	return strings.TrimSpace(response), nil
}

// GetLogs returns logs for a worker
func GetLogs(name string, lines int) (string, error) {
	logFile := filepath.Join(WorkersDir(), name, "worker.log")

	if lines <= 0 {
		lines = 50
	}

	cmd := exec.Command("tail", "-n", strconv.Itoa(lines), logFile)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

// IsProcessRunning checks if a process is running
func IsProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	return err == nil
}
