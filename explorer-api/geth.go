package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
)

type GethClient struct {
	url    string
	client http.Client
}

func newGethClient(url string) *GethClient {
	return &GethClient{url: url, client: http.Client{Timeout: 10 * time.Second}}
}

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

type rpcResp struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (g *GethClient) call(method string, params []any, out any) error {
	body, _ := json.Marshal(rpcReq{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	resp, err := g.client.Post(g.url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r rpcResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if r.Error != nil {
		return fmt.Errorf("rpc %s: %s", method, r.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Result, out)
}

func (g *GethClient) callBatch(reqs []rpcReq) ([]json.RawMessage, error) {
	body, _ := json.Marshal(reqs)
	resp, err := g.client.Post(g.url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var results []rpcResp
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, len(results))
	for i, r := range results {
		out[i] = r.Result
	}
	return out, nil
}

// EVM wire types

type EthBlock struct {
	Number        string  `json:"number"`
	Hash          string  `json:"hash"`
	ParentHash    string  `json:"parentHash"`
	Timestamp     string  `json:"timestamp"`
	GasUsed       string  `json:"gasUsed"`
	GasLimit      string  `json:"gasLimit"`
	Size          string  `json:"size"`
	ExtraData     string  `json:"extraData"`
	BaseFeePerGas string  `json:"baseFeePerGas"`
	Miner         string  `json:"miner"`
	Transactions  []EthTx `json:"transactions"`
}

type EthTx struct {
	Hash                 string `json:"hash"`
	BlockNumber          string `json:"blockNumber"`
	BlockHash            string `json:"blockHash"`
	TransactionIndex     string `json:"transactionIndex"`
	From                 string `json:"from"`
	To                   string `json:"to"`
	Value                string `json:"value"`
	Gas                  string `json:"gas"`
	GasPrice             string `json:"gasPrice"`
	MaxFeePerGas         string `json:"maxFeePerGas"`
	MaxPriorityFeePerGas string `json:"maxPriorityFeePerGas"`
	Nonce                string `json:"nonce"`
	Input                string `json:"input"`
	Type                 string `json:"type"`
}

type EthReceipt struct {
	TransactionHash string   `json:"transactionHash"`
	GasUsed         string   `json:"gasUsed"`
	Status          string   `json:"status"`
	ContractAddress string   `json:"contractAddress"`
	Logs            []EthLog `json:"logs"`
}

type EthLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	TransactionHash string   `json:"transactionHash"`
	LogIndex        string   `json:"logIndex"`
	Removed         bool     `json:"removed"`
}

// RPC methods

func (g *GethClient) BlockNumber() (int64, error) {
	var result string
	if err := g.call("eth_blockNumber", []any{}, &result); err != nil {
		return 0, err
	}
	return hexToInt64(result), nil
}

func (g *GethClient) GetBlockByNumber(height int64) (*EthBlock, error) {
	var block EthBlock
	if err := g.call("eth_getBlockByNumber", []any{fmt.Sprintf("0x%x", height), true}, &block); err != nil {
		return nil, err
	}
	if block.Hash == "" {
		return nil, nil
	}
	return &block, nil
}

func (g *GethClient) GetReceiptsBatch(hashes []string) ([]*EthReceipt, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	reqs := make([]rpcReq, len(hashes))
	for i, h := range hashes {
		reqs[i] = rpcReq{JSONRPC: "2.0", Method: "eth_getTransactionReceipt", Params: []any{h}, ID: i + 1}
	}
	raw, err := g.callBatch(reqs)
	if err != nil {
		return nil, err
	}
	receipts := make([]*EthReceipt, len(raw))
	for i, r := range raw {
		if r == nil || string(r) == "null" {
			continue
		}
		var receipt EthReceipt
		if json.Unmarshal(r, &receipt) == nil {
			receipts[i] = &receipt
		}
	}
	return receipts, nil
}

func (g *GethClient) GetBalance(addr string) (string, error) {
	var result string
	if err := g.call("eth_getBalance", []any{addr, "latest"}, &result); err != nil {
		return "0", err
	}
	return hexToDec(result), nil
}

func (g *GethClient) GetTxCount(addr string) (int64, error) {
	var result string
	if err := g.call("eth_getTransactionCount", []any{addr, "latest"}, &result); err != nil {
		return 0, err
	}
	return hexToInt64(result), nil
}

func (g *GethClient) GetCode(addr string) (string, error) {
	var result string
	if err := g.call("eth_getCode", []any{addr, "latest"}, &result); err != nil {
		return "0x", err
	}
	return result, nil
}

func (g *GethClient) Call(to, data string) (string, error) {
	var result string
	if err := g.call("eth_call", []any{map[string]string{"to": to, "data": data}, "latest"}, &result); err != nil {
		return "", err
	}
	return result, nil
}

func (g *GethClient) GasPrice() string {
	var result string
	g.call("eth_gasPrice", []any{}, &result)
	return result
}

// Token metadata via eth_call

const (
	transferTopic    = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	erc20NameSel     = "0x06fdde03"
	erc20SymbolSel   = "0x95d89b41"
	erc20DecimalsSel = "0x313ce567"
	erc20TotalSel    = "0x18160ddd"
)

type TokenMeta struct {
	Name        string
	Symbol      string
	Decimals    int
	TotalSupply string
	Type        string
}

func (g *GethClient) FetchTokenMeta(addr string, is721 bool) *TokenMeta {
	t := &TokenMeta{Type: "ERC-20"}
	if is721 {
		t.Type = "ERC-721"
	}
	if name, err := g.Call(addr, erc20NameSel); err == nil {
		t.Name = decodeABIString(name)
	}
	if sym, err := g.Call(addr, erc20SymbolSel); err == nil {
		t.Symbol = decodeABIString(sym)
	}
	if !is721 {
		if dec, err := g.Call(addr, erc20DecimalsSel); err == nil {
			t.Decimals = int(hexToUint64(dec))
		}
		if ts, err := g.Call(addr, erc20TotalSel); err == nil {
			t.TotalSupply = hexToDec(ts)
		}
	}
	return t
}

// Numeric helpers

func hexToInt64(h string) int64 {
	h = strings.TrimPrefix(h, "0x")
	if h == "" {
		return 0
	}
	n := new(big.Int)
	n.SetString(h, 16)
	if !n.IsInt64() {
		return n.Int64()
	}
	return n.Int64()
}

func hexToUint64(h string) uint64 {
	h = strings.TrimPrefix(h, "0x")
	if h == "" {
		return 0
	}
	n := new(big.Int)
	n.SetString(h, 16)
	return n.Uint64()
}

func hexToDec(h string) string {
	h = strings.TrimPrefix(h, "0x")
	if h == "" {
		return "0"
	}
	n, ok := new(big.Int).SetString(h, 16)
	if !ok {
		return "0"
	}
	return n.String()
}

func addBigStr(a, b string) string {
	ai, bi := new(big.Int), new(big.Int)
	ai.SetString(a, 10)
	bi.SetString(b, 10)
	return new(big.Int).Add(ai, bi).String()
}

func subBigStr(a, b string) string {
	ai, bi := new(big.Int), new(big.Int)
	ai.SetString(a, 10)
	bi.SetString(b, 10)
	r := new(big.Int).Sub(ai, bi)
	if r.Sign() < 0 {
		return "0"
	}
	return r.String()
}

// decodeABIString decodes a hex-encoded ABI string return value.
// Handles both ABI-encoded dynamic strings and static bytes32.
func decodeABIString(result string) string {
	result = strings.TrimPrefix(result, "0x")
	if len(result) == 0 || len(result)%2 != 0 {
		return ""
	}
	data, err := hex.DecodeString(result)
	if err != nil || len(data) == 0 {
		return ""
	}
	// Dynamic ABI string: first 32 bytes = offset (0x20), next 32 = length, rest = content
	if len(data) >= 64 {
		offset := new(big.Int).SetBytes(data[:32]).Int64()
		if offset == 32 {
			length := new(big.Int).SetBytes(data[32:64]).Int64()
			if length > 0 && length <= 512 && int64(len(data)) >= 64+length {
				return string(data[64 : 64+length])
			}
		}
	}
	// Fallback: static bytes32
	end := 32
	if len(data) < 32 {
		end = len(data)
	}
	return strings.TrimRight(string(data[:end]), "\x00")
}
