package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/meshdb"
)

var typeIcons = map[string]string{
	"docs":   "D",
	"code":   "C",
	"config": "F",
	"log":    "L",
}

func main() {
	args := os.Args[1:]
	if len(args) < 1 {
		printUsage()
		return
	}
	dbPath := os.Getenv("MESHDB_DB")
	db, err := meshdb.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()

	cmd := args[0]
	rest := args[1:]
	opts := parseOpts(rest)
	rest = opts.pos

	switch cmd {
	case "doctor":
		p := meshdb.DefaultDBPath()
		fmt.Println("meshdb doctor")
		fmt.Println("  DB:", p)
		if _, err := os.Stat(p); err != nil {
			fmt.Println("  file: not found (run meshdb index ...)")
		} else {
			fmt.Println("  file: OK")
		}
		srv := meshdb.LoadDistributedServers()
		if len(srv) == 0 {
			fmt.Println("  ~/.meshdb/config.json servers: (none)")
		} else {
			fmt.Printf("  distributed servers: %d (use search --all)\n", len(srv))
		}
		if _, err := exec.LookPath("ssh"); err != nil {
			fmt.Println("  ssh: not in PATH")
		} else {
			fmt.Println("  ssh: OK")
		}
		if meshdb.HasVSSHSecretConfigured() {
			fmt.Println("  vssh_secret: set (remote commands try TCP :48291 first)")
		}
	case "index", "reindex":
		if len(rest) < 1 {
			fmt.Fprintf(os.Stderr, "usage: meshdb %s <path|server:/path> [--replace] [--remote-agent]\n", cmd)
			os.Exit(1)
		}
		path := rest[0]
		replace := cmd == "reindex" || opts.replace
		t0 := time.Now()
		if srv, rpath, ok := meshdb.ParseRemotePath(path); ok {
			if opts.remoteAgent {
				result, err := meshdb.RemoteIndexViaAgent(srv, rpath, replace)
				elapsed := round1(time.Since(t0).Seconds())
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				result["elapsed_s"] = elapsed
				if opts.json {
					json.NewEncoder(os.Stdout).Encode(result)
					return
				}
				fmt.Printf("\n  Remote index (agent) SSH -> %s:%s (%.1fs)\n", srv, rpath, elapsed)
				pretty, _ := json.MarshalIndent(result, "  ", "  ")
				fmt.Println(string(pretty))
				fmt.Println()
				return
			}
			result, err := meshdb.IndexRemoteMerge(db, srv, rpath, replace)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			result.ElapsedS = round1(time.Since(t0).Seconds())
			if opts.json {
				json.NewEncoder(os.Stdout).Encode(result)
				return
			}
			label := "Indexing"
			if replace {
				label = "Reindexing"
			}
			fmt.Printf("\n  %s (remote -> local): %s\n", label, result.SourceDir)
			fmt.Printf("  scanned: %d, new: %d, updated: %d, skipped: %d, errors: %d\n",
				result.TotalScanned, result.New, result.Updated, result.Skipped, result.Errors)
			fmt.Printf("  time: %gs\n\n", result.ElapsedS)
			return
		}
		result, err := meshdb.IndexDirectory(db, path, replace)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		result.ElapsedS = round1(time.Since(t0).Seconds())
		if opts.json {
			json.NewEncoder(os.Stdout).Encode(result)
			return
		}
		label := "Indexing"
		if replace {
			label = "Reindexing"
		}
		fmt.Printf("\n  %s: %s\n", label, result.SourceDir)
		fmt.Printf("  scanned: %d, new: %d, updated: %d, moved: %d, skipped: %d, errors: %d\n",
			result.TotalScanned, result.New, result.Updated, result.Moved, result.Skipped, result.Errors)
		fmt.Printf("  time: %gs\n\n", result.ElapsedS)
	case "index-full":
		home := ""
		if len(rest) >= 1 {
			home = expandHomePath(rest[0])
		}
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		replace := opts.replace
		t0 := time.Now()
		result, err := meshdb.IndexHomeFull(db, home, replace)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		result.ElapsedS = round1(time.Since(t0).Seconds())
		if opts.json {
			json.NewEncoder(os.Stdout).Encode(result)
			return
		}
		label := "Index-full"
		if replace {
			label = "Index-full (replace)"
		}
		fmt.Printf("\n  %s: %s\n", label, result.SourceDir)
		fmt.Printf("  scanned: %d, new: %d, updated: %d, moved: %d, skipped: %d, errors: %d\n",
			result.TotalScanned, result.New, result.Updated, result.Moved, result.Skipped, result.Errors)
		fmt.Printf("  time: %gs\n\n", result.ElapsedS)
	case "search":
		if len(rest) < 1 {
			fmt.Println("usage: meshdb search <query> [--smart] [--all] [--limit N] [--type T] [--source DIR] [--json]")
			os.Exit(1)
		}
		q := strings.Join(rest, " ")
		limit := opts.limit
		if limit <= 0 {
			limit = 20
		}
		ftsQ := q
		if opts.smart {
			ftsQ = meshdb.SmartSearchQuery(q)
			if !opts.json {
				fmt.Printf("\n  smart search: %q\n  FTS: %s\n\n", q, ftsQ)
			}
		}
		t0 := time.Now()
		var rows []meshdb.SearchRow
		var stats map[string]interface{}
		var totalDist int
		var err error
		if opts.all {
			rows, stats, totalDist, err = meshdb.DistributedSearch(db, ftsQ, limit, opts.docType)
		} else {
			rows, err = meshdb.Search(db, ftsQ, limit, opts.docType, opts.sourceDir)
			totalDist = len(rows)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		elapsed := time.Since(t0).Milliseconds()
		if opts.json {
			out := map[string]interface{}{
				"query": q, "results": rows, "count": len(rows), "elapsed_ms": elapsed, "total": totalDist,
			}
			if opts.smart {
				out["smart"] = true
				out["fts_query"] = ftsQ
			}
			if opts.all {
				out["all"] = true
				out["server_stats"] = stats
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(out)
			return
		}
		if opts.all {
			fmt.Printf("  %d servers / total hits (pre-trim): %d\n", len(stats), totalDist)
			for srv, st := range stats {
				fmt.Printf("    %s: %v\n", srv, st)
			}
			fmt.Println()
		}
		fmt.Printf("  search time: %dms\n", elapsed)
		printSearchResults(rows, q)
	case "find":
		if len(rest) < 1 {
			fmt.Println("usage: meshdb find <filename> [--limit N] [--type T] [--source DIR] [--json]")
			os.Exit(1)
		}
		q := rest[0]
		limit := opts.limit
		if limit <= 0 {
			limit = 10
		}
		rows, err := meshdb.FindByFilename(db, q, limit, opts.docType, opts.sourceDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if opts.json {
			json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
				"query": q, "results": rows, "count": len(rows),
			})
			return
		}
		if len(rows) == 0 {
			fmt.Printf("\n  '%s' -- not found\n\n", q)
			return
		}
		fmt.Printf("\n  filename search: '%s' -> %d\n\n", q, len(rows))
		for _, r := range rows {
			icon := typeIcons[r.Type]
			if icon == "" {
				icon = "?"
			}
			fmt.Printf("  [%s] %s\n    %s\n", icon, r.Filename, r.Path)
		}
		fmt.Println()
	case "read":
		if len(rest) < 1 {
			fmt.Println("usage: meshdb read <path_or_filename> [--type T] [--source DIR] [--json]")
			os.Exit(1)
		}
		q := rest[0]
		p, content, err := meshdb.ReadDoc(db, q, opts.docType, opts.sourceDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if opts.json {
			if content == "" && p == "" {
				json.NewEncoder(os.Stdout).Encode(map[string]string{"error": fmt.Sprintf("'%s' not found or unreadable", q)})
				return
			}
			json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
				"path": p, "content": content, "size": len(content),
			})
			return
		}
		if content == "" && p == "" {
			fmt.Printf("\n  '%s' not found or unreadable\n\n", q)
			return
		}
		fmt.Printf("\n  --- %s ---\n%s", p, content)
		if len(content) > 0 && content[len(content)-1] != '\n' {
			fmt.Println()
		}
		fmt.Println()
	case "status":
		s, err := meshdb.Status(db)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if opts.json {
			json.NewEncoder(os.Stdout).Encode(s)
			return
		}
		printStatus(s)
	case "analytics":
		days := opts.days
		if days <= 0 {
			days = 30
		}
		lim := opts.limit
		if lim <= 0 {
			lim = 20
		}
		m, err := meshdb.SearchAnalytics(db, days, lim)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if opts.json {
			json.NewEncoder(os.Stdout).Encode(m)
			return
		}
		fmt.Printf("\n  meshdb analytics (last %d days)\n", days)
		fmt.Printf("  total searches: %v\n", m["total_searches"])
		fmt.Printf("  avg / max ms: %v / %v\n", m["avg_ms"], m["max_ms"])
		if tq, ok := m["top_queries"].([]map[string]interface{}); ok {
			fmt.Println("\n  top queries:")
			for _, row := range tq {
				fmt.Printf("    %v  (n=%v)\n", row["query"], row["count"])
			}
		}
		fmt.Println()
	case "search-history":
		limit := opts.limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := meshdb.SearchHistoryRecent(db, limit)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if opts.json {
			json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"entries": rows, "count": len(rows)})
			return
		}
		fmt.Printf("\n  search history (last %d)\n\n", limit)
		for _, e := range rows {
			fmt.Printf("  %s  n=%d  %dms  [%s]  %q\n",
				formatHistoryTS(e.Ts), e.ResultCount, int(e.ElapsedMs+0.5), e.SearchType, e.Query)
		}
		fmt.Println()
	case "index-all":
		replace := opts.replace
		t0 := time.Now()
		summary := meshdb.IndexAll(db, replace)
		summary["elapsed_s"] = round1(time.Since(t0).Seconds())
		if opts.json {
			json.NewEncoder(os.Stdout).Encode(summary)
			return
		}
		fmt.Printf("\n  index-all -- %.1fs\n", summary["elapsed_s"])
		fmt.Printf("  total projects: %v, files scanned: %v, new: %v, updated: %v, errors: %v\n",
			summary["total_projects"], summary["total_files_scanned"], summary["total_new"], summary["total_updated"], summary["total_errors"])
		if srv, ok := summary["servers"].(map[string]interface{}); ok {
			for name, v := range srv {
				fmt.Printf("\n  [%s] %+v\n", name, v)
			}
		}
		fmt.Println()
	case "help", "-h", "--help":
		printUsage()
	case "version", "-v", "--version":
		fmt.Println("meshdb v1.0.0")
	default:
		fmt.Printf("unknown command %q\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

type cliOpts struct {
	pos         []string
	json        bool
	smart       bool
	all         bool // distributed search
	remoteAgent bool // index server:/path via remote meshdb_agent
	replace     bool // --replace / -r
	limit       int
	days        int
	docType     string
	sourceDir   string
}

func parseOpts(argv []string) cliOpts {
	o := cliOpts{}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch a {
		case "--json":
			o.json = true
		case "--smart":
			o.smart = true
		case "--all":
			o.all = true
		case "--limit":
			if i+1 < len(argv) {
				o.limit, _ = strconv.Atoi(argv[i+1])
				i++
			}
		case "--type":
			if i+1 < len(argv) {
				o.docType = argv[i+1]
				i++
			}
		case "--source":
			if i+1 < len(argv) {
				o.sourceDir = argv[i+1]
				i++
			}
		case "--days":
			if i+1 < len(argv) {
				o.days, _ = strconv.Atoi(argv[i+1])
				i++
			}
		case "--remote-agent":
			o.remoteAgent = true
		case "--replace", "-r":
			o.replace = true
		default:
			o.pos = append(o.pos, a)
		}
	}
	return o
}

func printSearchResults(rows []meshdb.SearchRow, query string) {
	if len(rows) == 0 {
		fmt.Printf("  '%s' -- no results\n", query)
		return
	}
	fmt.Printf("  '%s' results: %d\n\n", query, len(rows))
	for i, r := range rows {
		icon := typeIcons[r.Type]
		if icon == "" {
			icon = "?"
		}
		snip := strings.ReplaceAll(r.Snippet, "\n", " ")
		snip = strings.TrimSpace(snip)
		if len(snip) > 150 {
			snip = snip[:150] + "..."
		}
		fmt.Printf("  %d. [%s] %s [%.2f]\n", i+1, icon, r.Filename, r.Score)
		if r.Server != "" {
			fmt.Printf("     [%s] %s\n", r.Server, r.Path)
		} else {
			fmt.Printf("     %s\n", r.Path)
		}
		if snip != "" {
			fmt.Printf("     %s\n", snip)
		}
		fmt.Printf("     [%s] %s %s\n\n", r.Type, r.Mtime, humanSize(r.Size))
	}
}

func humanSize(n int64) string {
	if n == 0 {
		return ""
	}
	size := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB"} {
		if size < 1024 {
			return fmt.Sprintf("%.1f%s", size, unit)
		}
		size /= 1024
	}
	return fmt.Sprintf("%.1fTB", size)
}

func printStatus(stats map[string]interface{}) {
	fmt.Println("\n  meshdb status")
	fmt.Println("  " + strings.Repeat("=", 50))
	fmt.Printf("  documents: %v\n", stats["total_docs"])
	fmt.Printf("  embedded: %v\n", stats["embedded_docs"])
	fmt.Printf("  db size: %v\n", stats["db_size_human"])
	fmt.Printf("  last indexed: %v\n", stats["last_indexed"])
	if bt, ok := stats["by_type"].(map[string]int); ok && len(bt) > 0 {
		fmt.Println("\n  by type:")
		for tp, c := range bt {
			icon := typeIcons[tp]
			fmt.Printf("    [%s] %s: %d\n", icon, tp, c)
		}
	}
	if bs, ok := stats["by_source"].(map[string]int); ok && len(bs) > 0 {
		fmt.Println("\n  by source:")
		for s, c := range bs {
			fmt.Printf("    %s: %d\n", s, c)
		}
	}
	fmt.Println()
}

func expandHomePath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Clean(filepath.Join(h, p[2:]))
	}
	if p == "~" {
		h, _ := os.UserHomeDir()
		return h
	}
	return filepath.Clean(p)
}

func formatHistoryTS(ts float64) string {
	if ts <= 0 {
		return "?"
	}
	t := time.Unix(int64(ts), 0)
	return t.Local().Format("2006-01-02 15:04:05")
}

func printUsage() {
	fmt.Print(`meshdb - Full-text document search

  meshdb doctor
  meshdb index <path|server:/path> [--replace] [--remote-agent]
  meshdb reindex <path|server:/path> [--replace] [--remote-agent]
  meshdb index-full [home] [--replace] [--json]
  meshdb index-all [--replace] [--json]
  meshdb analytics [--days N] [--limit N] [--json]
  meshdb search-history [--limit N] [--json]
  meshdb search <query> [--smart] [--all] [--limit N] [--type T] [--source DIR] [--json]
  meshdb find <filename> [--limit N] [--type T] [--source DIR] [--json]
  meshdb read <path_or_filename> [--type T] [--source DIR] [--json]
  meshdb status [--json]

  MESHDB_DB overrides default ~/.meshdb/meshdb.db

`)
}

func round1(x float64) float64 {
	return float64(int(x*10+0.5)) / 10
}
