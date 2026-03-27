package meshdb

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ParseRemotePath parses "server:/path".
func ParseRemotePath(spec string) (server, remotePath string, ok bool) {
	if strings.HasPrefix(spec, "/") {
		return "", "", false
	}
	i := strings.Index(spec, ":")
	if i <= 0 {
		return "", "", false
	}
	server = spec[:i]
	remotePath = spec[i+1:]
	if len(server) == 0 || len(server) > 10 {
		return "", "", false
	}
	for _, r := range server {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return "", "", false
		}
	}
	if remotePath == "" {
		return "", "", false
	}
	return server, remotePath, true
}

// RemoteIndexViaAgent runs meshdb_agent.py json index on remote host via SSH.
func RemoteIndexViaAgent(serverName, remotePath string, replace bool) (map[string]interface{}, error) {
	srv := LoadDistributedServers()
	info, ok := srv[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not in ~/.meshdb/config.json servers", serverName)
	}
	if info.IP == "" || info.User == "" {
		return nil, fmt.Errorf("missing ip/user for server %q", serverName)
	}
	agent := agentScriptPath(info)
	remote := fmt.Sprintf("python3 %s json index %s", agent, shellSingleQuoted(remotePath))
	if replace {
		remote += " --replace"
	}
	out, err := sshRun(info, remote, 15*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", serverName, err, strings.TrimSpace(string(out)))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, fmt.Errorf("%s: parse JSON: %w (output: %s)", serverName, err, truncate(string(out), 400))
	}
	m["_remote_server"] = serverName
	m["_remote_path"] = remotePath
	m["_note"] = "remote host's meshdb.db updated only; local DB unchanged (default meshdb index server:/path merges into local DB)"
	return m, nil
}
