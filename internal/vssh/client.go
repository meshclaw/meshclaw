package vssh

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
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
