package meshclaw

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/meshclaw/meshclaw/internal/mpop"
)

const (
	StatsFreshnessLimit = 60  // seconds
	MaxRetries          = 3
	ReservationTTL      = 30  // seconds
)

// NodeRequirement specifies requirements for node selection
type NodeRequirement struct {
	GPU       bool   // requires GPU
	MinMemGB  int    // minimum memory in GB
	MinCPU    int    // minimum CPU cores
	Prefer    string // prefer specific node
	Exclude   []string // exclude these nodes
}

// ScoredNode is a node with its computed score
type ScoredNode struct {
	Peer  mpop.Peer
	Score float64
}

// Reservation tracks node reservations to prevent race conditions
type Reservation struct {
	NodeName  string
	TaskID    string
	ExpiresAt int64
}

var (
	reservations   = make(map[string]*Reservation)
	reservationsMu sync.RWMutex
)

// Reserve marks a node as busy
func Reserve(nodeName, taskID string) bool {
	reservationsMu.Lock()
	defer reservationsMu.Unlock()

	now := time.Now().Unix()

	// Clean expired reservations
	for name, r := range reservations {
		if r.ExpiresAt < now {
			delete(reservations, name)
		}
	}

	// Check if already reserved
	if r, exists := reservations[nodeName]; exists && r.ExpiresAt > now {
		return false
	}

	// Reserve
	reservations[nodeName] = &Reservation{
		NodeName:  nodeName,
		TaskID:    taskID,
		ExpiresAt: now + ReservationTTL,
	}
	return true
}

// Release removes a reservation
func Release(nodeName string) {
	reservationsMu.Lock()
	defer reservationsMu.Unlock()
	delete(reservations, nodeName)
}

// IsReserved checks if a node is reserved
func IsReserved(nodeName string) bool {
	reservationsMu.RLock()
	defer reservationsMu.RUnlock()

	r, exists := reservations[nodeName]
	if !exists {
		return false
	}
	return r.ExpiresAt > time.Now().Unix()
}

// SelectNode selects the best node based on requirements and current stats
func SelectNode(req NodeRequirement) ([]ScoredNode, error) {
	peers, err := mpop.GetPeersWithStats()
	if err != nil {
		return nil, fmt.Errorf("failed to get peers: %w", err)
	}

	now := time.Now().Unix()
	var candidates []ScoredNode

	// Build exclude map
	excludeMap := make(map[string]bool)
	for _, name := range req.Exclude {
		excludeMap[name] = true
	}

	for _, p := range peers {
		// Skip excluded nodes
		if excludeMap[p.NodeName] {
			continue
		}

		// Skip nodes without recent stats (freshness check)
		if p.Stats == nil || now-p.Stats.UpdatedAt > StatsFreshnessLimit {
			continue
		}

		// Skip reserved nodes
		if IsReserved(p.NodeName) {
			continue
		}

		// Filter by requirements
		if req.GPU && p.Stats.GPUCount == 0 {
			continue
		}
		if req.MinMemGB > 0 && p.Stats.MemTotal < int64(req.MinMemGB)*1024*1024*1024 {
			continue
		}
		if req.MinCPU > 0 && p.Stats.CPUCores < req.MinCPU {
			continue
		}

		// Calculate score (lower is better)
		// Weights: CPU 30%, Mem 20%, Load 20%, Connections 20%, Relay bias 10%
		score := float64(p.Stats.CPUPct)*0.3 +
			float64(p.Stats.MemPct)*0.2 +
			p.Stats.LoadValue*10*0.2 + // normalize load (0-10 -> 0-100)
			float64(p.Stats.Connections)*0.2

		// Prefer nodes with GPU if not required but available
		if !req.GPU && p.Stats.GPUCount > 0 {
			score += 10 // slight penalty to save GPU nodes for GPU tasks
		}

		// Boost preferred node
		if req.Prefer != "" && strings.HasPrefix(p.NodeName, req.Prefer) {
			score -= 50
		}

		candidates = append(candidates, ScoredNode{p, score})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no suitable nodes available")
	}

	// Sort by score (ascending)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score < candidates[j].Score
	})

	return candidates, nil
}

// RunResult contains the result of running an agent
type RunResult struct {
	Node    string
	Success bool
	Output  string
	Error   string
}

// RunAgent runs an agent on the best available node with retry
func RunAgent(agentName string, req NodeRequirement) (*RunResult, error) {
	candidates, err := SelectNode(req)
	if err != nil {
		return nil, err
	}

	taskID := fmt.Sprintf("%s-%d", agentName, time.Now().UnixNano())
	retries := MaxRetries
	if retries > len(candidates) {
		retries = len(candidates)
	}

	var lastErr error
	for i := 0; i < retries; i++ {
		node := candidates[i]

		// Try to reserve
		if !Reserve(node.Peer.NodeName, taskID) {
			continue // Already reserved, try next
		}

		// Execute on node
		result := executeOnNode(node.Peer, agentName)
		Release(node.Peer.NodeName)

		if result.Success {
			return result, nil
		}

		lastErr = fmt.Errorf("%s: %s", node.Peer.NodeName, result.Error)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all nodes failed, last error: %w", lastErr)
	}
	return nil, fmt.Errorf("no nodes available")
}

// executeOnNode runs the agent on a specific node via vssh/ssh
func executeOnNode(peer mpop.Peer, agentName string) *RunResult {
	result := &RunResult{
		Node: peer.NodeName,
	}

	// Build command - simple batch execution
	// Just run a command and get output (for now)
	cmd := fmt.Sprintf("hostname && echo 'agent:%s' && date", agentName)

	// Try vssh first, then SSH fallback
	output, err := mpop.VsshExec(peer.VpnIP, cmd, 30*time.Second)
	if err != nil {
		// Try SSH fallback
		output, err = mpop.SSHExec("root", peer.VpnIP, cmd, 22, 30*time.Second)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
	}

	result.Success = true
	result.Output = output
	return result
}

// GetNodeStats returns current stats for display
func GetNodeStats() ([]NodeStat, error) {
	peers, err := mpop.GetPeersWithStats()
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	var stats []NodeStat

	for _, p := range peers {
		stat := NodeStat{
			Name:   p.NodeName,
			IP:     p.VpnIP,
			Online: false,
		}

		if p.Stats != nil && now-p.Stats.UpdatedAt < StatsFreshnessLimit {
			stat.Online = true
			stat.CPU = p.Stats.CPUPct
			stat.Mem = p.Stats.MemPct
			stat.Load = p.Stats.LoadValue
			stat.GPU = p.Stats.GPUCount
			stat.Reserved = IsReserved(p.NodeName)
		}

		stats = append(stats, stat)
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Name < stats[j].Name
	})

	return stats, nil
}

// NodeStat is a simplified node status
type NodeStat struct {
	Name     string
	IP       string
	Online   bool
	CPU      int
	Mem      int
	Load     float64
	GPU      int
	Reserved bool
}
