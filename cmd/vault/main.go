// Command vault is a secure secrets manager with Shamir's Secret Sharing.
package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"

	vbackup "github.com/meshclaw/meshclaw/internal/vault/backup"
	"github.com/meshclaw/meshclaw/internal/vault/engine"
	"github.com/meshclaw/meshclaw/internal/vault/manager"
	"github.com/meshclaw/meshclaw/internal/vault/shamir"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printDoc()
		return
	}
	cmd := args[0]
	if cmd == "key" && len(args) > 1 {
		runKey(args[1], parseArgs(args[2:]))
		return
	}
	p := parseArgs(args[1:])
	switch cmd {
	case "init":
		cmdInit(p)
	case "unlock":
		cmdUnlock(p)
	case "lock":
		cmdLock(p)
	case "add":
		cmdAdd(p)
	case "get":
		cmdGet(p)
	case "update":
		cmdUpdate(p)
	case "delete":
		cmdDelete(p)
	case "list":
		cmdList(p)
	case "search":
		cmdSearch(p)
	case "status":
		cmdStatus(p)
	case "info":
		cmdInfo()
	case "encrypt":
		cmdEncrypt(p)
	case "decrypt":
		cmdDecrypt(p)
	case "verify":
		cmdVerify(p)
	case "backup":
		cmdBackup(p)
	case "restore":
		cmdRestore(p)
	case "export":
		cmdExport(p)
	case "import":
		cmdImport(p)
	case "audit":
		cmdAudit(p)
	case "rekey":
		cmdRekey(p)
	case "distribute":
		cmdDistribute(p)
	case "collect":
		cmdCollect(p)
	case "help", "-h", "--help":
		printDoc()
	case "version", "-v", "--version":
		fmt.Println("vault v1.0.0")
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		printDoc()
		os.Exit(1)
	}
}

func parseArgs(argv []string) map[string]string {
	args := map[string]string{}
	var pos []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") {
			key := a
			if i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				args[key] = argv[i+1]
				i++
			} else {
				args[key] = "true"
			}
			continue
		}
		pos = append(pos, a)
	}
	if len(pos) > 0 {
		args["<name>"] = pos[0]
		args["<file>"] = pos[0]
	}
	if len(pos) > 1 {
		args["<query>"] = pos[1]
		args["<nodes>"] = pos[1]
	}
	return args
}

func vaultFrom(args map[string]string) *manager.Manager {
	dir := ""
	if d := args["--vault-dir"]; d != "" {
		dir = d
	}
	m := manager.NewManager(dir)
	_ = m.LoadMetaIfExists()
	return m
}

func readPassword(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return string(b)
}

func readPasswordConfirm(prompt string) string {
	a := readPassword(prompt)
	b := readPassword("Confirm password: ")
	if a != b {
		fmt.Println("Passwords do not match.")
		os.Exit(1)
	}
	return a
}

func cmdInit(args map[string]string) {
	m := vaultFrom(args)
	n := 5
	k := 3
	if v := args["-n"]; v != "" {
		n, _ = strconv.Atoi(v)
	}
	if v := args["-k"]; v != "" {
		k, _ = strconv.Atoi(v)
	}
	pw := readPasswordConfirm("Set master password: ")
	vid, shamirDesc, keyHash, shares, err := m.Init(pw, n, k)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Println("SecureVault initialized")
	fmt.Println("   Vault ID:", vid)
	fmt.Println("   Shamir:", shamirDesc)
	fmt.Println("   Master key hash:", keyHash)
	fmt.Println()
	fmt.Printf("Shamir shares %d total (%d needed to recover):\n", n, k)
	for _, sh := range shares {
		idx, b := sh.ToPair()
		fmt.Printf("   share %d: %s\n", idx, base64.StdEncoding.EncodeToString(b))
	}
	fmt.Println()
	fmt.Println("Store each share in a separate safe location!")
}

func cmdUnlock(args map[string]string) {
	m := vaultFrom(args)
	if m.IsUnlocked() {
		fmt.Println("Already unlocked")
		return
	}
	pw := readPassword("Password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	fmt.Println("Vault unlocked")
	s := m.Status()
	fmt.Println("   Secrets:", s["entry_count"])
}

func cmdLock(args map[string]string) {
	m := vaultFrom(args)
	m.Lock()
	fmt.Println("Vault locked -- memory cleared")
}

func cmdAdd(args map[string]string) {
	m := vaultFrom(args)
	_ = m.LoadMetaIfExists()
	pw := readPassword("Password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	name := args["<name>"]
	if name == "" {
		fmt.Println("Usage: vault add <name> [--cat TYPE] [--tags t1,t2] [--note TEXT]")
		os.Exit(1)
	}
	val := readPasswordConfirm("Secret value: ")
	cat := args["--cat"]
	if cat == "" {
		cat = "default"
	}
	var tags []string
	if t := args["--tags"]; t != "" {
		tags = strings.Split(t, ",")
	}
	note := args["--note"]
	if err := m.Add(name, val, cat, tags, note); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Printf("Added: %s (category: %s)\n", name, cat)
}

func cmdGet(args map[string]string) {
	m := vaultFrom(args)
	pw := readPassword("Password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	name := args["<name>"]
	if name == "" {
		fmt.Println("Usage: vault get <name>")
		os.Exit(1)
	}
	e, ok := m.Get(name)
	if !ok {
		fmt.Printf("Not found: %s\n", name)
		os.Exit(1)
	}
	fmt.Println("Name:", e.Name)
	fmt.Println("Value:", e.Value)
	fmt.Println("Category:", e.Category)
	if len(e.Tags) > 0 {
		fmt.Println("Tags:", strings.Join(e.Tags, ", "))
	}
	if e.Note != "" {
		fmt.Println("Note:", e.Note)
	}
	fmt.Println("Created:", e.CreatedAt)
	fmt.Println("Updated:", e.UpdatedAt)
}

func cmdUpdate(args map[string]string) {
	m := vaultFrom(args)
	pw := readPassword("Password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	name := args["<name>"]
	if name == "" {
		fmt.Println("Usage: vault update <name> [--cat TYPE] [--note TEXT] [--value]")
		os.Exit(1)
	}
	var val *string
	if args["--value"] == "true" || args["-v"] == "true" {
		s := readPasswordConfirm("New secret value: ")
		val = &s
	}
	var cat *string
	if c := args["--cat"]; c != "" {
		cat = &c
	}
	var tags *[]string
	if t := args["--tags"]; t != "" {
		spl := strings.Split(t, ",")
		tags = &spl
	}
	var note *string
	if n := args["--note"]; n != "" {
		note = &n
	}
	if val == nil && cat == nil && tags == nil && note == nil {
		s := readPasswordConfirm("New secret value: ")
		val = &s
	}
	if err := m.Update(name, val, cat, tags, note); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Printf("Updated: %s\n", name)
}

func cmdDelete(args map[string]string) {
	m := vaultFrom(args)
	pw := readPassword("Password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	name := args["<name>"]
	if name == "" {
		fmt.Println("Usage: vault delete <name>")
		os.Exit(1)
	}
	fmt.Printf("Delete '%s'? (y/N): ", name)
	var line string
	fmt.Scanln(&line)
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		fmt.Println("Cancelled")
		return
	}
	if m.Delete(name) {
		fmt.Printf("Deleted: %s\n", name)
	} else {
		fmt.Printf("Not found: %s\n", name)
	}
}

func cmdList(args map[string]string) {
	m := vaultFrom(args)
	pw := readPassword("Password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	cat := args["--cat"]
	secrets := m.ListSecrets(cat)
	if len(secrets) == 0 {
		fmt.Println("(empty)")
		return
	}
	maxN, maxC := 4, 8
	for _, s := range secrets {
		n := s["name"].(string)
		c := s["category"].(string)
		if len(n) > maxN {
			maxN = len(n)
		}
		if len(c) > maxC {
			maxC = len(c)
		}
	}
	fmt.Printf("%-*s %-*s Tags\n", maxN+2, "Name", maxC+2, "Category")
	fmt.Println(strings.Repeat("-", maxN+maxC+30))
	for _, s := range secrets {
		tags := formatTags(s["tags"])
		fmt.Printf("%-*s %-*s %s\n", maxN+2, s["name"].(string), maxC+2, s["category"].(string), tags)
	}
	fmt.Printf("\nTotal: %d\n", len(secrets))
}

func cmdSearch(args map[string]string) {
	m := vaultFrom(args)
	pw := readPassword("Password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	q := args["<name>"]
	if q == "" {
		q = args["<query>"]
	}
	if q == "" {
		fmt.Println("Usage: vault search <query>")
		os.Exit(1)
	}
	results := m.Search(q)
	if len(results) == 0 {
		fmt.Printf("No secrets matching '%s'\n", q)
		return
	}
	for _, r := range results {
		t := formatTags(r["tags"])
		tags := ""
		if t != "" {
			tags = " [" + t + "]"
		}
		fmt.Printf("  %s (%s)%s\n", r["name"], r["category"], tags)
	}
	fmt.Printf("\n%d found\n", len(results))
}

func cmdStatus(args map[string]string) {
	_ = args
	m := vaultFrom(map[string]string{})
	_ = m.LoadMetaIfExists()
	s := m.Status()
	if s["initialized"] == false {
		fmt.Println("Not initialized")
		return
	}
	fmt.Printf("vault_id: %v\nunlocked: %v\nentries: %v\nshamir: %v\n",
		s["vault_id"], s["unlocked"], s["entry_count"], s["shamir"])
}

func cmdInfo() {
	for k, v := range manager.Info() {
		fmt.Printf("%s: %s\n", k, v)
	}
}

func cmdEncrypt(args map[string]string) {
	src := args["<file>"]
	if src == "" {
		fmt.Println("Usage: vault encrypt <file> [-o output]")
		os.Exit(1)
	}
	out := src + ".vault"
	if o := args["-o"]; o != "" {
		out = o
	}
	data, err := os.ReadFile(src)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	pw := readPassword("Password: ")
	eng := engine.VaultEngine{}
	base := filepath.Base(src)
	aad := sha256.Sum256([]byte(base))
	blob, err := eng.EncryptAAD(data, []byte(pw), "file", aad[:])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, blob.ToBytes(), 0o600); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Wrote", out)
}

func cmdDecrypt(args map[string]string) {
	src := args["<file>"]
	if src == "" {
		fmt.Println("Usage: vault decrypt <file> [-o output]")
		os.Exit(1)
	}
	out := strings.TrimSuffix(src, ".vault")
	if o := args["-o"]; o != "" {
		out = o
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	blob, err := engine.BlobFromBytes(raw)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	orig := args["--original-name"]
	if orig == "" {
		if strings.HasSuffix(src, ".vault") {
			orig = filepath.Base(strings.TrimSuffix(src, ".vault"))
		} else {
			orig = filepath.Base(out)
		}
	}
	aad := sha256.Sum256([]byte(orig))
	pw := readPassword("Password: ")
	eng := engine.VaultEngine{}
	plain, err := eng.DecryptAAD(blob, []byte(pw), aad[:])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, plain, 0o600); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Wrote", out)
}

func cmdVerify(args map[string]string) {
	pw := readPassword("Verification password: ")
	vdir := args["--vault-dir"]
	if vdir == "" {
		home, _ := os.UserHomeDir()
		vdir = filepath.Join(home, ".vault")
	}
	eng := engine.VaultEngine{}

	var files []string
	if f := args["--file"]; f != "" {
		files = []string{f}
	} else {
		backupDir := filepath.Join(vdir, "backups")
		entries, err := os.ReadDir(backupDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "backups directory: %v (use --file <path>)\n", err)
			os.Exit(1)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(strings.ToLower(name), ".vault") {
				files = append(files, filepath.Join(backupDir, name))
			}
		}
		sort.Slice(files, func(i, j int) bool { return files[i] > files[j] })
		if len(files) == 0 {
			fmt.Println("No .vault files in", backupDir)
			os.Exit(1)
		}
		files = files[:1]
	}

	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("  [X] %s: %v\n", path, err)
			continue
		}
		blob, err := engine.BlobFromBytes(raw)
		if err != nil {
			fmt.Printf("  [X] %s: %v\n", filepath.Base(path), err)
			continue
		}
		if _, err := eng.Decrypt(blob, []byte(pw)); err != nil {
			fmt.Printf("  [X] %s: %v\n", filepath.Base(path), err)
			continue
		}
		fmt.Printf("  [OK] %s (context=%q created=%s)\n", filepath.Base(path), blob.Context, blob.CreatedAt)
	}
}

func configDirFrom(args map[string]string) string {
	if d := args["--config-dir"]; d != "" {
		return d
	}
	if d := args["--vault-dir"]; d != "" {
		return d
	}
	return vbackup.ConfigDir("")
}

func cmdBackup(args map[string]string) {
	src := args["<file>"]
	if src == "" {
		src = args["<name>"]
	}
	if src == "" {
		fmt.Println("Usage: vault backup <file> [--config-dir DIR]")
		os.Exit(1)
	}
	if st, err := os.Stat(src); err != nil || st.IsDir() {
		fmt.Println("File not found:", src)
		os.Exit(1)
	}
	pw := readPasswordConfirm("Backup encryption password: ")
	cfgDir := configDirFrom(args)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := os.MkdirAll(vbackup.BackupDir(cfgDir), 0o700); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	rec, path, err := vbackup.Backup(cfgDir, src, []byte(pw))
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Println("Backup complete")
	fmt.Println("   Wrote:", path)
	if len(rec.Targets) > 0 {
		fmt.Println("   Targets:", strings.Join(rec.Targets, ", "))
	} else {
		fmt.Println("   Targets: (local only)")
	}
}

func cmdRestore(args map[string]string) {
	out := args["-o"]
	if out == "" {
		out = "restored_secrets"
	}
	cfgDir := configDirFrom(args)
	pw := readPassword("Decryption password: ")

	var vaultPath string
	if f := args["--file"]; f != "" {
		vaultPath = f
	} else {
		latest := vbackup.LatestLocalVault(cfgDir)
		if latest == "" {
			fmt.Println("No local backup found in", vbackup.BackupDir(cfgDir))
			os.Exit(1)
		}
		vaultPath = filepath.Join(vbackup.BackupDir(cfgDir), latest)
	}
	if err := vbackup.Restore(vaultPath, []byte(pw), out); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Println("Restore complete ->", out)
}

func cmdExport(args map[string]string) {
	m := vaultFrom(args)
	_ = m.LoadMetaIfExists()
	pw := readPassword("Vault password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	exp := readPasswordConfirm("Export password: ")
	out := args["-o"]
	if out == "" {
		out = "vault_export.vault"
	}
	data, err := m.ExportEncrypted(exp)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Export complete: %s (%d bytes)\n", out, len(data))
}

func cmdImport(args map[string]string) {
	m := vaultFrom(args)
	_ = m.LoadMetaIfExists()
	pw := readPassword("Vault password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	src := args["<file>"]
	if src == "" {
		src = args["<name>"]
	}
	if src == "" {
		fmt.Println("Usage: vault import <file> [--merge]")
		os.Exit(1)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	ip := readPassword("Import password: ")
	merge := false
	if v := args["--merge"]; v != "" {
		merge = strings.ToLower(v) != "false" && v != "0"
	}
	n, err := m.ImportEncrypted(raw, ip, merge)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Printf("%d secrets imported\n", n)
}

func cmdAudit(args map[string]string) {
	m := vaultFrom(args)
	_ = m.LoadMetaIfExists()
	last := 20
	if v := args["--last"]; v != "" {
		last, _ = strconv.Atoi(v)
	}
	log, err := m.GetAuditLog(last)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if len(log) == 0 {
		fmt.Println("No audit log")
		return
	}
	for _, ev := range log {
		ts := "?"
		if s, ok := ev["ts"].(string); ok && len(s) >= 19 {
			ts = s[:19]
		}
		action := fmt.Sprint(ev["action"])
		detail := fmt.Sprint(ev["detail"])
		fmt.Printf("  [%s] %-12s %s\n", ts, action, detail)
	}
	fmt.Printf("\nLast %d events\n", len(log))
}

func cmdRekey(args map[string]string) {
	_ = args
	m := vaultFrom(args)
	if err := m.LoadMetaIfExists(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	old := readPassword("Current password: ")
	if err := m.Unlock(old); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	newPw := readPasswordConfirm("New password: ")
	shares, keyHash, err := m.Rekey(newPw)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	meta := m.Meta()
	fmt.Println("Master key replaced")
	fmt.Println("   New key hash:", keyHash)
	if meta != nil {
		fmt.Printf("   Shamir: %d-of-%d\n", meta.ShamirK, meta.ShamirN)
	}
	fmt.Println()
	for _, s := range shares {
		i, b := s.ToPair()
		fmt.Printf("   share %d: %s\n", i, base64.StdEncoding.EncodeToString(b))
	}
	fmt.Println()
	fmt.Println("Existing shares invalidated. Run `vault distribute --local-dir DIR`")
}

func cmdDistribute(args map[string]string) {
	localDir := args["--local-dir"]
	if localDir == "" {
		fmt.Println("vault distribute: pass --local-dir DIR to write share_*.bin files")
		os.Exit(1)
	}
	localDir = filepath.Clean(localDir)
	m := vaultFrom(args)
	if err := m.LoadMetaIfExists(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	pw := readPassword("Password: ")
	if err := m.Unlock(pw); err != nil {
		fmt.Println("Wrong password")
		os.Exit(1)
	}
	if err := m.DistributeLocal(localDir); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	meta := m.Meta()
	n := 0
	if meta != nil {
		n = meta.ShamirN
	}
	fmt.Printf("Wrote %d share files to %s\n", n, localDir)
	fmt.Println("   Filenames: share_<index>.bin")
}

func cmdCollect(args map[string]string) {
	localDir := args["--local-dir"]
	if localDir == "" {
		fmt.Println("vault collect: pass --local-dir DIR containing share_*.bin files")
		os.Exit(1)
	}
	localDir = filepath.Clean(localDir)
	m := vaultFrom(args)
	if err := m.LoadMetaIfExists(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	shares, err := manager.LoadSharesFromDir(localDir)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	if err := m.UnlockShamir(shares); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	s := m.Status()
	fmt.Println("Vault unlocked (Shamir)")
	if ec, ok := s["entry_count"]; ok {
		fmt.Println("   Secrets:", ec)
	}
}

func runKey(sub string, args map[string]string) {
	switch sub {
	case "generate":
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Printf("%x\n", b)
	case "split":
		n := 5
		k := 3
		if v := args["-n"]; v != "" {
			n, _ = strconv.Atoi(v)
		}
		if v := args["-k"]; v != "" {
			k, _ = strconv.Atoi(v)
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		sh, err := shamir.Split(secret, n, k)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		for _, s := range sh {
			i, b := s.ToPair()
			fmt.Printf("share %d: %s\n", i, base64.StdEncoding.EncodeToString(b))
		}
	case "recover":
		fmt.Fprintln(os.Stderr, "Enter shares (index:base64), empty line to finish:")
		sc := bufio.NewScanner(os.Stdin)
		var shares []shamir.Share
		for {
			fmt.Fprintf(os.Stderr, "share %d> ", len(shares)+1)
			if !sc.Scan() {
				break
			}
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				break
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				fmt.Println("expected index:base64")
				os.Exit(1)
			}
			idx, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err != nil {
				fmt.Println("bad index")
				os.Exit(1)
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
			if err != nil {
				fmt.Println("bad base64")
				os.Exit(1)
			}
			shares = append(shares, shamir.ShareFromPair(idx, raw))
		}
		if len(shares) < 2 {
			fmt.Println("need at least 2 shares")
			os.Exit(1)
		}
		out, err := shamir.Recover(shares)
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
		fmt.Printf("Recovered secret (hex, %d bytes): %x\n", len(out), out)
	default:
		fmt.Println("key subcommand: generate, split, recover")
		os.Exit(1)
	}
}

func formatTags(v interface{}) string {
	switch t := v.(type) {
	case []string:
		return strings.Join(t, ", ")
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, fmt.Sprint(x))
		}
		return strings.Join(parts, ", ")
	default:
		return ""
	}
}

func printDoc() {
	fmt.Print(`SecureVault CLI - Encrypted secrets with Shamir's Secret Sharing

  vault init [-n 5] [-k 3]
  vault unlock | lock
  vault add <name> [--cat TYPE] [--tags a,b] [--note TEXT]
  vault get <name> | update <name> | delete <name>
  vault list [--cat TYPE] | search <query>
  vault status | info
  vault encrypt <file> [-o out] | decrypt <file> [-o out]
  vault verify [--vault-dir DIR] [--file PATH.vault]
  vault backup <file> [--config-dir DIR]
  vault restore [-o out] [--file PATH.vault] [--config-dir DIR]
  vault export [-o vault_export.vault]
  vault import <file> [--merge]
  vault audit [--last N]
  vault rekey
  vault distribute --local-dir DIR
  vault collect --local-dir DIR
  vault key split [-n 5] [-k 3] | key recover

`)
}
