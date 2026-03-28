package vssh

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// SendFile sends a file to remote host
func SendFile(host string, port int, secret, localPath, remotePath string) error {
	// Open local file
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	// Connect
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Auth
	token := GenerateAuthToken(secret)
	conn.Write([]byte(token + "\n"))

	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\n')
	if err != nil || resp[:7] != "AUTH_OK" {
		return fmt.Errorf("auth failed")
	}

	// Send transfer command: PUT <size> <path>
	if remotePath == "" {
		remotePath = filepath.Base(localPath)
	}
	cmd := fmt.Sprintf("PUT %d %s\n", stat.Size(), remotePath)
	conn.Write([]byte(cmd))

	// Read ready
	resp, err = reader.ReadString('\n')
	if err != nil || resp[:5] != "READY" {
		return fmt.Errorf("server not ready: %s", resp)
	}

	// Send file data
	n, err := io.Copy(conn, f)
	if err != nil {
		return err
	}

	// Read confirmation
	resp, err = reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("no confirmation")
	}
	if resp[:2] != "OK" {
		return fmt.Errorf("transfer failed: %s", resp)
	}

	fmt.Printf("Sent %d bytes\n", n)
	return nil
}

// RecvFile receives a file from remote host
func RecvFile(host string, port int, secret, remotePath, localPath string) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Auth
	token := GenerateAuthToken(secret)
	conn.Write([]byte(token + "\n"))

	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\n')
	if err != nil || resp[:7] != "AUTH_OK" {
		return fmt.Errorf("auth failed")
	}

	// Send GET command
	cmd := fmt.Sprintf("GET %s\n", remotePath)
	conn.Write([]byte(cmd))

	// Read size
	resp, err = reader.ReadString('\n')
	if err != nil {
		return err
	}
	var size int64
	if _, err := fmt.Sscanf(resp, "SIZE %d", &size); err != nil {
		return fmt.Errorf("invalid response: %s", resp)
	}

	// Create local file
	if localPath == "" {
		localPath = filepath.Base(remotePath)
	}
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Receive data (use reader to handle buffered data)
	n, err := io.CopyN(f, reader, size)
	if err != nil {
		return err
	}

	fmt.Printf("Received %d bytes\n", n)
	return nil
}

// HandleTransfer handles file transfer on server side
func HandleTransfer(conn net.Conn, cmd string) {
	if len(cmd) < 4 {
		conn.Write([]byte("ERROR invalid command\n"))
		return
	}

	switch cmd[:3] {
	case "PUT":
		var size int64
		var path string
		fmt.Sscanf(cmd, "PUT %d %s", &size, &path)

		// Expand ~ to home dir
		if len(path) > 0 && path[0] == '~' {
			home, _ := os.UserHomeDir()
			path = home + path[1:]
		}

		// Create directory if needed
		dir := filepath.Dir(path)
		os.MkdirAll(dir, 0755)

		f, err := os.Create(path)
		if err != nil {
			conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
			return
		}
		defer f.Close()

		conn.Write([]byte("READY\n"))

		n, err := io.CopyN(f, conn, size)
		if err != nil {
			conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
			return
		}
		conn.Write([]byte(fmt.Sprintf("OK %d\n", n)))

	case "GET":
		path := cmd[4:]
		path = path[:len(path)-1] // remove newline

		// Expand ~
		if len(path) > 0 && path[0] == '~' {
			home, _ := os.UserHomeDir()
			path = home + path[1:]
		}

		f, err := os.Open(path)
		if err != nil {
			conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
			return
		}
		defer f.Close()

		stat, _ := f.Stat()
		conn.Write([]byte(fmt.Sprintf("SIZE %d\n", stat.Size())))
		io.Copy(conn, f)

	case "EXE": // EXEC or EXEC_STDIN
		// Check if it's EXEC_STDIN
		if len(cmd) > 10 && cmd[:10] == "EXEC_STDIN" {
			var size int64
			var cmdStr string
			// Parse: EXEC_STDIN <size> <command>
			parts := cmd[11:] // after "EXEC_STDIN "
			fmt.Sscanf(parts, "%d", &size)
			// Find command after size
			for i := 0; i < len(parts); i++ {
				if parts[i] == ' ' {
					cmdStr = parts[i+1:]
					break
				}
			}
			if len(cmdStr) > 0 && cmdStr[len(cmdStr)-1] == '\n' {
				cmdStr = cmdStr[:len(cmdStr)-1]
			}

			// Send ready
			conn.Write([]byte("READY\n"))

			// Read stdin data
			stdinData := make([]byte, size)
			io.ReadFull(conn, stdinData)

			// Execute with stdin
			out, err := execShellWithStdin(cmdStr, stdinData)
			if err != nil {
				conn.Write([]byte(fmt.Sprintf("ERROR: %v\n%s", err, out)))
			} else {
				conn.Write(out)
			}
		} else {
			// Regular EXEC
			cmdStr := cmd[5:]
			if len(cmdStr) > 0 && cmdStr[len(cmdStr)-1] == '\n' {
				cmdStr = cmdStr[:len(cmdStr)-1]
			}

			out, err := execShell(cmdStr)
			if err != nil {
				conn.Write([]byte(fmt.Sprintf("ERROR: %v\n%s", err, out)))
			} else {
				conn.Write(out)
			}
		}
	}
}

func execShellWithStdin(cmdStr string, stdin []byte) ([]byte, error) {
	shell := "/bin/bash"
	if _, err := os.Stat("/bin/zsh"); err == nil {
		shell = "/bin/zsh"
	}
	cmd := exec.Command(shell, "-c", cmdStr)

	// Create stdin pipe
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	// Start command
	var output []byte
	outputCh := make(chan []byte)
	go func() {
		out, _ := cmd.CombinedOutput()
		outputCh <- out
	}()

	// Write stdin and close
	stdinPipe.Write(stdin)
	stdinPipe.Close()

	output = <-outputCh
	return output, nil
}

func execShell(cmdStr string) ([]byte, error) {
	shell := "/bin/bash"
	if _, err := os.Stat("/bin/zsh"); err == nil {
		shell = "/bin/zsh"
	}
	cmd := exec.Command(shell, "-c", cmdStr)
	return cmd.CombinedOutput()
}

// ExecLocal executes command locally
func ExecLocal(command string) (string, error) {
	out, err := execShell(command)
	return string(out), err
}

// ExecCommand executes a command on remote host
func ExecCommand(host string, port int, secret, command string) (string, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 30*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// Auth - use raw reads to avoid buffering issues
	token := GenerateAuthToken(secret)
	conn.Write([]byte(token + "\n"))

	// Read AUTH response
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	authBuf := make([]byte, 64)
	n, err := conn.Read(authBuf)
	if err != nil || n < 7 || string(authBuf[:7]) != "AUTH_OK" {
		return "", fmt.Errorf("auth failed")
	}

	// Send EXEC command
	conn.Write([]byte(fmt.Sprintf("EXEC %s\n", command)))

	// Read output with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var output []byte
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		output = append(output, buf[:n]...)
		// Reset deadline on successful read
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}

	return string(output), nil
}

// ExecCommandWithStdin executes a command with stdin data
func ExecCommandWithStdin(host string, port int, secret, command string, stdin []byte) (string, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 30*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// Auth
	token := GenerateAuthToken(secret)
	conn.Write([]byte(token + "\n"))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	authBuf := make([]byte, 64)
	n, err := conn.Read(authBuf)
	if err != nil || n < 7 || string(authBuf[:7]) != "AUTH_OK" {
		return "", fmt.Errorf("auth failed")
	}

	// Send EXEC_STDIN command with size
	conn.Write([]byte(fmt.Sprintf("EXEC_STDIN %d %s\n", len(stdin), command)))

	// Wait for READY
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readyBuf := make([]byte, 64)
	n, err = conn.Read(readyBuf)
	if err != nil || n < 5 || string(readyBuf[:5]) != "READY" {
		return "", fmt.Errorf("server not ready")
	}

	// Send stdin data
	conn.Write(stdin)

	// Read output
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	var output []byte
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		output = append(output, buf[:n]...)
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}

	return string(output), nil
}
