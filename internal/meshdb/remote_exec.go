package meshdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const vsshPort = "48291"

func loadVSSHSecret() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"meshdb", "mpop"} {
		p := filepath.Join(home, "."+name, "config.json")
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var root struct {
			VSSHSecret string `json:"vssh_secret"`
		}
		if json.Unmarshal(b, &root) != nil || root.VSSHSecret == "" {
			continue
		}
		return root.VSSHSecret
	}
	return ""
}

// HasVSSHSecretConfigured is true when config has a non-empty vssh_secret.
func HasVSSHSecretConfigured() bool {
	return loadVSSHSecret() != ""
}

func vsshExec(ip, cmd, secret string, timeout time.Duration) ([]byte, bool) {
	d := timeout
	if d < time.Second {
		d = time.Second
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, vsshPort), minDuration(8*time.Second, d))
	if err != nil {
		return nil, false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	line := fmt.Sprintf("SSH:%s:%s\n", secret, cmd)
	if _, err := conn.Write([]byte(line)); err != nil {
		return nil, false
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return nil, false
	}
	resp := buf[:n]
	if !bytes.HasPrefix(resp, []byte("OK")) {
		return nil, false
	}
	nl := bytes.IndexByte(resp, '\n')
	if nl < 0 {
		return nil, false
	}
	data := append([]byte(nil), resp[nl+1:]...)
	for !bytes.Contains(data, []byte{0x04}) && !bytes.Contains(data, []byte("__END__")) {
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			break
		}
		data = append(data, buf[:n]...)
		if len(data) > 50<<20 {
			return nil, false
		}
	}
	data = bytes.ReplaceAll(data, []byte{0x04}, nil)
	data = bytes.ReplaceAll(data, []byte("__END__"), nil)
	return bytes.TrimSpace(data), true
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func sshBatchMode(info ServerInfo, remoteCmd string, timeout time.Duration) ([]byte, error) {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return nil, err
	}
	target := fmt.Sprintf("%s@%s", info.User, info.IP)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sshBin,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		target,
		remoteCmd,
	)
	return cmd.CombinedOutput()
}

func sshRun(info ServerInfo, remoteCmd string, timeout time.Duration) ([]byte, error) {
	if info.IP == "" || info.User == "" {
		return nil, fmt.Errorf("missing ip/user for SSH")
	}
	if secret := loadVSSHSecret(); secret != "" {
		if out, ok := vsshExec(info.IP, remoteCmd, secret, timeout); ok {
			return out, nil
		}
	}
	return sshBatchMode(info, remoteCmd, timeout)
}
