package abci

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	abcitypes "github.com/cometbft/cometbft/abci/types"

	"chakram/engine"
)

// mockEngine implements EngineClient for tests.
type mockEngine struct {
	fcuResult     engine.ForkchoiceUpdatedResult
	fcuErr        error
	getPayload    engine.GetPayloadResult
	getPayloadErr error
	newPayload    engine.PayloadStatusV1
	newPayloadErr error
}

func (m *mockEngine) ForkchoiceUpdatedV3(_ context.Context, _ engine.ForkchoiceStateV1, _ *engine.PayloadAttributesV3) (engine.ForkchoiceUpdatedResult, error) {
	return m.fcuResult, m.fcuErr
}
func (m *mockEngine) GetPayloadV3(_ context.Context, _ string) (engine.GetPayloadResult, error) {
	return m.getPayload, m.getPayloadErr
}
func (m *mockEngine) NewPayloadV3(_ context.Context, _ engine.ExecutionPayloadV3, _ []engine.Hash, _ engine.Hash) (engine.PayloadStatusV1, error) {
	return m.newPayload, m.newPayloadErr
}

func testApp(t *testing.T, eng EngineClient) *ChakramApp {
	t.Helper()
	dir := t.TempDir()
	app, err := NewChakramApp(eng, Config{
		Home:          dir,
		RewardAddress: "0x1234567890123456789012345678901234567890",
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("NewChakramApp: %v", err)
	}
	return app
}

// buildPayloadBytes returns a minimal ExecutionPayloadV3 JSON with the given parent hash and timestamp.
func buildPayloadBytes(parentHash string, ts uint64) []byte {
	logsBloom := "0x" + fmt.Sprintf("%0512x", 0)
	p := engine.ExecutionPayloadV3{
		ParentHash:    parentHash,
		FeeRecipient:  "0x1234567890123456789012345678901234567890",
		StateRoot:     zeroHash,
		ReceiptsRoot:  zeroHash,
		LogsBloom:     logsBloom,
		PrevRandao:    zeroHash,
		BlockNumber:   "0x1",
		GasLimit:      "0x1c9c380",
		GasUsed:       "0x0",
		Timestamp:     fmt.Sprintf("0x%x", ts),
		ExtraData:     "0x",
		BaseFeePerGas: "0x3b9aca00",
		BlockHash:     "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Transactions:  []string{},
		Withdrawals:   []engine.WithdrawalV1{},
		BlobGasUsed:   "0x0",
		ExcessBlobGas: "0x0",
	}
	b, _ := json.Marshal(p)
	return b
}

// makeValidEnvelope encodes a minimal envelope with the given parent hash and timestamp.
func makeValidEnvelope(t *testing.T, parentHash string, ts uint64) []byte {
	t.Helper()
	env := &ExecutionEnvelope{
		Version:               envelopeVersion,
		ExecutionPayload:      buildPayloadBytes(parentHash, ts),
		VersionedHashes:       [][]byte{},
		ParentBeaconBlockRoot: make([]byte, 32),
	}
	b, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	return b
}

// ── Timestamp rule tests ──────────────────────────────────────────────────────

func TestTimestampRule(t *testing.T) {
	cases := []struct {
		name     string
		comet    time.Time
		prevTime uint64
		want     uint64
	}{
		{"comet_ahead", time.Unix(100, 0), 90, 100},
		{"comet_equal", time.Unix(90, 0), 90, 91},
		{"comet_behind", time.Unix(85, 0), 90, 91},
		{"comet_zero", time.Unix(0, 0), 0, 1},
		{"comet_exactly_plus1", time.Unix(91, 0), 90, 91},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := computeTimestamp(tc.comet, tc.prevTime)
			if got != tc.want {
				t.Errorf("computeTimestamp(%v, %d) = %d, want %d", tc.comet, tc.prevTime, got, tc.want)
			}
		})
	}
}

// ── ProcessProposal tests ─────────────────────────────────────────────────────

func TestProcessProposal_ZeroTxs(t *testing.T) {
	app := testApp(t, &mockEngine{newPayload: engine.PayloadStatusV1{Status: "VALID"}})
	resp, err := app.ProcessProposal(context.Background(), &abcitypes.RequestProcessProposal{
		Height: 1,
		Txs:    nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != abcitypes.ResponseProcessProposal_REJECT {
		t.Errorf("expected REJECT for 0 txs, got %v", resp.Status)
	}
}

func TestProcessProposal_TwoTxs(t *testing.T) {
	app := testApp(t, &mockEngine{newPayload: engine.PayloadStatusV1{Status: "VALID"}})
	env := makeValidEnvelope(t, zeroHash, 1)
	resp, err := app.ProcessProposal(context.Background(), &abcitypes.RequestProcessProposal{
		Height: 1,
		Txs:    [][]byte{env, env},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != abcitypes.ResponseProcessProposal_REJECT {
		t.Errorf("expected REJECT for 2 txs, got %v", resp.Status)
	}
}

func TestProcessProposal_WrongParent(t *testing.T) {
	app := testApp(t, &mockEngine{newPayload: engine.PayloadStatusV1{Status: "VALID"}})
	wrongParent := "0x1111111111111111111111111111111111111111111111111111111111111111"
	env := makeValidEnvelope(t, wrongParent, 1)
	resp, err := app.ProcessProposal(context.Background(), &abcitypes.RequestProcessProposal{
		Height: 1,
		Txs:    [][]byte{env},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != abcitypes.ResponseProcessProposal_REJECT {
		t.Errorf("expected REJECT for wrong parent, got %v", resp.Status)
	}
}

func TestProcessProposal_BadTimestamp(t *testing.T) {
	eng := &mockEngine{newPayload: engine.PayloadStatusV1{Status: "VALID"}}
	app := testApp(t, eng)
	app.state.LastPayloadTime = 100 // require ts >= 101

	env := makeValidEnvelope(t, zeroHash, 99) // ts = 99, violates rule
	resp, err := app.ProcessProposal(context.Background(), &abcitypes.RequestProcessProposal{
		Height: 1,
		Txs:    [][]byte{env},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != abcitypes.ResponseProcessProposal_REJECT {
		t.Errorf("expected REJECT for bad timestamp (99 < 101), got %v", resp.Status)
	}
}

func TestProcessProposal_Valid(t *testing.T) {
	eng := &mockEngine{newPayload: engine.PayloadStatusV1{Status: "VALID"}}
	app := testApp(t, eng)
	env := makeValidEnvelope(t, zeroHash, 1)
	resp, err := app.ProcessProposal(context.Background(), &abcitypes.RequestProcessProposal{
		Height: 1,
		Txs:    [][]byte{env},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != abcitypes.ResponseProcessProposal_ACCEPT {
		t.Errorf("expected ACCEPT for valid proposal, got %v", resp.Status)
	}
}

func TestProcessProposal_EngineInvalid(t *testing.T) {
	invalid := "bad state transition"
	eng := &mockEngine{newPayload: engine.PayloadStatusV1{Status: "INVALID", ValidationError: &invalid}}
	app := testApp(t, eng)
	env := makeValidEnvelope(t, zeroHash, 1)
	resp, err := app.ProcessProposal(context.Background(), &abcitypes.RequestProcessProposal{
		Height: 1,
		Txs:    [][]byte{env},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != abcitypes.ResponseProcessProposal_REJECT {
		t.Errorf("expected REJECT when engine returns INVALID, got %v", resp.Status)
	}
}

// F1: SYNCING must now REJECT (not optimistic accept).
func TestProcessProposal_EngineSyncing_Rejects(t *testing.T) {
	eng := &mockEngine{newPayload: engine.PayloadStatusV1{Status: "SYNCING"}}
	app := testApp(t, eng)
	env := makeValidEnvelope(t, zeroHash, 1)
	resp, err := app.ProcessProposal(context.Background(), &abcitypes.RequestProcessProposal{
		Height: 1,
		Txs:    [][]byte{env},
	})
	if err != nil {
		t.Fatal(err)
	}
	// F1: SYNCING → REJECT (changed from optimistic accept in the old code)
	if resp.Status != abcitypes.ResponseProcessProposal_REJECT {
		t.Errorf("expected REJECT for SYNCING (F1), got %v", resp.Status)
	}
}

// F2: Health gate skips proposal when geth is behind.
func TestHealthGate_SkipsProposal(t *testing.T) {
	payloadID := "0xdeadbeef"
	eng := &mockEngine{
		fcuResult: engine.ForkchoiceUpdatedResult{
			PayloadStatus: engine.PayloadStatusV1{Status: "VALID"},
			PayloadID:     &payloadID,
		},
		getPayload: engine.GetPayloadResult{
			ExecutionPayload: engine.ExecutionPayloadV3{
				BlockHash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Timestamp: "0x1",
			},
		},
		newPayload: engine.PayloadStatusV1{Status: "VALID"},
	}

	// Start a mock geth RPC that returns block number 0 (far behind).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"jsonrpc":"2.0","id":1,"result":"0x0"}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	app, err := NewChakramApp(eng, Config{
		Home:          dir,
		RewardAddress: "0x1234567890123456789012345678901234567890",
		GethRPCURL:    srv.URL,
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// Set committed height to 10 so geth (at 0) is 10 behind (> 2).
	app.state.LastHeight = 10
	app.state.LastPayloadTime = 100

	resp, err := app.PrepareProposal(context.Background(), &abcitypes.RequestPrepareProposal{
		Height: 11,
		Time:   time.Unix(200, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Txs) != 0 {
		t.Errorf("health gate: expected empty proposal (0 txs), got %d txs", len(resp.Txs))
	}
}
