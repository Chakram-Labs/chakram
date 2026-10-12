package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type GethClient struct {
	url    string
	client *http.Client
}

func NewGethClient(url string) *GethClient {
	return &GethClient{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *GethClient) call(ctx context.Context, method string, params []interface{}, out interface{}) error {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var rpcResp rpcResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return err
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(rpcResp.Result, out)
}

func hexToUint64(s string) (uint64, error) {
	s = strings.TrimPrefix(s, "0x")
	return strconv.ParseUint(s, 16, 64)
}

func (c *GethClient) BlockNumber(ctx context.Context) (uint64, error) {
	var result string
	if err := c.call(ctx, "eth_blockNumber", nil, &result); err != nil {
		return 0, err
	}
	return hexToUint64(result)
}

func (c *GethClient) GetBalance(ctx context.Context, addr string) (*big.Int, error) {
	var result string
	if err := c.call(ctx, "eth_getBalance", []interface{}{addr, "latest"}, &result); err != nil {
		return nil, err
	}
	result = strings.TrimPrefix(result, "0x")
	n := new(big.Int)
	n.SetString(result, 16)
	return n, nil
}

type syncStatus struct {
	CurrentBlock string `json:"currentBlock"`
	HighestBlock string `json:"highestBlock"`
}

// SyncStatus returns (synced, currentBlock, highestBlock, error).
// If not syncing, synced=true and both block numbers are 0.
func (c *GethClient) SyncStatus(ctx context.Context) (bool, uint64, uint64, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "eth_syncing", nil, &raw); err != nil {
		return false, 0, 0, err
	}
	// false means fully synced
	if string(raw) == "false" {
		return true, 0, 0, nil
	}
	var ss syncStatus
	if err := json.Unmarshal(raw, &ss); err != nil {
		return false, 0, 0, err
	}
	cur, _ := hexToUint64(ss.CurrentBlock)
	high, _ := hexToUint64(ss.HighestBlock)
	return false, cur, high, nil
}

func (c *GethClient) PeerCount(ctx context.Context) (int, error) {
	var result string
	if err := c.call(ctx, "net_peerCount", nil, &result); err != nil {
		return 0, err
	}
	n, err := hexToUint64(result)
	return int(n), err
}

type GethBlock struct {
	Number     uint64
	Hash       string
	Miner      string
	Timestamp  uint64
	Difficulty string
	TxCount    int
}

type rawBlock struct {
	Number       string        `json:"number"`
	Hash         string        `json:"hash"`
	Miner        string        `json:"miner"`
	Timestamp    string        `json:"timestamp"`
	Difficulty   string        `json:"difficulty"`
	Transactions []interface{} `json:"transactions"`
}

func (c *GethClient) GetBlockByNumber(ctx context.Context, n uint64) (GethBlock, error) {
	hexN := fmt.Sprintf("0x%x", n)
	var rb rawBlock
	if err := c.call(ctx, "eth_getBlockByNumber", []interface{}{hexN, false}, &rb); err != nil {
		return GethBlock{}, err
	}
	num, _ := hexToUint64(rb.Number)
	ts, _ := hexToUint64(rb.Timestamp)
	return GethBlock{
		Number:     num,
		Hash:       rb.Hash,
		Miner:      rb.Miner,
		Timestamp:  ts,
		Difficulty: rb.Difficulty,
		TxCount:    len(rb.Transactions),
	}, nil
}

func (c *GethClient) GetLatestBlocks(ctx context.Context, count int) ([]GethBlock, error) {
	tip, err := c.BlockNumber(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]GethBlock, 0, count)
	for i := 0; i < count; i++ {
		if tip < uint64(i) {
			break
		}
		blk, err := c.GetBlockByNumber(ctx, tip-uint64(i))
		if err != nil {
			break
		}
		out = append(out, blk)
	}
	return out, nil
}
