package vssh

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"syscall"
	"time"
)

// Server runs vssh server
type Server struct {
	Port   int
	Secret string
}

// NewServer creates a new server
func NewServer(port int, secret string) *Server {
	return &Server{Port: port, Secret: secret}
}

// Run starts the server
func (s *Server) Run() error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return err
	}
	defer listener.Close()

	fmt.Printf("vssh server listening on :%d\n", s.Port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read auth line
	reader := bufio.NewReader(conn)
	authLine, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	authLine = authLine[:len(authLine)-1]

	// Validate
	if !ValidateAuthToken(authLine, s.Secret) {
		conn.Write([]byte("AUTH_FAILED\n"))
		return
	}
	conn.Write([]byte("AUTH_OK\n"))

	// Check for transfer/exec command (peek first bytes)
	// Wait longer for network latency
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	peek, err := reader.Peek(4)
	conn.SetReadDeadline(time.Time{})
	if err == nil {
		prefix := string(peek[:3])
		if prefix == "PUT" || prefix == "GET" || prefix == "EXE" {
			cmd, _ := reader.ReadString('\n')
			HandleTransfer(conn, cmd)
			return
		}
		if prefix == "SYN" { // SYNC
			cmd, _ := reader.ReadString('\n')
			HandleSync(conn, cmd)
			return
		}
		if prefix == "REL" { // RELAY
			cmd, _ := reader.ReadString('\n')
			HandleRelay(conn, cmd)
			return
		}
	}

	// Get user shell
	shell := "/bin/bash"
	if u, err := user.Current(); err == nil {
		if sh := lookupShell(u.Uid); sh != "" {
			shell = sh
		}
	}

	// Open PTY
	pty, tty, err := openPty()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pty open failed: %v\n", err)
		return
	}
	defer pty.Close()
	defer tty.Close()

	// Start shell
	cmd := exec.Command(shell, "-l")
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "shell start failed: %v\n", err)
		return
	}

	// Close tty in parent
	tty.Close()

	// Bidirectional copy
	done := make(chan struct{})

	// PTY -> conn
	go func() {
		io.Copy(conn, pty)
		done <- struct{}{}
	}()

	// conn -> PTY
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break
			}
			data := buf[:n]
			// Check for resize: \x1b[8;<rows>;<cols>t
			if len(data) >= 8 && data[0] == 0x1b && data[1] == '[' && data[2] == '8' && data[3] == ';' {
				var rows, cols int
				fmt.Sscanf(string(data), "\x1b[8;%d;%dt", &rows, &cols)
				if rows > 0 && cols > 0 {
					setWinsize(pty, rows, cols)
					continue
				}
			}
			pty.Write(data)
		}
		done <- struct{}{}
	}()

	cmd.Wait()
	conn.Close()
	<-done
}

func lookupShell(uid string) string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := splitColon(line)
		if len(parts) >= 7 && parts[2] == uid {
			return parts[6]
		}
	}
	return ""
}

func splitColon(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}
