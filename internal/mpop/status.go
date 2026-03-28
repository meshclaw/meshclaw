package mpop

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServerStatus represents the status of a server
type ServerStatus struct {
	Name      string
	IP        string
	Online    bool
	Load      string
	Memory    string
	Disk      string
	Uptime    string
	Role      string
	MemPct    int
	DiskPct   int
	LoadValue float64
}

// BarGraph creates a visual bar graph
func BarGraph(percent int, width int) string {
	if width == 0 {
		width = 10
	}

	filled := percent * width / 100
	empty := width - filled

	var color string
	if percent > 90 {
		color = "\033[91m" // Red
	} else if percent > 70 {
		color = "\033[93m" // Yellow
	} else {
		color = "\033[92m" // Green
	}

	return fmt.Sprintf("%s%s%s\033[0m",
		color,
		strings.Repeat("█", filled),
		strings.Repeat("░", empty))
}

// GetServerStatus fetches status for a single server
func GetServerStatus(name, ip string, timeout time.Duration) *ServerStatus {
	status := &ServerStatus{
		Name:   name,
		IP:     ip,
		Online: false,
	}

	// Get role from config
	cfg, _ := LoadConfig()
	if cfg != nil {
		if srv, ok := cfg.Servers[name]; ok {
			status.Role = srv.Role
		}
	}

	// Check if reachable
	if !PingServer(ip, timeout) {
		return status
	}
	status.Online = true

	// Get system info (works on Linux and macOS)
	cmd := `if [ -f /proc/loadavg ];then L=$(cat /proc/loadavg|cut -d" " -f1);else L=$(sysctl -n vm.loadavg 2>/dev/null|tr -d "{}"|awk "{print \$1}");fi;M=$(free -m 2>/dev/null|awk "NR==2{printf \"%.0f\",\$3/\$2*100}");if [ -z "$M" ];then T=$(sysctl -n hw.memsize 2>/dev/null);P=$(vm_stat 2>/dev/null|awk "/Pages active/{a=\$3}/Pages wired/{w=\$4}END{print a+w}");S=$(sysctl -n vm.pagesize 2>/dev/null);[ -n "$T" ]&&[ -n "$P" ]&&[ -n "$S" ]&&M=$((P*S*100/T));fi;D=$(df -h / 2>/dev/null|awk "NR==2{print \$5}"|tr -d "%");U=$(uptime|sed "s/.*up //" |cut -d"," -f1-2);echo "$L||$M||$D||$U"`

	result, err := VsshExec(ip, cmd, timeout)
	if err != nil {
		// Try SSH fallback
		if cfg != nil {
			if srv, ok := cfg.Servers[name]; ok {
				result, _ = SSHExec(srv.User, ip, cmd, srv.SSHPort, timeout)
			}
		}
	}

	if result != "" {
		parts := strings.Split(result, "||")
		if len(parts) >= 4 {
			status.Load = parts[0]
			if load, err := strconv.ParseFloat(parts[0], 64); err == nil {
				status.LoadValue = load
			}

			if memPct, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				status.MemPct = memPct
				status.Memory = fmt.Sprintf("%d%%", memPct)
			}

			if diskPct, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil {
				status.DiskPct = diskPct
				status.Disk = fmt.Sprintf("%d%%", diskPct)
			}

			status.Uptime = strings.TrimSpace(parts[3])
		}
	}

	return status
}

// GetAllServerStatus fetches status for all servers
// Uses coordinator-cached stats (from workers) for instant results
func GetAllServerStatus(timeout time.Duration) []*ServerStatus {
	// Try to get stats from coordinator first (fast path)
	peers, err := GetPeersWithStats()
	if err == nil && len(peers) > 0 {
		return peersToStatus(peers)
	}

	// Fallback to direct server query (slow path)
	servers := GetServers()
	if len(servers) == 0 {
		return nil
	}

	// Sort server names
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var wg sync.WaitGroup
	results := make([]*ServerStatus, len(names))
	resultMu := sync.Mutex{}

	for i, name := range names {
		wg.Add(1)
		go func(idx int, n string) {
			defer wg.Done()
			ip := servers[n]
			status := GetServerStatus(n, ip, timeout)
			resultMu.Lock()
			results[idx] = status
			resultMu.Unlock()
		}(i, name)
	}

	wg.Wait()
	return results
}

// peersToStatus converts coordinator peers to ServerStatus
func peersToStatus(peers []Peer) []*ServerStatus {
	now := time.Now().Unix()
	// Use map to deduplicate and prefer entries with stats or newer LastSeen
	bestPeer := make(map[string]*Peer)

	for i := range peers {
		p := &peers[i]
		if p.NodeName == "" || p.VpnIP == "" {
			continue
		}

		existing := bestPeer[p.NodeName]
		if existing == nil {
			bestPeer[p.NodeName] = p
			continue
		}

		// Prefer peer with stats
		hasStats := p.Stats != nil && p.Stats.UpdatedAt > 0
		existingHasStats := existing.Stats != nil && existing.Stats.UpdatedAt > 0

		if hasStats && !existingHasStats {
			bestPeer[p.NodeName] = p
		} else if hasStats && existingHasStats && p.Stats.UpdatedAt > existing.Stats.UpdatedAt {
			bestPeer[p.NodeName] = p
		} else if !hasStats && !existingHasStats {
			// Neither has stats - prefer newer LastSeen
			var pTime, eTime int64
			switch v := p.LastSeen.(type) {
			case float64:
				pTime = int64(v)
			case int64:
				pTime = v
			}
			switch v := existing.LastSeen.(type) {
			case float64:
				eTime = int64(v)
			case int64:
				eTime = v
			}
			if pTime > eTime {
				bestPeer[p.NodeName] = p
			}
		}
	}

	var results []*ServerStatus
	for _, p := range bestPeer {
		// Check if online (last seen within 90 seconds)
		var lastSeenTime int64
		switch v := p.LastSeen.(type) {
		case float64:
			lastSeenTime = int64(v)
		case int64:
			lastSeenTime = v
		}
		online := lastSeenTime > 0 && now-lastSeenTime < 90

		status := &ServerStatus{
			Name:   p.NodeName,
			IP:     p.VpnIP,
			Online: online,
		}

		// Use cached stats if available
		if p.Stats != nil && p.Stats.UpdatedAt > 0 && now-p.Stats.UpdatedAt < 120 {
			status.Load = p.Stats.Load
			status.LoadValue = p.Stats.LoadValue
			status.MemPct = p.Stats.MemPct
			status.Memory = fmt.Sprintf("%d%%", p.Stats.MemPct)
			status.DiskPct = p.Stats.DiskPct
			status.Disk = fmt.Sprintf("%d%%", p.Stats.DiskPct)
			status.Uptime = p.Stats.Uptime
		}

		results = append(results, status)
	}

	// Sort by name
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	return results
}

// PrintDashboard prints the dashboard
func PrintDashboard(statuses []*ServerStatus) {
	Reset := "\033[0m"
	Bold := "\033[1m"
	Dim := "\033[2m"
	Green := "\033[92m"
	Yellow := "\033[93m"
	Red := "\033[91m"
	Cyan := "\033[96m"

	fmt.Println()
	fmt.Printf("  %s%smpop dashboard%s\n", Bold, Cyan, Reset)
	fmt.Println()

	// Header - simple format (2 spaces for dot column)
	fmt.Printf("  %s  %-8s %-15s %5s %5s %5s  %s%s\n",
		Dim, "SERVER", "IP", "LOAD", "MEM", "DISK", "UPTIME", Reset)
	fmt.Printf("  %s%s%s\n", Dim, strings.Repeat("-", 56), Reset)

	onlineCount := 0
	for _, s := range statuses {
		var dot string
		name := s.Name
		if len(name) > 8 {
			name = name[:8]
		}

		if s.Online {
			dot = Green + "●" + Reset
			onlineCount++
		} else {
			dot = Red + "○" + Reset
		}

		load := fmt.Sprintf("%5s", "-")
		if s.Load != "" {
			l := s.Load
			if len(l) > 5 {
				l = l[:5]
			}
			load = fmt.Sprintf("%5s", l)
		}

		mem := fmt.Sprintf("%5s", "-")
		if s.MemPct > 0 {
			mem = fmt.Sprintf("%4d%%", s.MemPct)
		}

		disk := fmt.Sprintf("%5s", "-")
		if s.DiskPct > 0 {
			disk = fmt.Sprintf("%4d%%", s.DiskPct)
		}

		uptime := "-"
		if s.Uptime != "" {
			uptime = s.Uptime
		}

		fmt.Printf("  %s %-8s %-15s %s %s %s  %s\n",
			dot, name, s.IP, load, mem, disk, uptime)
	}

	fmt.Println()
	statusColor := Green
	if onlineCount < len(statuses) {
		statusColor = Yellow
	}
	if onlineCount == 0 {
		statusColor = Red
	}
	fmt.Printf("  %s%d/%d online%s\n", statusColor, onlineCount, len(statuses), Reset)
	fmt.Println()
}

// ParseUptime parses uptime string to duration
func ParseUptime(uptime string) time.Duration {
	// Parse "5 days, 3:45" or "up 5 days" format
	re := regexp.MustCompile(`(\d+)\s*day`)
	if matches := re.FindStringSubmatch(uptime); len(matches) > 1 {
		days, _ := strconv.Atoi(matches[1])
		return time.Duration(days) * 24 * time.Hour
	}
	return 0
}
