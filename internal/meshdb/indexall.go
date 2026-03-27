package meshdb

import (
	"database/sql"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// skipHomeDirs matches Python SKIP_HOME_DIRS (subset).
var skipHomeDirs = map[string]bool{
	"snap": true, "bin": true, "logs": true, "Library": true, "Applications": true,
	"Desktop": true, "Downloads": true, "Documents": true, "Pictures": true,
	"Movies": true, "Music": true, "Public": true, "Videos": true,
	"venv": true, ".venv": true, "node_modules": true,
}

// LoadMeshdbServerRegistry reads meshdb_servers from config.
func LoadMeshdbServerRegistry() map[string][]string {
	return loadMeshdbRegistry(true)
}

func loadMeshdbRegistry(expandTilde bool) map[string][]string {
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
			MeshdbServers map[string][]string `json:"meshdb_servers"`
		}
		if json.Unmarshal(b, &root) != nil || len(root.MeshdbServers) == 0 {
			continue
		}
		out := make(map[string][]string)
		for k, dirs := range root.MeshdbServers {
			var expanded []string
			for _, x := range dirs {
				x = strings.TrimSpace(x)
				if expandTilde {
					if strings.HasPrefix(x, "~/") {
						x = filepath.Join(home, x[2:])
					} else if x == "~" {
						x = home
					}
					expanded = append(expanded, filepath.Clean(x))
				} else {
					expanded = append(expanded, x)
				}
			}
			out[k] = expanded
		}
		return out
	}
	return nil
}

func expandLocalHome(s string) string {
	home, _ := os.UserHomeDir()
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "~/") {
		return filepath.Clean(filepath.Join(home, s[2:]))
	}
	if s == "~" {
		return home
	}
	return filepath.Clean(s)
}

// DiscoverProjectsOneLevel lists direct child dirs of home.
func DiscoverProjectsOneLevel(home string) []string {
	home = filepath.Clean(home)
	ent, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ent {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || skipHomeDirs[name] {
			continue
		}
		out = append(out, filepath.Join(home, name))
	}
	return out
}

// DiscoverProjectsRemoteSSH lists direct child dirs of a remote home.
func DiscoverProjectsRemoteSSH(info ServerInfo, remoteHome string) ([]string, error) {
	remoteHome = strings.TrimSpace(remoteHome)
	if remoteHome == "" {
		return nil, nil
	}
	cmd := "find " + shellSingleQuoted(remoteHome) + " -maxdepth 1 -mindepth 1 -type d 2>/dev/null | sort"
	out, err := sshRun(info, cmd, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		base := path.Base(line)
		if strings.HasPrefix(base, ".") || skipHomeDirs[base] {
			continue
		}
		paths = append(paths, line)
	}
	return paths, nil
}

// IndexAll indexes discoverable project dirs per meshdb_servers entry.
func IndexAll(db *sql.DB, replace bool) map[string]interface{} {
	reg := loadMeshdbRegistry(false)
	if len(reg) == 0 {
		h, _ := os.UserHomeDir()
		reg = map[string][]string{"local": {h}}
	}
	dist := LoadDistributedServers()
	totProj, totNew, totUpd, totErr, totScan, totSkip := 0, 0, 0, 0, 0, 0
	srvOut := map[string]interface{}{}
	for srv, homes := range reg {
		info, hasDist := dist[srv]
		remote := hasDist && info.IP != "" && info.User != ""
		projN, newN, upN, skipN, errN, movN, scanN := 0, 0, 0, 0, 0, 0, 0
		var dirList []interface{}
		note := ""
		if remote {
			for _, h := range homes {
				projects, err := DiscoverProjectsRemoteSSH(info, h)
				if err != nil {
					if note != "" {
						note += "; "
					}
					note += err.Error()
					continue
				}
				projN += len(projects)
				for _, proj := range projects {
					res, err := IndexRemoteMerge(db, srv, proj, replace)
					if err != nil {
						errN++
						dirList = append(dirList, map[string]interface{}{"path": srv + ":" + proj, "error": err.Error()})
						continue
					}
					newN += res.New
					upN += res.Updated
					skipN += res.Skipped
					errN += res.Errors
					scanN += res.TotalScanned
					dirList = append(dirList, map[string]interface{}{
						"path": srv + ":" + proj, "scanned": res.TotalScanned, "new": res.New, "updated": res.Updated,
					})
				}
			}
			srvOut[srv] = map[string]interface{}{
				"projects": projN, "new": newN, "updated": upN, "skipped": skipN, "errors": errN, "moved": 0,
				"dirs": dirList, "note": note, "mode": "remote",
				"files_scanned": scanN,
			}
		} else {
			for _, h := range homes {
				he := expandLocalHome(h)
				if _, err := os.Stat(he); err != nil {
					note = "missing home: " + he
					continue
				}
				projects := DiscoverProjectsOneLevel(he)
				projN += len(projects)
				for _, proj := range projects {
					res, err := IndexDirectory(db, proj, replace)
					if err != nil {
						errN++
						dirList = append(dirList, map[string]interface{}{"path": proj, "error": err.Error()})
						continue
					}
					newN += res.New
					upN += res.Updated
					skipN += res.Skipped
					errN += res.Errors
					movN += res.Moved
					scanN += res.TotalScanned
					dirList = append(dirList, map[string]interface{}{
						"path": proj, "scanned": res.TotalScanned, "new": res.New, "updated": res.Updated,
					})
				}
			}
			srvOut[srv] = map[string]interface{}{
				"projects": projN, "new": newN, "updated": upN, "skipped": skipN, "errors": errN, "moved": movN,
				"dirs": dirList, "note": note, "mode": "local",
				"files_scanned": scanN,
			}
		}
		totProj += projN
		totNew += newN
		totUpd += upN
		totErr += errN
		totScan += scanN
		totSkip += skipN
	}
	return map[string]interface{}{
		"servers": srvOut, "total_projects": totProj,
		"total_new": totNew, "total_updated": totUpd, "total_errors": totErr,
		"total_skipped": totSkip, "total_files_scanned": totScan,
	}
}

// IndexAllLocal is an alias for IndexAll.
func IndexAllLocal(db *sql.DB, replace bool) map[string]interface{} {
	return IndexAll(db, replace)
}
