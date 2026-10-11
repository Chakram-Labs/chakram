// blockchain.go — The Blockchain struct that manages chain state and validation.
// All persistence goes through Storage; this file contains no direct file I/O.
// Every other component in Chakram interacts with the chain through this file.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"
)

// ErrOrphanBlock is returned by AddBlock when the block's parent is not yet
// known to this node. The sync layer uses this to request the missing parent.
var ErrOrphanBlock = errors.New("orphan: parent block not found")

// ErrInvalidPoW is returned by AddBlock when the RandomX hash recomputed from
// the block header does not match the hash the block claims to have.
var ErrInvalidPoW = errors.New("invalid block: RandomX PoW hash mismatch")

// Blockchain holds the in-memory chain state and a handle to the storage layer.
type Blockchain struct {
	Storage      *Storage
	UTXOSet      *UTXOSet
	VerifyEngine PoWEngine    // PoW engine used to authenticate received blocks (mockable in tests)
	isSyncing    bool          // true during IBD
	syncTarget   uint64        // best peer's height when syncing started
	chainMu      sync.Mutex    // serializes all chain state mutations (cs_main equivalent)
	syncMu       sync.RWMutex  // guards isSyncing and syncTarget
	stateMu      sync.RWMutex  // guards Tip and Height — RPC reads never block on mining writes
	Tip          []byte         // hash of the current best (highest) block
	Height       uint64         // height of the current best block
}

// NewBlockchain opens (or resumes) the blockchain stored at dataDir.
// On a fresh database it creates and persists the genesis block automatically.
// On an existing database it loads the chain tip and height from storage.
// Pass createVerifyEngine=true for nodes that verify incoming blocks (seed
// nodes). Mining nodes pass false and call SetVerifyEngine to share the
// mining engine, eliminating the second Argon2d stall at epoch boundaries.
func NewBlockchain(dataDir string, createVerifyEngine bool, chainID []byte) (*Blockchain, error) {
	storage, err := NewStorage(dataDir)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	var engine PoWEngine // use interface type — avoids nil-interface-wrapping-nil-pointer
	if createVerifyEngine {
		engine = NewRandomXEngine()
	}
	bc := &Blockchain{
		Storage:      storage,
		UTXOSet:      NewUTXOSet(storage, chainID),
		VerifyEngine: engine,
	}

	tip, height, err := storage.GetChainTip()
	if err == nil {
		bc.Tip = tip
		bc.Height = height
		// Recovery: if applyBlock ran UpdateHeightIndex but not SaveChainTip in
		// a previous session, the height index is ahead of the stored chain tip.
		// Scan forward and advance the tip to match.
		for {
			next, nextErr := storage.GetBlockByHeight(bc.Height + 1)
			if nextErr != nil {
				break
			}
			if !bytes.Equal(next.Header.PreviousHash, bc.Tip) {
				break // inconsistent entry in height index — stop
			}
			if saveErr := storage.SaveChainTip(next.Hash, next.Header.Height); saveErr != nil {
				break
			}
			bc.Tip = next.Hash
			bc.Height = next.Header.Height
			fmt.Printf("[RECOVERY] Chain tip restored to h=%d\n", bc.Height)
		}
		return bc, nil
	}

	// Fresh database — bootstrap with the genesis block.
	genesis := NewGenesisBlock()
	if err := storage.SaveBlock(genesis); err != nil {
		storage.Close()
		return nil, fmt.Errorf("save genesis block: %w", err)
	}
	if err := storage.SaveChainTip(genesis.Hash, genesis.Header.Height); err != nil {
		storage.Close()
		return nil, fmt.Errorf("save genesis tip: %w", err)
	}
	bc.Tip = genesis.Hash
	bc.Height = genesis.Header.Height
	return bc, nil
}

// Close cleanly shuts down the storage layer.
// Always defer this after a successful NewBlockchain call.
func (bc *Blockchain) Close() error {
	return bc.Storage.Close()
}

// SetVerifyEngine wires a PoWEngine into the blockchain for block verification.
// Mining nodes call this to share their engine so only one Argon2d initialisation
// occurs per epoch. Tests pass a mockPoW to control the hash result directly.
func (bc *Blockchain) SetVerifyEngine(engine PoWEngine) {
	bc.VerifyEngine = engine
}

// SetSyncing marks the blockchain as in IBD mode.
// peerHeight is the best peer's known chain height (used to keep the last 10
// blocks fully PoW-verified even during IBD). Pass 0 when syncing = false.
func (bc *Blockchain) SetSyncing(syncing bool, peerHeight uint64) {
	bc.syncMu.Lock()
	bc.isSyncing = syncing
	bc.syncTarget = peerHeight
	bc.syncMu.Unlock()
}

// IsSyncing reports whether the node is currently in IBD mode.
func (bc *Blockchain) IsSyncing() bool {
	bc.syncMu.RLock()
	defer bc.syncMu.RUnlock()
	return bc.isSyncing
}

// IsBlockOnMainChain returns true only when hash is both stored AND indexed at
// the correct height on the canonical chain. A block whose raw data was saved
// but whose applyBlock never completed (limbo block) returns false, allowing
// handleInv to re-request it so AddBlock can retry the apply.
func (bc *Blockchain) IsBlockOnMainChain(hash []byte) bool {
	b, err := bc.Storage.GetBlockByHash(hash)
	if err != nil {
		return false
	}
	inChain, err := bc.Storage.GetBlockByHeight(b.Header.Height)
	if err != nil {
		return false
	}
	return bytes.Equal(inChain.Hash, hash)
}

// epochKey returns the RandomX seed for the epoch that contains height.
// Derived purely from the epoch number so every node computes the same key
// for the same height regardless of which blocks are in local storage.
// This eliminates IBD false-positive PoW failures caused by storage fallbacks.
func (bc *Blockchain) epochKey(height uint64) []byte {
	epochNum := height / RandomXEpochLen
	return []byte(fmt.Sprintf("chakram-epoch-%d", epochNum))
}

// blockWork returns the expected number of hashes for one block.
// v1: work = 2^difficulty  (bit-count difficulty).
// v2: work = 2^256 / target  (compact target — lower target = more work).
// Summing across all blocks gives cumulative chainwork for fork selection.
func blockWork(height uint64, difficulty uint64) *big.Int {
	if ProtocolVersionAt(height) >= 2 {
		target := decodeCompact(difficulty)
		if target.Sign() == 0 {
			return big.NewInt(0)
		}
		max256 := new(big.Int).Lsh(big.NewInt(1), 256)
		return new(big.Int).Div(max256, target)
	}
	return new(big.Int).Lsh(big.NewInt(1), uint(difficulty))
}

// chainWork sums proof-of-work for every block from genesis through b.
// Called only during fork evaluation (rare), so O(height) is acceptable.
// Returns an error if any ancestor is missing — incomplete work must never
// be compared against a complete chain, as it would bias fork selection.
func (bc *Blockchain) chainWork(b *Block) (*big.Int, error) {
	total := blockWork(b.Header.Height, b.Header.Difficulty)
	cur := b
	for cur.Header.Height > 0 {
		parent, err := bc.Storage.GetBlockByHash(cur.Header.PreviousHash)
		if err != nil {
			return nil, fmt.Errorf("chainwork: missing ancestor %x at height %d: %w",
				cur.Header.PreviousHash, cur.Header.Height-1, err)
		}
		total.Add(total, blockWork(parent.Header.Height, parent.Header.Difficulty))
		cur = parent
	}
	return total, nil
}

// medianTimePast computes the median timestamp of up to the last 11 blocks
// ending at the given height. Called by AddBlock with the parent's height so
// that the same chain state is read consistently under chainMu.
func (bc *Blockchain) medianTimePast(height uint64) (int64, error) {
	count := uint64(11)
	if height+1 < count {
		count = height + 1
	}
	timestamps := make([]int64, 0, count)
	for i := uint64(0); i < count; i++ {
		b, err := bc.Storage.GetBlockByHeight(height - i)
		if err != nil {
			return 0, fmt.Errorf("medianTimePast: get block at height %d: %w", height-i, err)
		}
		timestamps = append(timestamps, b.Header.Timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	return timestamps[len(timestamps)/2], nil
}

// MedianTimePast returns the median timestamp of the last 11 blocks (or fewer
// if the chain is shorter). This is the minimum timestamp that a new block must
// strictly exceed, preventing miners from back-dating blocks to suppress difficulty.
func (bc *Blockchain) MedianTimePast() (int64, error) {
	bc.stateMu.RLock()
	height := bc.Height
	bc.stateMu.RUnlock()
	return bc.medianTimePast(height)
}

// AddBlock validates b and integrates it into the chain.
//
// Accepted cases:
//   1. Direct extension of the current tip → apply to main chain immediately.
//   2. Competing chain with more cumulative chainwork → trigger a reorg.
//   3. Less-work side chain → store by hash for future reference, no tip change.
//
// Returns ErrOrphanBlock when the parent is not yet known; the sync layer uses
// this signal to request the missing ancestor from the peer.
func (bc *Blockchain) AddBlock(b *Block) error {
	bc.chainMu.Lock()
	defer bc.chainMu.Unlock()

	// Genesis blocks are immutable.
	if b.Header.Height == 0 {
		return errors.New("cannot replace genesis block")
	}

	// Deduplicate — only skip if the block is already on the main chain.
	// A block whose raw data was saved but whose applyBlock never completed
	// (limbo from a previous crash) is NOT on the main chain; fall through so
	// applyBlock can finish the work.
	alreadyStored := false
	if bc.Storage.HasBlock(b.Hash) {
		if inChain, chkErr := bc.Storage.GetBlockByHeight(b.Header.Height); chkErr == nil && bytes.Equal(inChain.Hash, b.Hash) {
			return nil // already fully committed to the main chain
		}
		alreadyStored = true
	}

	// Limbo-block fast path: data is in DB but applyBlock never ran.
	// Skip all validation (already checked in the previous session) and attempt
	// to apply directly. Only handles the direct-extension case for safety.
	if alreadyStored {
		for _, tx := range b.Transactions {
			tx.TxID = tx.ComputeTxID()
		}
		bc.stateMu.RLock()
		limbTip := bc.Tip
		bc.stateMu.RUnlock()
		if bytes.Equal(b.Header.PreviousHash, limbTip) {
			return bc.applyBlock(b)
		}
		return nil // not a direct extension of our tip — skip for now
	}

	// Locate parent; an unknown parent means this is an orphan.
	parent, err := bc.Storage.GetBlockByHash(b.Header.PreviousHash)
	if err != nil {
		return fmt.Errorf("%w (hash %x)", ErrOrphanBlock, b.Header.PreviousHash)
	}

	// Structural validity.
	if b.Header.Height != parent.Header.Height+1 {
		return fmt.Errorf("invalid block: expected height %d, got %d",
			parent.Header.Height+1, b.Header.Height)
	}
	if expectedDiff := NextDifficultyFromParent(bc.Storage, b.Header.Height, parent); b.Header.Difficulty != expectedDiff {
		return fmt.Errorf("invalid block: difficulty %d does not match expected %d at height %d",
			b.Header.Difficulty, expectedDiff, b.Header.Height)
	}
	if !b.HashIsValid() {
		return errors.New("invalid block: hash does not meet difficulty target")
	}

	// Checkpoint: a block at a checkpointed height must exactly match the
	// known-good hash committed to by this binary. Rejects any fork that
	// diverges from the canonical history before the checkpoint.
	if expected, ok := Checkpoints[b.Header.Height]; ok {
		if fmt.Sprintf("%x", b.Hash) != expected {
			return fmt.Errorf("block %d rejected: hash conflicts with checkpoint (got %x, want %s)",
				b.Header.Height, b.Hash, expected)
		}
	}
	// Timestamp rules:
	// 1. Future cap: block must not be more than 120 seconds ahead of wall-clock.
	// 2. MTP (median-time-past): block timestamp must strictly exceed the median
	//    of the last 11 blocks. This prevents miners from back-dating blocks to
	//    suppress difficulty growth via LWMA, and stops out-of-order timestamps
	//    from skewing the algorithm.
	if b.Header.Timestamp > time.Now().Unix()+120 {
		return fmt.Errorf("invalid block: timestamp %d is more than 120s in the future",
			b.Header.Timestamp)
	}
	mtp, err := bc.medianTimePast(parent.Header.Height)
	if err != nil {
		return fmt.Errorf("compute MTP: %w", err)
	}
	if b.Header.Timestamp <= mtp {
		return fmt.Errorf("invalid block: timestamp %d must be greater than median-time-past %d",
			b.Header.Timestamp, mtp)
	}

	// Validate coinbase structure and IsCoinbase field consistency.
	// A peer can forge any JSON field; we must derive IsCoinbase from structure
	// (no inputs = coinbase) rather than trusting the wire value. A spoofed
	// IsCoinbase would bypass input validation and allow coin duplication.
	if len(b.Transactions) == 0 {
		return errors.New("invalid block: no transactions")
	}
	for i, tx := range b.Transactions {
		expectedCoinbase := len(tx.Inputs) == 0
		if tx.IsCoinbase != expectedCoinbase {
			return fmt.Errorf("invalid block: tx %d IsCoinbase=%v but has %d inputs (must be consistent)",
				i, tx.IsCoinbase, len(tx.Inputs))
		}
		if tx.IsCoinbase && i != 0 {
			return errors.New("invalid block: coinbase must be the first transaction")
		}
	}
	if !b.Transactions[0].IsCoinbase {
		return errors.New("invalid block: first transaction must be coinbase")
	}

	// Coinbase height commitment: CoinbaseData must be exactly 8 bytes encoding
	// the block height in big-endian. This ensures every coinbase has a unique
	// TxID even when miner address, reward, and timestamp are all identical.
	cb := b.Transactions[0]
	if len(cb.CoinbaseData) != 8 {
		return fmt.Errorf("invalid block: coinbase CoinbaseData must be 8 bytes, got %d", len(cb.CoinbaseData))
	}
	if committedHeight := binary.BigEndian.Uint64(cb.CoinbaseData); committedHeight != b.Header.Height {
		return fmt.Errorf("invalid block: coinbase commits to height %d but block is at height %d",
			committedHeight, b.Header.Height)
	}

	// Re-derive every TxID from canonical content so stored IDs are always
	// authoritative, regardless of what arrived on the wire.
	for _, tx := range b.Transactions {
		tx.TxID = tx.ComputeTxID()
	}

	// Merkle root validation (uses re-derived TxIDs).
	if expectedMerkle := ComputeMerkleRoot(b.Transactions); !bytes.Equal(b.Header.MerkleRoot, expectedMerkle) {
		return errors.New("invalid block: merkle root mismatch")
	}

	// Enforce maximum block size.
	{
		data, err := json.Marshal(blockToJSON(b))
		if err != nil {
			return fmt.Errorf("marshal block for size check: %w", err)
		}
		if uint64(len(data)) > MaxBlockSize {
			return fmt.Errorf("invalid block: serialized size %d bytes exceeds MaxBlockSize %d",
				len(data), MaxBlockSize)
		}
	}

	// Bootstrap time floor: during the first DifficultyWindow blocks the protocol
	// enforces a minimum gap of TargetBlockTime (30s) between blocks. After
	// bootstrap a permanent PostBootstrapMinGap (45s) floor remains in force
	// forever. This prevents any miner from flooding the network with fast blocks
	// that would cause LWMA to overshoot difficulty regardless of window size.
	if b.Header.Height <= DifficultyWindow {
		minTS := parent.Header.Timestamp + TargetBlockTime
		if b.Header.Timestamp < minTS {
			return fmt.Errorf("invalid block: bootstrap time floor violated at h=%d (ts=%d, need>=%d)",
				b.Header.Height, b.Header.Timestamp, minTS)
		}
		fmt.Printf("[BOOTSTRAP] h=%d gap=%ds ✓\n", b.Header.Height, b.Header.Timestamp-parent.Header.Timestamp)
	} else {
		gap := PostBootstrapMinGap
		if ProtocolVersionAt(b.Header.Height) >= 2 {
			gap = PostBootstrapMinGapV2
		}
		minTS := parent.Header.Timestamp + gap
		if b.Header.Timestamp < minTS {
			return fmt.Errorf("invalid block: minimum block gap violated at h=%d (ts=%d, need>=%d)",
				b.Header.Height, b.Header.Timestamp, minTS)
		}
	}

	// RandomX PoW verification — fail closed: nil engine rejects the block.
	// Tests inject a mockPoW via SetVerifyEngine; production always provides
	// the real RandomXEngine. A nil engine is a hard reject, not a silent skip.
	if bc.VerifyEngine == nil {
		return errors.New("invalid block: PoW verification engine not initialised; call SetVerifyEngine before AddBlock")
	}
	key := bc.epochKey(b.Header.Height)
	// Pre-initialise the engine OUTSIDE chainMu so the 2 GB Argon2d
	// dataset init (minutes) never freezes the entire chain.
	// Engine.Init is idempotent — a no-op when the epoch key is unchanged.
	bc.chainMu.Unlock()
	initErr := bc.VerifyEngine.Init(key)
	bc.chainMu.Lock()
	if initErr != nil {
		return fmt.Errorf("randomx init: %w", initErr)
	}
	// Re-check dedup: a concurrent AddBlock may have stored this block
	// while we were outside chainMu initialising the engine.
	if bc.Storage.HasBlock(b.Hash) {
		return nil
	}
	if err := VerifyBlock(b, bc.VerifyEngine, key); err != nil {
		if errors.Is(err, ErrInvalidPoW) {
			return fmt.Errorf("%w (height %d hash %x)", ErrInvalidPoW, b.Header.Height, b.Hash)
		}
		fmt.Printf("[CHAIN] VerifyBlock engine error at h=%d: %v\n", b.Header.Height, err)
		return err
	}

	// Store the block data (by hash only — height index updated separately).
	if err := bc.Storage.SaveBlockData(b); err != nil {
		return fmt.Errorf("save block data: %w", err)
	}

	bc.stateMu.RLock()
	tip := bc.Tip
	height := bc.Height
	bc.stateMu.RUnlock()

	switch {
	case bytes.Equal(b.Header.PreviousHash, tip):
		// Fast path: direct extension of the main chain.
		return bc.applyBlock(b)

	case b.Header.Height >= height:
		// Candidate chain: same height or taller. Compare cumulative chainwork.
		// Strictly greater work always wins (Bitcoin rule).
		// Equal work: prefer the block whose hash is lexicographically lower —
		// both nodes see the same two hashes and make the identical decision,
		// so the tie resolves deterministically without ping-pong reorgs.
		currentTip, err := bc.Storage.GetBlockByHash(tip)
		if err != nil {
			return fmt.Errorf("fetch current tip for chainwork comparison: %w", err)
		}
		newWork, err := bc.chainWork(b)
		if err != nil {
			return fmt.Errorf("chainwork for candidate: %w", err)
		}
		curWork, err := bc.chainWork(currentTip)
		if err != nil {
			return fmt.Errorf("chainwork for current tip: %w", err)
		}
		cmp := newWork.Cmp(curWork)
		if cmp > 0 {
			return bc.reorganize(b)
		}
		if cmp == 0 && bytes.Compare(b.Hash[:], currentTip.Hash[:]) < 0 {
			return bc.reorganize(b)
		}
		return nil

	default:
		// Shorter, less-work side chain — stored for completeness, no tip change.
		return nil
	}
}

// applyBlock extends the main chain by one block: validates UTXOs, then commits
// all changes (UTXO mutations, undo data, height index, tx indexes, chain tip)
// in a single atomic Badger transaction via ApplyBlockAtomicFull.
func (bc *Blockchain) applyBlock(b *Block) error {
	spentUTXOs, creates, undo, err := bc.UTXOSet.ComputeBlockMutations(b, b.Header.Height)
	if err != nil {
		return fmt.Errorf("validate utxo set: %w", err)
	}
	if err := bc.Storage.ApplyBlockAtomicFull(b, undo, spentUTXOs, creates); err != nil {
		return fmt.Errorf("atomic block commit: %w", err)
	}
	bc.stateMu.Lock()
	bc.Tip = b.Hash
	bc.Height = b.Header.Height
	bc.stateMu.Unlock()
	return nil
}

// reorganize switches the main chain from the current tip to newTip.
//
// Each block applied or rolled back during the reorg is committed in a single
// Badger transaction via RollbackBlockAtomic / applyBlock. A crash at any point
// leaves the chain consistent — the tip always points to a fully committed block.
func (bc *Blockchain) reorganize(newTip *Block) error {
	oldTip, err := bc.Storage.GetBlockByHash(bc.GetTip())
	if err != nil {
		return fmt.Errorf("fetch current tip: %w", err)
	}

	ancestor, rollback, apply, err := bc.findReorgPath(oldTip, newTip)
	if err != nil {
		return fmt.Errorf("find reorg path: %w", err)
	}

	if cp := highestCheckpoint(); cp > 0 && ancestor.Header.Height < cp {
		return fmt.Errorf("reorganize rejected: common ancestor at height %d is below checkpoint at height %d",
			ancestor.Header.Height, cp)
	}

	fmt.Printf("⛓  Reorg: common ancestor at height %d, rolling back %d, applying %d\n",
		ancestor.Header.Height, len(rollback), len(apply))

	// Roll back old chain from tip to ancestor+1 — one atomic Badger txn per block.
	// Each commit updates the chain tip to the parent, so a crash mid-rollback
	// leaves the chain consistent at the last committed state.
	for _, b := range rollback {
		undo, err := bc.Storage.GetBlockUndo(b.Hash)
		if err != nil {
			return fmt.Errorf("get undo for %x: %w", b.Hash, err)
		}
		if err := bc.Storage.RollbackBlockAtomic(b, undo, b.Header.PreviousHash, b.Header.Height-1); err != nil {
			return fmt.Errorf("rollback block %x: %w", b.Hash, err)
		}
	}
	bc.stateMu.Lock()
	bc.Tip = ancestor.Hash
	bc.Height = ancestor.Header.Height
	bc.stateMu.Unlock()

	// Apply new chain from ancestor+1 to newTip — one atomic Badger txn per block.
	// applyBlock updates chain tip atomically, so a crash mid-apply leaves the
	// chain consistent at the last successfully applied new-chain block.
	for _, b := range apply {
		if err := bc.applyBlock(b); err != nil {
			return fmt.Errorf("reorg apply block %x: %w", b.Hash, err)
		}
	}

	fmt.Printf("⛓  Reorg complete — new tip %x at height %d\n", newTip.Hash, newTip.Header.Height)
	return nil
}

// findReorgPath walks back from oldTip and newTip simultaneously until they
// share a common ancestor. Returns:
//   - ancestor: the shared block
//   - rollback: old-chain blocks ordered tip→ancestor+1 (highest first)
//   - apply:    new-chain blocks ordered ancestor+1→newTip (lowest first)
func (bc *Blockchain) findReorgPath(oldTip, newTip *Block) (ancestor *Block, rollback, apply []*Block, err error) {
	old := oldTip
	new_ := newTip

	for !bytes.Equal(old.Hash, new_.Hash) {
		if old.Header.Height > new_.Header.Height {
			rollback = append(rollback, old)
			old, err = bc.Storage.GetBlockByHash(old.Header.PreviousHash)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("walk old chain: %w", err)
			}
		} else if new_.Header.Height > old.Header.Height {
			apply = append([]*Block{new_}, apply...)
			new_, err = bc.Storage.GetBlockByHash(new_.Header.PreviousHash)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("walk new chain: %w", err)
			}
		} else {
			// Same height, different blocks — step both back.
			rollback = append(rollback, old)
			apply = append([]*Block{new_}, apply...)
			old, err = bc.Storage.GetBlockByHash(old.Header.PreviousHash)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("walk old chain: %w", err)
			}
			new_, err = bc.Storage.GetBlockByHash(new_.Header.PreviousHash)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("walk new chain: %w", err)
			}
		}
	}
	return old, rollback, apply, nil
}

// GetLastBlock returns the block at the current chain tip.
func (bc *Blockchain) GetLastBlock() (*Block, error) {
	b, err := bc.Storage.GetBlockByHash(bc.GetTip())
	if err != nil {
		return nil, fmt.Errorf("get tip block: %w", err)
	}
	return b, nil
}

// GetBlock returns the block at the given height on the main chain.
func (bc *Blockchain) GetBlock(height uint64) (*Block, error) {
	b, err := bc.Storage.GetBlockByHeight(height)
	if err != nil {
		return nil, fmt.Errorf("get block at height %d: %w", height, err)
	}
	return b, nil
}

// IsValid walks the entire main chain from genesis through bc.Height and verifies
// linkage, proof-of-work, sequential height, timestamp ordering, difficulty,
// Merkle root integrity, coinbase structure, and coinbase height commitment.
func (bc *Blockchain) IsValid() (bool, error) {
	// Check genesis coinbase.
	genesis, err := bc.GetBlock(0)
	if err != nil {
		return false, fmt.Errorf("height 0 (genesis): %w", err)
	}
	if len(genesis.Transactions) == 0 {
		return false, fmt.Errorf("height 0 (genesis): no transactions")
	}
	genesisCb := genesis.Transactions[0]
	if !genesisCb.IsCoinbase {
		return false, fmt.Errorf("height 0 (genesis): first transaction is not coinbase")
	}
	if len(genesisCb.CoinbaseData) != 8 {
		return false, fmt.Errorf("height 0 (genesis): coinbase CoinbaseData must be 8 bytes, got %d", len(genesisCb.CoinbaseData))
	}
	if h := binary.BigEndian.Uint64(genesisCb.CoinbaseData); h != 0 {
		return false, fmt.Errorf("height 0 (genesis): coinbase commits to height %d, want 0", h)
	}

	for h := uint64(1); h <= bc.GetHeight(); h++ {
		block, err := bc.GetBlock(h)
		if err != nil {
			return false, fmt.Errorf("height %d: %w", h, err)
		}
		prev, err := bc.GetBlock(h - 1)
		if err != nil {
			return false, fmt.Errorf("height %d: fetch previous block: %w", h, err)
		}
		if !bytes.Equal(block.Header.PreviousHash, prev.Hash) {
			return false, fmt.Errorf("height %d: PreviousHash mismatch", h)
		}
		if !block.HashIsValid() {
			return false, fmt.Errorf("height %d: hash does not meet difficulty target", h)
		}
		if block.Header.Height != h {
			return false, fmt.Errorf("height %d: stored height field is %d", h, block.Header.Height)
		}
		if block.Header.Timestamp <= prev.Header.Timestamp {
			return false, fmt.Errorf("height %d: timestamp not after previous block", h)
		}
		if expectedDiff := NextDifficultyFromParent(bc.Storage, h, prev); block.Header.Difficulty != expectedDiff {
			return false, fmt.Errorf("height %d: difficulty %d != expected %d",
				h, block.Header.Difficulty, expectedDiff)
		}
		if expectedMerkle := ComputeMerkleRoot(block.Transactions); !bytes.Equal(block.Header.MerkleRoot, expectedMerkle) {
			return false, fmt.Errorf("height %d: merkle root mismatch", h)
		}
		if len(block.Transactions) == 0 {
			return false, fmt.Errorf("height %d: no transactions", h)
		}
		for i, tx := range block.Transactions {
			if tx.IsCoinbase != (len(tx.Inputs) == 0) {
				return false, fmt.Errorf("height %d: tx %d IsCoinbase inconsistent with inputs", h, i)
			}
			if tx.IsCoinbase && i != 0 {
				return false, fmt.Errorf("height %d: coinbase at position %d (must be 0)", h, i)
			}
		}
		if !block.Transactions[0].IsCoinbase {
			return false, fmt.Errorf("height %d: first transaction is not coinbase", h)
		}
		cb := block.Transactions[0]
		if len(cb.CoinbaseData) != 8 {
			return false, fmt.Errorf("height %d: coinbase CoinbaseData must be 8 bytes, got %d", h, len(cb.CoinbaseData))
		}
		if committedH := binary.BigEndian.Uint64(cb.CoinbaseData); committedH != h {
			return false, fmt.Errorf("height %d: coinbase CoinbaseData commits to height %d", h, committedH)
		}
	}
	return true, nil
}

// GetHeight returns the current best chain height.
// Safe for concurrent use — RPC handlers call this without blocking the miner.
func (bc *Blockchain) GetHeight() uint64 {
	bc.stateMu.RLock()
	defer bc.stateMu.RUnlock()
	return bc.Height
}

// GetTip returns a copy of the current best block hash.
// Safe for concurrent use — RPC handlers call this without blocking the miner.
func (bc *Blockchain) GetTip() []byte {
	bc.stateMu.RLock()
	defer bc.stateMu.RUnlock()
	return append([]byte{}, bc.Tip...)
}

// HasBlock reports whether the block with the given hash is stored (any chain).
func (bc *Blockchain) HasBlock(hash []byte) bool {
	return bc.Storage.HasBlock(hash)
}
