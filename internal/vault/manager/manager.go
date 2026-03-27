package manager

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/vault/engine"
	"github.com/meshclaw/meshclaw/internal/vault/shamir"
)

var shareBinName = regexp.MustCompile(`^share_(\d+)\.bin$`)

type SecretEntry struct {
	Name      string   `json:"name"`
	Value     string   `json:"value"`
	Category  string   `json:"category"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	CreatedBy string   `json:"created_by"`
	Note      string   `json:"note"`
}

type VaultMeta struct {
	VaultID       string                   `json:"vault_id"`
	Version       int                      `json:"version"`
	ShamirN       int                      `json:"shamir_n"`
	ShamirK       int                      `json:"shamir_k"`
	ShareMap      []map[string]interface{} `json:"share_map"`
	CreatedAt     string                   `json:"created_at"`
	LastModified  string                   `json:"last_modified"`
	EntryCount    int                      `json:"entry_count"`
	BackupTargets []interface{}            `json:"backup_targets"`
}

type Manager struct {
	VaultDir   string
	eng        engine.VaultEngine
	metaPath   string
	dataPath   string
	keyEncPath string
	masterKey  []byte
	secrets    map[string]*SecretEntry
	meta       *VaultMeta
}

func NewManager(vaultDir string) *Manager {
	if vaultDir == "" {
		home, _ := os.UserHomeDir()
		vaultDir = filepath.Join(home, ".vault")
	}
	return &Manager{
		VaultDir:   vaultDir,
		metaPath:   filepath.Join(vaultDir, "vault.meta.json"),
		dataPath:   filepath.Join(vaultDir, "vault.data"),
		keyEncPath: filepath.Join(vaultDir, "vault.key.enc"),
		secrets:    map[string]*SecretEntry{},
	}
}

func (m *Manager) IsUnlocked() bool { return len(m.masterKey) > 0 }

func (m *Manager) Init(password string, shamirN, shamirK int) (vaultID, shamirDesc, keyHash string, shares []shamir.Share, err error) {
	if err = os.MkdirAll(m.VaultDir, 0o700); err != nil {
		return "", "", "", nil, err
	}
	if _, err = os.Stat(m.metaPath); err == nil {
		return "", "", "", nil, fmt.Errorf("vault already exists: %w", os.ErrExist)
	}
	mk := make([]byte, engine.KeySize)
	if _, err = rand.Read(mk); err != nil {
		return "", "", "", nil, err
	}
	blob, err := m.eng.Encrypt(mk, []byte(password), "master-key")
	if err != nil {
		return "", "", "", nil, err
	}
	if err = os.WriteFile(m.keyEncPath, blob.ToBytes(), 0o600); err != nil {
		return "", "", "", nil, err
	}
	sh, err := shamir.Split(mk, shamirN, shamirK)
	if err != nil {
		return "", "", "", nil, err
	}
	m.masterKey = mk
	m.secrets = map[string]*SecretEntry{}
	if err = m.saveData(); err != nil {
		return "", "", "", nil, err
	}
	ts := fmt.Sprintf("%.6f", float64(time.Now().UnixNano())/1e9)
	sum := sha256.Sum256(append(mk, []byte(ts)...))
	vid := fmt.Sprintf("%x", sum[:6])
	m.meta = &VaultMeta{
		VaultID:      vid,
		Version:      2,
		ShamirN:      shamirN,
		ShamirK:      shamirK,
		ShareMap:     nil,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		LastModified: time.Now().UTC().Format(time.RFC3339Nano),
		EntryCount:   0,
	}
	if err = m.saveMeta(); err != nil {
		return "", "", "", nil, err
	}
	keyHash = engine.Sha256Prefix16(mk)
	m.audit("init", fmt.Sprintf("vault created: %s", vid))
	return vid, fmt.Sprintf("%d-of-%d", shamirK, shamirN), keyHash, sh, nil
}

func (m *Manager) Unlock(password string) error {
	b, err := os.ReadFile(m.keyEncPath)
	if err != nil {
		return err
	}
	blob, err := engine.BlobFromBytes(b)
	if err != nil {
		return err
	}
	mk, err := m.eng.Decrypt(blob, []byte(password))
	if err != nil {
		m.masterKey = nil
		m.secrets = map[string]*SecretEntry{}
		return err
	}
	m.masterKey = mk
	if err := m.loadData(); err != nil {
		return err
	}
	m.audit("unlock", "password")
	return nil
}

// UnlockShamir recovers the master key from Shamir shares and loads vault.data.
func (m *Manager) UnlockShamir(shares []shamir.Share) error {
	if len(shares) < 2 {
		return errors.New("need at least 2 shares")
	}
	if m.meta == nil {
		_ = m.LoadMetaIfExists()
	}
	mk, err := shamir.Recover(shares)
	if err != nil {
		return err
	}
	m.masterKey = mk
	if err := m.loadData(); err != nil {
		m.masterKey = nil
		m.secrets = map[string]*SecretEntry{}
		return fmt.Errorf("cannot decrypt vault data: %w", err)
	}
	m.audit("unlock", fmt.Sprintf("shamir (%d shares)", len(shares)))
	return nil
}

// DistributeLocal writes share_<index>.bin shards under dir.
func (m *Manager) DistributeLocal(dir string) error {
	if !m.IsUnlocked() {
		return errors.New("vault is locked")
	}
	if m.meta == nil {
		if err := m.LoadMetaIfExists(); err != nil {
			return err
		}
	}
	if m.meta == nil {
		return errors.New("vault not initialized")
	}
	shares, err := shamir.Split(m.masterKey, m.meta.ShamirN, m.meta.ShamirK)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, sh := range shares {
		idx, b := sh.ToPair()
		fn := filepath.Join(dir, fmt.Sprintf("share_%d.bin", idx))
		if err := os.WriteFile(fn, b, 0o600); err != nil {
			return err
		}
	}
	m.audit("distribute", fmt.Sprintf("local %d shards -> %s", len(shares), dir))
	return nil
}

// LoadSharesFromDir reads share_1.bin ... share_n.bin written by DistributeLocal.
func LoadSharesFromDir(dir string) ([]shamir.Share, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var shares []shamir.Share
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := shareBinName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		shares = append(shares, shamir.ShareFromPair(idx, raw))
	}
	if len(shares) == 0 {
		return nil, errors.New("no share_*.bin files found")
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].X < shares[j].X })
	return shares, nil
}

func (m *Manager) Lock() {
	m.audit("lock", "memory cleared")
	m.masterKey = nil
	m.secrets = map[string]*SecretEntry{}
}

func (m *Manager) Meta() *VaultMeta { return m.meta }

func (m *Manager) LoadMetaIfExists() error {
	if _, err := os.Stat(m.metaPath); err != nil {
		return err
	}
	b, err := os.ReadFile(m.metaPath)
	if err != nil {
		return err
	}
	var meta VaultMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return err
	}
	m.meta = &meta
	return nil
}

func (m *Manager) Add(name, value, category string, tags []string, note string) error {
	if !m.IsUnlocked() {
		return errors.New("vault is locked")
	}
	if _, ok := m.secrets[name]; ok {
		return fmt.Errorf("secret already exists: %s", name)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if category == "" {
		category = "default"
	}
	m.secrets[name] = &SecretEntry{
		Name: name, Value: value, Category: category, Tags: tags,
		CreatedAt: now, UpdatedAt: now, CreatedBy: "human", Note: note,
	}
	if err := m.persist(); err != nil {
		return err
	}
	m.audit("add", name)
	return nil
}

func (m *Manager) Get(name string) (*SecretEntry, bool) {
	if !m.IsUnlocked() {
		return nil, false
	}
	e, ok := m.secrets[name]
	if !ok {
		return nil, false
	}
	cp := *e
	return &cp, true
}

func (m *Manager) Delete(name string) bool {
	if !m.IsUnlocked() {
		return false
	}
	if _, ok := m.secrets[name]; !ok {
		return false
	}
	delete(m.secrets, name)
	if err := m.persist(); err != nil {
		return false
	}
	m.audit("delete", name)
	return true
}

func (m *Manager) Update(name string, value *string, category *string, tags *[]string, note *string) error {
	if !m.IsUnlocked() {
		return errors.New("vault is locked")
	}
	e, ok := m.secrets[name]
	if !ok {
		return fmt.Errorf("secret not found: %s", name)
	}
	if value != nil {
		e.Value = *value
	}
	if category != nil {
		e.Category = *category
	}
	if tags != nil {
		e.Tags = *tags
	}
	if note != nil {
		e.Note = *note
	}
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.persist(); err != nil {
		return err
	}
	m.audit("update", name)
	return nil
}

func (m *Manager) ListSecrets(category string) []map[string]interface{} {
	if !m.IsUnlocked() {
		return nil
	}
	var out []map[string]interface{}
	for _, e := range m.secrets {
		if category != "" && e.Category != category {
			continue
		}
		out = append(out, map[string]interface{}{
			"name": e.Name, "category": e.Category, "tags": e.Tags,
			"created_at": e.CreatedAt, "updated_at": e.UpdatedAt, "note": e.Note,
		})
	}
	return out
}

func (m *Manager) Search(query string) []map[string]interface{} {
	if !m.IsUnlocked() {
		return nil
	}
	q := strings.ToLower(query)
	var out []map[string]interface{}
	for _, e := range m.secrets {
		if strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Note), q) {
			out = append(out, map[string]interface{}{
				"name": e.Name, "category": e.Category, "tags": e.Tags,
			})
			continue
		}
		for _, t := range e.Tags {
			if strings.Contains(strings.ToLower(t), q) {
				out = append(out, map[string]interface{}{
					"name": e.Name, "category": e.Category, "tags": e.Tags,
				})
				break
			}
		}
	}
	return out
}

func (m *Manager) Status() map[string]interface{} {
	if m.meta == nil {
		return map[string]interface{}{"initialized": false}
	}
	nodes := make([]interface{}, 0, len(m.meta.ShareMap))
	for _, sm := range m.meta.ShareMap {
		if n, ok := sm["node"]; ok {
			nodes = append(nodes, n)
		}
	}
	return map[string]interface{}{
		"initialized":    true,
		"vault_id":       m.meta.VaultID,
		"version":        m.meta.Version,
		"unlocked":       m.IsUnlocked(),
		"entry_count":    m.meta.EntryCount,
		"shamir":         fmt.Sprintf("%d-of-%d", m.meta.ShamirK, m.meta.ShamirN),
		"share_nodes":    nodes,
		"backup_targets": m.meta.BackupTargets,
		"created_at":     m.meta.CreatedAt,
		"last_modified":  m.meta.LastModified,
	}
}

func (m *Manager) saveData() error {
	raw, err := json.Marshal(m.secrets)
	if err != nil {
		return err
	}
	blob, err := m.eng.EncryptWithKey(raw, m.masterKey, "default")
	if err != nil {
		return err
	}
	return os.WriteFile(m.dataPath, blob.ToBytes(), 0o600)
}

func (m *Manager) loadData() error {
	b, err := os.ReadFile(m.dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			m.secrets = map[string]*SecretEntry{}
			return nil
		}
		return err
	}
	blob, err := engine.BlobFromBytes(b)
	if err != nil {
		return err
	}
	raw, err := m.eng.DecryptWithKey(blob, m.masterKey)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &m.secrets); err != nil {
		return err
	}
	if m.secrets == nil {
		m.secrets = map[string]*SecretEntry{}
	}
	return nil
}

func (m *Manager) saveMeta() error {
	if m.meta == nil {
		return nil
	}
	m.meta.EntryCount = len(m.secrets)
	m.meta.LastModified = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.MarshalIndent(m.meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.metaPath, b, 0o600)
}

func (m *Manager) persist() error {
	if err := m.saveData(); err != nil {
		return err
	}
	return m.saveMeta()
}

func (m *Manager) auditPath() string { return filepath.Join(m.VaultDir, "audit.jsonl") }

func (m *Manager) audit(action, detail string) {
	vid := "none"
	if m.meta != nil {
		vid = m.meta.VaultID
	}
	ev := map[string]interface{}{
		"ts":       time.Now().UTC().Format(time.RFC3339Nano),
		"action":   action,
		"detail":   detail,
		"vault_id": vid,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f, err := os.OpenFile(m.auditPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// Rekey replaces the master key, re-encrypts vault.data, clears Shamir share_map, and returns new shares.
func (m *Manager) Rekey(newPassword string) (shares []shamir.Share, newKeyHash string, err error) {
	if !m.IsUnlocked() {
		return nil, "", errors.New("vault is locked")
	}
	if m.meta == nil {
		return nil, "", errors.New("vault not initialized")
	}
	newKey := make([]byte, engine.KeySize)
	if _, err := rand.Read(newKey); err != nil {
		return nil, "", err
	}
	blob, err := m.eng.Encrypt(newKey, []byte(newPassword), "master-key")
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(m.keyEncPath, blob.ToBytes(), 0o600); err != nil {
		return nil, "", err
	}
	m.masterKey = newKey
	if err := m.saveData(); err != nil {
		return nil, "", err
	}
	m.meta.ShareMap = nil
	if err := m.saveMeta(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(newKey)
	newKeyHash = fmt.Sprintf("%x", sum[:])[:16]
	shares, err = shamir.Split(newKey, m.meta.ShamirN, m.meta.ShamirK)
	if err != nil {
		return nil, "", err
	}
	m.audit("rekey", fmt.Sprintf("master key rotated, new %d-of-%d shares", m.meta.ShamirK, m.meta.ShamirN))
	return shares, newKeyHash, nil
}

// ExportEncrypted serializes secrets JSON and encrypts with exportPassword.
func (m *Manager) ExportEncrypted(exportPassword string) ([]byte, error) {
	if !m.IsUnlocked() {
		return nil, errors.New("vault is locked")
	}
	raw, err := json.Marshal(m.secrets)
	if err != nil {
		return nil, err
	}
	blob, err := m.eng.Encrypt(raw, []byte(exportPassword), "vault-export")
	if err != nil {
		return nil, err
	}
	m.audit("export", fmt.Sprintf("%d entries", len(m.secrets)))
	return blob.ToBytes(), nil
}

// ImportEncrypted decrypts an export blob and merges into the unlocked vault.
func (m *Manager) ImportEncrypted(data []byte, password string, merge bool) (int, error) {
	if !m.IsUnlocked() {
		return 0, errors.New("vault is locked")
	}
	blob, err := engine.BlobFromBytes(data)
	if err != nil {
		return 0, err
	}
	plain, err := m.eng.Decrypt(blob, []byte(password))
	if err != nil {
		return 0, err
	}
	var incoming map[string]*SecretEntry
	if err := json.Unmarshal(plain, &incoming); err != nil {
		return 0, err
	}
	n := 0
	for name, ent := range incoming {
		if ent == nil {
			continue
		}
		if !merge {
			if _, exists := m.secrets[name]; exists {
				continue
			}
		}
		if ent.Name == "" {
			ent.Name = name
		}
		m.secrets[name] = ent
		n++
	}
	if err := m.persist(); err != nil {
		return 0, err
	}
	m.audit("import", fmt.Sprintf("%d entries (merge=%v)", n, merge))
	return n, nil
}

// GetAuditLog returns the last lastN JSON lines from audit.jsonl.
func (m *Manager) GetAuditLog(lastN int) ([]map[string]interface{}, error) {
	if lastN <= 0 {
		lastN = 50
	}
	f, err := os.Open(m.auditPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(lines) > lastN {
		lines = lines[len(lines)-lastN:]
	}
	var out []map[string]interface{}
	for _, ln := range lines {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(ln), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Info returns engine info.
func Info() map[string]string {
	return map[string]string{
		"cipher":             "AES-256-GCM",
		"kdf":                "Argon2id",
		"argon2_time_cost":   strconv.Itoa(engine.Argon2Time),
		"argon2_memory_cost": fmt.Sprintf("%d MB", engine.Argon2Memory/1024),
		"argon2_parallelism": strconv.Itoa(engine.Argon2Threads),
		"key_size":           "256 bits",
		"shamir_field":       "GF(2^8) mod 0x11D",
	}
}
