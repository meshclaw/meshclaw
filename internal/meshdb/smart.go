package meshdb

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const smartTimeout = 25 * time.Second

// SmartExpand calls Ollama /api/generate like Python smart_expand.
func SmartExpand(query string) []string {
	base := os.Getenv("MESHDB_OLLAMA_URL")
	if base == "" {
		base = "http://localhost:11434"
	}
	model := os.Getenv("MESHDB_OLLAMA_MODEL")
	if model == "" {
		model = "qwen2:7b-instruct"
	}
	url := strings.TrimRight(base, "/") + "/api/generate"
	prompt := "You are a search keyword expander. Given a search query, " +
		"output ONLY a comma-separated list of related keywords, " +
		"synonyms, and translations (Korean<->English).\n" +
		"Rules: Output ONLY keywords, no explanation. Max 8 keywords. " +
		"Include synonyms, related technical terms, Korean/English translations. " +
		"Keep original query terms.\n\n" +
		"Query: " + query + "\nKeywords:"
	body, _ := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.3,
			"num_predict": 100,
		},
	})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return splitQuery(query)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: smartTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return splitQuery(query)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode >= 400 {
		return splitQuery(query)
	}
	var out struct {
		Response string `json:"response"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return splitQuery(query)
	}
	text := strings.TrimSpace(out.Response)
	var keywords []string
	for _, k := range strings.Split(text, ",") {
		k = strings.TrimSpace(k)
		k = strings.Trim(k, `"'`)
		if k != "" {
			keywords = append(keywords, k)
		}
	}
	seen := map[string]bool{}
	var terms []string
	for _, t := range append(strings.Fields(query), keywords...) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		terms = append(terms, t)
		if len(terms) >= 12 {
			break
		}
	}
	if len(terms) == 0 {
		return splitQuery(query)
	}
	return terms
}

func splitQuery(query string) []string {
	return strings.Fields(query)
}

// BuildFTSORQuery builds an FTS5 OR query from terms.
func BuildFTSORQuery(terms []string) string {
	if len(terms) == 0 {
		return ""
	}
	var parts []string
	for _, t := range terms {
		t = strings.TrimSpace(t)
		t = strings.ReplaceAll(t, `"`, "")
		t = strings.ReplaceAll(t, "'", "")
		if t == "" {
			continue
		}
		if strings.Contains(t, " ") {
			parts = append(parts, `"`+t+`"`)
		} else {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return terms[0]
	}
	return strings.Join(parts, " OR ")
}

// SmartSearchQuery returns an FTS query string for --smart mode.
func SmartSearchQuery(userQuery string) string {
	terms := SmartExpand(userQuery)
	q := BuildFTSORQuery(terms)
	if q == "" {
		return userQuery
	}
	return q
}
