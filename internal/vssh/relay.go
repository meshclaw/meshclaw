package vssh

import (
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// HandleRelay handles RELAY command on server (relay node)
// Note: Wire-level relay (WireGuard AllowedIPs) is preferred for NAT traversal.
// This is a fallback TCP relay for cases where wire-level relay fails.
func HandleRelay(clientConn net.Conn, cmd string) {
	// Parse target: RELAY <ip:port>
	parts := strings.SplitN(cmd, " ", 2)
	if len(parts) < 2 {
		clientConn.Write([]byte("ERROR invalid relay target\n"))
		return
	}
	target := strings.TrimSpace(parts[1])

	// Connect to target
	targetConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		clientConn.Write([]byte(fmt.Sprintf("ERROR cannot reach target: %v\n", err)))
		return
	}
	defer targetConn.Close()

	clientConn.Write([]byte("RELAY_OK\n"))

	// Bidirectional proxy
	done := make(chan struct{}, 2)

	// Client -> Target
	go func() {
		io.Copy(targetConn, clientConn)
		done <- struct{}{}
	}()

	// Target -> Client
	go func() {
		io.Copy(clientConn, targetConn)
		done <- struct{}{}
	}()

	// Wait for either direction to close
	<-done

	// Close both connections
	clientConn.Close()
	targetConn.Close()
}
