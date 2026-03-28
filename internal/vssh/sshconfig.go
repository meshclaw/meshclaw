package vssh

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// SSHHostConfig represents SSH config for a host
type SSHHostConfig struct {
	Host         string
	HostName     string
	User         string
	Port         string
	IdentityFile string
}

// ParseSSHConfig parses ~/.ssh/config and returns host configs
func ParseSSHConfig() map[string]*SSHHostConfig {
	configs := make(map[string]*SSHHostConfig)

	home, err := os.UserHomeDir()
	if err != nil {
		return configs
	}

	configPath := filepath.Join(home, ".ssh", "config")
	file, err := os.Open(configPath)
	if err != nil {
		return configs
	}
	defer file.Close()

	var current *SSHHostConfig
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip Include directives for now
		if strings.HasPrefix(strings.ToLower(line), "include") {
			continue
		}

		// Parse key-value
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			// Try with = or tab
			parts = strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				parts = strings.SplitN(line, "\t", 2)
			}
		}
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch key {
		case "host":
			// Skip wildcard hosts
			if value == "*" || strings.Contains(value, "*") {
				current = nil
				continue
			}
			current = &SSHHostConfig{Host: value}
			configs[value] = current
		case "hostname":
			if current != nil {
				current.HostName = value
			}
		case "user":
			if current != nil {
				current.User = value
			}
		case "port":
			if current != nil {
				current.Port = value
			}
		case "identityfile":
			if current != nil {
				// Expand ~ to home directory
				if strings.HasPrefix(value, "~") {
					value = filepath.Join(home, value[1:])
				}
				current.IdentityFile = value
			}
		}
	}

	return configs
}

// GetSSHConfig returns SSH config for a host
func GetSSHConfig(hostname string) *SSHHostConfig {
	configs := ParseSSHConfig()
	if cfg, ok := configs[hostname]; ok {
		return cfg
	}
	return nil
}
