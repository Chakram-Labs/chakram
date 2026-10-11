package p2p_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"chakram/cmd/chakram/p2p"
	"chakram/consensus"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func makeBlock(height uint64, parentHash [32]byte) *consensus.ConsensusBlock {
	hdr := consensus.ConsensusBlockHeader{
		Version:              consensus.GenesisVersion,
		Height:               height,
		ParentHash:           parentHash,
		ExecutionPayloadHash: [32]byte{byte(height)},
		Timestamp:            int64(1_000_000_000 + height*15),
		Difficulty:           consensus.InitialCompactTargetV2,
	}
	return &consensus.ConsensusBlock{
		Header:           hdr,
		Hash:             consensus.GenesisHash(&hdr),
		ExecutionPayload: []byte(`{"blockNumber":"0x1"}`),
	}
}

// staticStore is a minimal BlockStore implementation for tests.
type staticStore struct {
	blocks  map[[32]byte]*consensus.ConsensusBlock
	heights map[uint64][32]byte
	tip     [32]byte
	tipH    uint64
}

func newStaticStore() *staticStore {
	return &staticStore{
		blocks:  make(map[[32]byte]*consensus.ConsensusBlock),
		heights: make(map[uint64][32]byte),
	}
}

func (s *staticStore) add(b *consensus.ConsensusBlock) {
	s.blocks[b.Hash] = b
	s.heights[b.Header.Height] = b.Hash
	s.tip = b.Hash
	s.tipH = b.Header.Height
}

func (s *staticStore) GetTip() ([32]byte, uint64, error) { return s.tip, s.tipH, nil }
func (s *staticStore) GetBlock(h [32]byte) (*consensus.ConsensusBlock, error) {
	b, ok := s.blocks[h]
	if !ok {
		return nil, consensus.ErrBlockNotFound
	}
	return b, nil
}
func (s *staticStore) GetBlockByHeight(h uint64) (*consensus.ConsensusBlock, error) {
	hash, ok := s.heights[h]
	if !ok {
		return nil, consensus.ErrBlockNotFound
	}
	return s.GetBlock(hash)
}
func (s *staticStore) GetPayload(h [32]byte) ([]byte, error) {
	b, ok := s.blocks[h]
	if !ok {
		return nil, consensus.ErrBlockNotFound
	}
	return b.ExecutionPayload, nil
}

// alwaysValidPoW accepts every block — use in tests that need a PoW validator
// but aren't testing PoW validity.
var alwaysValidPoW p2p.PoWValidator = func(_ *consensus.ConsensusBlockHeader) bool { return true }

// alwaysInvalidPoW rejects every block — use to simulate a bad PoW response.
var alwaysInvalidPoW p2p.PoWValidator = func(_ *consensus.ConsensusBlockHeader) bool { return false }

// ── server tests ──────────────────────────────────────────────────────────────

func TestServeStatus(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var st p2p.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Height != 0 {
		t.Errorf("height: got %d, want 0", st.Height)
	}
	if st.TipHash != consensus.HashToHex(b0.Hash) {
		t.Errorf("tipHash mismatch: got %s", st.TipHash)
	}
}

func TestServeSingleBlock(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	url := ts.URL + "/block?hash=" + consensus.HashToHex(b0.Hash)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /block: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var wire p2p.WireBlock
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wire.Height != b0.Header.Height {
		t.Errorf("height mismatch: got %d, want %d", wire.Height, b0.Header.Height)
	}
	if wire.Hash != consensus.HashToHex(b0.Hash) {
		t.Errorf("hash mismatch")
	}
}

func TestServeSingleBlock_NotFound(t *testing.T) {
	store := newStaticStore()
	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	var missing [32]byte
	missing[0] = 0xff
	url := ts.URL + "/block?hash=" + consensus.HashToHex(missing)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /block: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServeBlockRange(t *testing.T) {
	store := newStaticStore()
	var parentHash [32]byte
	blocks := make([]*consensus.ConsensusBlock, 5)
	for i := range blocks {
		b := makeBlock(uint64(i), parentHash)
		store.add(b)
		blocks[i] = b
		parentHash = b.Hash
	}

	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/blocks?from=1&limit=3")
	if err != nil {
		t.Fatalf("GET /blocks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var result []p2p.WireBlock
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("got %d blocks, want 3", len(result))
	}
	for i, wb := range result {
		want := blocks[i+1]
		if wb.Height != want.Header.Height {
			t.Errorf("[%d] height mismatch: got %d", i, wb.Height)
		}
	}
}

func TestReceiveBlock(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	received := make(chan *consensus.ConsensusBlock, 1)
	srv := p2p.NewServer(store,
		p2p.WithPoWValidator(alwaysValidPoW),
		p2p.WithReceiver(func(b *consensus.ConsensusBlock) error {
			received <- b
			return nil
		}),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	b1 := makeBlock(1, b0.Hash)
	wire := p2p.BlockToWire(b1)
	body, _ := json.Marshal(wire)
	resp, err := http.Post(ts.URL+"/block", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /block: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	select {
	case got := <-received:
		if got.Hash != b1.Hash {
			t.Errorf("received wrong block hash")
		}
	default:
		t.Error("receiver was not called")
	}
}

// ── client tests ──────────────────────────────────────────────────────────────

func TestClientFetchStatus(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cl := p2p.NewClient(ts.URL)
	st, err := cl.FetchStatus(context.Background())
	if err != nil {
		t.Fatalf("FetchStatus: %v", err)
	}
	if st.Height != 0 {
		t.Errorf("height: got %d, want 0", st.Height)
	}
}

func TestClientFetchBlock(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cl := p2p.NewClient(ts.URL)
	got, err := cl.FetchBlock(context.Background(), b0.Hash)
	if err != nil {
		t.Fatalf("FetchBlock: %v", err)
	}
	if got.Hash != b0.Hash {
		t.Errorf("hash mismatch")
	}
}

func TestClientPushBlock(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	received := make(chan *consensus.ConsensusBlock, 1)
	srv := p2p.NewServer(store,
		p2p.WithPoWValidator(alwaysValidPoW),
		p2p.WithReceiver(func(b *consensus.ConsensusBlock) error {
			received <- b
			return nil
		}),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cl := p2p.NewClient(ts.URL)
	b1 := makeBlock(1, b0.Hash)
	if err := cl.PushBlock(context.Background(), b1); err != nil {
		t.Fatalf("PushBlock: %v", err)
	}
	select {
	case got := <-received:
		if got.Hash != b1.Hash {
			t.Errorf("wrong block pushed")
		}
	default:
		t.Error("receiver not called")
	}
}

func TestClientFetchBlocks(t *testing.T) {
	store := newStaticStore()
	var parentHash [32]byte
	for i := 0; i < 5; i++ {
		b := makeBlock(uint64(i), parentHash)
		store.add(b)
		parentHash = b.Hash
	}

	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cl := p2p.NewClient(ts.URL)
	blocks, err := cl.FetchBlocks(context.Background(), 2, 3)
	if err != nil {
		t.Fatalf("FetchBlocks: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(blocks))
	}
	if blocks[0].Header.Height != 2 {
		t.Errorf("first block height: got %d, want 2", blocks[0].Header.Height)
	}
}

// ── hardening tests ───────────────────────────────────────────────────────────

func TestReceiveBlock_BodyTooLarge(t *testing.T) {
	store := newStaticStore()
	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// 3 MB body exceeds the 2 MB limit.
	body := bytes.Repeat([]byte("x"), 3<<20)
	resp, err := http.Post(ts.URL+"/block", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /block: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized body, got %d", resp.StatusCode)
	}
}

func TestReceiveBlock_HashMismatch(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store, p2p.WithReceiver(func(_ *consensus.ConsensusBlock) error {
		t.Fatal("receiver must not be called for hash mismatch")
		return nil
	}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	b1 := makeBlock(1, b0.Hash)
	wire := p2p.BlockToWire(b1)
	wire.Hash = "0x" + strings.Repeat("ff", 32) // tampered hash

	body, _ := json.Marshal(wire)
	resp, err := http.Post(ts.URL+"/block", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /block: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for hash mismatch, got %d", resp.StatusCode)
	}
}

func TestReceiveBlock_BadPoW(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store,
		p2p.WithPoWValidator(alwaysInvalidPoW),
		p2p.WithReceiver(func(_ *consensus.ConsensusBlock) error {
			t.Fatal("receiver must not be called for bad PoW")
			return nil
		}),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	b1 := makeBlock(1, b0.Hash)
	body, _ := json.Marshal(p2p.BlockToWire(b1))
	resp, err := http.Post(ts.URL+"/block", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /block: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad PoW, got %d", resp.StatusCode)
	}
}

// TestReceiveBlock_ValidHashWrongPoW verifies that a block with a correct
// SHA-256² block identifier but failing RandomX PoW is rejected (400).
// This is the critical test: the wire hash is NOT the PoW hash; the validator
// must recompute RandomX from the header, not trust b.Hash.
func TestReceiveBlock_ValidHashWrongPoW(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store,
		// Validator always returns false — simulates RandomX check failing.
		p2p.WithPoWValidator(alwaysInvalidPoW),
		p2p.WithReceiver(func(_ *consensus.ConsensusBlock) error {
			t.Fatal("receiver must not be called when PoW fails")
			return nil
		}),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// This block has a correct SHA-256² hash (passes Check 1) but the
	// configured validator rejects it (fails Check 2).
	b1 := makeBlock(1, b0.Hash)
	wire := p2p.BlockToWire(b1)
	// Confirm wire hash really does match header before sending.
	recomputed := consensus.GenesisHash(&b1.Header)
	if consensus.HashToHex(recomputed) != wire.Hash {
		t.Fatalf("test setup broken: wire hash != GenesisHash(header)")
	}
	body, _ := json.Marshal(wire)
	resp, err := http.Post(ts.URL+"/block", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /block: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 (PoW rejected), got %d", resp.StatusCode)
	}
}

// TestNoPoWValidator_RejectsAllBlocks verifies fail-closed behaviour: a server
// with no PoWValidator configured rejects every non-genesis block with 503.
// Genesis (height=0) is exempt because it requires no PoW.
func TestNoPoWValidator_RejectsAllBlocks(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	// No WithPoWValidator — server is intentionally unconfigured.
	srv := p2p.NewServer(store,
		p2p.WithReceiver(func(_ *consensus.ConsensusBlock) error {
			t.Fatal("receiver must not be called without a PoW validator")
			return nil
		}),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	b1 := makeBlock(1, b0.Hash)
	body, _ := json.Marshal(p2p.BlockToWire(b1))
	resp, err := http.Post(ts.URL+"/block", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /block: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (no PoW validator configured), got %d", resp.StatusCode)
	}
}

func TestRateLimit_BlockPush(t *testing.T) {
	store := newStaticStore()
	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// 6 rapid POST /block requests; the 6th must be rate-limited (limit = 5/s).
	body := []byte(`{}`)
	var gotRateLimited bool
	for i := 0; i < 6; i++ {
		resp, err := http.Post(ts.URL+"/block", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST /block [%d]: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			gotRateLimited = true
			break
		}
	}
	if !gotRateLimited {
		t.Error("expected 429 after 5 block pushes, never received one")
	}
}

func TestBanAfterBadPoW(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store, p2p.WithPoWValidator(alwaysInvalidPoW))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	b1 := makeBlock(1, b0.Hash)
	body, _ := json.Marshal(p2p.BlockToWire(b1))
	resp, err := http.Post(ts.URL+"/block", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("first POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on bad PoW, got %d", resp.StatusCode)
	}

	// IP is now banned; next request (even GET /status) must be rejected.
	resp2, err := http.Get(ts.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status after ban: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429 for banned IP, got %d", resp2.StatusCode)
	}
}

// ── network identity tests ────────────────────────────────────────────────────

// TestStatusIncludesNetworkIdentity verifies that GET /status returns the chain
// ID and genesis hash configured via WithChainID and WithGenesisHash.
func TestStatusIncludesNetworkIdentity(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	const wantChainID = 11641
	const wantGenesis = "0xdeadbeef00000000000000000000000000000000000000000000000000000001"

	srv := p2p.NewServer(store,
		p2p.WithChainID(wantChainID),
		p2p.WithGenesisHash(wantGenesis),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cl := p2p.NewClient(ts.URL)
	st, err := cl.FetchStatus(context.Background())
	if err != nil {
		t.Fatalf("FetchStatus: %v", err)
	}
	if st.ChainID != wantChainID {
		t.Errorf("chainId: got %d, want %d", st.ChainID, wantChainID)
	}
	if st.GenesisHash != wantGenesis {
		t.Errorf("genesisHash: got %s, want %s", st.GenesisHash, wantGenesis)
	}
}

// TestStatusZeroWhenNotConfigured verifies that a server without
// WithChainID/WithGenesisHash returns zero values for those fields (not an
// error), so old servers remain compatible with new clients.
func TestStatusZeroWhenNotConfigured(t *testing.T) {
	store := newStaticStore()
	b0 := makeBlock(0, [32]byte{})
	store.add(b0)

	srv := p2p.NewServer(store) // no chain identity options
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cl := p2p.NewClient(ts.URL)
	st, err := cl.FetchStatus(context.Background())
	if err != nil {
		t.Fatalf("FetchStatus: %v", err)
	}
	if st.ChainID != 0 {
		t.Errorf("expected chainId=0 when not configured, got %d", st.ChainID)
	}
	if st.GenesisHash != "" {
		t.Errorf("expected empty genesisHash when not configured, got %s", st.GenesisHash)
	}
}

// ── block locator tests ───────────────────────────────────────────────────────

func TestLocatorEndpoint_CommonAncestorFound(t *testing.T) {
	store := newStaticStore()
	var parentHash [32]byte
	blocks := make([]*consensus.ConsensusBlock, 10)
	for i := range blocks {
		b := makeBlock(uint64(i), parentHash)
		store.add(b)
		blocks[i] = b
		parentHash = b.Hash
	}

	srv := p2p.NewServer(store)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cl := p2p.NewClient(ts.URL)

	// Build a locator pointing at height 5 as tip (common with server up to 5).
	hashAt := func(h uint64) ([32]byte, error) {
		if int(h) >= len(blocks) {
			return [32]byte{}, consensus.ErrBlockNotFound
		}
		return blocks[h].Hash, nil
	}
	locator := p2p.BuildLocator(5, hashAt)

	resp, err := cl.FindCommonAncestor(context.Background(), locator)
	if err != nil {
		t.Fatalf("FindCommonAncestor: %v", err)
	}
	if resp.CommonHeight != 5 {
		t.Errorf("common height: got %d, want 5", resp.CommonHeight)
	}
}

func TestLocatorEndpoint_DivergentFork(t *testing.T) {
	// Server has blocks 0–9 on main chain.
	serverStore := newStaticStore()
	var serverParent [32]byte
	serverBlocks := make([]*consensus.ConsensusBlock, 10)
	for i := range serverBlocks {
		b := makeBlock(uint64(i), serverParent)
		serverStore.add(b)
		serverBlocks[i] = b
		serverParent = b.Hash
	}

	srv := p2p.NewServer(serverStore)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cl := p2p.NewClient(ts.URL)

	// Client has genesis in common but then diverges.
	// Send locator with: heights 5,4,3,2,1,0 using client's own hashes.
	// Heights 5–1 will differ; only genesis (height 0) will match.
	clientBlocks := make([]*consensus.ConsensusBlock, 6)
	var clientParent [32]byte
	clientParent = serverBlocks[0].Hash // same genesis
	clientBlocks[0] = serverBlocks[0]   // share genesis
	for i := 1; i < 6; i++ {
		// Add a distinguishing nonce so hashes differ from the server's chain.
		hdr := consensus.ConsensusBlockHeader{
			Version:              consensus.GenesisVersion,
			Height:               uint64(i),
			ParentHash:           clientParent,
			ExecutionPayloadHash: [32]byte{byte(i + 100)}, // different from server
			Timestamp:            int64(1_000_000_000 + uint64(i)*15),
			Difficulty:           consensus.InitialCompactTargetV2,
		}
		b := &consensus.ConsensusBlock{Header: hdr, Hash: consensus.GenesisHash(&hdr)}
		clientBlocks[i] = b
		clientParent = b.Hash
	}

	hashAt := func(h uint64) ([32]byte, error) {
		if int(h) >= len(clientBlocks) {
			return [32]byte{}, consensus.ErrBlockNotFound
		}
		return clientBlocks[h].Hash, nil
	}
	locator := p2p.BuildLocator(5, hashAt)

	resp, err := cl.FindCommonAncestor(context.Background(), locator)
	if err != nil {
		t.Fatalf("FindCommonAncestor: %v", err)
	}
	if resp.CommonHeight != 0 {
		t.Errorf("expected common ancestor at genesis (0), got %d", resp.CommonHeight)
	}
}

func TestBuildLocator_Shape(t *testing.T) {
	// 20-block chain: locator should contain heights 20,19,...,11 (10 consecutive)
	// then 9 (11-2), 5 (9-4), 0.
	heights := make(map[uint64][32]byte)
	for h := uint64(0); h <= 20; h++ {
		heights[h] = [32]byte{byte(h)}
	}
	hashAt := func(h uint64) ([32]byte, error) {
		hash, ok := heights[h]
		if !ok {
			return [32]byte{}, consensus.ErrBlockNotFound
		}
		return hash, nil
	}

	loc := p2p.BuildLocator(20, hashAt)
	if len(loc.Entries) == 0 {
		t.Fatal("locator is empty")
	}
	// First entry must be tip.
	if loc.Entries[0].Height != 20 {
		t.Errorf("first entry: got %d, want 20", loc.Entries[0].Height)
	}
	// Last entry must be 0 (genesis).
	if loc.Entries[len(loc.Entries)-1].Height != 0 {
		t.Errorf("last entry: got %d, want 0", loc.Entries[len(loc.Entries)-1].Height)
	}
	// Heights must be strictly decreasing.
	for i := 1; i < len(loc.Entries); i++ {
		if loc.Entries[i].Height >= loc.Entries[i-1].Height {
			t.Errorf("entry[%d].Height=%d >= entry[%d].Height=%d (not decreasing)",
				i, loc.Entries[i].Height, i-1, loc.Entries[i-1].Height)
		}
	}
}

// ── fuzz tests ────────────────────────────────────────────────────────────────

// FuzzDecodeWireBlock fuzzes the JSON→WireBlock→ConsensusBlock decode path.
func FuzzDecodeWireBlock(f *testing.F) {
	seed, _ := json.Marshal(p2p.BlockToWire(makeBlock(1, [32]byte{})))
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Add([]byte(`{"height":18446744073709551615}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var wire p2p.WireBlock
		if err := json.Unmarshal(data, &wire); err != nil {
			return // invalid JSON is expected; only crashes matter
		}
		_, _ = p2p.WireToBlock(wire) // must not panic
	})
}

// FuzzReceiveBlock fuzzes the full POST /block handler path.
func FuzzReceiveBlock(f *testing.F) {
	seed, _ := json.Marshal(p2p.BlockToWire(makeBlock(1, [32]byte{})))
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		// New server per iteration so rate limits don't accumulate.
		store := newStaticStore()
		store.add(makeBlock(0, [32]byte{}))
		srv := p2p.NewServer(store)
		req := httptest.NewRequest(http.MethodPost, "/block", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		_ = w.Result().StatusCode // any status is fine; must not panic
	})
}

// FuzzHandleBlocksParams fuzzes the from/limit query parameter parsing.
func FuzzHandleBlocksParams(f *testing.F) {
	f.Add("1", "10")
	f.Add("0", "500")
	f.Add("", "")
	f.Add("18446744073709551615", "18446744073709551615")
	f.Add("-1", "abc")

	store := newStaticStore()
	store.add(makeBlock(0, [32]byte{}))
	srv := p2p.NewServer(store)

	f.Fuzz(func(t *testing.T, fromStr, limitStr string) {
		u := "/blocks?from=" + url.QueryEscape(fromStr) + "&limit=" + url.QueryEscape(limitStr)
		req := httptest.NewRequest(http.MethodGet, u, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		_ = w.Result().StatusCode // must not panic
	})
}
