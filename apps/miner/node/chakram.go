package node

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type ChakramClient struct {
	url    string
	client *http.Client
}

type ChakramStatus struct {
	Height           uint64  `json:"height"`
	TipHash          string  `json:"tipHash"`
	ChainID          uint64  `json:"chainId"`
	GenesisHash      string  `json:"genesisHash"`
	Difficulty       uint64  `json:"difficulty"`
	AvgBlockTimeSecs float64 `json:"avgBlockTimeSecs"`
}

func NewChakramClient(url string) *ChakramClient {
	return &ChakramClient{
		url:    url,
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *ChakramClient) Status(ctx context.Context) (ChakramStatus, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.url+"/status", nil)
	if err != nil {
		return ChakramStatus{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return ChakramStatus{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ChakramStatus{}, err
	}
	var s ChakramStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return ChakramStatus{}, err
	}
	return s, nil
}

// MinedBlockCount returns the number of blocks whose reward address matches
// minerAddr, scanned from chain data via GET /minedblocks.
func (c *ChakramClient) MinedBlockCount(ctx context.Context, minerAddr string) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		c.url+"/minedblocks?miner="+minerAddr, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var result struct {
		Count uint64 `json:"count"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		return 0, err
	}
	return result.Count, nil
}

// Peers returns the peer URL list from GET /peers.
func (c *ChakramClient) Peers(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.url+"/peers", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		Peers []string `json:"peers"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result); err != nil {
		return nil, err
	}
	return result.Peers, nil
}
