// Package meshdb provides distributed full-text search using SQLite FTS5.
package meshdb

import (
	"crypto/md5"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const maxFileSize = 10 * 1024 * 1024
const maxContentLen = 500000
const previewLen = 1024

var textExts = map[string]bool{
	".md": true, ".txt": true, ".rst": true, ".org": true, ".adoc": true,
	".py": true, ".rs": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".go": true, ".java": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true,
	".sql": true, ".sh": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".html": true, ".css": true, ".log": true,
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true, ".venv": true,
	"venv": true, "target": true, "dist": true, "build": true,
}

// skipHomeFullDirs - top-level dirs skipped when scanning a home.
var skipHomeFullDirs = map[string]bool{
	"Library": true, "Applications": true, ".Trash": true, "Movies": true, "Music": true,
	"Pictures": true, "Downloads": true, "Desktop": true, "Documents": true, "Public": true,
	"Videos": true, "Photos": true, "Templates": true,
	".local": true, ".npm": true, ".cargo": true, ".rustup": true, ".gradle": true, ".m2": true,
	".docker": true, ".kube": true, ".minikube": true, ".cache": true, ".gem": true,
	".cpan": true, ".config": true, ".ssh": true, ".gnupg": true, "go": true,
	".pyenv": true, ".nvm": true, ".rbenv": true, ".sdkman": true,
	".mpop": true, ".vscode": true, ".cursor": true,
	"stanza_resources": true, "models": true, "snap": true, "chromadb-data": true,
	"open-webui-env": true, "executor_venv": true, "llama.cpp": true, "stable-diffusion-webui": true,
}

// shallowHomeDirs - only top-level files inside these dirs.
var shallowHomeDirs = map[string]bool{
	"corpus": true, "corpus_raw": true, "corpus_raw2": true, "corpus_backup": true, "corpus_fast": true,
	"corpus_catalog": true, "builds": true, "builds_wiki_full": true, "builds_wiki_mini": true,
	"wiki_ppmi": true, "wiki_ppmi_large": true, "wiki_ppmi_out": true,
	"ppmi": true, "ppmi_out": true, "ppmi_archive": true, "ppmi_all": true, "ppmi_backup": true, "ppmi_data": true,
	"cc100_data": true, "cc100_ppmi_out": true, "cc_news": true,
	"news_data": true, "news_pipeline": true,
	"wikidata": true, "wikidata_output": true, "wikipedia": true,
}

// DefaultDBPath matches Python resolution order.
func DefaultDBPath() string {
	if e := os.Getenv("MESHDB_DB"); e != "" {
		return e
	}
	home, _ := os.UserHomeDir()
	newP := filepath.Join(home, ".meshdb", "meshdb.db")
	oldP := filepath.Join(home, ".mpop", "meshdb.db")
	if _, err := os.Stat(newP); err == nil {
		return newP
	}
	if _, err := os.Stat(oldP); err == nil {
		return oldP
	}
	return newP
}

func Open(path string) (*sql.DB, error) {
	if path == "" {
		path = DefaultDBPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		return nil, err
	}
	schema := `
CREATE TABLE IF NOT EXISTS docs (
    id TEXT PRIMARY KEY, path TEXT NOT NULL, filename TEXT NOT NULL,
    preview TEXT, type TEXT, size INTEGER, file_hash TEXT,
    mtime REAL, indexed_at REAL, source_dir TEXT, embedding BLOB
);
CREATE INDEX IF NOT EXISTS idx_docs_path ON docs(path);
CREATE INDEX IF NOT EXISTS idx_docs_type ON docs(type);
CREATE INDEX IF NOT EXISTS idx_docs_source ON docs(source_dir);
CREATE INDEX IF NOT EXISTS idx_docs_hash ON docs(file_hash);
`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
    filename, content, type,
    tokenize='unicode61 remove_diacritics 2'
);`); err != nil {
		// FTS may fail on very old sqlite - continue
	}
	_, _ = db.Exec(`
CREATE TABLE IF NOT EXISTS search_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts REAL NOT NULL,
    query TEXT,
    result_count INTEGER,
    elapsed_ms REAL,
    search_type TEXT,
    lang TEXT
);
CREATE INDEX IF NOT EXISTS idx_search_history_ts ON search_history(ts);
`)
	return db, nil
}

func fileHash(fp string) (string, error) {
	f, err := os.Open(fp)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := st.Size()
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	var tail []byte
	if size > 8192 {
		f.Seek(-4096, io.SeekEnd)
		tail = make([]byte, 4096)
		n, _ = f.Read(tail)
		tail = tail[:n]
	}
	h := md5.Sum(append(append(head, tail...), []byte(fmt.Sprintf("%d", size))...))
	return fmt.Sprintf("%x", h), nil
}

func readContent(fp string) (string, error) {
	b, err := os.ReadFile(fp)
	if err != nil {
		return "", err
	}
	if len(b) > maxContentLen {
		b = b[:maxContentLen]
	}
	return string(b), nil
}

func detectType(fp string) string {
	ext := strings.ToLower(filepath.Ext(fp))
	name := strings.ToLower(filepath.Base(fp))
	if ext == ".md" || ext == ".txt" || ext == ".rst" || ext == ".org" || ext == ".adoc" {
		return "docs"
	}
	if ext == ".log" {
		return "log"
	}
	if ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".ini" || ext == ".cfg" || ext == ".conf" || ext == ".env" {
		return "config"
	}
	if name == "makefile" || name == "dockerfile" || name == "docker-compose.yml" {
		return "config"
	}
	return "code"
}

func shouldIndexFile(fp string) bool {
	ext := strings.ToLower(filepath.Ext(fp))
	name := strings.ToLower(filepath.Base(fp))
	if !textExts[ext] {
		if name != "makefile" && name != "dockerfile" && name != "procfile" {
			return false
		}
	}
	st, err := os.Stat(fp)
	if err != nil {
		return false
	}
	s := st.Size()
	return s > 0 && s <= maxFileSize
}

func scanDir(dir string) ([]string, error) {
	dir = filepath.Clean(dir)
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if skipDirs[base] || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldIndexFile(path) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func scanDirectoryShallow(dir string) ([]string, error) {
	ent, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ent {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if shouldIndexFile(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// ScanHomeFull lists files under a home directory.
func ScanHomeFull(homeDir string) ([]string, error) {
	homeDir, err := filepath.Abs(filepath.Clean(homeDir))
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(homeDir)
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", homeDir)
	}
	ent, err := os.ReadDir(homeDir)
	if err != nil {
		return nil, err
	}
	var all []string
	for _, e := range ent {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(homeDir, e.Name())
		if shouldIndexFile(p) {
			all = append(all, p)
		}
	}
	for _, e := range ent {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || skipHomeFullDirs[name] {
			continue
		}
		sub := filepath.Join(homeDir, name)
		var part []string
		var err error
		if shallowHomeDirs[name] {
			part, err = scanDirectoryShallow(sub)
		} else {
			part, err = scanDir(sub)
		}
		if err != nil {
			continue
		}
		all = append(all, part...)
	}
	return all, nil
}

// IndexResult matches Python index_directory return dict.
type IndexResult struct {
	SourceDir    string  `json:"source_dir"`
	TotalScanned int     `json:"total_scanned"`
	New          int     `json:"new"`
	Updated      int     `json:"updated"`
	Moved        int     `json:"moved"`
	Skipped      int     `json:"skipped"`
	Errors       int     `json:"errors"`
	ElapsedS     float64 `json:"elapsed_s,omitempty"`
}

// IndexDirectory mirrors Python index_directory (incremental + hash).
func IndexDirectory(db *sql.DB, dirpath string, replace bool) (*IndexResult, error) {
	dirpath, _ = filepath.Abs(filepath.Clean(dirpath))
	files, err := scanDir(dirpath)
	if err != nil {
		return nil, err
	}
	return indexFromFileList(db, dirpath, replace, files)
}

// IndexHomeFull indexes using home scan (skip heavy dirs, shallow corpus-like dirs).
func IndexHomeFull(db *sql.DB, home string, replace bool) (*IndexResult, error) {
	home, _ = filepath.Abs(filepath.Clean(home))
	files, err := ScanHomeFull(home)
	if err != nil {
		return nil, err
	}
	return indexFromFileList(db, home, replace, files)
}

func indexFromFileList(db *sql.DB, sourceDir string, replace bool, files []string) (*IndexResult, error) {
	sourceDir, _ = filepath.Abs(filepath.Clean(sourceDir))
	res := &IndexResult{SourceDir: sourceDir}
	if replace {
		rows, _ := db.Query("SELECT rowid FROM docs WHERE source_dir = ?", sourceDir)
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
		db.Exec("DELETE FROM docs WHERE source_dir = ?", sourceDir)
	}

	existing := map[string]struct {
		hash  string
		mtime float64
		rowid int64
	}{}
	hashToPath := map[string]string{}
	rows, _ := db.Query("SELECT path, file_hash, mtime, rowid FROM docs WHERE source_dir = ?", sourceDir)
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
		if fh != "" {
			hashToPath[fh] = p
		}
	}
	rows.Close()

	res.TotalScanned = len(files)
	now := float64(time.Now().Unix())

	for _, fp := range files {
		st, err := os.Stat(fp)
		if err != nil {
			res.Errors++
			continue
		}
		fh, err := fileHash(fp)
		if err != nil {
			res.Errors++
			continue
		}
		mt := float64(st.ModTime().Unix())

		if ex, ok := existing[fp]; ok && ex.hash == fh && ex.mtime == mt {
			res.Skipped++
			continue
		}
		content, err := readContent(fp)
		if err != nil {
			res.Errors++
			continue
		}
		fname := filepath.Base(fp)
		ftype := detectType(fp)
		docID := fmt.Sprintf("%x", md5.Sum([]byte(fp)))
		preview := content
		if len(preview) > previewLen {
			preview = preview[:previewLen]
		}

		if ex, ok := existing[fp]; ok {
			db.Exec(`UPDATE docs SET filename=?, preview=?, type=?, size=?, file_hash=?, mtime=?, indexed_at=? WHERE path=? AND source_dir=?`,
				fname, preview, ftype, st.Size(), fh, mt, now, fp, sourceDir)
			db.Exec("DELETE FROM search_index WHERE rowid=?", ex.rowid)
			db.Exec("INSERT INTO search_index(rowid,filename,content,type) VALUES(?,?,?,?)", ex.rowid, fname, content, ftype)
			res.Updated++
			continue
		}

		if oldPath, ok := hashToPath[fh]; ok && oldPath != fp {
			oi := existing[oldPath]
			if _, err := os.Stat(oldPath); os.IsNotExist(err) {
				nid := fmt.Sprintf("%x", md5.Sum([]byte(fp)))
				db.Exec(`UPDATE docs SET id=?, path=?, filename=?, preview=?, mtime=?, indexed_at=? WHERE path=? AND source_dir=?`,
					nid, fp, fname, preview, mt, now, oldPath, sourceDir)
				db.Exec("DELETE FROM search_index WHERE rowid=?", oi.rowid)
				db.Exec("INSERT INTO search_index(rowid,filename,content,type) VALUES(?,?,?,?)", oi.rowid, fname, content, ftype)
				res.Moved++
				delete(existing, oldPath)
				hashToPath[fh] = fp
				existing[fp] = oi
				continue
			}
		}

		_, err = db.Exec(`INSERT OR REPLACE INTO docs
            (id,path,filename,preview,type,size,file_hash,mtime,indexed_at,source_dir)
            VALUES(?,?,?,?,?,?,?,?,?,?)`,
			docID, fp, fname, preview, ftype, st.Size(), fh, mt, now, sourceDir)
		if err != nil {
			res.Errors++
			continue
		}
		var rid int64
		if err := db.QueryRow("SELECT rowid FROM docs WHERE path=? AND source_dir=?", fp, sourceDir).Scan(&rid); err != nil {
			res.Errors++
			continue
		}
		db.Exec("INSERT INTO search_index(rowid,filename,content,type) VALUES(?,?,?,?)", rid, fname, content, ftype)
		res.New++
	}
	return res, nil
}

type SearchRow struct {
	Path, Filename, Type string
	Size                 int64
	Mtime                string
	Snippet              string
	Score                float64
	Server               string `json:"server,omitempty"` // set by distributed search
}

func Search(db *sql.DB, query string, limit int, docType, sourceDir string) ([]SearchRow, error) {
	if limit <= 0 {
		limit = 20
	}
	start := time.Now()
	out, err := searchFTS(db, query, limit, docType, sourceDir)
	if err != nil {
		out, err = searchLike(db, query, limit, docType, sourceDir)
	}
	if err == nil {
		_ = logSearch(db, query, len(out), int(time.Since(start).Milliseconds()), "search")
	}
	return out, err
}

func searchFTS(db *sql.DB, query string, limit int, docType, sourceDir string) ([]SearchRow, error) {
	args := []any{query}
	q := `
SELECT d.path, d.filename, d.type, d.size, d.mtime,
    snippet(search_index, 1, '>>>', '<<<', '...', 64),
    bm25(search_index, 10.0, 5.0, 1.0)
FROM search_index JOIN docs d ON d.rowid = search_index.rowid
WHERE search_index MATCH ?`
	if docType != "" {
		q += " AND d.type = ?"
		args = append(args, docType)
	}
	if sourceDir != "" {
		abs, _ := filepath.Abs(filepath.Clean(sourceDir))
		q += " AND d.source_dir = ?"
		args = append(args, abs)
	}
	q += " ORDER BY rank LIMIT ?"
	args = append(args, limit)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchRow
	for rows.Next() {
		var r SearchRow
		var mt float64
		var rank float64
		rows.Scan(&r.Path, &r.Filename, &r.Type, &r.Size, &mt, &r.Snippet, &rank)
		r.Mtime = time.Unix(int64(mt), 0).Format("2006-01-02 15:04")
		if rank < 0 {
			r.Score = -rank
		} else {
			r.Score = rank
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func searchLike(db *sql.DB, query string, limit int, docType, sourceDir string) ([]SearchRow, error) {
	pat := "%" + strings.ToLower(query) + "%"
	args := []any{pat, pat}
	q := `SELECT path, filename, type, size, mtime, preview FROM docs WHERE (lower(filename) LIKE ? OR lower(preview) LIKE ?)`
	if docType != "" {
		q += " AND type = ?"
		args = append(args, docType)
	}
	if sourceDir != "" {
		abs, _ := filepath.Abs(filepath.Clean(sourceDir))
		q += " AND source_dir = ?"
		args = append(args, abs)
	}
	q += " LIMIT ?"
	args = append(args, limit)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchRow
	for rows.Next() {
		var r SearchRow
		var mt float64
		var prev sql.NullString
		rows.Scan(&r.Path, &r.Filename, &r.Type, &r.Size, &mt, &prev)
		sn := ""
		if prev.Valid {
			sn = prev.String
			if len(sn) > 200 {
				sn = sn[:200]
			}
		}
		r.Snippet = sn
		r.Mtime = time.Unix(int64(mt), 0).Format("2006-01-02 15:04")
		r.Score = 1
		out = append(out, r)
	}
	return out, nil
}

// FindRow is one row from search_filename.
type FindRow struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	Mtime    string `json:"mtime"`
	Server   string `json:"server,omitempty"`
}

// FindByFilename mirrors Python search_filename.
func FindByFilename(db *sql.DB, query string, limit int, docType, sourceDir string) ([]FindRow, error) {
	if limit <= 0 {
		limit = 10
	}
	pat := "%" + query + "%"
	q := `SELECT path, filename, type, size, mtime FROM docs WHERE lower(filename) LIKE lower(?)`
	args := []any{pat}
	if docType != "" {
		q += " AND type = ?"
		args = append(args, docType)
	}
	if sourceDir != "" {
		abs, _ := filepath.Abs(filepath.Clean(sourceDir))
		q += " AND source_dir = ?"
		args = append(args, abs)
	}
	q += " ORDER BY mtime DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FindRow
	for rows.Next() {
		var r FindRow
		var mt float64
		if err := rows.Scan(&r.Path, &r.Filename, &r.Type, &r.Size, &mt); err != nil {
			return nil, err
		}
		r.Mtime = time.Unix(int64(mt), 0).Format("2006-01-02 15:04")
		out = append(out, r)
	}
	return out, nil
}

// ReadDoc mirrors Python read_doc.
func ReadDoc(db *sql.DB, query string, docType, sourceDir string) (path string, content string, err error) {
	if query == "" {
		return "", "", nil
	}
	if st, e := os.Stat(query); e == nil && !st.IsDir() {
		b, e := readContent(query)
		if e != nil {
			return query, "", e
		}
		return query, b, nil
	}
	q := `SELECT path FROM docs WHERE (path = ? OR lower(filename) LIKE lower(?))`
	args := []any{query, "%" + query + "%"}
	if docType != "" {
		q += " AND type = ?"
		args = append(args, docType)
	}
	if sourceDir != "" {
		abs, _ := filepath.Abs(filepath.Clean(sourceDir))
		q += " AND source_dir = ?"
		args = append(args, abs)
	}
	q += " LIMIT 1"
	var rowPath string
	e := db.QueryRow(q, args...).Scan(&rowPath)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", e
	}
	if _, e := os.Stat(rowPath); e != nil {
		return "", "", nil
	}
	b, e := readContent(rowPath)
	if e != nil {
		return rowPath, "", e
	}
	return rowPath, b, nil
}

func formatSize(n int64) string {
	size := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB"} {
		if size < 1024 {
			return fmt.Sprintf("%.1f%s", size, unit)
		}
		size /= 1024
	}
	return fmt.Sprintf("%.1fTB", size)
}

func mainDBPath(db *sql.DB) string {
	rows, err := db.Query("PRAGMA database_list")
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if rows.Scan(&seq, &name, &file) != nil {
			continue
		}
		if name == "main" && file != "" {
			return file
		}
	}
	return ""
}

// Status mirrors Python get_status.
func Status(db *sql.DB) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM docs").Scan(&n); err != nil {
		return nil, err
	}
	out["total_docs"] = n

	byType := map[string]int{}
	trows, err := db.Query("SELECT type, COUNT(*) FROM docs GROUP BY type ORDER BY COUNT(*) DESC")
	if err == nil {
		for trows.Next() {
			var t string
			var c int
			trows.Scan(&t, &c)
			byType[t] = c
		}
		trows.Close()
	}
	out["by_type"] = byType

	bySource := map[string]int{}
	srows, err := db.Query("SELECT source_dir, COUNT(*) FROM docs GROUP BY source_dir ORDER BY COUNT(*) DESC")
	if err == nil {
		for srows.Next() {
			var s string
			var c int
			srows.Scan(&s, &c)
			bySource[s] = c
		}
		srows.Close()
	}
	out["by_source"] = bySource

	var emb int
	db.QueryRow("SELECT COUNT(*) FROM docs WHERE embedding IS NOT NULL").Scan(&emb)
	out["embedded_docs"] = emb

	dbPath := mainDBPath(db)
	if dbPath != "" {
		if st, err := os.Stat(dbPath); err == nil {
			sz := st.Size()
			out["db_size"] = sz
			out["db_size_human"] = formatSize(sz)
		}
	}

	var lastIndexed sql.NullFloat64
	db.QueryRow("SELECT MAX(indexed_at) FROM docs").Scan(&lastIndexed)
	if lastIndexed.Valid && lastIndexed.Float64 > 0 {
		out["last_indexed"] = time.Unix(int64(lastIndexed.Float64), 0).Format("2006-01-02 15:04:05")
	}
	return out, nil
}
