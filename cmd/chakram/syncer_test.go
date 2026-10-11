package main

import (
	"context"
	"errors"
	"testing"

	"chakram/consensus"
)

// ── stub types ────────────────────────────────────────────────────────────────

// stubEngine accepts any hash as valid PoW (for syncer unit tests — no RandomX).
type stubEngine struct{}

func (stubEngine) Init(_ []byte) error                { return nil }
func (stubEngine) Hash(data []byte) []byte            { return make([]byte, 32) }
func (stubEngine) HashMeetsTarget(_ [32]byte, _ uint64) bool { return true }
func (stubEngine) Close()                             {}

// syncerStore implements the minimum store interface the syncer needs.
type syncerStore struct {
	blocks    map[[32]byte]*consensus.ConsensusBlock
	heights   map[uint64][32]byte
	tip       [32]byte
	tipHeight uint64
	payloads  map[[32]byte][]byte
}

func newSyncerStore() *syncerStore {
	return &syncerStore{
		blocks:   make(map[[32]byte]*consensus.ConsensusBlock),
		heights:  make(map[uint64][32]byte),
		payloads: make(map[[32]byte][]byte),
	}
}

func (s *syncerStore) GetTip() ([32]byte, uint64, error) {
	return s.tip, s.tipHeight, nil
}
func (s *syncerStore) GetBlock(h [32]byte) (*consensus.ConsensusBlock, error) {
	b, ok := s.blocks[h]
	if !ok {
		return nil, consensus.ErrBlockNotFound
	}
	return b, nil
}
func (s *syncerStore) GetBlockByHeight(h uint64) (*consensus.ConsensusBlock, error) {
	hash, ok := s.heights[h]
	if !ok {
		return nil, consensus.ErrBlockNotFound
	}
	return s.GetBlock(hash)
}
func (s *syncerStore) StoreBlock(b *consensus.ConsensusBlock) error {
	s.blocks[b.Hash] = b
	s.heights[b.Header.Height] = b.Hash
	s.tip = b.Hash
	s.tipHeight = b.Header.Height
	return nil
}
func (s *syncerStore) StoreBlockOnly(b *consensus.ConsensusBlock) error {
	s.blocks[b.Hash] = b
	return nil
}
func (s *syncerStore) SetTip(hash [32]byte, height uint64) error {
	s.tip = hash
	s.tipHeight = height
	s.heights[height] = hash
	return nil
}
func (s *syncerStore) StorePayload(hash [32]byte, payload []byte) error {
	s.payloads[hash] = payload
	return nil
}
func (s *syncerStore) GetPayload(hash [32]byte) ([]byte, error) {
	p, ok := s.payloads[hash]
	if !ok {
		return nil, consensus.ErrBlockNotFound
	}
	return p, nil
}
func (s *syncerStore) HeaderFetcher() consensus.BlockHeaderFetcher {
	return func(h uint64) (consensus.ConsensusBlockHeader, error) {
		b, err := s.GetBlockByHeight(h)
		if err != nil {
			return consensus.ConsensusBlockHeader{}, err
		}
		return b.Header, nil
	}
}

func addBlockToStore(s *syncerStore, b *consensus.ConsensusBlock) {
	s.blocks[b.Hash] = b
	s.heights[b.Header.Height] = b.Hash
	s.tip = b.Hash
	s.tipHeight = b.Header.Height
}

// stubEngine doesn't verify PoW, so we need HashMeetsTarget on it.
func (stubEngine) VerifyPoW(_ [32]byte, _ uint64) bool { return true }

// newPayloadCalls records calls to newPayloadV3.
type newPayloadRecorder struct {
	calls []string // blockHash of each call
}

// ── syncer interface ──────────────────────────────────────────────────────────

// These tests verify the syncer's Receive logic without needing a real geth.
// The syncer is given a stub that always accepts newPayloadV3.

// stubAcceptor always returns VALID for newPayloadV3.
type stubAcceptor struct {
	accepted []*consensus.ConsensusBlock
}

func (a *stubAcceptor) AcceptBlock(ctx context.Context, b *consensus.ConsensusBlock) error {
	a.accepted = append(a.accepted, b)
	return nil
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestSyncerReceiveExtends(t *testing.T) {
	store := newSyncerStore()
	genesis := makeDevnetBlock(0, [32]byte{})
	addBlockToStore(store, genesis)

	acceptor := &stubAcceptor{}
	syn := NewSyncer(store, acceptor.AcceptBlock, nil)

	b1 := makeDevnetBlock(1, genesis.Hash)
	b1.ExecutionPayload = []byte(`{"blockNumber":"0x1"}`)

	if err := syn.Receive(context.Background(), b1); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	tipHash, tipHeight, _ := store.GetTip()
	if tipHeight != 1 || tipHash != b1.Hash {
		t.Errorf("tip not advanced: height=%d hash=%s", tipHeight, consensus.HashToHex(tipHash))
	}
	if len(acceptor.accepted) != 1 {
		t.Errorf("acceptor called %d times, want 1", len(acceptor.accepted))
	}
}

func TestSyncerReceiveDuplicate(t *testing.T) {
	store := newSyncerStore()
	genesis := makeDevnetBlock(0, [32]byte{})
	addBlockToStore(store, genesis)
	b1 := makeDevnetBlock(1, genesis.Hash)
	addBlockToStore(store, b1)

	acceptor := &stubAcceptor{}
	syn := NewSyncer(store, acceptor.AcceptBlock, nil)

	// Send b1 again — should be a no-op (already known).
	b1.ExecutionPayload = []byte(`{"blockNumber":"0x1"}`)
	if err := syn.Receive(context.Background(), b1); err != nil {
		t.Fatalf("Receive duplicate: %v", err)
	}

	if len(acceptor.accepted) != 0 {
		t.Errorf("acceptor should not be called for a duplicate block")
	}
}

func TestSyncerReceiveOrphan(t *testing.T) {
	store := newSyncerStore()
	genesis := makeDevnetBlock(0, [32]byte{})
	addBlockToStore(store, genesis)

	acceptor := &stubAcceptor{}
	syn := NewSyncer(store, acceptor.AcceptBlock, nil)

	// Block whose parent is unknown — should be stashed but not advance tip.
	unknown := makeDevnetBlock(99, [32]byte{0xff})
	unknown.ExecutionPayload = []byte(`{"blockNumber":"0x63"}`)
	err := syn.Receive(context.Background(), unknown)
	if err != nil {
		t.Fatalf("Receive orphan: %v", err)
	}
	// Tip unchanged.
	_, tipHeight, _ := store.GetTip()
	if tipHeight != 0 {
		t.Errorf("tip advanced for orphan block, height=%d", tipHeight)
	}
	if len(acceptor.accepted) != 0 {
		t.Errorf("acceptor called for orphan block")
	}
}

func TestSyncerReceiveReorg(t *testing.T) {
	// Main chain: genesis → A1
	// Fork chain: genesis → B1 → B2  (longer — should win)
	store := newSyncerStore()
	genesis := makeDevnetBlock(0, [32]byte{})
	addBlockToStore(store, genesis)

	a1 := makeDevnetBlock(1, genesis.Hash)
	a1.Header.Nonce = 1
	a1.Hash = consensus.GenesisHash(&a1.Header)
	addBlockToStore(store, a1)

	acceptor := &stubAcceptor{}
	syn := NewSyncer(store, acceptor.AcceptBlock, nil)

	// Fork: B1 off genesis, B2 off B1 — longer chain.
	b1 := makeDevnetBlock(1, genesis.Hash)
	b1.Header.Nonce = 2
	b1.Hash = consensus.GenesisHash(&b1.Header)
	b1.ExecutionPayload = []byte(`{"blockNumber":"0x1","blockHash":"0xb1"}`)

	b2 := makeDevnetBlock(2, b1.Hash)
	b2.ExecutionPayload = []byte(`{"blockNumber":"0x2","blockHash":"0xb2"}`)

	// First receive B1 — can't extend since tip is already A1 at height 1 (same height, different hash).
	// B1 gets stashed.
	if err := syn.Receive(context.Background(), b1); err != nil {
		t.Fatalf("Receive b1: %v", err)
	}

	// Then receive B2 — B2 extends B1, height 2 > current tip height 1 → reorg.
	if err := syn.Receive(context.Background(), b2); err != nil {
		t.Fatalf("Receive b2: %v", err)
	}

	tipHash, tipHeight, _ := store.GetTip()
	if tipHeight != 2 {
		t.Errorf("tip height after reorg: got %d, want 2", tipHeight)
	}
	if tipHash != b2.Hash {
		t.Errorf("tip hash after reorg: got %s, want %s", consensus.HashToHex(tipHash), consensus.HashToHex(b2.Hash))
	}
}

func TestSyncerReceiveInvalidMissingPayload(t *testing.T) {
	store := newSyncerStore()
	genesis := makeDevnetBlock(0, [32]byte{})
	addBlockToStore(store, genesis)

	acceptor := &stubAcceptor{}
	syn := NewSyncer(store, acceptor.AcceptBlock, nil)

	// Block with no execution payload — should be rejected.
	b1 := makeDevnetBlock(1, genesis.Hash)
	b1.ExecutionPayload = nil

	err := syn.Receive(context.Background(), b1)
	if err == nil {
		t.Fatal("expected error for block with no execution payload")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func makeDevnetBlock(height uint64, parentHash [32]byte) *consensus.ConsensusBlock {
	hdr := consensus.ConsensusBlockHeader{
		Version:              consensus.GenesisVersion,
		Height:               height,
		ParentHash:           parentHash,
		ExecutionPayloadHash: [32]byte{byte(height)},
		Timestamp:            int64(1_000_000_000 + height*15),
		Difficulty:           consensus.DevnetInitialCompactTarget,
		Nonce:                0,
	}
	return &consensus.ConsensusBlock{
		Header: hdr,
		Hash:   consensus.GenesisHash(&hdr),
	}
}

// TestSyncerIBD verifies that SyncFrom fetches and applies blocks in order.
func TestSyncerIBD(t *testing.T) {
	// Build a "remote" store with 5 blocks.
	remote := newSyncerStore()
	var parentHash [32]byte
	remoteBlocks := make([]*consensus.ConsensusBlock, 5)
	for i := range remoteBlocks {
		b := makeDevnetBlock(uint64(i), parentHash)
		b.ExecutionPayload = []byte(`{"blockNumber":"0x0"}`)
		addBlockToStore(remote, b)
		remoteBlocks[i] = b
		parentHash = b.Hash
	}

	// Local store: only genesis.
	local := newSyncerStore()
	addBlockToStore(local, remoteBlocks[0])

	applied := make([]*consensus.ConsensusBlock, 0)
	syn := NewSyncer(local, func(_ context.Context, b *consensus.ConsensusBlock) error {
		applied = append(applied, b)
		addBlockToStore(local, b)
		return nil
	}, nil)

	// IBD: fetch blocks 1..4 using a stub fetcher backed by the remote store.
	fetcher := func(ctx context.Context, from, limit uint64) ([]*consensus.ConsensusBlock, error) {
		var out []*consensus.ConsensusBlock
		for h := from; h < from+limit && int(h) < len(remoteBlocks); h++ {
			out = append(out, remoteBlocks[h])
		}
		return out, nil
	}

	if err := syn.SyncFrom(context.Background(), 1, 4, fetcher); err != nil {
		t.Fatalf("SyncFrom: %v", err)
	}

	if len(applied) != 4 {
		t.Errorf("applied %d blocks, want 4", len(applied))
	}
	_, tipHeight, _ := local.GetTip()
	if tipHeight != 4 {
		t.Errorf("tip height after IBD: got %d, want 4", tipHeight)
	}
}

// buildChain creates n blocks chained from parentHash starting at startHeight.
// nonceBase offsets nonces so two chains from the same parent produce distinct hashes.
// Each block gets a dummy execution payload so it can pass through the syncer.
func buildChain(n int, parentHash [32]byte, startHeight, nonceBase uint64) []*consensus.ConsensusBlock {
	blocks := make([]*consensus.ConsensusBlock, n)
	cur := parentHash
	for i := 0; i < n; i++ {
		h := startHeight + uint64(i) + 1
		b := makeDevnetBlock(h, cur)
		b.Header.Nonce = nonceBase + uint64(i)
		b.Hash = consensus.GenesisHash(&b.Header)
		b.ExecutionPayload = []byte(`{"blockNumber":"0x0"}`)
		blocks[i] = b
		cur = b.Hash
	}
	return blocks
}

// TestSyncerReorgDepthCapRejected: reorg depth 101 must be rejected.
func TestSyncerReorgDepthCapRejected(t *testing.T) {
	store := newSyncerStore()

	genesis := makeDevnetBlock(0, [32]byte{})
	addBlockToStore(store, genesis)

	// Canonical chain: genesis → A1 … A101 (tip height = 101, nonceBase=0).
	canonical := buildChain(101, genesis.Hash, 0, 0)
	for _, b := range canonical {
		addBlockToStore(store, b)
	}

	// Fork chain diverges at genesis: genesis → B1 … B102 (height = 102, depth = 101, nonceBase=5000).
	fork := buildChain(102, genesis.Hash, 0, 5000)
	forkTip := fork[len(fork)-1]
	// Pre-store all fork blocks except the tip so reorg walk can find ancestors.
	// The tip is delivered via Receive so it doesn't hit the duplicate check.
	for _, b := range fork[:len(fork)-1] {
		store.StoreBlockOnly(b) //nolint:errcheck
		store.StorePayload(b.Hash, b.ExecutionPayload) //nolint:errcheck
	}

	acceptor := &stubAcceptor{}
	syn := NewSyncer(store, acceptor.AcceptBlock, nil)

	err := syn.Receive(context.Background(), forkTip)
	if err == nil {
		t.Fatal("expected reorg depth cap error, got nil")
	}
	if len(acceptor.accepted) != 0 {
		t.Errorf("acceptor called despite cap: %d calls", len(acceptor.accepted))
	}
}

// TestSyncerReorgDepthCapAccepted: reorg depth exactly 100 must succeed.
func TestSyncerReorgDepthCapAccepted(t *testing.T) {
	store := newSyncerStore()

	genesis := makeDevnetBlock(0, [32]byte{})
	addBlockToStore(store, genesis)

	// Canonical chain: genesis → A1 … A100 (tip height = 100, nonceBase=0).
	canonical := buildChain(100, genesis.Hash, 0, 0)
	for _, b := range canonical {
		addBlockToStore(store, b)
	}

	// Fork chain diverges at genesis: genesis → B1 … B101 (height = 101, depth = 100, nonceBase=5000).
	fork := buildChain(101, genesis.Hash, 0, 5000)
	forkTip := fork[len(fork)-1]
	for _, b := range fork[:len(fork)-1] {
		store.StoreBlockOnly(b) //nolint:errcheck
		store.StorePayload(b.Hash, b.ExecutionPayload) //nolint:errcheck
	}

	acceptor := &stubAcceptor{}
	syn := NewSyncer(store, acceptor.AcceptBlock, nil)

	err := syn.Receive(context.Background(), forkTip)
	if err != nil {
		t.Fatalf("unexpected error for reorg depth=100: %v", err)
	}
	_, tipHeight, _ := store.GetTip()
	if tipHeight != 101 {
		t.Errorf("tip height after reorg: got %d, want 101", tipHeight)
	}
}

// Ensure errors.Is works correctly for ErrBlockNotFound.
func TestErrBlockNotFoundSentinel(t *testing.T) {
	s := newSyncerStore()
	var missing [32]byte
	missing[0] = 0xde
	_, err := s.GetBlock(missing)
	if !errors.Is(err, consensus.ErrBlockNotFound) {
		t.Errorf("expected ErrBlockNotFound, got %v", err)
	}
}
