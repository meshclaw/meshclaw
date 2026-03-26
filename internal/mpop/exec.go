package mpop

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/vssh"
)

const (
	VsshPort   = 2222
	VsshSecret = "" // From env or config
)

// GetVsshSecret returns the vssh secret (delegates to vssh package)
func GetVsshSecret() string {
	return vssh.GetSecret()
}

// VsshExec executes a command on remote server via vssh
func VsshExec(ip, cmd string, timeout time.Duration) (string, error) {
	secret := GetVsshSecret()
	if secret == "" {
		return "", fmt.Errorf("no vssh secret configured")
	}

	// Use vssh package's ExecCommand
	return vssh.ExecCommand(ip, VsshPort, secret, cmd)
}

// SSHExec executes a command via standard SSH
func SSHExec(user, ip, cmd string, port int, timeout time.Duration) (string, error) {
	if port == 0 {
		port = 22
	}

	args := []string{
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(timeout.Seconds())),
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"-p", fmt.Sprintf("%d", port),
		fmt.Sprintf("%s@%s", user, ip),
		cmd,
	}

	sshCmd := exec.Command("ssh", args...)
	output, err := sshCmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// RemoteExec executes a command on remote server using configured method
func RemoteExec(serverName, cmd string, timeout time.Duration) (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}

	srv, ok := cfg.Servers[serverName]
	if !ok {
		// Try as direct IP
		if strings.Contains(serverName, ".") {
			return VsshExec(serverName, cmd, timeout)
		}
		return "", fmt.Errorf("server not found: %s", serverName)
	}

	// Check if local
	if srv.Local || serverName == GetMyName() {
		localCmd := exec.Command("sh", "-c", cmd)
		output, err := localCmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(output)), nil
	}

	method := cfg.Connection.SSHMethod
	user := srv.User
	if user == "" {
		user = "root"
	}

	// Get IP based on VPN type
	var ip string
	switch cfg.Connection.VPN {
	case "tailscale":
		ip = srv.TailscaleIP
		if ip == "" {
			ip = srv.IP
		}
	default:
		ip = srv.IP
	}

	if ip == "" {
		return "", fmt.Errorf("no IP for server: %s", serverName)
	}

	switch method {
	case "vssh":
		result, err := VsshExec(ip, cmd, timeout)
		if err != nil {
			// Fallback to SSH
			if srv.LanIP != "" {
				result, err = SSHExec(user, srv.LanIP, cmd, srv.SSHPort, timeout)
				if err == nil {
					return result, nil
				}
			}
			if srv.PublicIP != "" {
				return SSHExec(user, srv.PublicIP, cmd, srv.SSHPort, timeout)
			}
			return "", err
		}
		return result, nil

	case "ssh":
		return SSHExec(user, ip, cmd, srv.SSHPort, timeout)

	case "tailscale-ssh":
		hostname := serverName
		if srv.TailscaleIP != "" {
			hostname = srv.TailscaleIP
		}
		tsCmd := exec.Command("tailscale", "ssh", fmt.Sprintf("%s@%s", user, hostname), "--", cmd)
		output, err := tsCmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(output)), nil

	default:
		return "", fmt.Errorf("unknown SSH method: %s", method)
	}
}

// ParallelExec executes command on multiple servers in parallel
func ParallelExec(servers []string, cmd string, timeout time.Duration) map[string]string {
	results := make(map[string]string)
	resultCh := make(chan struct {
		name   string
		output string
	}, len(servers))

	for _, srv := range servers {
		go func(s string) {
			output, err := RemoteExec(s, cmd, timeout)
			if err != nil {
				output = fmt.Sprintf("ERROR: %v", err)
			}
			resultCh <- struct {
				name   string
				output string
			}{s, output}
		}(srv)
	}

	for range servers {
		r := <-resultCh
		results[r.name] = r.output
	}

	return results
}
