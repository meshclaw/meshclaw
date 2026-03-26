package wire

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/meshclaw/meshclaw/internal/common"
)

// SetupInterface creates and configures WireGuard interface
func SetupInterface(iface, vpnIP, privateKey string, port int) (string, error) {
	if runtime.GOOS == "darwin" {
		return setupMacOS(vpnIP, privateKey, port)
	}
	return setupLinux(iface, vpnIP, privateKey, port)
}

func setupLinux(iface, vpnIP, privateKey string, port int) (string, error) {
	ip := common.FindBin("ip")
	wg := common.FindBin("wg")

	// Delete existing interface
	common.RunShell(fmt.Sprintf("%s link delete %s 2>/dev/null", ip, iface))

	// Create interface
	_, stderr, code := common.Run(ip, "link", "add", "dev", iface, "type", "wireguard")
	if code != 0 {
		return "", fmt.Errorf("failed to create interface: %s", stderr)
	}

	// Write private key to temp file
	keyFile := fmt.Sprintf("/tmp/wg-key-%d", os.Getpid())
	if err := os.WriteFile(keyFile, []byte(privateKey), 0600); err != nil {
		return "", err
	}
	defer os.Remove(keyFile)

	// Configure WireGuard
	_, stderr, code = common.Run(wg, "set", iface, "listen-port", fmt.Sprintf("%d", port), "private-key", keyFile)
	if code != 0 {
		return "", fmt.Errorf("wg set failed: %s", stderr)
	}

	// Bring up interface
	subnet := strings.Split(vpnIP, ".")[0] + "." + strings.Split(vpnIP, ".")[1]
	common.Run(ip, "addr", "add", vpnIP+"/16", "dev", iface)
	common.Run(ip, "link", "set", iface, "up")
	common.RunShell(fmt.Sprintf("%s route add %s.0.0/16 dev %s 2>/dev/null", ip, subnet, iface))

	return iface, nil
}

func setupMacOS(vpnIP, privateKey string, port int) (string, error) {
	wgGo := common.FindBin("wireguard-go")
	wg := common.FindBin("wg")

	// Find available utun
	utun := "utun9"
	for i := 3; i < 20; i++ {
		name := fmt.Sprintf("utun%d", i)
		_, _, code := common.Run("ifconfig", name)
		if code != 0 {
			utun = name
			break
		}
	}

	// Kill existing wireguard-go for this utun
	common.RunShell(fmt.Sprintf("pkill -f 'wireguard-go %s' 2>/dev/null", utun))

	// Start wireguard-go
	cmd := exec.Command(wgGo, utun)
	cmd.Env = append(os.Environ(), "WG_TUN_NAME_FILE=/tmp/wg-utun-name")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("wireguard-go failed: %v", err)
	}

	// Wait for socket
	socketPath := fmt.Sprintf("/var/run/wireguard/%s.sock", utun)
	for i := 0; i < 20; i++ {
		if common.FileExists(socketPath) {
			break
		}
		common.RunShell("sleep 0.1")
	}

	// Write private key
	keyFile := fmt.Sprintf("/tmp/wg-key-%d", os.Getpid())
	if err := os.WriteFile(keyFile, []byte(privateKey), 0600); err != nil {
		return "", err
	}
	defer os.Remove(keyFile)

	// Configure
	_, stderr, code := common.Run(wg, "set", utun, "listen-port", fmt.Sprintf("%d", port), "private-key", keyFile)
	if code != 0 {
		return "", fmt.Errorf("wg set failed: %s", stderr)
	}

	// Assign IP
	subnet := strings.Split(vpnIP, ".")[0] + "." + strings.Split(vpnIP, ".")[1]
	common.Run("ifconfig", utun, "inet", vpnIP+"/16", vpnIP)
	common.RunShell(fmt.Sprintf("route delete -net %s.0.0/16 2>/dev/null", subnet))
	common.RunShell(fmt.Sprintf("route add -net %s.0.0/16 -interface %s", subnet, utun))

	return utun, nil
}

// TeardownInterface removes WireGuard interface
// subnet is the VPN subnet prefix (e.g., "10.99")
func TeardownInterface(iface string, subnet string) {
	if runtime.GOOS == "darwin" {
		common.RunShell(fmt.Sprintf("pkill -f 'wireguard-go %s' 2>/dev/null", iface))
		// Route cleanup using configured subnet
		if subnet != "" {
			common.RunShell(fmt.Sprintf("route delete -net %s.0.0/16 2>/dev/null", subnet))
		}
	} else {
		ip := common.FindBin("ip")
		common.Run(ip, "link", "delete", iface)
	}
}

// GetPublicKey gets WireGuard public key from private key
func GetPublicKey(privateKey string) string {
	wg := common.FindBin("wg")
	cmd := exec.Command(wg, "pubkey")
	cmd.Stdin = strings.NewReader(privateKey)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GeneratePrivateKey generates a new WireGuard private key
func GeneratePrivateKey() string {
	wg := common.FindBin("wg")
	out, _, _ := common.Run(wg, "genkey")
	return strings.TrimSpace(out)
}

// GetOrCreatePrivateKey loads or creates private key
func GetOrCreatePrivateKey() string {
	var keyPath string
	if common.IsRoot() {
		keyPath = "/etc/wire/private.key"
	} else {
		keyPath = common.WireDir() + "/private.key"
	}

	if data, err := os.ReadFile(keyPath); err == nil {
		return strings.TrimSpace(string(data))
	}

	// Generate new key
	key := GeneratePrivateKey()
	common.EnsureDir(common.WireDir())
	if common.IsRoot() {
		common.EnsureDir("/etc/wire")
		os.WriteFile("/etc/wire/private.key", []byte(key), 0600)
	} else {
		os.WriteFile(keyPath, []byte(key), 0600)
	}
	return key
}

// AddPeer adds a peer to WireGuard interface
func AddPeer(iface, pubKey, vpnIP, endpoint string) {
	wg := common.FindBin("wg")
	allowedIPs := vpnIP + "/32"

	args := []string{"set", iface, "peer", pubKey, "allowed-ips", allowedIPs}
	if endpoint != "" {
		args = append(args, "endpoint", endpoint)
	}
	args = append(args, "persistent-keepalive", "25")

	common.Run(wg, args...)
}

// FindExistingInterface finds existing wire interface
// subnet is optional - if empty, will try to get from config
func FindExistingInterface() string {
	// Try to get subnet from config
	subnet := ""
	if cfg, err := LoadConfig(); err == nil && cfg.VpnIP != "" {
		parts := strings.Split(cfg.VpnIP, ".")
		if len(parts) >= 2 {
			subnet = parts[0] + "." + parts[1]
		}
	}

	if runtime.GOOS == "darwin" {
		for i := 3; i < 20; i++ {
			name := fmt.Sprintf("utun%d", i)
			stdout, _, code := common.Run("ifconfig", name)
			if code == 0 {
				// Check if this interface has our VPN subnet
				if subnet != "" && strings.Contains(stdout, subnet+".") {
					return name
				}
				// Fallback: check for wireguard socket
				socketPath := fmt.Sprintf("/var/run/wireguard/%s.sock", name)
				if common.FileExists(socketPath) {
					return name
				}
			}
		}
	} else {
		stdout, _, code := common.Run("ip", "link", "show", DefaultInterface)
		if code == 0 && stdout != "" {
			return DefaultInterface
		}
	}
	return ""
}
