package consensus

import (
	"errors"
	"testing"
)

// makeTestBlock builds a synthetic ConsensusBlock for testing the store.
// hash is set to SHA256(SHA256(header)) for determinism (same as GenesisHash).
func makeTestBlock(height uint64, parentHash [32]byte) *ConsensusBlock {
	header := ConsensusBlockHeader{
		Version:              GenesisVersion,
		Height:               height,
		ParentHash:           parentHash,
		ExecutionPayloadHash: [32]byte{byte(height)},
		Timestamp:            int64(1_000_000_000 + height*15),
		Difficulty:           InitialCompactTargetV2,
	}
	hash := GenesisHash(&header)
	return &ConsensusBlock{Header: header, Hash: hash}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestErrTipNotFoundOnFreshDB(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.GetTip()
	if !errors.Is(err, ErrTipNotFound) {
		t.Errorf("got %v, want ErrTipNotFound", err)
	}
}

func TestStoreAndGetBlock(t *testing.T) {
	s := openTestStore(t)
	b := makeTestBlock(0, [32]byte{})
	if err := s.StoreBlock(b); err != nil {
		t.Fatalf("StoreBlock: %v", err)
	}

	got, err := s.GetBlock(b.Hash)
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	if got.Header.Height != b.Header.Height {
		t.Errorf("height mismatch: got %d, want %d", got.Header.Height, b.Header.Height)
	}
	if got.Hash != b.Hash {
		t.Errorf("hash mismatch: got %x, want %x", got.Hash, b.Hash)
	}
}

func TestGetBlockByHeight(t *testing.T) {
	s := openTestStore(t)
	b := makeTestBlock(5, [32]byte{})
	if err := s.StoreBlock(b); err != nil {
		t.Fatalf("StoreBlock: %v", err)
	}

	got, err := s.GetBlockByHeight(5)
	if err != nil {
		t.Fatalf("GetBlockByHeight(5): %v", err)
	}
	if got.Hash != b.Hash {
		t.Errorf("hash mismatch")
	}
}

func TestGetTipUpdatesOnEachStore(t *testing.T) {
	s := openTestStore(t)
	var parentHash [32]byte
	for h := uint64(0); h < 5; h++ {
		b := makeTestBlock(h, parentHash)
		if err := s.StoreBlock(b); err != nil {
			t.Fatalf("StoreBlock(h=%d): %v", h, err)
		}
		tipHash, tipHeight, err := s.GetTip()
		if err != nil {
			t.Fatalf("GetTip(h=%d): %v", h, err)
		}
		if tipHeight != h {
			t.Errorf("tip height after storing h=%d: got %d", h, tipHeight)
		}
		if tipHash != b.Hash {
			t.Errorf("tip hash mismatch at h=%d", h)
		}
		parentHash = b.Hash
	}
}

func TestHeaderFetcher(t *testing.T) {
	s := openTestStore(t)
	b := makeTestBlock(3, [32]byte{})
	if err := s.StoreBlock(b); err != nil {
		t.Fatalf("StoreBlock: %v", err)
	}
	fetch := s.HeaderFetcher()
	hdr, err := fetch(3)
	if err != nil {
		t.Fatalf("HeaderFetcher(3): %v", err)
	}
	if hdr.Height != 3 {
		t.Errorf("got height %d, want 3", hdr.Height)
	}
}

func TestBlockHashAt(t *testing.T) {
	s := openTestStore(t)
	b := makeTestBlock(7, [32]byte{})
	if err := s.StoreBlock(b); err != nil {
		t.Fatalf("StoreBlock: %v", err)
	}
	hashAt := s.BlockHashAt()
	got, err := hashAt(7)
	if err != nil {
		t.Fatalf("BlockHashAt(7): %v", err)
	}
	if got != b.Hash {
		t.Errorf("hash mismatch")
	}
}

// ── Phase 3 store extensions ──────────────────────────────────────────────────

func TestStoreBlockOnlyDoesNotUpdateTip(t *testing.T) {
	s := openTestStore(t)

	// Establish a tip via StoreBlock.
	genesis := makeTestBlock(0, [32]byte{})
	if err := s.StoreBlock(genesis); err != nil {
		t.Fatalf("StoreBlock genesis: %v", err)
	}

	// Store a fork block at the same height — should NOT move the tip.
	fork := makeTestBlock(1, genesis.Hash)
	fork.Header.Nonce = 999 // make hash differ
	fork.Hash = GenesisHash(&fork.Header)
	if err := s.StoreBlockOnly(fork); err != nil {
		t.Fatalf("StoreBlockOnly: %v", err)
	}

	tipHash, tipHeight, err := s.GetTip()
	if err != nil {
		t.Fatalf("GetTip: %v", err)
	}
	if tipHash != genesis.Hash || tipHeight != 0 {
		t.Errorf("tip moved unexpectedly: got height=%d hash=%s", tipHeight, HashToHex(tipHash))
	}

	// The fork block must still be retrievable by hash.
	got, err := s.GetBlock(fork.Hash)
	if err != nil {
		t.Fatalf("GetBlock fork: %v", err)
	}
	if got.Hash != fork.Hash {
		t.Errorf("fork hash mismatch")
	}
}

func TestSetTipAdvancesCanonicalChain(t *testing.T) {
	s := openTestStore(t)

	genesis := makeTestBlock(0, [32]byte{})
	if err := s.StoreBlock(genesis); err != nil {
		t.Fatalf("StoreBlock genesis: %v", err)
	}

	b1 := makeTestBlock(1, genesis.Hash)
	if err := s.StoreBlockOnly(b1); err != nil {
		t.Fatalf("StoreBlockOnly b1: %v", err)
	}

	// Tip still at genesis; explicitly advance to b1.
	if err := s.SetTip(b1.Hash, b1.Header.Height); err != nil {
		t.Fatalf("SetTip: %v", err)
	}

	tipHash, tipHeight, err := s.GetTip()
	if err != nil {
		t.Fatalf("GetTip: %v", err)
	}
	if tipHash != b1.Hash {
		t.Errorf("tip hash: got %s, want %s", HashToHex(tipHash), HashToHex(b1.Hash))
	}
	if tipHeight != 1 {
		t.Errorf("tip height: got %d, want 1", tipHeight)
	}

	// Height index must be updated so GetBlockByHeight works.
	got, err := s.GetBlockByHeight(1)
	if err != nil {
		t.Fatalf("GetBlockByHeight(1): %v", err)
	}
	if got.Hash != b1.Hash {
		t.Errorf("height index not updated")
	}
}

func TestStoreAndGetPayload(t *testing.T) {
	s := openTestStore(t)
	b := makeTestBlock(1, [32]byte{})
	payload := []byte(`{"blockNumber":"0x1","blockHash":"0xabc"}`)

	if err := s.StorePayload(b.Hash, payload); err != nil {
		t.Fatalf("StorePayload: %v", err)
	}

	got, err := s.GetPayload(b.Hash)
	if err != nil {
		t.Fatalf("GetPayload: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", got, payload)
	}
}

func TestGetPayloadNotFound(t *testing.T) {
	s := openTestStore(t)
	var missing [32]byte
	missing[0] = 0xff

	_, err := s.GetPayload(missing)
	if err == nil {
		t.Fatal("expected error for missing payload, got nil")
	}
}
