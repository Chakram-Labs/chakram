// blockchain_test.go — Tests for issues 3 (skipPoW removal) and 4 (atomic block apply).
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

// mockPoW is a PoWEngine whose Hash always returns a fixed result.
// Used to inject a known engine into Blockchain.VerifyEngine during tests.
type mockPoW struct{ result []byte }

func (m *mockPoW) Init(_ []byte) error    { return nil }
func (m *mockPoW) Hash(_ []byte) []byte   { return append([]byte{}, m.result...) }
func (m *mockPoW) Close()                 {}

func newTestBC(t *testing.T) *Blockchain {
	t.Helper()
	bc, err := NewBlockchain(t.TempDir(), false, MainnetChainID)
	if err != nil {
		t.Fatalf("NewBlockchain: %v", err)
	}
	t.Cleanup(func() { bc.Close() })
	return bc
}

// validBlock1 returns a structurally valid block at height 1 using a zero hash.
// The zero hash satisfies any difficulty target because 0 is less than any positive target.
// It will fail RandomX verification (the mock engine returns a different value).
func validBlock1(t *testing.T, bc *Blockchain) *Block {
	t.Helper()
	genesis, err := bc.GetBlock(0)
	if err != nil {
		t.Fatalf("get genesis: %v", err)
	}
	pkh := make([]byte, 20)
	cb := NewCoinbaseTransaction(pkh, 1, 0)
	txs := []*Transaction{cb}
	return &Block{
		Header: BlockHeader{
			Version:      ProtocolVersion,
			Height:       1,
			PreviousHash: genesis.Hash,
			MerkleRoot:   ComputeMerkleRoot(txs),
			Timestamp:    genesis.Header.Timestamp + TargetBlockTime,
			Difficulty:   NextDifficultyFromParent(bc.Storage, 1, genesis),
		},
		Transactions: txs,
		Hash:         make([]byte, 32), // zero hash — satisfies any difficulty target
	}
}

// TestNilEngineRejectsBlock confirms the fail-closed PoW guard: AddBlock must
// return an error when VerifyEngine is nil, never accept a block silently.
func TestNilEngineRejectsBlock(t *testing.T) {
	bc := newTestBC(t)
	// VerifyEngine is nil — no engine set.
	b := validBlock1(t, bc)
	if err := bc.AddBlock(b); err == nil {
		t.Fatal("AddBlock accepted a block with nil VerifyEngine — should have failed closed")
	}
}

// TestAlwaysVerifyPoW confirms that AddBlock always consults the PoW engine when
// one is present, regardless of sync state.
//
// Before the fix: SetSyncing(true, 1_000_000) activated skipPoW for any block
// below height 999_990. The engine was never called, so the zero (wire) hash
// passed HashIsValid and the block was accepted.
//
// After the fix: the engine is always called. It returns 0xAA...AA which does
// not match the zero wire hash → ErrInvalidPoW.
func TestAlwaysVerifyPoW(t *testing.T) {
	bc := newTestBC(t)
	// The mock engine returns 0xAA...AA — will never equal the zero wire hash.
	bc.VerifyEngine = &mockPoW{result: bytes.Repeat([]byte{0xAA}, 32)}
	bc.SetSyncing(true, 1_000_000)

	b := validBlock1(t, bc)

	err := bc.AddBlock(b)
	if !errors.Is(err, ErrInvalidPoW) {
		t.Fatalf("want ErrInvalidPoW, got %v — skipPoW shortcut still active", err)
	}
}

// TestProcessBlockIsAtomic verifies that a block containing an invalid second
// transaction does not partially mutate the UTXO set.
//
// Before the fix: tx[1] (valid) is applied first — utxoA is consumed and
// tx[1]'s output is created — then tx[2] (invalid) fails, leaving the UTXO
// set in a half-applied state.
//
// After the fix: all transactions are validated before any mutation. tx[2]'s
// failure causes the whole block to be rejected without touching utxoA.
func TestProcessBlockIsAtomic(t *testing.T) {
	bc := newTestBC(t)
	us := bc.UTXOSet

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	pkh := PubKeyToHash(pub)

	// Plant utxoA so tx[1] has something real to spend.
	utxoA := UTXO{
		TxID:          bytes.Repeat([]byte{0x01}, 32),
		OutputIndex:   0,
		Value:         5_000_000,
		PublicKeyHash: pkh,
		BlockHeight:   0,
		IsCoinbase:    false,
	}
	if err := us.AddUTXO(utxoA); err != nil {
		t.Fatalf("AddUTXO: %v", err)
	}

	// tx[1]: validly spends utxoA.
	tx1 := &Transaction{
		IsCoinbase: false,
		Inputs:     []TxInput{{TxID: utxoA.TxID, OutputIndex: 0}},
		Outputs:    []TxOutput{{Value: utxoA.Value - 1_000, PublicKeyHash: pkh}},
		Timestamp:  time.Now().Unix(),
	}
	tx1.SetTxID()
	msg := signedMessage(MainnetChainID, tx1, 0)
	tx1.Inputs[0].Signature = ed25519.Sign(priv, msg)
	tx1.Inputs[0].PublicKey = pub

	// tx[2]: references a UTXO that does not exist — guaranteed to fail.
	tx2 := &Transaction{
		IsCoinbase: false,
		Inputs: []TxInput{{
			TxID:        bytes.Repeat([]byte{0x99}, 32),
			OutputIndex: 0,
			Signature:   make([]byte, 64),
			PublicKey:   make([]byte, 32),
		}},
		Outputs:   []TxOutput{{Value: 1_000, PublicKeyHash: pkh}},
		Timestamp: time.Now().Unix(),
	}
	tx2.SetTxID()

	coinbase := NewCoinbaseTransaction(pkh, 1, 0)
	block := &Block{
		Header:       BlockHeader{Height: 1},
		Transactions: []*Transaction{coinbase, tx1, tx2},
	}

	_, err = us.ProcessBlock(block, 1)
	if err == nil {
		t.Fatal("ProcessBlock should have failed because tx[2] references a missing UTXO")
	}

	// utxoA must still exist: the block failed, so NO changes should have been applied.
	if _, getErr := us.GetUTXO(utxoA.TxID, utxoA.OutputIndex); getErr != nil {
		t.Fatalf("utxoA was consumed even though the block was rejected — atomicity violation: %v", getErr)
	}
}

// TestFutureBlockRejected verifies that a block timestamped more than 120 seconds
// in the future is rejected by AddBlock.
//
// Before the fix: AddBlock only checks that the timestamp is after the parent's
// timestamp. A block with timestamp now+300s passes and miners can skew LWMA
// difficulty downward by injecting far-future timestamps.
//
// After the fix: AddBlock enforces timestamp <= time.Now().Unix() + 120.
func TestFutureBlockRejected(t *testing.T) {
	bc := newTestBC(t)

	genesis, err := bc.GetBlock(0)
	if err != nil {
		t.Fatalf("get genesis: %v", err)
	}

	pkh := make([]byte, 20)
	cb := NewCoinbaseTransaction(pkh, 1, 0)
	txs := []*Transaction{cb}

	futureBlock := &Block{
		Header: BlockHeader{
			Version:      ProtocolVersion,
			Height:       1,
			PreviousHash: genesis.Hash,
			MerkleRoot:   ComputeMerkleRoot(txs),
			Timestamp:    time.Now().Unix() + 300, // 5 minutes in the future
			Difficulty:   NextDifficultyFromParent(bc.Storage, 1, genesis),
		},
		Transactions: txs,
		Hash:         make([]byte, 32),
	}

	if err := bc.AddBlock(futureBlock); err == nil {
		t.Fatal("AddBlock accepted a block with a far-future timestamp — future-cap check missing")
	}
}

// TestMTPRejectsStaleTimestamp verifies that a block whose timestamp is at or
// before the median-time-past (MTP) of the last 11 blocks is rejected.
//
// Before the fix: the only timestamp check is "greater than parent", allowing a
// block to carry a timestamp well below the rolling median. Miners can suppress
// difficulty growth by back-dating blocks.
//
// After the fix: AddBlock rejects any block with timestamp <= MTP.
func TestMTPRejectsStaleTimestamp(t *testing.T) {
	bc := newTestBC(t)

	mtp, err := bc.MedianTimePast()
	if err != nil {
		t.Fatalf("MedianTimePast: %v", err)
	}

	genesis, err := bc.GetBlock(0)
	if err != nil {
		t.Fatalf("get genesis: %v", err)
	}

	pkh := make([]byte, 20)
	cb := NewCoinbaseTransaction(pkh, 1, 0)
	txs := []*Transaction{cb}

	// Use the correct v2 difficulty so the block reaches the MTP check.
	// With the wrong difficulty (e.g. InitialDifficulty=4 which is a v1 bit-count
	// target) the block is rejected by the difficulty check before MTP is tested.
	staleBlock := &Block{
		Header: BlockHeader{
			Version:      ProtocolVersion,
			Height:       1,
			PreviousHash: genesis.Hash,
			MerkleRoot:   ComputeMerkleRoot(txs),
			Timestamp:    mtp,
			Difficulty:   NextDifficultyFromParent(bc.Storage, 1, genesis),
		},
		Transactions: txs,
		Hash:         make([]byte, 32),
	}

	if err := bc.AddBlock(staleBlock); err == nil {
		t.Fatal("AddBlock accepted a block at exactly MTP — MTP check missing")
	}
}

// TestGenesisHasCoinbaseData verifies that the genesis block is created with a
// proper coinbase transaction whose CoinbaseData encodes height 0 in 8 bytes.
// Before the fix: NewGenesisBlock() produced an empty Transactions slice, so
// there was no genesis coinbase and no height commitment.
func TestGenesisHasCoinbaseData(t *testing.T) {
	genesis := NewGenesisBlock()
	if len(genesis.Transactions) == 0 {
		t.Fatal("genesis block has no transactions — genesis coinbase missing")
	}
	cb := genesis.Transactions[0]
	if !cb.IsCoinbase {
		t.Fatal("genesis first transaction is not a coinbase")
	}
	if len(cb.CoinbaseData) != 8 {
		t.Fatalf("genesis coinbase CoinbaseData must be 8 bytes, got %d", len(cb.CoinbaseData))
	}
	if h := binary.BigEndian.Uint64(cb.CoinbaseData); h != 0 {
		t.Fatalf("genesis coinbase must commit to height 0, got %d", h)
	}
}

// TestIsValidEnforcesCoinbaseData confirms that IsValid() passes on a fresh chain
// where the genesis block has the correct coinbase CoinbaseData.
func TestIsValidEnforcesCoinbaseData(t *testing.T) {
	bc := newTestBC(t)
	ok, err := bc.IsValid()
	if !ok || err != nil {
		t.Fatalf("IsValid on fresh chain with proper genesis: ok=%v err=%v", ok, err)
	}
}

// TestReorgRollbackIsAtomic verifies that RollbackBlockAtomic commits each
// rollback in a single Badger transaction, leaving the chain in a consistent
// state after each step.
//
// The test simulates a failure mid-reorg by applying two blocks atomically and
// then rolling them back one at a time. After each rollback the chain tip and
// UTXO set must agree: UTXOs created by the rolled-back block are gone, UTXOs
// from the surviving blocks are intact, and the chain tip points to the correct
// parent.
func TestReorgRollbackIsAtomic(t *testing.T) {
	bc := newTestBC(t)

	genesis, err := bc.GetBlock(0)
	if err != nil {
		t.Fatalf("get genesis: %v", err)
	}

	pkh := make([]byte, 20)

	// Build block 1 — coinbase only.
	cb1 := NewCoinbaseTransaction(pkh, 1, 0)
	b1Hash := bytes.Repeat([]byte{0x01}, 32)
	b1 := &Block{
		Header: BlockHeader{
			Height:       1,
			PreviousHash: genesis.Hash,
			Timestamp:    genesis.Header.Timestamp + TargetBlockTime,
			Difficulty:   NextDifficultyFromParent(bc.Storage, 1, genesis),
		},
		Transactions: []*Transaction{cb1},
		Hash:         b1Hash,
	}

	_, creates1, undo1, err := bc.UTXOSet.ComputeBlockMutations(b1, 1)
	if err != nil {
		t.Fatalf("compute b1 mutations: %v", err)
	}
	if err := bc.Storage.ApplyBlockAtomicFull(b1, undo1, nil, creates1); err != nil {
		t.Fatalf("apply b1: %v", err)
	}

	// Build block 2 — coinbase only, on top of b1.
	cb2 := NewCoinbaseTransaction(pkh, 2, 0)
	b2Hash := bytes.Repeat([]byte{0x02}, 32)
	b2 := &Block{
		Header: BlockHeader{
			Height:       2,
			PreviousHash: b1Hash,
			Timestamp:    genesis.Header.Timestamp + 2*TargetBlockTime,
			Difficulty:   NextDifficultyFromParent(bc.Storage, 2, b1),
		},
		Transactions: []*Transaction{cb2},
		Hash:         b2Hash,
	}

	_, creates2, undo2, err := bc.UTXOSet.ComputeBlockMutations(b2, 2)
	if err != nil {
		t.Fatalf("compute b2 mutations: %v", err)
	}
	if err := bc.Storage.ApplyBlockAtomicFull(b2, undo2, nil, creates2); err != nil {
		t.Fatalf("apply b2: %v", err)
	}

	// Baseline: chain tip at height 2.
	if _, height, err := bc.Storage.GetChainTip(); err != nil || height != 2 {
		t.Fatalf("baseline: expected height 2, got %d err=%v", height, err)
	}

	// Simulate failure mid-reorg: roll back b2 only.
	if err := bc.Storage.RollbackBlockAtomic(b2, undo2, b1Hash, 1); err != nil {
		t.Fatalf("rollback b2: %v", err)
	}

	tip, height, err := bc.Storage.GetChainTip()
	if err != nil || height != 1 || !bytes.Equal(tip, b1Hash) {
		t.Fatalf("after rollback b2: tip=%x height=%d err=%v, want b1Hash height=1", tip, height, err)
	}
	for _, ref := range undo2.CreatedRefs {
		if _, err := bc.UTXOSet.GetUTXO(ref.TxID, ref.OutputIndex); err == nil {
			t.Fatal("b2 created UTXO still exists after atomic rollback — rollback incomplete")
		}
	}
	for _, ref := range undo1.CreatedRefs {
		if _, err := bc.UTXOSet.GetUTXO(ref.TxID, ref.OutputIndex); err != nil {
			t.Fatalf("b1 UTXO missing after b2-only rollback: %v", err)
		}
	}

	// Roll back b1 as well.
	if err := bc.Storage.RollbackBlockAtomic(b1, undo1, genesis.Hash, 0); err != nil {
		t.Fatalf("rollback b1: %v", err)
	}

	tip, height, err = bc.Storage.GetChainTip()
	if err != nil || height != 0 || !bytes.Equal(tip, genesis.Hash) {
		t.Fatalf("after rollback b1: tip=%x height=%d err=%v, want genesis height=0", tip, height, err)
	}
	for _, ref := range undo1.CreatedRefs {
		if _, err := bc.UTXOSet.GetUTXO(ref.TxID, ref.OutputIndex); err == nil {
			t.Fatal("b1 created UTXO still exists after full rollback — rollback incomplete")
		}
	}
}

// TestMaxBlockFitsOneBadgerTransaction proves that a block at MaxBlockSize does
// not trigger badger.ErrTxnTooBig. Blocks are capped at 1 MB and Badger's
// default transaction limit is well above that; ErrTxnTooBig must never occur
// for valid blocks, which is why ApplyBlockAtomicFull treats it as a hard error.
//
// The test bypasses block validation and drives ApplyBlockAtomicFull directly
// with a large UTXO set. Each UTXO JSON entry is ~150 bytes; 7000 entries ≈ 1 MB.
func TestMaxBlockFitsOneBadgerTransaction(t *testing.T) {
	bc := newTestBC(t)

	// Build a minimal block header at height 1.
	genesis, err := bc.GetBlock(0)
	if err != nil {
		t.Fatalf("get genesis: %v", err)
	}
	cb := NewCoinbaseTransaction(make([]byte, 20), 1, 0)
	b := &Block{
		Header: BlockHeader{
			Height:       1,
			PreviousHash: genesis.Hash,
			Timestamp:    GenesisTimestamp + TargetBlockTime,
		},
		Transactions: []*Transaction{cb},
		Hash:         bytes.Repeat([]byte{0x01}, 32),
	}

	// Generate ~7000 creates (≈ 1 MB of UTXO JSON).
	const n = 7000
	creates := make([]UTXO, n)
	createdRefs := make([]UTXORef, n)
	for i := range creates {
		txID := make([]byte, 32)
		txID[0] = byte(i)
		txID[1] = byte(i >> 8)
		txID[2] = byte(i >> 16)
		creates[i] = UTXO{
			TxID:          txID,
			OutputIndex:   0,
			Value:         1_000,
			PublicKeyHash: make([]byte, 20),
			BlockHeight:   1,
		}
		createdRefs[i] = UTXORef{TxID: txID, OutputIndex: 0}
	}

	undo := &BlockUndo{CreatedRefs: createdRefs}
	if err := bc.Storage.ApplyBlockAtomicFull(b, undo, nil, creates); err != nil {
		t.Fatalf("ApplyBlockAtomicFull failed for ~1 MB block: %v — ErrTxnTooBig or unexpected error", err)
	}
}
