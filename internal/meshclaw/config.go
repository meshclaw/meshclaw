package meshclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config represents meshclaw configuration
type Config struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Model       string            `json:"model,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	SystemPrompt string           `json:"system_prompt,omitempty"`
	Tools       []string          `json:"tools,omitempty"`
	Schedule    []ScheduleConfig  `json:"schedule,omitempty"`
	Workers     map[string]Worker `json:"workers,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// ScheduleConfig for scheduled tasks
type ScheduleConfig struct {
	Interval string `json:"interval"` // "5m", "1h", "1d"
	Task     string `json:"task"`
}

// Worker represents a remote worker
type Worker struct {
	Host   string `json:"host,omitempty"`
	Worker string `json:"worker,omitempty"`
}

// WorkerState represents a running worker
type WorkerState struct {
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

// WorkersDir returns the workers directory
func WorkersDir() string {
	return filepath.Join(ConfigDir(), "workers")
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
func LoadState(name string) (*WorkerState, error) {
	path := filepath.Join(StateDir(), name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state WorkerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// SaveState saves the state for a worker
func SaveState(state *WorkerState) error {
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
