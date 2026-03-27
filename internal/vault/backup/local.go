// Package backup implements local backup/restore matching Python vault/backup.py
package backup

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/vault/engine"
)

const maxVersionsDefault = 30

// Record is a minimal audit line compatible with Python BackupRecord JSON.
type Record struct {
	Timestamp   string          `json:"timestamp"`
	SourceFile  string          `json:"source_file"`
	SourceHash  string          `json:"source_hash"`
	VaultFile   string          `json:"vault_file"`
	VaultHash   string          `json:"vault_hash"`
	Targets     []string        `json:"targets"`
	Results     map[string]bool `json:"results"`
	DurationSec float64         `json:"duration_sec"`
}

// ConfigDir returns Python ConfigManager default: ~/.vault.
func ConfigDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vault")
}

// BackupDir is configDir/backups.
func BackupDir(configDir string) string {
	return filepath.Join(configDir, "backups")
}

func AuditLogPath(configDir string) string {
	return filepath.Join(configDir, "backup_audit.log")
}

// Backup encrypts sourceFile into backups/secrets_YYYYMMDD_HHMMSS.vault (UTC).
func Backup(configDir, sourceFile string, password []byte) (*Record, string, error) {
	t0 := time.Now()
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	hexFull := fmt.Sprintf("%x", sum[:])
	ctx := "backup:" + hexFull[:16]

	eng := engine.VaultEngine{}
	blob, err := eng.Encrypt(data, password, ctx)
	if err != nil {
		return nil, "", err
	}

	bdir := BackupDir(configDir)
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return nil, "", err
	}
	ts := time.Now().UTC().Format("20060102_150405")
	name := "secrets_" + ts + ".vault"
	outPath := filepath.Join(bdir, name)
	raw := blob.ToBytes()
	if err := os.WriteFile(outPath, raw, 0o600); err != nil {
		return nil, "", err
	}

	vh := sha256.Sum256(raw)
	rec := &Record{
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		SourceFile:  filepath.Base(sourceFile),
		SourceHash:  hexFull[:16],
		VaultFile:   name,
		VaultHash:   fmt.Sprintf("%x", vh[:])[:16],
		Targets:     nil,
		Results:     map[string]bool{},
		DurationSec: time.Since(t0).Seconds(),
	}
	_ = appendAudit(configDir, rec)
	_ = cleanupOldVaults(bdir, maxVersionsDefault)

	return rec, outPath, nil
}

func appendAudit(configDir string, rec *Record) error {
	p := AuditLogPath(configDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func cleanupOldVaults(bdir string, maxKeep int) error {
	entries, err := os.ReadDir(bdir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".vault") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i] > names[j] })
	for _, old := range names[maxKeep:] {
		_ = os.Remove(filepath.Join(bdir, old))
	}
	return nil
}

// LatestLocalVault returns newest secrets_*.vault name in backup dir, or "".
func LatestLocalVault(configDir string) string {
	bdir := BackupDir(configDir)
	entries, err := os.ReadDir(bdir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".vault") && strings.HasPrefix(n, "secrets_") {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Slice(names, func(i, j int) bool { return names[i] > names[j] })
	return names[0]
}

// Restore decrypts vaultFile to outputPath.
func Restore(vaultFile string, password []byte, outputPath string) error {
	raw, err := os.ReadFile(vaultFile)
	if err != nil {
		return err
	}
	blob, err := engine.BlobFromBytes(raw)
	if err != nil {
		return err
	}
	eng := engine.VaultEngine{}
	plain, err := eng.Decrypt(blob, password)
	if err != nil {
		return err
	}
	if _, err := os.Stat(outputPath); err == nil {
		backupPath := fmt.Sprintf("%s.pre_restore.%d", outputPath, time.Now().Unix())
		if err := os.Rename(outputPath, backupPath); err != nil {
			return err
		}
	}
	return os.WriteFile(outputPath, plain, 0o600)
}
