package meshdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ServerInfo matches ~/.meshdb/config.json "servers" entries.
type ServerInfo struct {
	IP       string `json:"ip"`
	User     string `json:"user"`
	HomePath string `json:"home_path"`
}

// LoadDistributedServers reads Python-compatible config from ~/.meshdb/config.json or ~/.mpop/config.json.
func LoadDistributedServers() map[string]ServerInfo {
	home, _ := os.UserHomeDir()
	paths := []string{
		filepath.Join(home, ".meshdb", "config.json"),
		filepath.Join(home, ".mpop", "config.json"),
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var root struct {
			Servers map[string]ServerInfo `json:"servers"`
		}
		if json.Unmarshal(b, &root) != nil || len(root.Servers) == 0 {
			continue
		}
		return root.Servers
	}
	return nil
}

func agentScriptPath(info ServerInfo) string {
	if info.HomePath != "" {
		return filepath.Join(info.HomePath, ".local/bin/meshdb_agent.py")
	}
	if info.User == "root" {
		return "/root/.local/bin/meshdb_agent.py"
	}
	if info.User != "" {
		return fmt.Sprintf("/home/%s/.local/bin/meshdb_agent.py", info.User)
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local/bin/meshdb_agent.py")
}

func pickLocalServerName(servers map[string]ServerInfo) string {
	host, _ := os.Hostname()
	hl := strings.ToLower(host)
	for name := range servers {
		if strings.Contains(hl, strings.ToLower(name)) {
			return name
		}
	}
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		locals := map[string]bool{}
		for _, a := range addrs {
			if ip, ok := a.(*net.IPNet); ok && !ip.IP.IsLoopback() {
				locals[ip.IP.String()] = true
			}
		}
		for name, info := range servers {
			if locals[info.IP] {
				return name
			}
		}
	}
	return "local"
}

func shellSingleQuoted(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// RemoteSearchSSH runs meshdb_agent.py json search on a host via ssh(1).
func RemoteSearchSSH(serverName string, info ServerInfo, query string, limit int, docType, sourceDir string) ([]SearchRow, error) {
	if info.IP == "" || info.User == "" {
		return nil, fmt.Errorf("missing ip/user for %q", serverName)
	}
	agent := agentScriptPath(info)
	lim := limit * 2
	if lim < 10 {
		lim = 20
	}
	remote := fmt.Sprintf("python3 %s json search %s --limit %d", agent, shellSingleQuoted(query), lim)
	if docType != "" {
		remote += fmt.Sprintf(" --type %s", shellSingleQuoted(docType))
	}
	if sourceDir != "" {
		remote += fmt.Sprintf(" --dir %s", shellSingleQuoted(sourceDir))
	}
	out, err := sshRun(info, remote, 25*time.Second)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", serverName, err, strings.TrimSpace(string(out)))
	}
	var payload struct {
		Results []struct {
			Filepath string  `json:"filepath"`
			Filename string  `json:"filename"`
			DocType  string  `json:"doc_type"`
			FileSize int64   `json:"file_size"`
			Rank     float64 `json:"rank"`
			Snippet  string  `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("%s: parse JSON: %w (output: %s)", serverName, err, truncate(string(out), 200))
	}
	var rows []SearchRow
	for _, r := range payload.Results {
		sc := r.Rank
		if sc < 0 {
			sc = -sc
		}
		rows = append(rows, SearchRow{
			Path:     r.Filepath,
			Filename: r.Filename,
			Type:     r.DocType,
			Size:     r.FileSize,
			Mtime:    "",
			Snippet:  r.Snippet,
			Score:    sc,
			Server:   serverName,
		})
	}
	return rows, nil
}

// RemoteFindSSH runs meshdb_agent.py json find on a host.
func RemoteFindSSH(serverName string, info ServerInfo, query string, limit int, docType, sourceDir string) ([]FindRow, error) {
	if info.IP == "" || info.User == "" {
		return nil, fmt.Errorf("missing ip/user for %q", serverName)
	}
	agent := agentScriptPath(info)
	lim := limit
	if lim < 1 {
		lim = 10
	}
	remote := fmt.Sprintf("python3 %s json find %s --limit %d", agent, shellSingleQuoted(query), lim)
	out, err := sshRun(info, remote, 25*time.Second)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", serverName, err, strings.TrimSpace(string(out)))
	}
	var payload struct {
		Results []struct {
			Filepath string `json:"filepath"`
			Filename string `json:"filename"`
			DocType  string `json:"doc_type"`
			FileSize int64  `json:"file_size"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("%s: parse JSON: %w (output: %s)", serverName, err, truncate(string(out), 200))
	}
	var rows []FindRow
	for _, r := range payload.Results {
		if docType != "" && r.DocType != docType {
			continue
		}
		if sourceDir != "" && !strings.Contains(r.Filepath, sourceDir) {
			continue
		}
		rows = append(rows, FindRow{
			Path:     r.Filepath,
			Filename: r.Filename,
			Type:     r.DocType,
			Size:     r.FileSize,
			Mtime:    "",
			Server:   serverName,
		})
	}
	return rows, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// DistributedSearch runs local FTS plus parallel remote meshdb_agent searches.
func DistributedSearch(db *sql.DB, query string, limit int, docType string) ([]SearchRow, map[string]interface{}, int, error) {
	if limit <= 0 {
		limit = 20
	}
	servers := LoadDistributedServers()
	localName := pickLocalServerName(servers)
	localLimit := limit * 2
	if localLimit < limit {
		localLimit = limit
	}
	localRows, err := Search(db, query, localLimit, docType, "")
	if err != nil {
		return nil, nil, 0, err
	}
	for i := range localRows {
		localRows[i].Server = localName
	}
	var all []SearchRow
	all = append(all, localRows...)
	stats := map[string]interface{}{localName: len(localRows)}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, info := range servers {
		if name == localName {
			continue
		}
		wg.Add(1)
		go func(n string, inf ServerInfo) {
			defer wg.Done()
			rows, err := RemoteSearchSSH(n, inf, query, limit, docType, "")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				stats[n] = "error: " + err.Error()
				return
			}
			stats[n] = len(rows)
			all = append(all, rows...)
		}(name, info)
	}
	wg.Wait()

	total := len(all)
	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, stats, total, nil
}
