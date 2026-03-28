package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/meshclaw/meshclaw/internal/wire"
)

const (
	DefaultInterval = 30 * time.Second
)

func main() {
	// Load wire config for node_id and coordinator URL
	cfg, err := wire.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wire not configured: %v\n", err)
		os.Exit(1)
	}

	interval := DefaultInterval
	if i := os.Getenv("WORKER_INTERVAL"); i != "" {
		if d, err := time.ParseDuration(i); err == nil {
			interval = d
		}
	}

	fmt.Printf("meshclaw worker starting (interval: %v)\n", interval)
	fmt.Printf("  Coordinator: %s\n", cfg.ServerURL)
	fmt.Printf("  NodeID: %s\n", cfg.NodeID)

	// Run once immediately
	reportStats(cfg)

	// Then loop
	ticker := time.NewTicker(interval)
	for range ticker.C {
		reportStats(cfg)
	}
}

func reportStats(cfg *wire.Config) {
	stats := collectStats()

	data := map[string]interface{}{
		"network":    "default",
		"node_id":    cfg.NodeID,
		"load":       stats.Load,
		"load_value": stats.LoadValue,
		"mem_pct":    stats.MemPct,
		"disk_pct":   stats.DiskPct,
		"uptime":     stats.Uptime,
	}

	body, _ := json.Marshal(data)

	url := cfg.ServerURL + "/stats"
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("  [%s] stats report failed: %v\n", time.Now().Format("15:04:05"), err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("  [%s] stats report failed: HTTP %d\n", time.Now().Format("15:04:05"), resp.StatusCode)
		return
	}

	fmt.Printf("  [%s] stats: load=%.2f mem=%d%% disk=%d%%\n",
		time.Now().Format("15:04:05"), stats.LoadValue, stats.MemPct, stats.DiskPct)
}

type Stats struct {
	Load      string
	LoadValue float64
	MemPct    int
	DiskPct   int
	Uptime    string
}

func collectStats() Stats {
	var stats Stats

	// Collect stats - works on Linux and macOS
	cmd := `if [ -f /proc/loadavg ];then L=$(cat /proc/loadavg|cut -d" " -f1);else L=$(sysctl -n vm.loadavg 2>/dev/null|tr -d "{}"|awk "{print \$1}");fi;M=$(free -m 2>/dev/null|awk "NR==2{printf \"%.0f\",\$3/\$2*100}");if [ -z "$M" ];then T=$(sysctl -n hw.memsize 2>/dev/null);P=$(vm_stat 2>/dev/null|awk "/Pages active/{a=\$3}/Pages wired/{w=\$4}END{print a+w}");S=$(sysctl -n vm.pagesize 2>/dev/null);[ -n "$T" ]&&[ -n "$P" ]&&[ -n "$S" ]&&M=$((P*S*100/T));fi;D=$(df -h / 2>/dev/null|awk "NR==2{print \$5}"|tr -d "%");U=$(uptime|sed "s/.*up //" |cut -d"," -f1-2);echo "$L||$M||$D||$U"`

	output, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return stats
	}

	parts := strings.Split(strings.TrimSpace(string(output)), "||")
	if len(parts) >= 4 {
		stats.Load = strings.TrimSpace(parts[0])
		if load, err := strconv.ParseFloat(stats.Load, 64); err == nil {
			stats.LoadValue = load
		}

		if memPct, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			stats.MemPct = memPct
		}

		if diskPct, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil {
			stats.DiskPct = diskPct
		}

		stats.Uptime = strings.TrimSpace(parts[3])
	}

	return stats
}
