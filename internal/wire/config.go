package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/meshclaw/meshclaw/internal/common"
)

const (
	DefaultInterface = "wire0"
	DefaultPort      = 51820
	RefreshInterval  = 30
)

// NetworkConfig represents per-network configuration
type NetworkConfig struct {
	Network    string `json:"network"`
	AccessKey  string `json:"access_key,omitempty"`
	VpnIP      string `json:"vpn_ip"`
	VpnSubnet  string `json:"vpn_subnet"`
	Interface  string `json:"interface"`  // wire0, wire1, etc.
	ListenPort int    `json:"listen_port"`
	PrivateKey string `json:"private_key,omitempty"` // per-network key
}

// Config represents wire configuration (multi-network)
type Config struct {
	ServerURL string                    `json:"server_url"`
	NodeName  string                    `json:"node_name"`
	NodeID    string                    `json:"node_id"`
	Networks  map[string]*NetworkConfig `json:"networks"` // network_name -> config

	// Legacy fields for compatibility
	Network    string `json:"network,omitempty"`
	AccessKey  string `json:"access_key,omitempty"`
	VpnIP      string `json:"vpn_ip,omitempty"`
	ListenPort int    `json:"listen_port,omitempty"`
	RelayNode  string `json:"relay_node,omitempty"`
	VpnSubnet  string `json:"vpn_subnet,omitempty"`
}

// ConfigPath returns path to wire config file
func ConfigPath() string {
	if common.IsRoot() {
		return "/etc/wire/config.json"
	}
	return filepath.Join(common.WireDir(), "config.json")
}

// LoadConfig loads wire configuration
func LoadConfig() (*Config, error) {
	// Try user config first, then system config
	paths := []string{
		filepath.Join(common.WireDir(), "config.json"),
		"/etc/wire/config.json",
	}

	var data []byte
	var err error
	for _, path := range paths {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Networks == nil {
		cfg.Networks = make(map[string]*NetworkConfig)
	}
	// Migrate legacy config
	if cfg.Network != "" && cfg.Networks[cfg.Network] == nil {
		cfg.Networks[cfg.Network] = &NetworkConfig{
			Network:    cfg.Network,
			AccessKey:  cfg.AccessKey,
			VpnIP:      cfg.VpnIP,
			VpnSubnet:  cfg.VpnSubnet,
			Interface:  DefaultInterface,
			ListenPort: cfg.ListenPort,
		}
	}
	return &cfg, nil
}

// SaveConfig saves wire configuration
func SaveConfig(cfg *Config) error {
	var dir string
	if common.IsRoot() {
		dir = "/etc/wire"
	} else {
		dir = common.WireDir()
	}
	common.EnsureDir(dir)
	path := filepath.Join(dir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// GetNetworkConfig returns config for a specific network
func (c *Config) GetNetworkConfig(network string) *NetworkConfig {
	if c.Networks == nil {
		return nil
	}
	return c.Networks[network]
}

// SetNetworkConfig sets config for a specific network
func (c *Config) SetNetworkConfig(nc *NetworkConfig) {
	if c.Networks == nil {
		c.Networks = make(map[string]*NetworkConfig)
	}
	c.Networks[nc.Network] = nc
}

// NextInterface returns the next available interface name
func (c *Config) NextInterface() string {
	used := make(map[string]bool)
	for _, nc := range c.Networks {
		used[nc.Interface] = true
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("wire%d", i)
		if !used[name] {
			return name
		}
	}
	return "wire9"
}

// NextPort returns the next available listen port
func (c *Config) NextPort() int {
	used := make(map[int]bool)
	for _, nc := range c.Networks {
		used[nc.ListenPort] = true
	}
	for port := DefaultPort; port < DefaultPort+10; port++ {
		if !used[port] {
			return port
		}
	}
	return DefaultPort + len(c.Networks)
}

// ActiveNetworks returns sorted list of active network names
func (c *Config) ActiveNetworks() []string {
	names := make([]string, 0, len(c.Networks))
	for name := range c.Networks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Subnet returns configured subnet or default
func (c *Config) Subnet() string {
	if c.VpnSubnet != "" {
		return c.VpnSubnet
	}
	return "10.98" // 10.98 to avoid conflict with Python version (10.99)
}

// GenerateNodeID generates a node ID from hostname
func GenerateNodeID() string {
	hostname, _ := os.Hostname()
	h := sha256.Sum256([]byte(hostname + "-wire-node"))
	return hex.EncodeToString(h[:16])
}

// GenerateVpnIP generates VPN IP from node ID and subnet
func GenerateVpnIP(nodeID, subnet string) string {
	h := sha256.Sum256([]byte(nodeID))
	return fmt.Sprintf("%s.%d.%d", subnet, h[0], h[1])
}

// GenerateVpnIPForNetwork generates VPN IP for a specific network
func GenerateVpnIPForNetwork(nodeID, network, subnet string) string {
	h := sha256.Sum256([]byte(nodeID + "-" + network))
	return fmt.Sprintf("%s.%d.%d", subnet, h[0], h[1])
}
