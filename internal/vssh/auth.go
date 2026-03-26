package vssh

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPort    = 2222
	AuthWindowSecs = 30
)

// GetSecret returns VSSH secret derived from WIRE_SERVER_URL
func GetSecret() string {
	// Always derive from coordinator URL (ignore legacy VSSH_SECRET env)
	if url := os.Getenv("WIRE_SERVER_URL"); url != "" {
		h := sha256.Sum256([]byte(url + "-vssh-secret"))
		return hex.EncodeToString(h[:16])
	}
	// Try to read from wire config
	data, err := os.ReadFile("/etc/wire/config.json")
	if err != nil {
		home, _ := os.UserHomeDir()
		data, err = os.ReadFile(home + "/.wire/config.json")
	}
	if err == nil {
		// Simple JSON parse for server_url
		s := string(data)
		if idx := strings.Index(s, `"server_url"`); idx >= 0 {
			s = s[idx+len(`"server_url"`):]         // skip key
			if idx = strings.Index(s, `"`); idx >= 0 {
				s = s[idx+1:]                       // skip to value start
				if end := strings.Index(s, `"`); end > 0 {
					url := s[:end]
					h := sha256.Sum256([]byte(url + "-vssh-secret"))
					return hex.EncodeToString(h[:16])
				}
			}
		}
	}
	return ""
}

// GenerateAuthToken creates HMAC auth token
func GenerateAuthToken(secret string) string {
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d", ts)))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%d:%s", ts, sig)
}

// ValidateAuthToken validates HMAC auth token
func ValidateAuthToken(token, secret string) bool {
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		return false
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}
	// Check timestamp within window
	now := time.Now().Unix()
	if now-ts > AuthWindowSecs || ts-now > AuthWindowSecs {
		return false
	}
	// Verify signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[1]), []byte(expected))
}
