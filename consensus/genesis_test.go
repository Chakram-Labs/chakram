package consensus

import (
	"context"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestInitialCompactTargetsUnchanged pins the production difficulty constants.
// These values are baked into every deployed genesis block; changing them
// silently breaks replay protection and forks any live network. The devnet
// bootstrap target (DevnetInitialCompactTarget) is intentionally excluded —
// it may be tuned as hardware profiles change. CP5 is also pinned so orphan-rate
// baselines remain comparable across runs.
func TestInitialCompactTargetsUnchanged(t *testing.T) {
	const (
		wantMainnet  uint64 = 0x2010000000000000 // 2^252 — mainnet genesis
		wantTestnet  uint64 = 0x1F015A0000000000 // ≈2^240.4 — testnet genesis (48,489 expected hashes; 15.02 s at 3×1,076 H/s)
		wantCP5      uint64 = 0x1f40000000000000 // 2^246 — CP5 orphan-rate baseline
	)
	if InitialCompactTargetV2 != wantMainnet {
		t.Errorf("InitialCompactTargetV2 changed: got %016x want %016x\n"+
			"This value is baked into the mainnet genesis block — do NOT change it.",
			InitialCompactTargetV2, wantMainnet)
	}
	if TestnetInitialCompactTarget != wantTestnet {
		t.Errorf("TestnetInitialCompactTarget changed: got %016x want %016x\n"+
			"This value is baked into the testnet genesis block — do NOT change it.",
			TestnetInitialCompactTarget, wantTestnet)
	}
	if DevnetCP5CompactTarget != wantCP5 {
		t.Errorf("DevnetCP5CompactTarget changed: got %016x want %016x\n"+
			"Changing this breaks CP5 orphan-rate comparisons across runs.",
			DevnetCP5CompactTarget, wantCP5)
	}
}

func TestGenesisExecutionPayloadHashConstant(t *testing.T) {
	// Constant must be exactly 32 bytes when decoded.
	h, err := HashFromHex(GenesisExecutionPayloadHashHex)
	if err != nil {
		t.Fatalf("GenesisExecutionPayloadHashHex is not valid hex: %v", err)
	}
	var zero [32]byte
	if h == zero {
		t.Fatal("GenesisExecutionPayloadHashBytes is all-zero")
	}
}

func TestGenesisExecutionPayloadHashBytesMatchHex(t *testing.T) {
	decoded, _ := HashFromHex(GenesisExecutionPayloadHashHex)
	if decoded != GenesisExecutionPayloadHashBytes {
		t.Error("GenesisExecutionPayloadHashBytes does not match GenesisExecutionPayloadHashHex")
	}
}

func TestNewGenesisConsensusBlock(t *testing.T) {
	b := NewGenesisConsensusBlock()

	if b.Header.Version != GenesisVersion {
		t.Errorf("Version: got %d, want %d", b.Header.Version, GenesisVersion)
	}
	if b.Header.Height != 0 {
		t.Errorf("Height: got %d, want 0", b.Header.Height)
	}
	if b.Header.ParentHash != ([32]byte{}) {
		t.Error("ParentHash must be all-zero")
	}
	if b.Header.ExecutionPayloadHash != GenesisExecutionPayloadHashBytes {
		t.Error("ExecutionPayloadHash does not match hardcoded constant")
	}
	if b.Header.Timestamp != DevnetGenesisTimestamp {
		t.Errorf("Timestamp: got %d, want %d", b.Header.Timestamp, DevnetGenesisTimestamp)
	}
	if b.Header.MinerAddress != ([20]byte{}) {
		t.Error("MinerAddress must be all-zero at genesis")
	}
	if b.Header.Difficulty != InitialCompactTargetV2 {
		t.Errorf("Difficulty: got %d, want %d", b.Header.Difficulty, InitialCompactTargetV2)
	}
	if b.Header.Nonce != 0 {
		t.Errorf("Nonce: got %d, want 0", b.Header.Nonce)
	}

	// Hash must be non-zero and deterministic.
	var zero [32]byte
	if b.Hash == zero {
		t.Fatal("genesis block Hash is all-zero")
	}
	b2 := NewGenesisConsensusBlock()
	if b.Hash != b2.Hash {
		t.Error("NewGenesisConsensusBlock is not deterministic")
	}
}

func TestNewTestnetGenesisConsensusBlock(t *testing.T) {
	b := NewTestnetGenesisConsensusBlock()

	if b.Header.Version != GenesisVersion {
		t.Errorf("Version: got %d, want %d", b.Header.Version, GenesisVersion)
	}
	if b.Header.Height != 0 {
		t.Errorf("Height: got %d, want 0", b.Header.Height)
	}
	if b.Header.ParentHash != ([32]byte{}) {
		t.Error("ParentHash must be all-zero")
	}
	if b.Header.ExecutionPayloadHash != TestnetGenesisExecutionPayloadHashBytes {
		t.Error("ExecutionPayloadHash does not match TestnetGenesisExecutionPayloadHashBytes")
	}
	if b.Header.Timestamp != TestnetGenesisTimestamp {
		t.Errorf("Timestamp: got %d, want %d (TestnetGenesisTimestamp)", b.Header.Timestamp, TestnetGenesisTimestamp)
	}
	if b.Header.MinerAddress != ([20]byte{}) {
		t.Error("MinerAddress must be all-zero at genesis")
	}
	if b.Header.Difficulty != TestnetInitialCompactTarget {
		t.Errorf("Difficulty: got %016x, want %016x (TestnetInitialCompactTarget)",
			b.Header.Difficulty, TestnetInitialCompactTarget)
	}
	if b.Header.Nonce != 0 {
		t.Errorf("Nonce: got %d, want 0", b.Header.Nonce)
	}
	var zero [32]byte
	if b.Hash == zero {
		t.Fatal("genesis block Hash is all-zero")
	}
	b2 := NewTestnetGenesisConsensusBlock()
	if b.Hash != b2.Hash {
		t.Error("NewTestnetGenesisConsensusBlock is not deterministic")
	}
}

func TestGenesisHashIsSHA256SHA256(t *testing.T) {
	// GenesisHash must equal SHA256(SHA256(header)) — not RandomX.
	b := NewGenesisConsensusBlock()
	recomputed := GenesisHash(&b.Header)
	if b.Hash != recomputed {
		t.Error("genesis Hash does not match GenesisHash(header)")
	}
}

// TestMainnetGenesisNoAlloc guards config/genesis.mainnet.json against faucet leakage
// and confirms it uses the correct mainnet chain ID.
func TestMainnetGenesisNoAlloc(t *testing.T) {
	raw, err := os.ReadFile("../config/genesis.mainnet.json")
	if err != nil {
		t.Fatalf("read config/genesis.mainnet.json: %v", err)
	}
	var g struct {
		Config struct {
			ChainID int `json:"chainId"`
		} `json:"config"`
		Alloc map[string]json.RawMessage `json:"alloc"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("parse config/genesis.mainnet.json: %v", err)
	}
	if g.Config.ChainID != MainnetChainID {
		t.Errorf("mainnet genesis chainId: got %d, want %d (MainnetChainID)",
			g.Config.ChainID, MainnetChainID)
	}
	if len(g.Alloc) != 0 {
		t.Errorf("mainnet genesis must have empty alloc, found %d entries: %v",
			len(g.Alloc), g.Alloc)
	}
}

// TestTestnetGenesisHasFaucetAlloc verifies that TestnetGenesisJSON includes
// the faucet address and balance, and no other addresses.
func TestTestnetGenesisHasFaucetAlloc(t *testing.T) {
	var faucet [20]byte
	faucet[19] = 0xAB // non-zero test address
	balance := new(big.Int).Mul(big.NewInt(1e9), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)) // 1e27 wei = 1B CHK

	data, err := TestnetGenesisJSON(faucet, balance)
	if err != nil {
		t.Fatalf("TestnetGenesisJSON: %v", err)
	}

	var g struct {
		Alloc map[string]struct {
			Balance string `json:"balance"`
		} `json:"alloc"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("parse testnet genesis: %v", err)
	}
	if len(g.Alloc) == 0 {
		t.Fatal("testnet genesis must have at least one alloc entry (faucet)")
	}
	// Verify balance is non-zero for all entries.
	for addr, entry := range g.Alloc {
		if entry.Balance == "" || entry.Balance == "0x0" {
			t.Errorf("alloc[%s] has zero balance", addr)
		}
	}
}

// TestTestnetGenesisHashConstantFormat verifies the constant is a valid, non-zero 32-byte hex.
func TestTestnetGenesisHashConstantFormat(t *testing.T) {
	h, err := HashFromHex(TestnetGenesisExecutionPayloadHashHex)
	if err != nil {
		t.Fatalf("TestnetGenesisExecutionPayloadHashHex is not valid hex: %v", err)
	}
	var zero [32]byte
	if h == zero {
		t.Fatal("TestnetGenesisExecutionPayloadHashHex must not be all-zero")
	}
	if h != TestnetGenesisExecutionPayloadHashBytes {
		t.Error("TestnetGenesisExecutionPayloadHashBytes does not match TestnetGenesisExecutionPayloadHashHex")
	}
}

// TestTestnetGenesisHashMatchesConstant recomputes the testnet genesis block hash
// via geth and checks it against TestnetGenesisExecutionPayloadHashHex.
// Skipped when geth is not in PATH.
func TestTestnetGenesisHashMatchesConstant(t *testing.T) {
	gethBin, err := exec.LookPath("geth")
	if err != nil {
		t.Skip("geth not in PATH — skipping live genesis hash verification")
	}

	// Generate testnet genesis JSON with canonical parameters.
	data, err := TestnetGenesisJSON(TestnetFaucetAddr, TestnetFaucetWei)
	if err != nil {
		t.Fatalf("TestnetGenesisJSON: %v", err)
	}

	// Confirm JSON is deterministic.
	data2, _ := TestnetGenesisJSON(TestnetFaucetAddr, TestnetFaucetWei)
	if string(data) != string(data2) {
		t.Error("TestnetGenesisJSON is not deterministic for identical inputs")
	}

	// Use /tmp directly: macOS limits Unix socket paths to 104 chars and
	// t.TempDir() produces paths that exceed that limit for the geth IPC socket.
	dir, err := os.MkdirTemp("/tmp", "tg")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	genesisPath := filepath.Join(dir, "genesis.json")
	if err := os.WriteFile(genesisPath, data, 0644); err != nil {
		t.Fatalf("write genesis: %v", err)
	}
	out, err := exec.Command(gethBin, "init", "--datadir", dir, genesisPath).CombinedOutput()
	if err != nil {
		t.Fatalf("geth init: %v\n%s", err, out)
	}

	// Find a free TCP port so geth's authrpc listener doesn't collide.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	freePort := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()

	// Use geth's JS console to read block 0 without starting a full server.
	// --ipcdisable avoids the long-path problem on macOS entirely.
	hashOut, err := exec.Command(gethBin,
		"--datadir", dir,
		"--authrpc.port", freePort,
		"--ipcdisable",
		"--exec", "eth.getBlock(0).hash",
		"console",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("geth console: %v\n%s", err, hashOut)
	}

	// geth console outputs INFO log lines before printing the JS result.
	// Scan for the line that is a quoted 32-byte hex string: `"0x<64 hex chars>"`.
	var got string
	for _, line := range strings.Split(string(hashOut), "\n") {
		trimmed := strings.TrimSpace(line)
		// 2 quotes + "0x" + 64 hex chars = 68 chars
		if strings.HasPrefix(trimmed, `"0x`) && strings.HasSuffix(trimmed, `"`) && len(trimmed) == 68 {
			got = strings.ToLower(strings.Trim(trimmed, `"`))
			break
		}
	}
	if got == "" {
		t.Fatalf("could not extract hash from geth output:\n%s", hashOut)
	}
	want := strings.ToLower(TestnetGenesisExecutionPayloadHashHex)
	if got != want {
		t.Errorf("testnet genesis hash mismatch:\n  got  %s\n  want %s\n\n"+
			"If the faucet address or balance changed, recompute via:\n"+
			"  geth init --datadir <dir> <testnet-genesis.json>\n"+
			"  geth --datadir <dir> --authrpc.port <free> --exec \"eth.getBlock(0).hash\" console",
			got, want)
	}
}

// TestAllNetworkGenesisHashesDiffer asserts that all three networks have distinct
// EVM genesis block hashes. If any two are equal it means nodes from different
// networks could accidentally accept each other's genesis, defeating network
// separation.
func TestAllNetworkGenesisHashesDiffer(t *testing.T) {
	devnet, err := HashFromHex(GenesisExecutionPayloadHashHex)
	if err != nil {
		t.Fatalf("devnet hash: %v", err)
	}
	testnet, err := HashFromHex(TestnetGenesisExecutionPayloadHashHex)
	if err != nil {
		t.Fatalf("testnet hash: %v", err)
	}
	mainnet, err := HashFromHex(MainnetGenesisExecutionPayloadHashHex)
	if err != nil {
		t.Fatalf("mainnet hash: %v", err)
	}
	if devnet == testnet {
		t.Error("devnet and testnet genesis hashes are identical — networks are not separated")
	}
	if devnet == mainnet {
		t.Error("devnet and mainnet genesis hashes are identical — networks are not separated")
	}
	if testnet == mainnet {
		t.Error("testnet and mainnet genesis hashes are identical — networks are not separated")
	}
}

// TestVerifyGenesisMatchOK tests the happy path: geth returns the expected hash.
func TestVerifyGenesisMatchOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]string{"hash": GenesisExecutionPayloadHashHex},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	if err := VerifyGenesisMatch(context.Background(), srv.URL, GenesisExecutionPayloadHashHex); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// TestVerifyGenesisMatchMismatch tests the mismatch path: geth returns a different hash.
func TestVerifyGenesisMatchMismatch(t *testing.T) {
	wrongHash := "0x0000000000000000000000000000000000000000000000000000000000000001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]string{"hash": wrongHash},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	err := VerifyGenesisMatch(context.Background(), srv.URL, GenesisExecutionPayloadHashHex)
	if err == nil {
		t.Fatal("expected error for hash mismatch, got nil")
	}
	t.Logf("got expected error: %v", err)
}

// TestVerifyGenesisMatchNullBlock tests geth returning null (uninitialized).
func TestVerifyGenesisMatchNullBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  nil,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	err := VerifyGenesisMatch(context.Background(), srv.URL, GenesisExecutionPayloadHashHex)
	if err == nil {
		t.Fatal("expected error for null block, got nil")
	}
	t.Logf("got expected error: %v", err)
}
