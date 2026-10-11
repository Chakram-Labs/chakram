package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"chakram/consensus"
)

// Client pushes and pulls consensus blocks from a single peer.
type Client struct {
	base string
	http *http.Client
}

// NewClient creates a Client that talks to the peer at baseURL
// (e.g. "http://127.0.0.1:9001").
func NewClient(baseURL string) *Client {
	return &Client{
		base: baseURL,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

// FetchStatus returns the peer's current chain tip.
func (c *Client) FetchStatus(ctx context.Context) (StatusResponse, error) {
	var st StatusResponse
	if err := c.get(ctx, "/status", &st); err != nil {
		return StatusResponse{}, fmt.Errorf("fetch status from %s: %w", c.base, err)
	}
	return st, nil
}

// FetchBlock fetches a single block by hash from the peer.
func (c *Client) FetchBlock(ctx context.Context, hash [32]byte) (*consensus.ConsensusBlock, error) {
	url := "/block?hash=" + consensus.HashToHex(hash)
	var wire WireBlock
	if err := c.get(ctx, url, &wire); err != nil {
		return nil, fmt.Errorf("fetch block %s from %s: %w", consensus.HashToHex(hash), c.base, err)
	}
	return WireToBlock(wire)
}

// PushBlock sends a block to the peer via POST /block.
func (c *Client) PushBlock(ctx context.Context, b *consensus.ConsensusBlock) error {
	wire := BlockToWire(b)
	body, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/block", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("push block to %s: %w", c.base, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("push block: peer returned %d", resp.StatusCode)
	}
	return nil
}

// FetchBlocks fetches up to limit blocks starting from height from.
func (c *Client) FetchBlocks(ctx context.Context, from, limit uint64) ([]*consensus.ConsensusBlock, error) {
	url := fmt.Sprintf("/blocks?from=%d&limit=%d", from, limit)
	var wires []WireBlock
	if err := c.get(ctx, url, &wires); err != nil {
		return nil, fmt.Errorf("fetch blocks from %s: %w", c.base, err)
	}
	blocks := make([]*consensus.ConsensusBlock, 0, len(wires))
	for i, w := range wires {
		b, err := WireToBlock(w)
		if err != nil {
			return nil, fmt.Errorf("wire block [%d]: %w", i, err)
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

// FindCommonAncestor sends a block locator to the peer and returns the locator
// response containing the common ancestor height.
func (c *Client) FindCommonAncestor(ctx context.Context, req LocatorRequest) (LocatorResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return LocatorResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/locator", bytes.NewReader(body))
	if err != nil {
		return LocatorResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return LocatorResponse{}, fmt.Errorf("locator from %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return LocatorResponse{}, fmt.Errorf("locator HTTP %d: %s", resp.StatusCode, msg)
	}
	var result LocatorResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return LocatorResponse{}, fmt.Errorf("decode locator response: %w", err)
	}
	return result, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
