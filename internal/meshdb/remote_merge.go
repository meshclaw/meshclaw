package meshdb

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// remoteListFiles mirrors Python remote_list_files (find | grep | head).
func remoteListFiles(info ServerInfo, dirpath string) ([]string, error) {
	remote := fmt.Sprintf(
		`find %s `+
			`-name ".git" -prune -o -name "node_modules" -prune -o `+
			`-name "__pycache__" -prune -o -name ".venv" -prune -o `+
			`-name "venv" -prune -o -name ".mypy_cache" -prune -o `+
			`-name "target" -prune -o -name "dist" -prune -o `+
			`-name ".tox" -prune -o -name ".eggs" -prune -o `+
			`-type f -print 2>/dev/null | `+
			`grep -E '\.(py|sh|bash|js|ts|jsx|tsx|md|txt|rst|json|yaml|yml|toml|ini|cfg|conf|env|html|css|scss|go|rs|java|c|cpp|h|hpp|rb|lua|sql|xml|csv|log|swift|kt)$' | head -5000`,
		shellSingleQuoted(dirpath))
	out, err := sshRun(info, remote, 90*time.Second)
	if err != nil {
		return nil, fmt.Errorf("remote list: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasPrefix(line, "/") {
			files = append(files, line)
		}
	}
	return files, nil
}

func remoteStatFile(info ServerInfo, fp string) (size int64, mtime float64, err error) {
	cmd := fmt.Sprintf(`stat -c "%%s %%Y" %s 2>/dev/null || stat -f "%%z %%m" %s 2>/dev/null`,
		shellSingleQuoted(fp), shellSingleQuoted(fp))
	out, err := sshRun(info, cmd, 20*time.Second)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return 0, 0, fmt.Errorf("stat: %q", out)
	}
	size, _ = strconv.ParseInt(fields[0], 10, 64)
	mtime, _ = strconv.ParseFloat(fields[1], 64)
	return size, mtime, nil
}

func remoteReadFile(info ServerInfo, fp string) (string, error) {
	cmd := fmt.Sprintf(`cat %s 2>/dev/null | head -c %d`, shellSingleQuoted(fp), maxContentLen)
	out, err := sshRun(info, cmd, 120*time.Second)
	if err != nil {
		return "", err
	}
	s := string(out)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return s, nil
}

// remoteFileHashPython matches meshdb.py index_remote.
func remoteFileHashPython(content string, size int64) string {
	r := []rune(content)
	var seg []rune
	if len(r) <= 4096 {
		seg = append(append([]rune(nil), r...), r...)
	} else {
		seg = append(r[:4096], r[len(r)-4096:]...)
	}
	piece := string(seg) + fmt.Sprintf("%d", size)
	return fmt.Sprintf("%x", md5.Sum([]byte(piece)))
}

// IndexRemoteMerge pulls file contents over SSH into the local meshdb.
func IndexRemoteMerge(db *sql.DB, serverName, dirpath string, replace bool) (*IndexResult, error) {
	srv := LoadDistributedServers()
	info, ok := srv[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not in ~/.meshdb/config.json servers", serverName)
	}
	sourceKey := serverName + ":" + dirpath
	res := &IndexResult{SourceDir: sourceKey}

	if replace {
		rows, _ := db.Query("SELECT rowid FROM docs WHERE source_dir = ?", sourceKey)
		var rowids []any
		for rows.Next() {
			var rid int64
			rows.Scan(&rid)
			rowids = append(rowids, rid)
		}
		rows.Close()
		if len(rowids) > 0 {
			place := strings.Repeat("?,", len(rowids)-1) + "?"
			db.Exec("DELETE FROM search_index WHERE rowid IN ("+place+")", rowids...)
		}
		db.Exec("DELETE FROM docs WHERE source_dir = ?", sourceKey)
	}

	existing := map[string]struct {
		hash  string
		mtime float64
		rowid int64
	}{}
	rows, _ := db.Query("SELECT path, file_hash, mtime, rowid FROM docs WHERE source_dir = ?", sourceKey)
	for rows.Next() {
		var p, fh string
		var mt float64
		var rid int64
		rows.Scan(&p, &fh, &mt, &rid)
		existing[p] = struct {
			hash  string
			mtime float64
			rowid int64
		}{fh, mt, rid}
	}
	rows.Close()

	files, err := remoteListFiles(info, dirpath)
	if err != nil {
		return res, err
	}
	res.TotalScanned = len(files)
	now := float64(time.Now().Unix())

	for _, fp := range files {
		size, mtime, err := remoteStatFile(info, fp)
		if err != nil {
			res.Errors++
			continue
		}
		remotePath := serverName + ":" + fp

		if ex, ok := existing[remotePath]; ok && ex.mtime == mtime {
			res.Skipped++
			continue
		}

		content, err := remoteReadFile(info, fp)
		if err != nil || content == "" {
			res.Errors++
			continue
		}

		fh := remoteFileHashPython(content, size)
		fname := path.Base(fp)
		ftype := detectType(fp)
		docID := fmt.Sprintf("%x", md5.Sum([]byte(remotePath)))
		preview := content
		if r := []rune(preview); len(r) > previewLen {
			preview = string(r[:previewLen])
		}

		if ex, ok := existing[remotePath]; ok {
			_, err = db.Exec(`UPDATE docs SET filename=?, preview=?, type=?, size=?, file_hash=?, mtime=?, indexed_at=? WHERE path=? AND source_dir=?`,
				fname, preview, ftype, size, fh, mtime, now, remotePath, sourceKey)
			if err != nil {
				res.Errors++
				continue
			}
			db.Exec("DELETE FROM search_index WHERE rowid=?", ex.rowid)
			db.Exec("INSERT INTO search_index(rowid,filename,content,type) VALUES(?,?,?,?)", ex.rowid, fname, content, ftype)
			res.Updated++
			continue
		}

		_, err = db.Exec(`INSERT OR REPLACE INTO docs
            (id,path,filename,preview,type,size,file_hash,mtime,indexed_at,source_dir)
            VALUES(?,?,?,?,?,?,?,?,?,?)`,
			docID, remotePath, fname, preview, ftype, size, fh, mtime, now, sourceKey)
		if err != nil {
			res.Errors++
			continue
		}
		var rid int64
		if err := db.QueryRow("SELECT rowid FROM docs WHERE path=? AND source_dir=?", remotePath, sourceKey).Scan(&rid); err != nil {
			res.Errors++
			continue
		}
		db.Exec("INSERT INTO search_index(rowid,filename,content,type) VALUES(?,?,?,?)", rid, fname, content, ftype)
		res.New++
	}

	return res, nil
}
