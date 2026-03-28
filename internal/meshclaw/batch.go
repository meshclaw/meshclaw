package meshclaw

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// BatchAgent defines a batch agent that runs once and exits
type BatchAgent struct {
	Name        string
	Description string
	Run         func() (string, error)
}

// BuiltinBatchAgents contains built-in batch agents
var BuiltinBatchAgents = map[string]*BatchAgent{
	"news": {
		Name:        "news",
		Description: "Fetch and summarize tech news",
		Run:         runNewsAgent,
	},
	"system": {
		Name:        "system",
		Description: "System health check with AI analysis",
		Run:         runSystemAgent,
	},
	"hello": {
		Name:        "hello",
		Description: "Simple test agent",
		Run:         runHelloAgent,
	},
}

// RunBatchAgent runs a batch agent by name
func RunBatchAgent(name string) (string, error) {
	agent, ok := BuiltinBatchAgents[name]
	if !ok {
		return "", fmt.Errorf("unknown batch agent: %s", name)
	}
	return agent.Run()
}

// ListBatchAgents returns list of available batch agents
func ListBatchAgents() []string {
	names := make([]string, 0, len(BuiltinBatchAgents))
	for name := range BuiltinBatchAgents {
		names = append(names, name)
	}
	return names
}

// runHelloAgent - simple test
func runHelloAgent() (string, error) {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("Hello from %s at %s", hostname, time.Now().Format(time.RFC3339)), nil
}

// runNewsAgent - fetch and summarize news
func runNewsAgent() (string, error) {
	// Fetch HackerNews top stories
	resp, err := http.Get("https://hacker-news.firebaseio.com/v0/topstories.json")
	if err != nil {
		return "", fmt.Errorf("failed to fetch HN: %w", err)
	}
	defer resp.Body.Close()

	var ids []int
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return "", err
	}

	// Get top 5 stories
	var stories []string
	for i := 0; i < 5 && i < len(ids); i++ {
		url := fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", ids[i])
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		var story struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Score int    `json:"score"`
		}
		json.NewDecoder(resp.Body).Decode(&story)
		resp.Body.Close()
		stories = append(stories, fmt.Sprintf("- %s (score: %d)", story.Title, story.Score))
	}

	newsText := strings.Join(stories, "\n")

	// Call LLM to summarize
	summary, err := callLLM(fmt.Sprintf(`Here are today's top tech news from HackerNews:

%s

Please provide a brief 2-3 sentence summary of these headlines, highlighting any common themes or particularly interesting stories.`, newsText))

	if err != nil {
		// Fallback to just showing headlines
		return fmt.Sprintf("Top HackerNews Stories:\n%s\n\n(LLM unavailable: %v)", newsText, err), nil
	}

	return fmt.Sprintf("Top HackerNews Stories:\n%s\n\nSummary: %s", newsText, summary), nil
}

// runSystemAgent - system health with AI analysis
func runSystemAgent() (string, error) {
	// Gather system info
	hostname, _ := os.Hostname()

	// Get load
	loadCmd := exec.Command("sh", "-c", "cat /proc/loadavg 2>/dev/null || sysctl -n vm.loadavg 2>/dev/null | tr -d '{}'")
	loadOut, _ := loadCmd.Output()
	load := strings.TrimSpace(string(loadOut))

	// Get memory
	memCmd := exec.Command("sh", "-c", "free -m 2>/dev/null | awk 'NR==2{printf \"%.0f%% (%dMB/%dMB)\", $3/$2*100, $3, $2}' || vm_stat 2>/dev/null | head -3")
	memOut, _ := memCmd.Output()
	mem := strings.TrimSpace(string(memOut))

	// Get disk
	diskCmd := exec.Command("sh", "-c", "df -h / | awk 'NR==2{print $5\" used of \"$2}'")
	diskOut, _ := diskCmd.Output()
	disk := strings.TrimSpace(string(diskOut))

	// Get top processes
	topCmd := exec.Command("sh", "-c", "ps aux --sort=-%cpu 2>/dev/null | head -4 || ps aux | head -4")
	topOut, _ := topCmd.Output()
	top := strings.TrimSpace(string(topOut))

	systemInfo := fmt.Sprintf(`Host: %s
Load: %s
Memory: %s
Disk: %s

Top Processes:
%s`, hostname, load, mem, disk, top)

	// Call LLM to analyze
	analysis, err := callLLM(fmt.Sprintf(`Analyze this system health report and provide a brief assessment. Flag any concerns:

%s

Respond in 2-3 sentences.`, systemInfo))

	if err != nil {
		return fmt.Sprintf("System Report:\n%s\n\n(LLM unavailable: %v)", systemInfo, err), nil
	}

	return fmt.Sprintf("System Report:\n%s\n\nAnalysis: %s", systemInfo, analysis), nil
}

// callLLM calls the configured LLM
func callLLM(prompt string) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	reqBody := map[string]interface{}{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 500,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	data, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if result.Error.Message != "" {
		return "", fmt.Errorf(result.Error.Message)
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}

	return "", fmt.Errorf("no response from LLM")
}
