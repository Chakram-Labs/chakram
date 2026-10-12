package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/sha3"

	"chakram/consensus"
	"chakram/engine"
)

const (
	payloadRefreshInterval = 3 * time.Second // re-fetch payload every 3 s
	hashrateReportInterval = 30 * time.Second
)

// mineResult is sent on the result channel when a valid nonce is found.
type mineResult struct {
	header consensus.ConsensusBlockHeader
	hash   [32]byte
}

// Miner drives the single-node devnet mining loop.
type Miner struct {
	store     *consensus.Store
	client    engineAPI
	pow       consensus.PoWEngine
	minerAddr [20]byte
	// onBlock is called with each successfully mined block (for P2P gossip).
	onBlock func(*consensus.ConsensusBlock)
	// chainMu guards all Engine API calls and canonical-chain store writes.
	// Shared with the syncer's AcceptFunc so that a reorg and a concurrent
	// block submission cannot interleave their forkchoiceUpdatedV3 calls and
	// leave geth on the wrong chain.
	chainMu *sync.Mutex
}

// NewMiner creates a Miner. chainMu must be the same mutex passed to
// makeAcceptFunc so that mining and syncer reorgs are serialised.
func NewMiner(store *consensus.Store, client engineAPI, pow consensus.PoWEngine, minerAddr [20]byte, chainMu *sync.Mutex) *Miner {
	return &Miner{store: store, client: client, pow: pow, minerAddr: minerAddr, chainMu: chainMu}
}

// Run mines blocks until ctx is cancelled. Each successfully mined block is
// stored and submitted to geth before the next one starts.
func (m *Miner) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.mineOne(ctx); err != nil {
			log.Printf("[miner] error: %v — retrying in 1 s", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

// mineOne mines exactly one block on top of the current chain tip.
func (m *Miner) mineOne(ctx context.Context) error {
	// ── 1. load current tip ───────────────────────────────────────────────────
	parentHash, parentHeight, err := m.store.GetTip()
	if err != nil {
		return fmt.Errorf("get tip: %w", err)
	}
	parent, err := m.store.GetBlock(parentHash)
	if err != nil {
		return fmt.Errorf("get parent block: %w", err)
	}

	height := parent.Header.Height + 1

	// ── 2. compute mining parameters ─────────────────────────────────────────
	_ = parentHeight // not needed beyond what parent gives us

	fetch := m.store.HeaderFetcher()
	timestamp := consensus.MiningTimestamp(time.Now().Unix(), parent.Header.Height, parent.Header.Timestamp, fetch)
	difficulty := consensus.NextDifficulty(fetch, height, parent.Header)
	rewardGwei := consensus.BlockRewardGwei(height)
	prevRandao := keccak256Hex(parentHash)

	minerAddrHex := "0x" + fmt.Sprintf("%x", m.minerAddr)

	withdrawal := engine.WithdrawalV1{
		Index:          consensus.QuantityHex(consensus.WithdrawalIndex(height)),
		ValidatorIndex: "0x0",
		Address:        minerAddrHex,
		Amount:         consensus.QuantityHex(rewardGwei),
	}

	// ── 3. derive RandomX epoch key ───────────────────────────────────────────
	epochKey, err := consensus.EpochKey(height, m.store.BlockHashAt())
	if err != nil {
		return fmt.Errorf("epoch key: %w", err)
	}
	if err := m.pow.Init(epochKey); err != nil {
		return fmt.Errorf("init RandomX: %w", err)
	}

	// ── 4. request first payload from geth ───────────────────────────────────
	payload, payloadID, execHash, err := m.fetchPayload(ctx, parent, height, timestamp, prevRandao, withdrawal)
	if err != nil {
		return fmt.Errorf("fetch initial payload: %w", err)
	}

	// Build the initial header with nonce = 0.
	header := consensus.ConsensusBlockHeader{
		Version:              consensus.GenesisVersion,
		Height:               height,
		ParentHash:           parentHash,
		ExecutionPayloadHash: execHash,
		Timestamp:            timestamp,
		MinerAddress:         m.minerAddr,
		Difficulty:           difficulty,
		Nonce:                0,
	}

	log.Printf("[miner] height=%d difficulty=%x prevRandao=%s...", height, difficulty, prevRandao[:10])

	// ── 5. mine with periodic payload refresh ────────────────────────────────
	resultCh := make(chan mineResult, 1)
	quitMine := make(chan struct{})

	// hashCount is shared across all hashLoop restarts for this height so the
	// 30-second report interval isn't reset on every 3-second payload refresh.
	var hashCount int64
	reportCtx, reportCancel := context.WithCancel(ctx)
	defer reportCancel()
	go func() {
		ticker := time.NewTicker(hashrateReportInterval)
		defer ticker.Stop()
		last := time.Now()
		for {
			select {
			case <-reportCtx.Done():
				return
			case <-ticker.C:
				n := atomic.SwapInt64(&hashCount, 0)
				hps := float64(n) / time.Since(last).Seconds()
				last = time.Now()
				log.Printf("[miner] height=%d hashrate=%.0f H/s", height, hps)
			}
		}
	}()

	go m.hashLoop(&header, resultCh, quitMine, &hashCount)

	refreshTicker := time.NewTicker(payloadRefreshInterval)
	defer refreshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(quitMine)
			<-resultCh // wait for hash goroutines to exit before pow.Close() destroys VMs
			return ctx.Err()

		case result := <-resultCh:
			// Nonce found — submit the block.
			return m.submitBlock(ctx, result, payload, parentHash, parent.Header.ExecutionPayloadHash, height, rewardGwei)

		case <-refreshTicker.C:
			// If the consensus store tip has advanced to this height or beyond, a peer
			// already mined this block. Abort and let the outer loop restart at the
			// new tip rather than fighting the gossip path's forkchoiceUpdatedV3.
			_, storeTipHeight, _ := m.store.GetTip()
			if storeTipHeight >= height {
				close(quitMine)
				<-resultCh
				return nil
			}

			// Refresh: get an updated payload (new transactions, updated timestamp).
			newTS := max64(time.Now().Unix(), timestamp)
			newPayload, _, newExecHash, err := m.fetchPayload(ctx, parent, height, newTS, prevRandao, withdrawal)
			if err != nil {
				log.Printf("[miner] payload refresh failed: %v (continuing with old payload)", err)
				continue
			}
			if newExecHash == execHash {
				continue // same payload — no need to restart
			}
			// Different payload — stop current mining, restart with new header.
			close(quitMine)
			<-resultCh // drain (may be empty if hashLoop is still running — wait for it)

			payload = newPayload
			_ = payloadID
			execHash = newExecHash
			timestamp = newTS
			header.ExecutionPayloadHash = execHash
			header.Timestamp = newTS
			header.Nonce = 0

			quitMine = make(chan struct{})
			resultCh = make(chan mineResult, 1)
			go m.hashLoop(&header, resultCh, quitMine, &hashCount)
			log.Printf("[miner] refreshed payload at height=%d execHash=%s...", height, consensus.HashToHex(execHash)[:10])
		}
	}
}

// fetchPayload calls forkchoiceUpdatedV3 with payload attributes, waits a
// short time for geth to build the payload, then calls getPayloadV3.
func (m *Miner) fetchPayload(
	ctx context.Context,
	parent *consensus.ConsensusBlock,
	height uint64,
	timestamp int64,
	prevRandaoHex string,
	withdrawal engine.WithdrawalV1,
) (engine.ExecutionPayloadV3, string, [32]byte, error) {
	parentExecHex := consensus.HashToHex(parent.Header.ExecutionPayloadHash)

	_, parentTipHeight, err := m.store.GetTip()
	if err != nil {
		return engine.ExecutionPayloadV3{}, "", [32]byte{}, err
	}

	safeHeight := uint64(0)
	if parentTipHeight >= 10 {
		safeHeight = parentTipHeight - 10
	}
	finalHeight := uint64(0)
	if parentTipHeight >= 100 {
		finalHeight = parentTipHeight - 100
	}
	safeExecHash, err := executionHashAtHeight(m.store, safeHeight)
	if err != nil {
		safeExecHash = parentExecHex
	}
	finalExecHash, err := executionHashAtHeight(m.store, finalHeight)
	if err != nil {
		finalExecHash = parentExecHex
	}

	minerAddrHex := "0x" + fmt.Sprintf("%x", m.minerAddr)

	attrs := &engine.PayloadAttributesV3{
		Timestamp:             consensus.QuantityHex(uint64(timestamp)),
		PrevRandao:            prevRandaoHex,
		SuggestedFeeRecipient: minerAddrHex,
		Withdrawals:           []engine.WithdrawalV1{withdrawal},
		ParentBeaconBlockRoot: consensus.HashToHex(parent.Hash),
	}

	fcs := engine.ForkchoiceStateV1{
		HeadBlockHash:      parentExecHex,
		SafeBlockHash:      safeExecHash,
		FinalizedBlockHash: finalExecHash,
	}

	fcResult, err := m.client.ForkchoiceUpdatedV3(ctx, fcs, attrs)
	if err != nil {
		return engine.ExecutionPayloadV3{}, "", [32]byte{}, fmt.Errorf("forkchoiceUpdatedV3: %w", err)
	}
	if fcResult.PayloadStatus.Status != "VALID" {
		return engine.ExecutionPayloadV3{}, "", [32]byte{}, fmt.Errorf("forkchoiceUpdated status: %s", fcResult.PayloadStatus.Status)
	}
	if fcResult.PayloadID == nil {
		return engine.ExecutionPayloadV3{}, "", [32]byte{}, fmt.Errorf("forkchoiceUpdated returned no payloadId")
	}
	payloadID := *fcResult.PayloadID

	// Give geth a moment to build the payload.
	time.Sleep(100 * time.Millisecond)

	gpResult, err := m.client.GetPayloadV3(ctx, payloadID)
	if err != nil {
		return engine.ExecutionPayloadV3{}, "", [32]byte{}, fmt.Errorf("getPayloadV3: %w", err)
	}

	execHash, err := consensus.HashFromHex(gpResult.ExecutionPayload.BlockHash)
	if err != nil {
		return engine.ExecutionPayloadV3{}, "", [32]byte{}, fmt.Errorf("parse blockHash: %w", err)
	}

	// Pre-flight validation (spec §5.4 step 4a).
	if err := engine.ValidateBuiltPayload(
		gpResult.ExecutionPayload,
		height,
		timestamp,
		parentExecHex,
		prevRandaoHex,
		withdrawal,
	); err != nil {
		return engine.ExecutionPayloadV3{}, "", [32]byte{}, fmt.Errorf("built payload pre-flight: %w", err)
	}

	return gpResult.ExecutionPayload, payloadID, execHash, nil
}

// multiThreadPoW extends PoWEngine with per-VM hashing for parallel mining.
// Implemented by *randomXEngine when built with CGo and numWantVMs >= 2.
type multiThreadPoW interface {
	NumMiningThreads() int
	HashOnThread(threadIndex int, input []byte) []byte
}

// hashLoop runs RandomX hashing until a valid nonce is found or quit is closed.
// Uses parallel goroutines when the PoW engine exposes multiThreadPoW.
// hashCount is atomically incremented for each hash computed; the caller owns the counter.
func (m *Miner) hashLoop(hdr *consensus.ConsensusBlockHeader, result chan<- mineResult, quit <-chan struct{}, hashCount *int64) {
	if mt, ok := m.pow.(multiThreadPoW); ok && mt.NumMiningThreads() >= 2 {
		hashLoopMulti(hdr, result, quit, mt, hashCount)
		return
	}
	hashLoopSingle(hdr, result, quit, m.pow, hashCount)
}

// hashLoopSingle is the original single-threaded mining loop.
func hashLoopSingle(hdr *consensus.ConsensusBlockHeader, result chan<- mineResult, quit <-chan struct{}, pow consensus.PoWEngine, hashCount *int64) {
	startNonce := hdr.Nonce
	h := *hdr

	for {
		select {
		case <-quit:
			select {
			case result <- mineResult{}:
			default:
			}
			return
		default:
		}

		serialized := h.Serialize()
		hashBytes := pow.Hash(serialized[:])
		atomic.AddInt64(hashCount, 1)
		runtime.Gosched()

		var hash [32]byte
		copy(hash[:], hashBytes)

		if consensus.HashMeetsTarget(hash, h.Difficulty) {
			result <- mineResult{header: h, hash: hash}
			return
		}

		h.Nonce++
		if h.Nonce == startNonce {
			select {
			case result <- mineResult{}:
			default:
			}
			return
		}
	}
}

// hashLoopMulti runs n goroutines, each owning one mining VM.
// Nonce space is striped: goroutine i uses nonces startNonce+i, startNonce+i+n, …
//
// The result is sent ONLY after wg.Wait() — meaning every goroutine has fully
// returned before the caller unblocks. This guarantees that when mineOne
// receives the result and calls Init() for the next epoch, no goroutine is
// still executing HashOnThread on the shared dataset.
func hashLoopMulti(hdr *consensus.ConsensusBlockHeader, result chan<- mineResult, quit <-chan struct{}, mt multiThreadPoW, hashCount *int64) {
	n := mt.NumMiningThreads()
	startNonce := hdr.Nonce

	stop := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stop) }) }

	var (
		winner  mineResult // zero value = no winner found
		winOnce sync.Once
	)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(tidx int) {
			defer wg.Done()
			h := *hdr
			h.Nonce = startNonce + uint64(tidx)

			for {
				select {
				case <-quit:
					closeStop()
					return
				case <-stop:
					return
				default:
				}

				serialized := h.Serialize()
				hashBytes := mt.HashOnThread(tidx, serialized[:])
				atomic.AddInt64(hashCount, 1)
				runtime.Gosched()

				var hash [32]byte
				copy(hash[:], hashBytes)

				if consensus.HashMeetsTarget(hash, h.Difficulty) {
					winOnce.Do(func() {
						winner = mineResult{header: h, hash: hash}
					})
					closeStop()
					return
				}

				h.Nonce += uint64(n)
				if h.Nonce < uint64(tidx) { // overflow wrap
					closeStop()
					return
				}
			}
		}(i)
	}

	// Block until every goroutine has exited before sending.
	// This means: when mineOne receives from result and calls Init() for the
	// next epoch, no goroutine is still mid-HashOnThread on the old dataset.
	wg.Wait()
	result <- winner
}

// submitBlock calls newPayloadV3, then forkchoiceUpdatedV3, then stores the block.
// It also persists the raw execution payload JSON so peers can fetch it for IBD.
//
// The Engine API calls and store writes are serialised under m.chainMu, which is
// the same mutex used by makeAcceptFunc. This prevents a reorg's FCU from being
// overridden by a concurrent submitBlock (CP2 EVM divergence fix).
func (m *Miner) submitBlock(
	ctx context.Context,
	result mineResult,
	payload engine.ExecutionPayloadV3,
	parentHash [32]byte,
	parentExecHash [32]byte,
	height uint64,
	rewardGwei uint64,
) error {
	log.Printf("[miner] nonce found: height=%d nonce=%d hash=%s",
		result.header.Height, result.header.Nonce, consensus.HashToHex(result.hash)[:16]+"…")

	// Critical section: orphan-check → newPayload → FCU → StoreBlock.
	// Must be atomic with the syncer's acceptFunc (also holding chainMu) so
	// that a reorg's FCU is never overridden by a stale miner FCU.
	var block *consensus.ConsensusBlock
	if err := func() error {
		m.chainMu.Lock()
		defer m.chainMu.Unlock()

		// Discard if the canonical chain has already moved past our height, OR if
		// our parent hash no longer matches the canonical tip.  The second check
		// catches the case where a same-height reorg (A12 → B12) happened while we
		// were mining A13: storeTipHeight (12) < height (13) passes the first check,
		// but B12.Hash ≠ A12.Hash (our parent) correctly identifies the orphan.
		tipHash, storeTipHeight, _ := m.store.GetTip()
		if storeTipHeight >= height || tipHash != parentHash {
			log.Printf("[miner] orphan discarded: height=%d parent=%s… tip=%s…@%d",
				height, consensus.HashToHex(parentHash)[:8], consensus.HashToHex(tipHash)[:8], storeTipHeight)
			return nil
		}

		// engine_newPayloadV3
		parentBeaconRoot := consensus.HashToHex(parentHash)
		status, err := m.client.NewPayloadV3(ctx, payload, []string{}, parentBeaconRoot)
		if err != nil {
			return fmt.Errorf("newPayloadV3: %w", err)
		}
		if status.Status != "VALID" {
			return fmt.Errorf("newPayloadV3 status: %s (validationError: %v)", status.Status, status.ValidationError)
		}

		execHashHex := consensus.HashToHex(result.header.ExecutionPayloadHash)
		_, tipHeight, _ := m.store.GetTip()
		safeHeight := uint64(0)
		if tipHeight >= 9 {
			safeHeight = tipHeight - 9
		}
		finalHeight := uint64(0)
		if tipHeight >= 99 {
			finalHeight = tipHeight - 99
		}
		safeExecHash, _ := executionHashAtHeight(m.store, safeHeight)
		finalExecHash, _ := executionHashAtHeight(m.store, finalHeight)
		if safeExecHash == "" {
			safeExecHash = execHashHex
		}
		if finalExecHash == "" {
			finalExecHash = execHashHex
		}

		// engine_forkchoiceUpdatedV3 to the new head
		fcs := engine.ForkchoiceStateV1{
			HeadBlockHash:      execHashHex,
			SafeBlockHash:      safeExecHash,
			FinalizedBlockHash: finalExecHash,
		}
		fcResult, err := m.client.ForkchoiceUpdatedV3(ctx, fcs, nil)
		if err != nil {
			return fmt.Errorf("post-mine forkchoiceUpdatedV3: %w", err)
		}
		if fcResult.PayloadStatus.Status != "VALID" {
			return fmt.Errorf("post-mine forkchoiceUpdated status: %s", fcResult.PayloadStatus.Status)
		}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}

		block = blockFromResult(result, payloadJSON)
		if err := m.store.StoreBlock(block); err != nil {
			return fmt.Errorf("store block: %w", err)
		}
		if err := m.store.StorePayload(block.Hash, payloadJSON); err != nil {
			return fmt.Errorf("store payload: %w", err)
		}
		return nil
	}(); err != nil {
		return err
	}

	if block == nil {
		return nil // orphan discarded
	}

	log.Printf("[miner] ✓ block %d mined  hash=%s  reward=%.6f CHK",
		height, consensus.HashToHex(result.hash)[:16]+"…",
		float64(rewardGwei)/1e9)

	if m.onBlock != nil {
		m.onBlock(block)
	}

	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func keccak256Hex(data [32]byte) string {
	h := sha3.NewLegacyKeccak256()
	h.Write(data[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return consensus.HashToHex(out)
}

func executionHashAtHeight(store *consensus.Store, height uint64) (string, error) {
	b, err := store.GetBlockByHeight(height)
	if err != nil {
		return "", err
	}
	return consensus.HashToHex(b.Header.ExecutionPayloadHash), nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// blockFromResult constructs a ConsensusBlock from a mineResult.
// The block identifier (Hash) is SHA256²(header), not the RandomX PoW output.
// The PoW output (result.hash) is only used for the HashMeetsTarget check
// inside hashLoop and is not stored anywhere.
func blockFromResult(result mineResult, payload []byte) *consensus.ConsensusBlock {
	return &consensus.ConsensusBlock{
		Header:           result.header,
		Hash:             consensus.GenesisHash(&result.header),
		ExecutionPayload: payload,
	}
}
