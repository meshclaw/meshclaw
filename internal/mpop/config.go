package mpop

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config represents mpop configuration
type Config struct {
	ServerURL string                  `json:"server_url,omitempty"`
	Language  string                  `json:"language,omitempty"`
	NodeName  string                  `json:"node_name,omitempty"`
	VpnPrefix string                  `json:"vpn_prefix,omitempty"`
	Servers   map[string]ServerConfig `json:"servers,omitempty"`
	Relays    []string                `json:"relays,omitempty"`
	Connection ConnectionConfig       `json:"connection,omitempty"`
}

// ServerConfig represents a server entry
type ServerConfig struct {
	IP          string `json:"ip,omitempty"`
	PublicIP    string `json:"public_ip,omitempty"`
	TailscaleIP string `json:"tailscale_ip,omitempty"`
	LanIP       string `json:"lan_ip,omitempty"`
	User        string `json:"user,omitempty"`
	Role        string `json:"role,omitempty"`
	SSHPort     int    `json:"ssh_port,omitempty"`
	Local       bool   `json:"local,omitempty"`
}

// ConnectionConfig for VPN/SSH settings
type ConnectionConfig struct {
	VPN       string `json:"vpn,omitempty"`       // wire, tailscale
	SSHMethod string `json:"ssh_method,omitempty"` // vssh, ssh, tailscale-ssh
}

var configCache *Config

// ConfigDir returns the mpop config directory
func ConfigDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		// Fallback to os.UserHomeDir or /root
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		} else {
			home = "/root"
		}
	}
	return filepath.Join(home, ".mpop")
}

// ConfigPath returns the path to config.json
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

// LoadConfig loads mpop configuration
func LoadConfig() (*Config, error) {
	if configCache != nil {
		return configCache, nil
	}

	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Set defaults
	if cfg.VpnPrefix == "" {
		cfg.VpnPrefix = "10."
	}
	if cfg.Connection.VPN == "" {
		cfg.Connection.VPN = "wire"
	}
	if cfg.Connection.SSHMethod == "" {
		cfg.Connection.SSHMethod = "vssh"
	}

	configCache = &cfg
	return &cfg, nil
}

// SaveConfig saves mpop configuration
func SaveConfig(cfg *Config) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(ConfigPath(), data, 0600)
}

// EnsureConfig creates a sample config if it doesn't exist
func EnsureConfig() bool {
	if _, err := os.Stat(ConfigPath()); err == nil {
		return false // Already exists
	}

	cfg := &Config{
		Language: "en",
		Connection: ConnectionConfig{
			VPN:       "wire",
			SSHMethod: "vssh",
		},
		Servers: map[string]ServerConfig{
			"web1": {IP: "10.98.1.10", User: "deploy", Role: "Web server"},
			"db1":  {IP: "10.98.1.20", User: "deploy", Role: "Database server"},
			"app1": {IP: "10.98.1.30", User: "deploy", Role: "Application server"},
		},
	}

	SaveConfig(cfg)
	return true
}

// GetServerIP returns the appropriate IP for a server
func GetServerIP(name string) string {
	cfg, err := LoadConfig()
	if err != nil {
		return ""
	}

	srv, ok := cfg.Servers[name]
	if !ok {
		return ""
	}

	vpn := cfg.Connection.VPN
	switch vpn {
	case "tailscale":
		if srv.TailscaleIP != "" {
			return srv.TailscaleIP
		}
	case "wire":
		if srv.IP != "" {
			return srv.IP
		}
	}

	// Fallback
	if srv.IP != "" {
		return srv.IP
	}
	if srv.TailscaleIP != "" {
		return srv.TailscaleIP
	}
	return srv.PublicIP
}

// GetRelays returns relay IPs from config
func GetRelays() []string {
	cfg, err := LoadConfig()
	if err != nil {
		return nil
	}
	return cfg.Relays
}

// GetVPNType returns the configured VPN type
func GetVPNType() string {
	cfg, err := LoadConfig()
	if err != nil {
		return "wire"
	}
	return cfg.Connection.VPN
}

// GetSSHMethod returns the configured SSH method
func GetSSHMethod() string {
	cfg, err := LoadConfig()
	if err != nil {
		return "vssh"
	}
	return cfg.Connection.SSHMethod
}
