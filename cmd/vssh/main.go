package main

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/common"
	"github.com/meshclaw/meshclaw/internal/vssh"
	"github.com/meshclaw/meshclaw/internal/wire"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "server":
		cmdServer()
	case "status":
		cmdStatus()
	case "put":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: vssh put <host> <local-file> [remote-path]")
			os.Exit(1)
		}
		remotePath := ""
		if len(os.Args) >= 5 {
			remotePath = os.Args[4]
		}
		cmdPut(os.Args[2], os.Args[3], remotePath)
	case "get":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: vssh get <host> <remote-file> [local-path]")
			os.Exit(1)
		}
		localPath := ""
		if len(os.Args) >= 5 {
			localPath = os.Args[4]
		}
		cmdGet(os.Args[2], os.Args[3], localPath)
	case "exec":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: vssh exec <host> <command...>")
			os.Exit(1)
		}
		cmdExec(os.Args[2], os.Args[3:])
	case "sync":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: vssh sync <host> <local-file> [remote-path]")
			os.Exit(1)
		}
		remotePath := ""
		if len(os.Args) >= 5 {
			remotePath = os.Args[4]
		}
		cmdSync(os.Args[2], os.Args[3], remotePath)
	case "help", "--help", "-h":
		printUsage()
	default:
		// Assume it's a hostname to connect to
		cmdConnect(cmd)
	}
}

func printUsage() {
	fmt.Println("vssh - VPN SSH with HMAC authentication (Go)")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  vssh <hostname>              Connect to peer (interactive shell)")
	fmt.Println("  vssh exec <host> <cmd...>    Execute command on remote host")
	fmt.Println("  vssh put <host> <file> [dst] Upload file (simple)")
	fmt.Println("  vssh get <host> <file> [dst] Download file")
	fmt.Println("  vssh sync <host> <file> [dst] Sync large file (chunked, MD5 verified)")
	fmt.Println("  vssh status                  Show all peers and availability")
	fmt.Println("  vssh server                  Start vssh server daemon")
	fmt.Println()
	fmt.Println("Sync features:")
	fmt.Println("  - Chunked 1MB transfer for large files (300GB+)")
	fmt.Println("  - MD5 verification")
	fmt.Println("  - Progress display")
	fmt.Println("  - Skip if unchanged")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  vssh node1                   Interactive shell")
	fmt.Println("  vssh exec node1 uname -a     Run command")
	fmt.Println("  vssh sync node1 ./large.tar  Sync large file")
	fmt.Println()
	fmt.Println("Environment:")
	fmt.Println("  WIRE_SERVER_URL              Coordinator URL (required)")
	fmt.Println("  VSSH_PORT                    Server port (default: 2222)")
	fmt.Println()
}

func cmdServer() {
	secret := vssh.GetSecret()
	if secret == "" {
		fmt.Fprintln(os.Stderr, "No VSSH_SECRET or WIRE_SERVER_URL configured")
		os.Exit(1)
	}

	port := vssh.DefaultPort
	if p := os.Getenv("VSSH_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}

	server := vssh.NewServer(port, secret)
	if err := server.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdStatus() {
	cfg, err := wire.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wire not configured")
		os.Exit(1)
	}

	peers, err := wire.GetPeers(cfg.ServerURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get peers: %v\n", err)
		os.Exit(1)
	}

	secret := vssh.GetSecret()

	// Sort peers by node name
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].NodeName < peers[j].NodeName
	})

	fmt.Println()
	fmt.Printf("%svssh status%s  %s\n", common.Reset, common.Reset, cfg.ServerURL)
	fmt.Println()

	now := time.Now()

	// Test all peers in parallel
	type result struct {
		idx      int
		vsshOk   bool
		isOnline bool
	}
	results := make(chan result, len(peers))

	for i, p := range peers {
		go func(idx int, p wire.Peer) {
			isOnline := false
			var lastSeenTime time.Time
			switch v := p.LastSeen.(type) {
			case string:
				if v != "" {
					lastSeenTime, _ = time.Parse(time.RFC3339, v)
				}
			case float64:
				lastSeenTime = time.Unix(int64(v), 0)
			}
			if !lastSeenTime.IsZero() && now.Sub(lastSeenTime) < 90*time.Second {
				isOnline = true
			}

			vsshOk := false
			if isOnline && p.VpnIP != "" {
				targetIP := p.VpnIP
				if p.NodeID == cfg.NodeID {
					targetIP = "127.0.0.1"
				}
				vsshOk = testVssh(targetIP, vssh.DefaultPort, secret)
			}
			results <- result{idx, vsshOk, isOnline}
		}(i, p)
	}

	// Collect results
	peerResults := make([]result, len(peers))
	for range peers {
		r := <-results
		peerResults[r.idx] = r
	}

	// Print results
	for i, p := range peers {
		r := peerResults[i]
		var dot, nameCol string
		if r.vsshOk {
			dot = common.Green + "●" + common.Reset
			nameCol = common.Green + fmt.Sprintf("%-16s", p.NodeName) + common.Reset
		} else if r.isOnline {
			dot = common.Yellow + "○" + common.Reset
			nameCol = common.Yellow + fmt.Sprintf("%-16s", p.NodeName) + common.Reset
		} else {
			dot = common.Red + "○" + common.Reset
			nameCol = common.Dim + fmt.Sprintf("%-16s", p.NodeName) + common.Reset
		}
		fmt.Printf("  %s %s  %s\n", dot, nameCol, p.VpnIP)
	}
	fmt.Println()
}

func testVssh(host string, port int, secret string) bool {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()

	// Try auth
	token := vssh.GenerateAuthToken(secret)
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	conn.Write([]byte(token + "\n"))

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(buf[:n]), "AUTH_OK")
}

func cmdConnect(hostname string) {
	cfg, err := wire.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wire not configured")
		os.Exit(1)
	}

	// Get secret
	secret := vssh.GetSecret()
	if secret == "" {
		fmt.Fprintln(os.Stderr, "No VSSH_SECRET or WIRE_SERVER_URL configured")
		os.Exit(1)
	}

	// Find peer by hostname
	peers, err := wire.GetPeers(cfg.ServerURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get peers: %v\n", err)
		os.Exit(1)
	}

	var targetIP string
	for _, p := range peers {
		if p.NodeName == hostname || strings.HasPrefix(p.NodeName, hostname) {
			if p.NodeID == cfg.NodeID {
				targetIP = "127.0.0.1"
			} else {
				targetIP = p.VpnIP
			}
			break
		}
	}

	if targetIP == "" {
		// Try as direct IP
		if strings.Contains(hostname, ".") {
			targetIP = hostname
		} else {
			fmt.Fprintf(os.Stderr, "Host not found: %s\n", hostname)
			os.Exit(1)
		}
	}

	port := vssh.DefaultPort

	// Connect
	fmt.Printf("Connecting to %s:%d...\n", targetIP, port)
	if err := vssh.Connect(targetIP, port, secret); err != nil {
		fmt.Fprintf(os.Stderr, "Connection failed: %v\n", err)
		os.Exit(1)
	}
}

// resolveHost resolves hostname to VPN IP
func resolveHost(hostname string) (string, error) {
	cfg, err := wire.LoadConfig()
	if err != nil {
		return "", err
	}

	peers, err := wire.GetPeers(cfg.ServerURL)
	if err != nil {
		return "", err
	}

	for _, p := range peers {
		if p.NodeName == hostname || strings.HasPrefix(p.NodeName, hostname) {
			if p.NodeID == cfg.NodeID {
				return "127.0.0.1", nil
			}
			return p.VpnIP, nil
		}
	}

	// Try as direct IP
	if strings.Contains(hostname, ".") {
		return hostname, nil
	}
	return "", fmt.Errorf("host not found: %s", hostname)
}

func cmdPut(hostname, localPath, remotePath string) {
	targetIP, err := resolveHost(hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	secret := vssh.GetSecret()
	if secret == "" {
		fmt.Fprintln(os.Stderr, "No secret configured")
		os.Exit(1)
	}

	fmt.Printf("Uploading %s to %s:%s...\n", localPath, hostname, remotePath)
	if err := vssh.SendFile(targetIP, vssh.DefaultPort, secret, localPath, remotePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func cmdSync(hostname, localPath, remotePath string) {
	targetIP, err := resolveHost(hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	secret := vssh.GetSecret()
	if secret == "" {
		fmt.Fprintln(os.Stderr, "No secret configured")
		os.Exit(1)
	}

	stat, err := os.Stat(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Syncing %s (%d MB) to %s\n", localPath, stat.Size()/1024/1024, hostname)
	if err := vssh.SyncFile(targetIP, vssh.DefaultPort, secret, localPath, remotePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func cmdGet(hostname, remotePath, localPath string) {
	targetIP, err := resolveHost(hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	secret := vssh.GetSecret()
	if secret == "" {
		fmt.Fprintln(os.Stderr, "No secret configured")
		os.Exit(1)
	}

	fmt.Printf("Downloading %s:%s...\n", hostname, remotePath)
	if err := vssh.RecvFile(targetIP, vssh.DefaultPort, secret, remotePath, localPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func cmdExec(hostname string, command []string) {
	cmd := strings.Join(command, " ")

	// Check if local
	cfg, _ := wire.LoadConfig()
	if hostname == "localhost" || hostname == "local" || hostname == cfg.NodeName {
		// Run locally
		result, err := vssh.ExecLocal(cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(result)
		return
	}

	targetIP, err := resolveHost(hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// If resolved to localhost, run locally
	if targetIP == "127.0.0.1" {
		result, err := vssh.ExecLocal(cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(result)
		return
	}

	secret := vssh.GetSecret()
	if secret == "" {
		fmt.Fprintln(os.Stderr, "No secret configured")
		os.Exit(1)
	}

	// Execute (wire handles relay at VPN level)
	result, err := vssh.ExecCommand(targetIP, vssh.DefaultPort, secret, cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(result)
}
