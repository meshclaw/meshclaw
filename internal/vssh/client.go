package vssh

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Connect connects to vssh server
func Connect(host string, port int, secret string) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("connection failed: %v", err)
	}
	defer conn.Close()

	// Send auth
	token := GenerateAuthToken(secret)
	conn.Write([]byte(token + "\n"))

	// Read response
	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("auth failed: %v", err)
	}
	resp = resp[:len(resp)-1]
	if resp != "AUTH_OK" {
		return fmt.Errorf("authentication failed")
	}

	// Set terminal to raw mode
	oldState, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %v", err)
	}
	defer restoreTerminal(int(os.Stdin.Fd()), oldState)

	// Send initial window size
	sendWinsize(conn)

	// Handle SIGWINCH
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			sendWinsize(conn)
		}
	}()

	// Bidirectional copy
	done := make(chan struct{}, 2)

	// Server -> stdout
	go func() {
		io.Copy(os.Stdout, conn)
		done <- struct{}{}
	}()

	// Stdin -> server
	go func() {
		io.Copy(conn, os.Stdin)
		done <- struct{}{}
	}()

	<-done
	return nil
}

func sendWinsize(conn net.Conn) {
	rows, cols := getWinsize()
	if rows > 0 && cols > 0 {
		conn.Write([]byte(fmt.Sprintf("\x1b[8;%d;%dt", rows, cols)))
	}
}

// ConnectSSH connects using standard SSH with config from ~/.ssh/config
func ConnectSSH(hostname string) error {
	cfg := GetSSHConfig(hostname)
	if cfg == nil {
		return fmt.Errorf("no SSH config found for host: %s", hostname)
	}

	// Build SSH command
	args := []string{}

	// Add port if specified
	if cfg.Port != "" {
		args = append(args, "-p", cfg.Port)
	}

	// Add identity file if specified
	if cfg.IdentityFile != "" {
		args = append(args, "-i", cfg.IdentityFile)
	}

	// Build destination
	dest := hostname
	if cfg.User != "" && cfg.HostName != "" {
		dest = cfg.User + "@" + cfg.HostName
	} else if cfg.HostName != "" {
		dest = cfg.HostName
	}
	args = append(args, dest)

	// Run ssh
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// ConnectWithFallback tries vssh protocol first, then falls back to SSH
func ConnectWithFallback(host string, port int, secret string, hostname string) error {
	// Try vssh protocol first
	err := Connect(host, port, secret)
	if err == nil {
		return nil
	}

	// Check if SSH config exists for this host
	cfg := GetSSHConfig(hostname)
	if cfg == nil {
		return err // Return original vssh error
	}

	// Fall back to SSH
	fmt.Printf("vssh failed, falling back to SSH...\n")
	return ConnectSSH(hostname)
}

// ExecSSH executes a command via SSH using ~/.ssh/config
func ExecSSH(hostname string, command string) (string, error) {
	cfg := GetSSHConfig(hostname)
	if cfg == nil {
		return "", fmt.Errorf("no SSH config found for host: %s", hostname)
	}

	// Build SSH command
	args := []string{}

	// Add port if specified
	if cfg.Port != "" {
		args = append(args, "-p", cfg.Port)
	}

	// Add identity file if specified
	if cfg.IdentityFile != "" {
		args = append(args, "-i", cfg.IdentityFile)
	}

	// Build destination
	dest := hostname
	if cfg.User != "" && cfg.HostName != "" {
		dest = cfg.User + "@" + cfg.HostName
	} else if cfg.HostName != "" {
		dest = cfg.HostName
	}
	args = append(args, dest, command)

	// Run ssh
	cmd := exec.Command("ssh", args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
