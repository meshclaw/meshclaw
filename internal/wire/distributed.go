package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/meshclaw/meshclaw/internal/common"
)

// PeersCache manages local peer cache
type PeersCache struct {
	Peers     []Peer `json:"peers"`
	UpdatedAt int64  `json:"updated_at"`
}

// CachePath returns path to peers cache file
func CachePath() string {
	if common.IsRoot() {
		return "/etc/wire/peers.json"
	}
	return common.WireDir() + "/peers.json"
}

// LoadPeersCache loads cached peers
func LoadPeersCache() ([]Peer, error) {
	data, err := os.ReadFile(CachePath())
	if err != nil {
		return nil, err
	}
	var cache PeersCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return cache.Peers, nil
}

// SavePeersCache saves peers to cache
func SavePeersCache(peers []Peer) error {
	cache := PeersCache{
		Peers:     peers,
		UpdatedAt: time.Now().Unix(),
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(CachePath(), data, 0644)
}

// GetPeersFromAll fetches peers from all known coordinators and merges
func GetPeersFromAll(primaryServer string, cachedPeers []Peer) []Peer {
	// Collect all coordinator URLs (primary + all known peers)
	coordURLs := make(map[string]bool)
	coordURLs[primaryServer] = true

	// Each peer can also be a coordinator
	for _, p := range cachedPeers {
		if p.PublicIP != "" && p.Port > 0 {
			url := fmt.Sprintf("http://%s:%d", p.PublicIP, 8790)
			coordURLs[url] = true
		}
		if p.VpnIP != "" {
			url := fmt.Sprintf("http://%s:%d", p.VpnIP, 8790)
			coordURLs[url] = true
		}
	}

	// Fetch from all coordinators in parallel
	var wg sync.WaitGroup
	results := make(chan []Peer, len(coordURLs))

	for url := range coordURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			peers, err := GetPeers(u)
			if err == nil && len(peers) > 0 {
				results <- peers
			}
		}(url)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	close(results)

	// Merge all results
	peerMap := make(map[string]Peer)

	// Start with cached peers
	for _, p := range cachedPeers {
		peerMap[p.NodeID] = p
	}

	// Merge fetched peers (newer wins)
	for peers := range results {
		for _, p := range peers {
			if existing, ok := peerMap[p.NodeID]; ok {
				// Keep newer one
				existingTime := parseLastSeen(existing.LastSeen)
				newTime := parseLastSeen(p.LastSeen)
				if newTime > existingTime {
					peerMap[p.NodeID] = p
				}
			} else {
				peerMap[p.NodeID] = p
			}
		}
	}

	// Convert back to slice
	merged := make([]Peer, 0, len(peerMap))
	for _, p := range peerMap {
		merged = append(merged, p)
	}

	return merged
}

func parseLastSeen(v interface{}) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}

// RegisterWithAll registers with all known coordinators
func RegisterWithAll(primaryServer string, cachedPeers []Peer, cfg *Config, pubKey, lanIP string, natPort int) {
	// Collect all coordinator URLs
	coordURLs := make(map[string]bool)
	coordURLs[primaryServer] = true

	for _, p := range cachedPeers {
		if p.PublicIP != "" {
			url := fmt.Sprintf("http://%s:%d", p.PublicIP, 8790)
			coordURLs[url] = true
		}
		if p.VpnIP != "" {
			url := fmt.Sprintf("http://%s:%d", p.VpnIP, 8790)
			coordURLs[url] = true
		}
	}

	// Register with all in parallel
	var wg sync.WaitGroup
	for url := range coordURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			Register(u, cfg, pubKey, lanIP, natPort)
		}(url)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}
