package meshclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config represents meshclaw configuration
type Config struct {
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	Model          string            `json:"model,omitempty"`
	Provider       string            `json:"provider,omitempty"` // anthropic, openai, ollama
	SystemPrompt   string            `json:"system_prompt,omitempty"`
	Tools          []string          `json:"tools,omitempty"`
	OnMessage      string            `json:"on_message,omitempty"`
	Schedule       string            `json:"schedule,omitempty"`        // "every 1h", "every 15m"
	ScheduleTask   string            `json:"schedule_task,omitempty"`   // LLM task
	ScheduleScript string            `json:"schedule_script,omitempty"` // Bash script
	Notify         *NotifyConfig     `json:"notify,omitempty"`
	Workers        map[string]Worker `json:"workers,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	WebChat        *WebChatConfig    `json:"webchat,omitempty"`
}

// NotifyConfig for notifications
type NotifyConfig struct {
	Platform  string `json:"platform"`            // telegram, slack, discord, webhook
	Token     string `json:"token,omitempty"`     // Bot token
	ChatID    string `json:"chat_id,omitempty"`   // Telegram chat ID
	Channel   string `json:"channel,omitempty"`   // Slack channel
	WebhookURL string `json:"webhook_url,omitempty"`
}

// WebChatConfig for web UI
type WebChatConfig struct {
	Port     int    `json:"port,omitempty"`
	Host     string `json:"host,omitempty"`
	Password string `json:"password,omitempty"`
}

// Worker represents a remote worker
type Worker struct {
	Host   string `json:"host,omitempty"`
	Worker string `json:"worker,omitempty"`
}

// AgentState represents a running worker
type AgentState struct {
	Name      string `json:"name"`
	PID       int    `json:"pid"`
	Socket    string `json:"socket"`
	Status    string `json:"status"` // running, stopped, error
	StartTime int64  `json:"start_time"`
	Config    string `json:"config"`
}

// ConfigDir returns the meshclaw config directory
func ConfigDir() string {
	return filepath.Join(os.Getenv("HOME"), ".meshclaw")
}

// AgentsDir returns the workers directory
func AgentsDir() string {
	return filepath.Join(ConfigDir(), "agents")
}

// SocketPath returns the socket path for a worker
func SocketPath(name string) string {
	return filepath.Join("/tmp", "meshclaw-"+name+".sock")
}

// LoadConfig loads a worker configuration
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Set defaults
	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-20250514"
	}
	if cfg.Provider == "" {
		cfg.Provider = "anthropic"
	}

	return &cfg, nil
}

// SaveConfig saves a worker configuration
func SaveConfig(cfg *Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// StateDir returns the state directory
func StateDir() string {
	dir := filepath.Join(ConfigDir(), "state")
	os.MkdirAll(dir, 0755)
	return dir
}

// LoadState loads the state for a worker
func LoadState(name string) (*AgentState, error) {
	path := filepath.Join(StateDir(), name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state AgentState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// SaveState saves the state for a worker
func SaveState(state *AgentState) error {
	path := filepath.Join(StateDir(), state.Name+".json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// DeleteState deletes the state for a worker
func DeleteState(name string) error {
	path := filepath.Join(StateDir(), name+".json")
	return os.Remove(path)
}
