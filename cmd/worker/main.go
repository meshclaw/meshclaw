package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
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
	fmt.Printf("  Coordinators: %d URLs\n", len(cfg.GetServerURLs()))
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
		// Extended stats
		"hostname":    stats.Hostname,
		"os":          stats.OS,
		"arch":        stats.Arch,
		"cpu_cores":   stats.CPUCores,
		"cpu_pct":     stats.CPUPct,
		"mem_total":   stats.MemTotal,
		"mem_used":    stats.MemUsed,
		"mem_free":    stats.MemFree,
		"swap_total":  stats.SwapTotal,
		"swap_used":   stats.SwapUsed,
		"disk_total":  stats.DiskTotal,
		"disk_used":   stats.DiskUsed,
		"net_rx":      stats.NetRX,
		"net_tx":      stats.NetTX,
		"procs":       stats.Procs,
		"connections": stats.Connections,
		// Additional
		"docker_count":  stats.DockerCount,
		"gpu_count":     stats.GPUCount,
		"gpu_mem_used":  stats.GPUMemUsed,
		"gpu_mem_total": stats.GPUMemTotal,
		"gpu_util":      stats.GPUUtil,
		"io_wait":       stats.IOWait,
		"top_process":   stats.TopProcess,
	}

	body, _ := json.Marshal(data)

	// Try all coordinator URLs with failover
	client := &http.Client{Timeout: 5 * time.Second}
	var lastErr error
	for _, baseURL := range cfg.GetServerURLs() {
		url := baseURL + "/stats"
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			fmt.Printf("  [%s] load=%.2f cpu=%d%% mem=%d%% disk=%d%% procs=%d conns=%d\n",
				time.Now().Format("15:04:05"), stats.LoadValue, stats.CPUPct, stats.MemPct, stats.DiskPct, stats.Procs, stats.Connections)
			return
		}
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	fmt.Printf("  [%s] stats report failed: %v\n", time.Now().Format("15:04:05"), lastErr)
}

type Stats struct {
	// Basic
	Load      string
	LoadValue float64
	MemPct    int
	DiskPct   int
	Uptime    string

	// Extended
	Hostname    string
	OS          string
	Arch        string
	CPUCores    int
	CPUPct      int
	MemTotal    int64 // MB
	MemUsed     int64 // MB
	MemFree     int64 // MB
	SwapTotal   int64 // MB
	SwapUsed    int64 // MB
	DiskTotal   int64 // GB
	DiskUsed    int64 // GB
	NetRX       int64 // bytes
	NetTX       int64 // bytes
	Procs       int
	Connections int

	// Additional
	DockerCount int    // running containers
	GPUCount    int    // nvidia GPUs
	GPUMemUsed  int    // MB
	GPUMemTotal int    // MB
	GPUUtil     int    // percent
	IOWait      int    // percent
	TopProcess  string // highest CPU process
}

func collectStats() Stats {
	var stats Stats

	// Static info
	stats.Hostname, _ = os.Hostname()
	stats.OS = runtime.GOOS
	stats.Arch = runtime.GOARCH
	stats.CPUCores = runtime.NumCPU()

	if runtime.GOOS == "linux" {
		collectLinuxStats(&stats)
	} else if runtime.GOOS == "darwin" {
		collectDarwinStats(&stats)
	}

	return stats
}

func collectLinuxStats(stats *Stats) {
	// Load average
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		parts := strings.Fields(string(data))
		if len(parts) >= 1 {
			stats.Load = parts[0]
			stats.LoadValue, _ = strconv.ParseFloat(parts[0], 64)
		}
	}

	// Memory from /proc/meminfo
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(data), "\n")
		var total, free, buffers, cached int64
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			val, _ := strconv.ParseInt(fields[1], 10, 64)
			switch fields[0] {
			case "MemTotal:":
				total = val / 1024 // KB to MB
			case "MemFree:":
				free = val / 1024
			case "Buffers:":
				buffers = val / 1024
			case "Cached:":
				cached = val / 1024
			}
		}
		stats.MemTotal = total
		stats.MemFree = free + buffers + cached
		stats.MemUsed = total - stats.MemFree
		if total > 0 {
			stats.MemPct = int(stats.MemUsed * 100 / total)
		}
	}

	// Disk usage
	if out, err := exec.Command("df", "-BG", "/").Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				stats.DiskTotal, _ = strconv.ParseInt(strings.TrimSuffix(fields[1], "G"), 10, 64)
				stats.DiskUsed, _ = strconv.ParseInt(strings.TrimSuffix(fields[2], "G"), 10, 64)
				pct := strings.TrimSuffix(fields[4], "%")
				stats.DiskPct, _ = strconv.Atoi(pct)
			}
		}
	}

	// CPU usage (simple method: 1 second sample)
	if idle1, total1 := readCPUStat(); total1 > 0 {
		time.Sleep(100 * time.Millisecond)
		if idle2, total2 := readCPUStat(); total2 > total1 {
			idleDelta := idle2 - idle1
			totalDelta := total2 - total1
			stats.CPUPct = int(100 * (totalDelta - idleDelta) / totalDelta)
		}
	}

	// Network I/O
	if data, err := os.ReadFile("/proc/net/dev"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.Contains(line, ":") && !strings.Contains(line, "lo:") {
				fields := strings.Fields(strings.Split(line, ":")[1])
				if len(fields) >= 9 {
					rx, _ := strconv.ParseInt(fields[0], 10, 64)
					tx, _ := strconv.ParseInt(fields[8], 10, 64)
					stats.NetRX += rx
					stats.NetTX += tx
				}
			}
		}
	}

	// Process count
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				if _, err := strconv.Atoi(e.Name()); err == nil {
					stats.Procs++
				}
			}
		}
	}

	// Connection count
	if out, err := exec.Command("sh", "-c", "ss -tun 2>/dev/null | wc -l").Output(); err == nil {
		cnt, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		if cnt > 0 {
			stats.Connections = cnt - 1 // subtract header
		}
	}

	// Uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 1 {
			secs, _ := strconv.ParseFloat(fields[0], 64)
			stats.Uptime = formatUptime(int64(secs))
		}
	}

	// Swap
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			val, _ := strconv.ParseInt(fields[1], 10, 64)
			switch fields[0] {
			case "SwapTotal:":
				stats.SwapTotal = val / 1024
			case "SwapFree:":
				stats.SwapUsed = stats.SwapTotal - val/1024
			}
		}
	}

	// Docker container count
	if out, err := exec.Command("docker", "ps", "-q").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if lines[0] != "" {
			stats.DockerCount = len(lines)
		}
	}

	// GPU stats (nvidia-smi)
	if out, err := exec.Command("nvidia-smi", "--query-gpu=count,memory.used,memory.total,utilization.gpu",
		"--format=csv,noheader,nounits").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			parts := strings.Split(lines[0], ", ")
			if len(parts) >= 4 {
				stats.GPUCount, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
				stats.GPUMemUsed, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
				stats.GPUMemTotal, _ = strconv.Atoi(strings.TrimSpace(parts[2]))
				stats.GPUUtil, _ = strconv.Atoi(strings.TrimSpace(parts[3]))
			}
		}
	}

	// IO Wait from /proc/stat
	if data, err := os.ReadFile("/proc/stat"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "cpu ") {
				fields := strings.Fields(line)
				if len(fields) >= 6 {
					// iowait is field 5 (0-indexed)
					iowait, _ := strconv.ParseInt(fields[5], 10, 64)
					var total int64
					for i := 1; i < len(fields); i++ {
						v, _ := strconv.ParseInt(fields[i], 10, 64)
						total += v
					}
					if total > 0 {
						stats.IOWait = int(iowait * 100 / total)
					}
				}
				break
			}
		}
	}

	// Top process by CPU
	if out, err := exec.Command("sh", "-c", "ps aux --sort=-%cpu 2>/dev/null | head -2 | tail -1 | awk '{print $11}'").Output(); err == nil {
		stats.TopProcess = strings.TrimSpace(string(out))
		// Truncate if too long
		if len(stats.TopProcess) > 30 {
			stats.TopProcess = stats.TopProcess[:30]
		}
	}
}

func collectDarwinStats(stats *Stats) {
	// Load average
	if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		s := strings.Trim(string(out), "{ }\n")
		parts := strings.Fields(s)
		if len(parts) >= 1 {
			stats.Load = parts[0]
			stats.LoadValue, _ = strconv.ParseFloat(parts[0], 64)
		}
	}

	// Memory
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		total, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		stats.MemTotal = total / 1024 / 1024 // bytes to MB
	}
	if out, err := exec.Command("vm_stat").Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		var active, wired, pageSize int64
		for _, line := range lines {
			if strings.Contains(line, "page size") {
				fmt.Sscanf(line, "Mach Virtual Memory Statistics: (page size of %d bytes)", &pageSize)
			} else if strings.Contains(line, "Pages active") {
				fmt.Sscanf(line, "Pages active: %d", &active)
			} else if strings.Contains(line, "Pages wired") {
				fmt.Sscanf(line, "Pages wired down: %d", &wired)
			}
		}
		if pageSize > 0 {
			stats.MemUsed = (active + wired) * pageSize / 1024 / 1024
			stats.MemFree = stats.MemTotal - stats.MemUsed
			if stats.MemTotal > 0 {
				stats.MemPct = int(stats.MemUsed * 100 / stats.MemTotal)
			}
		}
	}

	// Disk
	if out, err := exec.Command("df", "-g", "/").Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				stats.DiskTotal, _ = strconv.ParseInt(fields[1], 10, 64)
				stats.DiskUsed, _ = strconv.ParseInt(fields[2], 10, 64)
				pct := strings.TrimSuffix(fields[4], "%")
				stats.DiskPct, _ = strconv.Atoi(pct)
			}
		}
	}

	// CPU (use top for quick sample)
	if out, err := exec.Command("sh", "-c", "top -l 1 -n 0 | grep 'CPU usage'").Output(); err == nil {
		// CPU usage: 5.26% user, 10.52% sys, 84.21% idle
		line := string(out)
		if idx := strings.Index(line, "idle"); idx > 0 {
			parts := strings.Fields(line[:idx])
			if len(parts) >= 1 {
				idleStr := strings.TrimSuffix(parts[len(parts)-1], "%")
				idle, _ := strconv.ParseFloat(idleStr, 64)
				stats.CPUPct = int(100 - idle)
			}
		}
	}

	// Process count
	if out, err := exec.Command("sh", "-c", "ps aux | wc -l").Output(); err == nil {
		cnt, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		stats.Procs = cnt - 1
	}

	// Connections
	if out, err := exec.Command("sh", "-c", "netstat -an | grep ESTABLISHED | wc -l").Output(); err == nil {
		stats.Connections, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}

	// Network I/O
	if out, err := exec.Command("sh", "-c", "netstat -ibn | grep -E '^en[0-9]' | head -1").Output(); err == nil {
		fields := strings.Fields(string(out))
		if len(fields) >= 10 {
			stats.NetRX, _ = strconv.ParseInt(fields[6], 10, 64)
			stats.NetTX, _ = strconv.ParseInt(fields[9], 10, 64)
		}
	}

	// Uptime
	if out, err := exec.Command("sysctl", "-n", "kern.boottime").Output(); err == nil {
		// { sec = 1774123456, usec = 0 }
		s := string(out)
		if idx := strings.Index(s, "sec = "); idx >= 0 {
			s = s[idx+6:]
			if idx2 := strings.Index(s, ","); idx2 > 0 {
				bootTime, _ := strconv.ParseInt(s[:idx2], 10, 64)
				if bootTime > 0 {
					stats.Uptime = formatUptime(time.Now().Unix() - bootTime)
				}
			}
		}
	}
}

func readCPUStat() (idle, total int64) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				for i := 1; i < len(fields); i++ {
					val, _ := strconv.ParseInt(fields[i], 10, 64)
					total += val
					if i == 4 {
						idle = val
					}
				}
			}
			break
		}
	}
	return
}

func formatUptime(secs int64) string {
	days := secs / 86400
	hours := (secs % 86400) / 3600
	mins := (secs % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	} else if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
