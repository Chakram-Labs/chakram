package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockCometBFT returns a test CometBFT RPC server with preset responses.
func mockCometBFT(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "status"):
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      -1,
				"result": map[string]any{
					"node_info": map[string]any{
						"id":      "abc123",
						"moniker": "test-node",
						"version": "0.40.0",
						"network": "chakram-test-1",
					},
					"sync_info": map[string]any{
						"latest_block_height": "42",
						"latest_block_hash":   "AABBCC",
						"latest_app_hash":     "DDEEFF",
						"catching_up":         false,
					},
					"validator_info": map[string]any{
						"address":      "1234ABCD",
						"pub_key":      map[string]any{"type": "ed25519", "value": "base64val"},
						"voting_power": "1",
					},
				},
			})
		case strings.Contains(r.URL.Path, "net_info"):
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      -1,
				"result": map[string]any{
					"n_peers": "3",
					"peers":   []any{},
				},
			})
		case strings.Contains(r.URL.Path, "block"):
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      -1,
				"result": map[string]any{
					"block_id": map[string]any{"hash": "BLOCK42HASH"},
					"block": map[string]any{
						"header": map[string]any{
							"height":           "42",
							"time":             "2026-01-01T00:00:00Z",
							"proposer_address": "PROPOSER",
							"app_hash":         "DDEEFF",
						},
						"data": map[string]any{"txs": []any{}},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// mockGeth returns a test geth JSON-RPC server.
func mockGeth(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_blockNumber":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": "0x2a", // 42
			})
		case "eth_getBlockByNumber":
			// Return different data for "finalized" vs numbered blocks.
			tag := ""
			if len(req.Params) > 0 {
				if s, ok := req.Params[0].(string); ok {
					tag = s
				}
			}
			blockNum := "0x2a"
			if tag == "finalized" {
				blockNum = "0x28" // 40
			}
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": map[string]any{
					"hash":         "0xdeadbeef",
					"number":       blockNum,
					"gasUsed":      "0x5208",
					"gasLimit":     "0x1c9c380",
					"transactions": []any{},
				},
			})
		case "eth_getBalance":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": "0xde0b6b3a7640000",
			})
		case "eth_getTransactionCount":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": "0x5",
			})
		case "eth_getCode":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": "0x",
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": nil,
			})
		}
	}))
}

func newTestServer(t *testing.T) (*Server, *httptest.Server, *httptest.Server) {
	t.Helper()
	cometSrv := mockCometBFT(t)
	gethSrv := mockGeth(t)
	s := New(Config{
		ListenAddr:    "127.0.0.1:0",
		CometRPCURL:   cometSrv.URL,
		GethRPCURL:    gethSrv.URL,
		DevMode:       false,
		RewardAddress: "0x1111111111111111111111111111111111111111",
	}, nil)
	return s, cometSrv, gethSrv
}

func TestHandleStatus(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Height != 42 {
		t.Errorf("height: got %d, want 42", resp.Height)
	}
	if resp.ChainID != "chakram-test-1" {
		t.Errorf("chain_id: got %q", resp.ChainID)
	}
	if resp.NodeID != "abc123" {
		t.Errorf("node_id: got %q", resp.NodeID)
	}
}

func TestHandleValidator(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/validator", nil)
	w := httptest.NewRecorder()
	s.handleValidator(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp ValidatorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.IsActive {
		t.Error("expected is_active=true")
	}
	if resp.Power != 1 {
		t.Errorf("voting_power: got %d, want 1", resp.Power)
	}
}

func TestHandleAddress(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/address/0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", nil)
	req.URL.Path = "/api/address/0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	w := httptest.NewRecorder()
	s.handleAddress(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AddressResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Balance == "" || resp.Balance == "0" {
		t.Errorf("expected non-zero balance, got %q", resp.Balance)
	}
	if resp.Nonce != 5 {
		t.Errorf("nonce: got %d, want 5", resp.Nonce)
	}
}

func TestHandleBlock(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/block/42", nil)
	req.URL.Path = "/api/block/42"
	w := httptest.NewRecorder()
	s.handleBlock(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp BlockResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Height != 42 {
		t.Errorf("height: got %d, want 42", resp.Height)
	}
}

func TestHandleBlockInvalidHeight(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/block/notanumber", nil)
	req.URL.Path = "/api/block/notanumber"
	w := httptest.NewRecorder()
	s.handleBlock(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandlePeers(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	w := httptest.NewRecorder()
	s.handlePeers(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestOpenAPI(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	w := httptest.NewRecorder()
	serveOpenAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var doc map[string]any
	if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
		t.Fatalf("openapi not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Errorf("unexpected openapi version: %v", doc["openapi"])
	}
}

func TestHexToDecStr(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0x0", "0"},
		{"0xde0b6b3a7640000", "1000000000000000000"},
		{"0x5208", "21000"},
		{"0x", "0"},
		{"", "0"},
	}
	for _, c := range cases {
		got := hexToDecStr(c.in)
		if got != c.want {
			t.Errorf("hexToDecStr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHandleStatusGethFields(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.GethAvailable {
		t.Error("expected geth_available=true")
	}
	if resp.GethHeight != 42 {
		t.Errorf("geth_height: got %d, want 42", resp.GethHeight)
	}
	if resp.GethFinalized != 40 {
		t.Errorf("geth_finalized: got %d, want 40", resp.GethFinalized)
	}
	// height=42, geth_height=42 → gap=0 → health gate NOT blocked
	if resp.HealthGateBlocked {
		t.Error("expected health_gate_blocked=false when geth is caught up")
	}
}

func TestHandleValidatorRewardBalance(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/validator", nil)
	w := httptest.NewRecorder()
	s.handleValidator(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ValidatorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RewardAddress == "" {
		t.Error("expected reward_address to be set")
	}
	if resp.RewardBalance == "" || resp.RewardBalance == "0" {
		t.Errorf("expected non-zero reward_balance, got %q", resp.RewardBalance)
	}
	// signed_last_100 + missed_last_100 should equal blocks sampled (≤100)
	total := resp.SignedLast100 + resp.MissedLast100
	if total > 100 {
		t.Errorf("signed+missed=%d > 100", total)
	}
}

func TestBuildSignedTx(t *testing.T) {
	// Verify buildSignedTx produces valid hex with the expected structure.
	chainID := uint64(11642)
	gasPrice := new(bigInt).SetUint64(1_000_000_000) // 1 gwei
	value := new(bigInt).SetUint64(1_000_000_000_000_000_000)
	to := "0xDeaDbeefdEAdbeefdEadbEEFdeadbeEFdEaDbeeF"

	raw, err := buildSignedTx(hardhatPrivKeyHex, chainID, 0, gasPrice, to, value)
	if err != nil {
		t.Fatalf("buildSignedTx: %v", err)
	}
	if !strings.HasPrefix(raw, "0x") {
		t.Errorf("expected 0x prefix, got %s", raw[:min(10, len(raw))])
	}
	// Signed legacy tx must be at least 100 bytes (nonce+gasPrice+gasLimit+to+value+v+r+s)
	rawBytes := (len(raw) - 2) / 2
	if rawBytes < 100 {
		t.Errorf("signed tx too short: %d bytes", rawBytes)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func mockGethFaucet(t *testing.T) *httptest.Server {
	t.Helper()
	nonce := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": "0x2d7a", // 11642
			})
		case "eth_getTransactionCount":
			n := nonce
			nonce++
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": fmt.Sprintf("0x%x", n),
			})
		case "eth_gasPrice":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": "0x3b9aca00", // 1 gwei
			})
		case "eth_sendRawTransaction":
			// Return a fake tx hash — verify the raw tx was passed.
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1,
				"result": "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": nil,
			})
		}
	}))
}

func TestHandleFaucetSuccess(t *testing.T) {
	cometSrv := mockCometBFT(t)
	gethSrv := mockGethFaucet(t)
	defer cometSrv.Close()
	defer gethSrv.Close()

	s := New(Config{
		ListenAddr:  "127.0.0.1:0",
		CometRPCURL: cometSrv.URL,
		GethRPCURL:  gethSrv.URL,
		DevMode:     true,
	}, nil)

	body := `{"address":"0xDeaDbeefdEAdbeefdEadbEEFdeadbeEFdEaDbeeF"}`
	req := httptest.NewRequest(http.MethodPost, "/api/faucet", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	s.handleFaucet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp FaucetResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TxHash == "" {
		t.Error("expected tx_hash in response")
	}
	if resp.Amount != "1000000000000000000" {
		t.Errorf("amount: got %q", resp.Amount)
	}
}

func TestHandleFaucetRateLimit(t *testing.T) {
	cometSrv := mockCometBFT(t)
	gethSrv := mockGethFaucet(t)
	defer cometSrv.Close()
	defer gethSrv.Close()

	s := New(Config{
		ListenAddr:  "127.0.0.1:0",
		CometRPCURL: cometSrv.URL,
		GethRPCURL:  gethSrv.URL,
		DevMode:     true,
	}, nil)

	send := func() int {
		body := `{"address":"0xDeaDbeefdEAdbeefdEadbEEFdeadbeEFdEaDbeeF"}`
		req := httptest.NewRequest(http.MethodPost, "/api/faucet", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:9000"
		w := httptest.NewRecorder()
		s.handleFaucet(w, req)
		return w.Code
	}

	if code := send(); code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", code)
	}
	if code := send(); code != http.StatusTooManyRequests {
		t.Errorf("second request: got %d, want 429", code)
	}
}

func TestHandleFaucetInvalidAddress(t *testing.T) {
	cometSrv := mockCometBFT(t)
	gethSrv := mockGethFaucet(t)
	defer cometSrv.Close()
	defer gethSrv.Close()

	s := New(Config{
		ListenAddr:  "127.0.0.1:0",
		CometRPCURL: cometSrv.URL,
		GethRPCURL:  gethSrv.URL,
		DevMode:     true,
	}, nil)

	body := `{"address":"notanaddress"}`
	req := httptest.NewRequest(http.MethodPost, "/api/faucet", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.2:9000"
	w := httptest.NewRecorder()
	s.handleFaucet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCORSHeaders(t *testing.T) {
	s, comet, geth := newTestServer(t)
	defer comet.Close()
	defer geth.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	handler := corsMiddleware(mux)

	req := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header")
	}
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
}
