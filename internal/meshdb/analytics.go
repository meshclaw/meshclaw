package meshdb

import (
	"database/sql"
	"encoding/json"
	"time"
)

// SearchHistoryEntry is one row from search_history.
type SearchHistoryEntry struct {
	Ts          float64 `json:"ts"`
	Query       string  `json:"query"`
	ResultCount int     `json:"result_count"`
	ElapsedMs   float64 `json:"elapsed_ms"`
	SearchType  string  `json:"search_type"`
}

// SearchHistoryRecent returns the latest search_history rows (newest first).
func SearchHistoryRecent(db *sql.DB, limit int) ([]SearchHistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(`
SELECT ts, query, result_count, elapsed_ms, IFNULL(search_type,'') FROM search_history ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHistoryEntry
	for rows.Next() {
		var e SearchHistoryEntry
		if err := rows.Scan(&e.Ts, &e.Query, &e.ResultCount, &e.ElapsedMs, &e.SearchType); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func logSearch(db *sql.DB, query string, resultCount, elapsedMs int, searchType string) error {
	if db == nil {
		return nil
	}
	ts := float64(time.Now().Unix())
	_, err := db.Exec(
		`INSERT INTO search_history(ts,query,result_count,elapsed_ms,search_type,lang) VALUES(?,?,?,?,?,?)`,
		ts, query, resultCount, elapsedMs, searchType, "",
	)
	return err
}

// SearchAnalytics mirrors Python get_search_analytics.
func SearchAnalytics(db *sql.DB, days, limit int) (map[string]interface{}, error) {
	if days <= 0 {
		days = 30
	}
	if limit <= 0 {
		limit = 20
	}
	cutoff := float64(time.Now().Unix() - int64(days*86400))
	out := map[string]interface{}{}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_history WHERE ts >= ?`, cutoff).Scan(&total); err != nil {
		return nil, err
	}
	out["total_searches"] = total

	var avg, max sql.NullFloat64
	_ = db.QueryRow(`SELECT AVG(elapsed_ms), MAX(elapsed_ms) FROM search_history WHERE ts >= ?`, cutoff).Scan(&avg, &max)
	av := 0.0
	if avg.Valid {
		av = avg.Float64
	}
	mx := 0.0
	if max.Valid {
		mx = max.Float64
	}
	out["avg_ms"] = round1(av)
	out["max_ms"] = mx

	rows, err := db.Query(`
SELECT query, COUNT(*) as cnt, AVG(result_count)
FROM search_history WHERE ts >= ?
GROUP BY lower(query) ORDER BY cnt DESC LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var top []map[string]interface{}
	for rows.Next() {
		var q string
		var cnt int
		var avgRes sql.NullFloat64
		rows.Scan(&q, &cnt, &avgRes)
		ar := 0.0
		if avgRes.Valid {
			ar = avgRes.Float64
		}
		top = append(top, map[string]interface{}{"query": q, "count": cnt, "avg_results": round1(ar)})
	}
	out["top_queries"] = top

	zrows, err := db.Query(`
SELECT query, COUNT(*) as cnt
FROM search_history WHERE ts >= ? AND result_count = 0
GROUP BY lower(query) ORDER BY cnt DESC LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer zrows.Close()
	var zeros []map[string]interface{}
	for zrows.Next() {
		var q string
		var cnt int
		zrows.Scan(&q, &cnt)
		zeros = append(zeros, map[string]interface{}{"query": q, "count": cnt})
	}
	out["zero_result_queries"] = zeros

	drows, err := db.Query(`
SELECT date(ts, 'unixepoch') as day, COUNT(*)
FROM search_history WHERE ts >= ?
GROUP BY day ORDER BY day DESC LIMIT 30`, cutoff)
	if err != nil {
		return nil, err
	}
	defer drows.Close()
	var daily []map[string]interface{}
	for drows.Next() {
		var day string
		var cnt int
		drows.Scan(&day, &cnt)
		daily = append(daily, map[string]interface{}{"date": day, "count": cnt})
	}
	out["daily_volume"] = daily

	trows, err := db.Query(`
SELECT search_type, COUNT(*) FROM search_history WHERE ts >= ?
GROUP BY search_type`, cutoff)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	byType := map[string]int{}
	for trows.Next() {
		var typ string
		var c int
		trows.Scan(&typ, &c)
		byType[typ] = c
	}
	out["by_type"] = byType

	return out, nil
}

func round1(x float64) float64 {
	return float64(int(x*10+0.5)) / 10
}

// AnalyticsJSON returns indented JSON.
func AnalyticsJSON(db *sql.DB, days, limit int) ([]byte, error) {
	m, err := SearchAnalytics(db, days, limit)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(m, "", "  ")
}
