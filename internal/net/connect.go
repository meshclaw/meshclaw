// Package net provides network connection abstraction with automatic fallback
package net

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ConnectionMode represents the connection method
type ConnectionMode string

const (
	ModeWire      ConnectionMode = "wire"      // wire VPN mesh
	ModeTailscale ConnectionMode = "tailscale" // Tailscale VPN
	ModeSSH       ConnectionMode = "ssh"       // Standard SSH
	ModeLocal     ConnectionMode = "local"     // Local execution
)

// ConnectionConfig holds connection preferences
type ConnectionConfig struct {
	PreferredMode ConnectionMode // Primary connection method
	FallbackModes []ConnectionMode // Fallback order
	SSHUser       string
	SSHPort       int
	Timeout       time.Duration
}

// DefaultConfig returns sensible defaults with full fallback chain
func DefaultConfig() *ConnectionConfig {
	return &ConnectionConfig{
		PreferredMode: ModeWire,
		FallbackModes: []ConnectionMode{ModeTailscale, ModeSSH},
		SSHUser:       os.Getenv("USER"),
		SSHPort:       22,
		Timeout:       10 * time.Second,
	}
}

// Connection represents a connection to a remote host
type Connection struct {
	Host     string
	Mode     ConnectionMode
	IP       string
	User     string
	Port     int
	IsLocal  bool
}

var (
	// Cache for discovered connections
	connCache   = make(map[string]*Connection)
	connCacheMu sync.RWMutex

	// Detected capabilities
	hasWire      *bool
	hasTailscale *bool
	hasVssh      *bool
)

// Detect checks what network capabilities are available
func Detect() map[string]bool {
	caps := make(map[string]bool)

	// Check wire
	if hasWire == nil {
		_, err := exec.LookPath("wire")
		wireOK := err == nil
		if wireOK {
			// Check if wire daemon is running
			cmd := exec.Command("wire", "status")
			wireOK = cmd.Run() == nil
		}
		hasWire = &wireOK
	}
	caps["wire"] = *hasWire

	// Check tailscale
	if hasTailscale == nil {
		_, err := exec.LookPath("tailscale")
		tsOK := err == nil
		if tsOK {
			// Check if tailscale is connected
			cmd := exec.Command("tailscale", "status", "--json")
			tsOK = cmd.Run() == nil
		}
		hasTailscale = &tsOK
	}
	caps["tailscale"] = *hasTailscale

	// Check vssh
	if hasVssh == nil {
		_, err := exec.LookPath("vssh")
		vsshOK := err == nil
		hasVssh = &vsshOK
	}
	caps["vssh"] = *hasVssh

	// SSH is always available on Unix
	caps["ssh"] = true

	return caps
}

// Connect establishes a connection to a host with automatic fallback
func Connect(host string, cfg *ConnectionConfig) (*Connection, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Check cache
	connCacheMu.RLock()
	if conn, ok := connCache[host]; ok {
		connCacheMu.RUnlock()
		return conn, nil
	}
	connCacheMu.RUnlock()

	// Check if local
	if isLocalHost(host) {
		conn := &Connection{
			Host:    host,
			Mode:    ModeLocal,
			IsLocal: true,
		}
		cacheConnection(host, conn)
		return conn, nil
	}

	// Try connection modes in order
	modes := append([]ConnectionMode{cfg.PreferredMode}, cfg.FallbackModes...)
	caps := Detect()

	for _, mode := range modes {
		conn, err := tryConnect(host, mode, cfg, caps)
		if err == nil {
			cacheConnection(host, conn)
			return conn, nil
		}
	}

	return nil, fmt.Errorf("could not connect to %s (tried: %v)", host, modes)
}

func tryConnect(host string, mode ConnectionMode, cfg *ConnectionConfig, caps map[string]bool) (*Connection, error) {
	switch mode {
	case ModeWire:
		if !caps["wire"] {
			return nil, fmt.Errorf("wire not available")
		}
		return tryWire(host, cfg)

	case ModeTailscale:
		if !caps["tailscale"] {
			return nil, fmt.Errorf("tailscale not available")
		}
		return tryTailscale(host, cfg)

	case ModeSSH:
		return trySSH(host, cfg)

	case ModeLocal:
		return &Connection{Host: host, Mode: ModeLocal, IsLocal: true}, nil
	}

	return nil, fmt.Errorf("unknown mode: %s", mode)
}

func tryWire(host string, cfg *ConnectionConfig) (*Connection, error) {
	// Get IP from wire peers
	cmd := exec.Command("wire", "peers", "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Parse peers to find matching host
	ip := parseWirePeerIP(string(output), host)
	if ip == "" {
		return nil, fmt.Errorf("host not found in wire peers: %s", host)
	}

	// Check if reachable via vssh
	if *hasVssh {
		if checkPort(ip, 2222, cfg.Timeout) {
			return &Connection{
				Host: host,
				Mode: ModeWire,
				IP:   ip,
				Port: 2222,
			}, nil
		}
	}

	// Fallback to ping check
	if pingHost(ip, cfg.Timeout) {
		return &Connection{
			Host: host,
			Mode: ModeWire,
			IP:   ip,
		}, nil
	}

	return nil, fmt.Errorf("wire peer not reachable: %s", host)
}

func tryTailscale(host string, cfg *ConnectionConfig) (*Connection, error) {
	// Get IP from tailscale
	cmd := exec.Command("tailscale", "ip", "-4", host)
	output, err := cmd.Output()
	if err != nil {
		// Try with hostname lookup
		cmd = exec.Command("tailscale", "status", "--json")
		output, err = cmd.Output()
		if err != nil {
			return nil, err
		}
		ip := parseTailscalePeerIP(string(output), host)
		if ip == "" {
			return nil, fmt.Errorf("host not found in tailscale: %s", host)
		}
		output = []byte(ip)
	}

	ip := strings.TrimSpace(string(output))
	if ip == "" {
		return nil, fmt.Errorf("no tailscale IP for: %s", host)
	}

	// Check reachability
	if !pingHost(ip, cfg.Timeout) {
		return nil, fmt.Errorf("tailscale peer not reachable: %s", host)
	}

	return &Connection{
		Host: host,
		Mode: ModeTailscale,
		IP:   ip,
		User: cfg.SSHUser,
		Port: cfg.SSHPort,
	}, nil
}

func trySSH(host string, cfg *ConnectionConfig) (*Connection, error) {
	// Resolve IP
	ip := host
	if !isIP(host) {
		addrs, err := net.LookupHost(host)
		if err != nil || len(addrs) == 0 {
			return nil, fmt.Errorf("could not resolve: %s", host)
		}
		ip = addrs[0]
	}

	// Check SSH port
	if !checkPort(ip, cfg.SSHPort, cfg.Timeout) {
		return nil, fmt.Errorf("SSH port not reachable: %s:%d", ip, cfg.SSHPort)
	}

	return &Connection{
		Host: host,
		Mode: ModeSSH,
		IP:   ip,
		User: cfg.SSHUser,
		Port: cfg.SSHPort,
	}, nil
}

// Exec executes a command on the connection
func (c *Connection) Exec(cmd string, timeout time.Duration) (string, error) {
	if c.IsLocal {
		return execLocal(cmd, timeout)
	}

	switch c.Mode {
	case ModeWire:
		// Try vssh first, fallback to SSH
		if hasVssh != nil && *hasVssh {
			result, err := execVssh(c.IP, cmd, timeout)
			if err == nil {
				return result, nil
			}
		}
		return execSSH(c.User, c.IP, c.Port, cmd, timeout)

	case ModeTailscale:
		// Use tailscale ssh or regular ssh
		return execTailscaleSSH(c.User, c.IP, cmd, timeout)

	case ModeSSH:
		return execSSH(c.User, c.IP, c.Port, cmd, timeout)
	}

	return "", fmt.Errorf("unknown connection mode: %s", c.Mode)
}

// Helper functions

func isLocalHost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	hostname, _ := os.Hostname()
	return strings.EqualFold(host, hostname)
}

func isIP(s string) bool {
	return net.ParseIP(s) != nil
}

func pingHost(ip string, timeout time.Duration) bool {
	cmd := exec.Command("ping", "-c", "1", "-W", "1", ip)
	return cmd.Run() == nil
}

func checkPort(ip string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func execLocal(cmd string, timeout time.Duration) (string, error) {
	c := exec.Command("sh", "-c", cmd)
	output, err := c.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func execVssh(ip, cmd string, timeout time.Duration) (string, error) {
	c := exec.Command("vssh", "exec", ip, cmd)
	output, err := c.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func execSSH(user, ip string, port int, cmd string, timeout time.Duration) (string, error) {
	args := []string{
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(timeout.Seconds())),
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
	}
	if port != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", port))
	}
	if user != "" {
		args = append(args, fmt.Sprintf("%s@%s", user, ip))
	} else {
		args = append(args, ip)
	}
	args = append(args, cmd)

	c := exec.Command("ssh", args...)
	output, err := c.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func execTailscaleSSH(user, host, cmd string, timeout time.Duration) (string, error) {
	// Try tailscale ssh first
	target := host
	if user != "" {
		target = user + "@" + host
	}

	c := exec.Command("tailscale", "ssh", target, "--", cmd)
	output, err := c.CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(output)), nil
	}

	// Fallback to regular SSH
	return execSSH(user, host, 22, cmd, timeout)
}

func parseWirePeerIP(jsonOutput, host string) string {
	// Simple parsing - look for hostname in output
	// In real implementation, parse JSON properly
	lines := strings.Split(jsonOutput, "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), strings.ToLower(host)) {
			// Extract IP (10.x.x.x pattern)
			for _, word := range strings.Fields(line) {
				if strings.HasPrefix(word, "10.") || strings.HasPrefix(word, "100.") {
					ip := strings.Trim(word, `",`)
					if net.ParseIP(ip) != nil {
						return ip
					}
				}
			}
		}
	}
	return ""
}

func parseTailscalePeerIP(jsonOutput, host string) string {
	// Simple parsing - look for hostname in output
	lines := strings.Split(jsonOutput, "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), strings.ToLower(host)) {
			for _, word := range strings.Fields(line) {
				if strings.HasPrefix(word, "100.") {
					ip := strings.Trim(word, `",`)
					if net.ParseIP(ip) != nil {
						return ip
					}
				}
			}
		}
	}
	return ""
}

func cacheConnection(host string, conn *Connection) {
	connCacheMu.Lock()
	connCache[host] = conn
	connCacheMu.Unlock()
}

// ClearCache clears the connection cache
func ClearCache() {
	connCacheMu.Lock()
	connCache = make(map[string]*Connection)
	connCacheMu.Unlock()
}

// GetMode returns the best available connection mode
func GetMode() ConnectionMode {
	caps := Detect()
	if caps["wire"] {
		return ModeWire
	}
	if caps["tailscale"] {
		return ModeTailscale
	}
	return ModeSSH
}

// Status returns a human-readable status of network capabilities
func Status() string {
	caps := Detect()
	var parts []string

	if caps["wire"] {
		parts = append(parts, "wire:ok")
	} else {
		parts = append(parts, "wire:no")
	}

	if caps["tailscale"] {
		parts = append(parts, "tailscale:ok")
	} else {
		parts = append(parts, "tailscale:no")
	}

	if caps["vssh"] {
		parts = append(parts, "vssh:ok")
	} else {
		parts = append(parts, "vssh:no")
	}

	parts = append(parts, "ssh:ok")

	return strings.Join(parts, " | ")
}
