package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Peer represents a peer from the coordinator
type Peer struct {
	NodeID      string      `json:"node_id"`
	NodeName    string      `json:"node_name"`
	WgPublicKey string      `json:"wg_public_key"`
	PublicIP    string      `json:"public_ip"`
	LanIP       string      `json:"lan_ip"`
	VpnIP       string      `json:"vpn_ip"`
	Port        int         `json:"port"`
	NatPort     int         `json:"nat_port"`
	LastSeen    interface{} `json:"last_seen"` // can be string or number
}

// PeersResponse from coordinator
type PeersResponse struct {
	Peers []Peer `json:"peers"`
}

// httpClient with timeout
func httpClient(timeoutSec int) *http.Client {
	return &http.Client{
		Timeout: time.Duration(timeoutSec) * time.Second,
	}
}

// APIGet performs GET request to coordinator
func APIGet(server, endpoint string, timeoutSec int) (map[string]interface{}, error) {
	url := strings.TrimSuffix(server, "/") + endpoint
	client := httpClient(timeoutSec)

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// APIPost performs POST request to coordinator
func APIPost(server, endpoint string, data interface{}, timeoutSec int) (map[string]interface{}, error) {
	url := strings.TrimSuffix(server, "/") + endpoint
	client := httpClient(timeoutSec)

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetPeers fetches peers from coordinator for a specific network
func GetPeers(server string) ([]Peer, error) {
	return GetPeersForNetwork(server, "default")
}

// GetPeersWithFailover tries multiple coordinator URLs
func GetPeersWithFailover(servers []string) ([]Peer, error) {
	var lastErr error
	for _, server := range servers {
		peers, err := GetPeers(server)
		if err == nil {
			return peers, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// GetPeersForNetwork fetches peers from coordinator for a specific network
func GetPeersForNetwork(server, network string) ([]Peer, error) {
	if network == "" {
		network = "default"
	}
	url := strings.TrimSuffix(server, "/") + "/peers?network=" + network
	client := httpClient(5)

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result PeersResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result.Peers, nil
}

// NetworkInfo represents network metadata from coordinator
type NetworkInfo struct {
	Name   string `json:"name"`
	Subnet string `json:"subnet"`
}

// GetNetworkInfo fetches network info from coordinator
func GetNetworkInfo(server, network string) (*NetworkInfo, error) {
	if network == "" {
		network = "default"
	}
	url := strings.TrimSuffix(server, "/") + "/networks"
	client := httpClient(5)

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Networks []NetworkInfo `json:"networks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	for _, n := range result.Networks {
		if n.Name == network {
			return &n, nil
		}
	}

	// Default subnet for unknown networks (10.98 to avoid conflict with Python version)
	return &NetworkInfo{Name: network, Subnet: "10.98"}, nil
}

// Register registers this node with the coordinator
func Register(server string, cfg *Config, pubKey, lanIP string, natPort int) error {
	network := cfg.Network
	if network == "" {
		network = "default"
	}
	data := map[string]interface{}{
		"network":       network,
		"access_key":    cfg.AccessKey,
		"node_id":       cfg.NodeID,
		"node_name":     cfg.NodeName,
		"port":          cfg.ListenPort,
		"nat_port":      natPort,
		"wg_public_key": pubKey,
		"lan_ip":        lanIP,
		"vpn_ip":        cfg.VpnIP,
	}
	result, err := APIPost(server, "/register", data, 5)
	if err != nil {
		return err
	}
	if errMsg, ok := result["error"]; ok {
		return fmt.Errorf("registration failed: %v", errMsg)
	}
	return nil
}
