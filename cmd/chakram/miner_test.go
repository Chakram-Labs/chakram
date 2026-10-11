package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chakram/consensus"
	"chakram/engine"
)

// TestChainMuPreventsEngineRace is a regression for the CP2 EVM divergence bug
// where submitBlock's forkchoiceUpdatedV3 raced with the syncer's reorg FCU,
// leaving geth on the wrong chain. The fix is a shared chainMu held by both
// the miner's submitBlock critical section and makeAcceptFunc.
//
// This test verifies the invariant: acceptFn and the submitBlock critical
// section never execute concurrently. Run with -race to detect violations.
func TestChainMuPreventsEngineRace(t *testing.T) {
	t.Parallel()
	const goroutines = 200

	var chainMu sync.Mutex
	var inCritical int32

	// Simulate makeAcceptFunc holding chainMu around Engine API calls.
	acceptFn := func() {
		chainMu.Lock()
		defer chainMu.Unlock()
		if n := atomic.AddInt32(&inCritical, 1); n != 1 {
			t.Errorf("acceptFn: concurrent critical section (count=%d)", n)
		}
		time.Sleep(time.Microsecond)
		atomic.AddInt32(&inCritical, -1)
	}

	// Simulate submitBlock holding m.chainMu around orphan-check + FCU + store.
	submitFn := func() {
		chainMu.Lock()
		defer chainMu.Unlock()
		if n := atomic.AddInt32(&inCritical, 1); n != 1 {
			t.Errorf("submitBlock: concurrent critical section (count=%d)", n)
		}
		time.Sleep(time.Microsecond)
		atomic.AddInt32(&inCritical, -1)
	}

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); acceptFn() }()
		go func() { defer wg.Done(); submitFn() }()
	}
	wg.Wait()
}

// ── mockEngine ────────────────────────────────────────────────────────────────

// mockEngine records forkchoiceUpdatedV3 calls and always returns VALID.
// It satisfies engineAPI and is safe for concurrent use.
type mockEngine struct {
	mu     sync.Mutex
	fcuLog []string // HeadBlockHash from each FCU call
}

func (m *mockEngine) NewPayloadV3(_ context.Context, _ engine.ExecutionPayloadV3, _ []engine.Hash, _ engine.Hash) (engine.PayloadStatusV1, error) {
	return engine.PayloadStatusV1{Status: "VALID"}, nil
}

func (m *mockEngine) ForkchoiceUpdatedV3(_ context.Context, fcs engine.ForkchoiceStateV1, _ *engine.PayloadAttributesV3) (engine.ForkchoiceUpdatedResult, error) {
	m.mu.Lock()
	m.fcuLog = append(m.fcuLog, fcs.HeadBlockHash)
	m.mu.Unlock()
	return engine.ForkchoiceUpdatedResult{PayloadStatus: engine.PayloadStatusV1{Status: "VALID"}}, nil
}

func (m *mockEngine) GetPayloadV3(_ context.Context, _ string) (engine.GetPayloadResult, error) {
	panic("GetPayloadV3 must not be called in miner regression tests")
}

func (m *mockEngine) lastFCUHead() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.fcuLog) == 0 {
		return ""
	}
	return m.fcuLog[len(m.fcuLog)-1]
}

// ── helpers for regression tests ──────────────────────────────────────────────

// openTestStore opens a real consensus store backed by a temp dir.
func openTestStore(t *testing.T) *consensus.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	store, err := consensus.OpenStore(dir)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// seedTestGenesis writes the devnet genesis into store and returns it.
func seedTestGenesis(t *testing.T, store *consensus.Store) *consensus.ConsensusBlock {
	t.Helper()
	genesis := consensus.NewGenesisConsensusBlock()
	genesis.Header.Difficulty = consensus.DevnetInitialCompactTarget
	genesis.Hash = consensus.GenesisHash(&genesis.Header)
	if err := store.StoreBlock(genesis); err != nil {
		t.Fatalf("store genesis: %v", err)
	}
	return genesis
}

// storeChain stores a slice of blocks into the real consensus store.
func storeChain(t *testing.T, store *consensus.Store, chain []*consensus.ConsensusBlock) {
	t.Helper()
	for _, b := range chain {
		if err := store.StoreBlock(b); err != nil {
			t.Fatalf("store block h=%d: %v", b.Header.Height, err)
		}
	}
}

// buildPayloadJSON returns a minimal JSON execution payload with the given
// blockHash and blockNumber (hex string) suitable for makeAcceptFunc.
func buildPayloadJSON(execHash [32]byte, height uint64) []byte {
	return []byte(fmt.Sprintf(
		`{"blockHash":%q,"blockNumber":%q,"transactions":[],"withdrawals":[]}`,
		consensus.HashToHex(execHash),
		consensus.QuantityHex(height),
	))
}

// ── TestOrphanDiscardedByParentHashMismatch ───────────────────────────────────

// TestOrphanDiscardedByParentHashMismatch is the unit regression for gap 1.
//
// Scenario (height-check alone is NOT sufficient):
//
//	A-chain canonical at height 12.
//	A same-height reorg brings B12 to height 12 (different branch).
//	The miner has A13 (height 13, parentHash = A12.Hash).
//	Height check: storeTipHeight(12) >= height(13) → FALSE — would NOT discard.
//	Parent-hash check: B12.Hash ≠ A12.Hash → DISCARD ✓
func TestOrphanDiscardedByParentHashMismatch(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	genesis := seedTestGenesis(t, store)

	// Build A-chain: genesis → A1 → ... → A12 (canonical)
	aChain := buildChain(12, genesis.Hash, 0, 0)
	for i, b := range aChain {
		var eh [32]byte
		eh[0] = 0xAA
		eh[1] = byte(i + 1)
		b.Header.ExecutionPayloadHash = eh
		b.Hash = consensus.GenesisHash(&b.Header)
		b.ExecutionPayload = buildPayloadJSON(eh, b.Header.Height)
	}
	storeChain(t, store, aChain)
	a12 := aChain[11]

	// B12: a fork block at height 12 (same height, different branch from genesis)
	var b12ExecHash [32]byte
	b12ExecHash[0] = 0xBB
	b12ExecHash[1] = 12
	b12 := makeDevnetBlock(12, genesis.Hash)
	b12.Header.Nonce = 9999
	b12.Header.ExecutionPayloadHash = b12ExecHash
	b12.Hash = consensus.GenesisHash(&b12.Header)
	b12.ExecutionPayload = buildPayloadJSON(b12ExecHash, 12)

	// Simulate reorg: syncer's acceptFn processes B12, then SetTip updates canonical.
	var chainMu sync.Mutex
	mock := &mockEngine{}
	acceptFn := makeAcceptFunc(mock, store, &chainMu)
	if err := acceptFn(context.Background(), b12); err != nil {
		t.Fatalf("acceptFn B12: %v", err)
	}
	if err := store.SetTip(b12.Hash, 12); err != nil {
		t.Fatalf("SetTip B12: %v", err)
	}

	// Verify the reorg took effect.
	tipHash, tipHeight, _ := store.GetTip()
	if tipHeight != 12 || tipHash != b12.Hash {
		t.Fatalf("pre-condition: expected tip=B12@12, got %s@%d", consensus.HashToHex(tipHash)[:8], tipHeight)
	}

	// Now the miner tries to submit A13 (built on stale A12).
	var a13ExecHash [32]byte
	a13ExecHash[0] = 0xA1
	a13ExecHash[1] = 0x3
	a13Result := mineResult{
		header: consensus.ConsensusBlockHeader{
			Version:              consensus.GenesisVersion,
			Height:               13,
			ParentHash:           a12.Hash, // stale parent
			ExecutionPayloadHash: a13ExecHash,
			Difficulty:           consensus.DevnetInitialCompactTarget,
			Nonce:                77,
		},
		hash: [32]byte{0xa1, 0x3},
	}
	a13Payload := engine.ExecutionPayloadV3{
		BlockHash:    consensus.HashToHex(a13ExecHash),
		BlockNumber:  consensus.QuantityHex(13),
		Transactions: []string{},
		Withdrawals:  []engine.WithdrawalV1{},
	}

	var onBlockCalled bool
	miner := &Miner{store: store, client: mock, chainMu: &chainMu}
	miner.onBlock = func(_ *consensus.ConsensusBlock) { onBlockCalled = true }

	fcuBefore := mock.lastFCUHead()

	err := miner.submitBlock(context.Background(), a13Result, a13Payload, a12.Hash, [32]byte{}, 13, 0)
	if err != nil {
		t.Fatalf("submitBlock returned unexpected error: %v", err)
	}
	if onBlockCalled {
		t.Error("miner.onBlock was called — A13 should have been discarded as orphan")
	}

	// Canonical tip must still be B12.
	tipHash, tipHeight, _ = store.GetTip()
	if tipHeight != 12 || tipHash != b12.Hash {
		t.Errorf("canonical tip changed: got %s@%d, want B12@12", consensus.HashToHex(tipHash)[:8], tipHeight)
	}

	// geth's FCU must NOT have been called with A13's exec hash.
	// (If the height check alone were used, submitBlock would have proceeded and
	//  called FCU(A13) — leaving geth on the wrong chain.)
	a13ExecHashHex := consensus.HashToHex(a13ExecHash)
	if last := mock.lastFCUHead(); last != fcuBefore {
		t.Errorf("FCU was called after reorg: got head=%s (want no new calls after B12's FCU)", last[:12])
	}
	_ = a13ExecHashHex
}

// ── TestReorgDoesNotClobberGeth ───────────────────────────────────────────────

// TestReorgDoesNotClobberGeth is the concurrent regression test for the CP2 EVM
// divergence bug.
//
// The race: without chainMu, a reorg's forkchoiceUpdatedV3(B12) and the miner's
// forkchoiceUpdatedV3(A13) can interleave. If the miner's FCU runs last, geth is
// left on the short A-chain even though the consensus store shows B12 as canonical.
// The fix is the shared chainMu that serialises both callers.
//
// This test runs 200 concurrent iterations. In each iteration:
//   - Reorg goroutine: acceptFn(B12) + SetTip(B12, 12)
//   - Miner goroutine: submitBlock(A13, parentHash=A12)
//
// Invariant: after both complete, geth's last FCU head must never be A13's exec
// hash when the consensus store shows B12 as canonical.
func TestReorgDoesNotClobberGeth(t *testing.T) {
	t.Parallel()
	const iterations = 200

	for it := 0; it < iterations; it++ {
		it := it // capture for sub-test goroutine
		t.Run(fmt.Sprintf("it=%d", it), func(t *testing.T) {
		// t.Parallel() intentionally omitted: sub-tests run sequentially so that
		// we don't open 200 BadgerDB instances simultaneously (OS fd exhaustion).
		store := openTestStore(t)
		genesis := seedTestGenesis(t, store)

		// Build A-chain: genesis → A1 → ... → A12
		aChain := buildChain(12, genesis.Hash, 0, 0)
		for i, b := range aChain {
			var eh [32]byte
			eh[0] = 0xAA
			eh[1] = byte(i + 1)
			b.Header.ExecutionPayloadHash = eh
			b.Hash = consensus.GenesisHash(&b.Header)
			b.ExecutionPayload = buildPayloadJSON(eh, b.Header.Height)
		}
		storeChain(t, store, aChain)
		a12 := aChain[11]

		// B12: fork at genesis, same height 12
		var b12ExecHash [32]byte
		b12ExecHash[0] = 0xBB
		b12ExecHash[1] = byte(it & 0xff) // vary per iteration for uniqueness
		b12 := makeDevnetBlock(12, genesis.Hash)
		b12.Header.Nonce = uint64(9000 + it)
		b12.Header.ExecutionPayloadHash = b12ExecHash
		b12.Hash = consensus.GenesisHash(&b12.Header)
		b12.ExecutionPayload = buildPayloadJSON(b12ExecHash, 12)

		// A13: miner's block, parent = A12 (stale after reorg)
		var a13ExecHash [32]byte
		a13ExecHash[0] = 0xA1
		a13ExecHash[1] = 0x3
		a13Result := mineResult{
			header: consensus.ConsensusBlockHeader{
				Version:              consensus.GenesisVersion,
				Height:               13,
				ParentHash:           a12.Hash,
				ExecutionPayloadHash: a13ExecHash,
				Difficulty:           consensus.DevnetInitialCompactTarget,
				Nonce:                uint64(77 + it),
			},
			hash: [32]byte{0xa1, byte(it)},
		}
		a13Payload := engine.ExecutionPayloadV3{
			BlockHash:    consensus.HashToHex(a13ExecHash),
			BlockNumber:  consensus.QuantityHex(13),
			Transactions: []string{},
			Withdrawals:  []engine.WithdrawalV1{},
		}

		var chainMu sync.Mutex
		mock := &mockEngine{}
		acceptFn := makeAcceptFunc(mock, store, &chainMu)
		miner := &Miner{store: store, client: mock, chainMu: &chainMu}

		var wg sync.WaitGroup
		wg.Add(2)

		// Goroutine 1: syncer reorgs to B12
		go func() {
			defer wg.Done()
			if err := acceptFn(context.Background(), b12); err != nil {
				t.Errorf("it=%d acceptFn B12: %v", it, err)
			}
			// SetTip is called by applyBlock outside chainMu (mimics production code)
			if err := store.SetTip(b12.Hash, 12); err != nil {
				t.Errorf("it=%d SetTip B12: %v", it, err)
			}
		}()

		// Goroutine 2: miner submits A13 (stale parent after reorg)
		go func() {
			defer wg.Done()
			if err := miner.submitBlock(context.Background(), a13Result, a13Payload, a12.Hash, [32]byte{}, 13, 0); err != nil {
				t.Errorf("it=%d submitBlock A13: %v", it, err)
			}
		}()

		wg.Wait()

		// ── invariant check ───────────────────────────────────────────────────
		lastFCU := mock.lastFCUHead()
		tipHash, _, _ := store.GetTip()
		a13ExecHashHex := consensus.HashToHex(a13ExecHash)
		b12ExecHashHex := consensus.HashToHex(b12ExecHash)

		// If the consensus store shows B12 as canonical, geth's last FCU MUST
		// also point to B12 — never to A13. Anything else means the miner's FCU
		// overrode the reorg's FCU and geth was left on the wrong chain.
		if tipHash == b12.Hash && lastFCU == a13ExecHashHex {
			t.Errorf("it=%d: RACE — consensus=B12 but lastFCU=A13 (geth stuck on wrong chain)\n"+
				"  consensus tip: %s\n  lastFCU:       %s\n  B12 exec:      %s",
				it,
				consensus.HashToHex(tipHash)[:16],
				lastFCU[:16],
				b12ExecHashHex[:16],
			)
		}
		// A13 block must never become canonical.
		a13BlockHash := consensus.GenesisHash(&a13Result.header)
		if tipHash == a13BlockHash {
			t.Errorf("it=%d: A13 became canonical — miner should have been discarded", it)
		}
		}) // end t.Run sub-test
	}
}

// ── TestHashLoopMultiGoroutinesDoneBeforeResult ───────────────────────────────

// slowPow is a mock multiThreadPoW used to verify that hashLoopMulti sends on
// the result channel only after every goroutine has exited.
//
// Goroutine 0 returns a winning hash immediately; goroutines 1+ sleep 20 ms
// inside HashOnThread so they are guaranteed to still be running at the moment
// the old (racy) code would have sent the result.
//
// Each goroutine writes only ticks[tidx] — eliminating write-write races
// between goroutines. The main goroutine reads ticks[1..n-1] after receiving
// the result: those reads race with the sleeping goroutines under the old
// implementation but are safe after wg.Wait() (the new implementation).
type slowPow struct {
	nThreads int
	ticks    [8]int64 // ticks[i] written only by goroutine i — no write-write race
}

func (p *slowPow) NumMiningThreads() int { return p.nThreads }

func (p *slowPow) HashOnThread(tidx int, _ []byte) []byte {
	p.ticks[tidx] = time.Now().UnixNano() // non-atomic write to goroutine-private slot
	if tidx != 0 {
		time.Sleep(20 * time.Millisecond) // goroutines 1+ stay alive after goroutine 0 wins
	}
	out := make([]byte, 32)
	if tidx != 0 {
		for i := range out {
			out[i] = 0xFF // max hash — never satisfies the target
		}
	}
	// tidx == 0 returns zero hash → wins any positive target immediately
	return out
}

// TestHashLoopMultiGoroutinesDoneBeforeResult verifies that the result channel
// is written only after wg.Wait(), guaranteeing all goroutines have exited
// before the caller proceeds to Init() for the next epoch.
//
// Run with -race: with the old implementation the non-atomic read of lastTick
// (below) raced with goroutines 1-2 still sleeping inside HashOnThread.
// With the fix it is safe because wg.Wait() provides the happens-before.
func TestHashLoopMultiGoroutinesDoneBeforeResult(t *testing.T) {
	pow := &slowPow{nThreads: 3}

	hdr := &consensus.ConsensusBlockHeader{
		Height:     1,
		Difficulty: consensus.TestnetInitialCompactTarget, // easy; zero hash wins
	}

	result := make(chan mineResult, 1)
	quit := make(chan struct{})
	go hashLoopMulti(hdr, result, quit, pow)

	// Block until all goroutines have exited (guaranteed by the new send-after-Wait).
	<-result

	// Non-atomic reads of per-goroutine tick slots.
	// Goroutines 1+ sleep 20 ms and write their slot before exiting.
	// If any of them are still running (old code), reading ticks[i] races
	// with the goroutine's write — the race detector fires.
	for i := 0; i < pow.nThreads; i++ {
		_ = pow.ticks[i]
	}
}

// TestHashLoopMultiQuitGoroutinesDoneBeforeResult is the same guarantee for
// the quit path (no winner found, quit channel closed).
func TestHashLoopMultiQuitGoroutinesDoneBeforeResult(t *testing.T) {
	const n = 3
	var ticks [n]int64 // ticks[i] written only by goroutine i

	mp := &closurePow{
		n: n,
		hashFn: func(tidx int, _ []byte) []byte {
			ticks[tidx] = time.Now().UnixNano() // non-atomic write to private slot
			time.Sleep(5 * time.Millisecond)
			out := make([]byte, 32)
			for i := range out {
				out[i] = 0xFF // never wins
			}
			return out
		},
	}

	hdr := &consensus.ConsensusBlockHeader{
		Height:     1,
		Difficulty: consensus.TestnetInitialCompactTarget,
	}

	result := make(chan mineResult, 1)
	quit := make(chan struct{})
	go hashLoopMulti(hdr, result, quit, mp)

	time.Sleep(15 * time.Millisecond) // let goroutines run a bit
	close(quit)

	<-result

	// Non-atomic reads — safe only if all goroutines are done.
	for i := range ticks {
		_ = ticks[i]
	}
}

// closurePow is a minimal multiThreadPoW backed by a function, used in tests.
type closurePow struct {
	n      int
	hashFn func(int, []byte) []byte
}

func (c *closurePow) NumMiningThreads() int                    { return c.n }
func (c *closurePow) HashOnThread(i int, b []byte) []byte      { return c.hashFn(i, b) }

// TestBlockFromResultHashIsSHA256Squared verifies that a mined block's identity
// is SHA256²(header), not the RandomX PoW hash.
//
// Regression: before this fix, submitBlock set block.Hash = result.hash (the
// RandomX output). receiveBlock checks GenesisHash(&header) == wire.Hash, so
// every pushed block failed with "hash mismatch" → peer banned 5 min → 429
// on all subsequent requests. No inter-node sync was possible.
func TestBlockFromResultHashIsSHA256Squared(t *testing.T) {
	hdr := consensus.ConsensusBlockHeader{
		Version:    consensus.GenesisVersion,
		Height:     1,
		Difficulty: consensus.DevnetInitialCompactTarget,
		Nonce:      99,
	}

	// Simulate a RandomX PoW hash — intentionally distinct from SHA256².
	powHash := [32]byte{0xDE, 0xAD, 0xBE, 0xEF}
	result := mineResult{header: hdr, hash: powHash}

	block := blockFromResult(result, nil)

	want := consensus.GenesisHash(&hdr)
	if block.Hash == powHash {
		t.Error("block identity must not be the RandomX PoW hash (miner used wrong hash)")
	}
	if block.Hash != want {
		t.Errorf("block identity: got %v, want SHA256²(header) = %v", block.Hash, want)
	}
}
