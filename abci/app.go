package abci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	abcitypes "github.com/cometbft/cometbft/abci/types"
	crypto "github.com/cometbft/cometbft/proto/tendermint/crypto"

	"chakram/consensus"
	"chakram/engine"
	"chakram/staking"
)

// EngineClient is the subset of engine.Client used by ChakramApp.
// Defined as an interface to allow mocking in tests.
type EngineClient interface {
	ForkchoiceUpdatedV3(ctx context.Context, state engine.ForkchoiceStateV1, attrs *engine.PayloadAttributesV3) (engine.ForkchoiceUpdatedResult, error)
	GetPayloadV3(ctx context.Context, payloadID string) (engine.GetPayloadResult, error)
	NewPayloadV3(ctx context.Context, payload engine.ExecutionPayloadV3, blobVersionedHashes []engine.Hash, parentBeaconBlockRoot engine.Hash) (engine.PayloadStatusV1, error)
}

// Config holds per-node configuration for ChakramApp.
type Config struct {
	Home                 string // CometBFT home directory (app_state.json lives here)
	RewardAddress        string // 0x-prefixed EVM address for withdrawal rewards
	GethRPCURL           string // geth plain HTTP RPC URL (e.g. http://127.0.0.1:8545)
	StakingContractAddr  string // 0x-prefixed address of StakingRegistry.sol; empty = PoA mode
}

// pendingBlock caches the execution block data for the current height while
// consensus is in progress, so FinalizeBlock doesn't have to re-decode the envelope.
type pendingBlock struct {
	envelope  *ExecutionEnvelope
	execHash  string // geth block hash (0x-prefixed hex)
	validated bool   // set to true if ProcessProposal got a VALID response
}

// prebuiltPayload holds the payloadID from a background FCU call started in
// Commit, so PrepareProposal can skip its own FCU and call GetPayload directly.
type prebuiltPayload struct {
	forHeight int64
	payloadID string
	timestamp uint64 // unix seconds used in the pre-built FCU attrs
}

// ChakramApp implements the CometBFT ABCI Application interface for Chakram.
// It bridges CometBFT BFT consensus to geth via the Engine API.
type ChakramApp struct {
	abcitypes.BaseApplication

	engine EngineClient
	cfg    Config
	log    *slog.Logger

	mu       sync.Mutex
	state    *AppState
	pending  map[int64]*pendingBlock // height → pending block data
	prebuilt *prebuiltPayload        // non-nil when Commit pre-built a payload for next height
}

// NewChakramApp creates a ChakramApp. It loads AppState from disk immediately.
func NewChakramApp(eng EngineClient, cfg Config, log *slog.Logger) (*ChakramApp, error) {
	state, err := loadState(cfg.Home)
	if err != nil {
		return nil, fmt.Errorf("chakram-app: load state: %w", err)
	}
	if state.ValidatorPubKeys == nil {
		state.ValidatorPubKeys = make(map[string][]byte)
	}
	if state.ValidatorRewardAddrs == nil {
		state.ValidatorRewardAddrs = make(map[string]string)
	}
	return &ChakramApp{
		engine:  eng,
		cfg:     cfg,
		log:     log,
		state:   state,
		pending: make(map[int64]*pendingBlock),
	}, nil
}

// ── ABCI Info ────────────────────────────────────────────────────────────────

// Info is called by CometBFT on startup to perform the handshake.
// It returns the last committed height and AppHash so CometBFT can replay
// any missing blocks through FinalizeBlock (D5).
//
// Fix 1 (restart/replay): if geth's block number is behind our app_state
// (e.g. geth data was wiped), we roll back our reported height so CometBFT
// calls FinalizeBlock for the missing blocks, letting geth rebuild its state.
func (app *ChakramApp) Info(_ context.Context, _ *abcitypes.RequestInfo) (*abcitypes.ResponseInfo, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	reportedHeight := app.state.LastHeight
	reportedHash := app.state.LastExecHash

	if app.cfg.GethRPCURL != "" && app.state.LastHeight > 1 {
		gethHeight, err := queryGethBlockNumber(app.cfg.GethRPCURL)
		if err != nil {
			app.log.Warn("Info: cannot query geth block number — using app_state height", "err", err)
		} else if gethHeight < app.state.LastHeight-1 {
			// Geth is significantly behind (e.g. data wiped). Roll back our
			// reported height to match geth so CometBFT replays the gap.
			app.log.Warn("Info: geth behind app_state — rolling back for FinalizeBlock replay",
				"app_height", app.state.LastHeight, "geth_height", gethHeight)

			if gethHeight <= 0 {
				reportedHeight = 0
				reportedHash = nil
			} else {
				// Report geth's actual head with its exec hash so replay starts
				// from exactly the right block.
				if ghash, err2 := queryGethBlockByNumber(app.cfg.GethRPCURL,
					fmt.Sprintf("0x%x", gethHeight)); err2 == nil {
					if hb, err3 := hexToBytes(ghash); err3 == nil {
						reportedHeight = gethHeight
						reportedHash = hb
					} else {
						reportedHeight = 0
						reportedHash = nil
					}
				} else {
					reportedHeight = 0
					reportedHash = nil
				}
			}
			// Sync in-memory state to what we report so subsequent calls are
			// consistent before the replay completes.
			app.state.LastHeight = reportedHeight
			app.state.LastExecHash = reportedHash
		}
	}

	return &abcitypes.ResponseInfo{
		Data:             "chakram",
		Version:          "0.3.0",
		AppVersion:       3,
		LastBlockHeight:  reportedHeight,
		LastBlockAppHash: reportedHash,
	}, nil
}

// ── ABCI InitChain ───────────────────────────────────────────────────────────

// InitChain is called once at genesis. It queries geth for the genesis block
// hash so that height-1 proposals use the correct parent hash, not the zero hash.
func (app *ChakramApp) InitChain(_ context.Context, req *abcitypes.RequestInitChain) (*abcitypes.ResponseInitChain, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.log.Info("InitChain", "chain_id", req.ChainId, "initial_height", req.InitialHeight)

	if app.cfg.GethRPCURL != "" && len(app.state.LastExecHash) == 0 {
		if genesisHash, err := queryGethBlockByNumber(app.cfg.GethRPCURL, "0x0"); err == nil && genesisHash != "" && genesisHash != zeroHash {
			if hashBytes, err2 := hexToBytes(genesisHash); err2 == nil {
				app.state.LastExecHash = hashBytes
				app.log.Info("InitChain: bootstrapped genesis exec hash", "hash", genesisHash)
			}
		} else {
			app.log.Warn("InitChain: could not fetch genesis hash from geth", "err", err)
		}
	}

	return &abcitypes.ResponseInitChain{}, nil
}

// ── ABCI CheckTx / InsertTx / ReapTxs ───────────────────────────────────────

// CheckTx always rejects: users must submit transactions to geth's RPC (port 8545).
func (app *ChakramApp) CheckTx(_ context.Context, _ *abcitypes.RequestCheckTx) (*abcitypes.ResponseCheckTx, error) {
	return &abcitypes.ResponseCheckTx{
		Code: 1,
		Log:  "transactions must be submitted to geth RPC (port 8545)",
	}, nil
}

// InsertTx is part of the v0.40.0 app-managed mempool API. Always rejects (D2).
func (app *ChakramApp) InsertTx(_ context.Context, _ *abcitypes.RequestInsertTx) (*abcitypes.ResponseInsertTx, error) {
	return &abcitypes.ResponseInsertTx{Code: 1}, nil
}

// ReapTxs returns an empty list: no txs live in the CometBFT mempool (D2).
func (app *ChakramApp) ReapTxs(_ context.Context, _ *abcitypes.RequestReapTxs) (*abcitypes.ResponseReapTxs, error) {
	return &abcitypes.ResponseReapTxs{}, nil
}

// ── ABCI PrepareProposal ─────────────────────────────────────────────────────

// PrepareProposal builds an execution payload via the Engine API and returns it
// as a single CometBFT tx (the execution envelope). Called only on the proposer.
//
// Fix 3: if Commit pre-built a payload for this height (optimistic pre-build),
// we skip the FCU call and call GetPayload directly. The 2s between blocks is
// enough for geth to fill the payload. Fallback to FCU+poll if pre-build is
// absent or its timestamp is stale.
func (app *ChakramApp) PrepareProposal(ctx context.Context, req *abcitypes.RequestPrepareProposal) (*abcitypes.ResponsePrepareProposal, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	height := req.Height
	app.log.Info("PrepareProposal", "height", height, "comet_time", req.Time)

	// F2/D6: health gate — do not propose if geth is more than 2 blocks behind.
	if app.cfg.GethRPCURL != "" {
		gethHead, err := queryGethBlockNumber(app.cfg.GethRPCURL)
		if err != nil {
			app.log.Warn("PrepareProposal: health gate: cannot query geth — skipping proposal",
				"height", height, "err", err)
			return &abcitypes.ResponsePrepareProposal{Txs: [][]byte{}}, nil
		}
		if gethHead < app.state.LastHeight-2 {
			app.log.Warn("PrepareProposal: health gate: geth is behind — skipping proposal",
				"height", height, "geth_head", gethHead, "committed_height", app.state.LastHeight)
			return &abcitypes.ResponsePrepareProposal{Txs: [][]byte{}}, nil
		}
	}

	// D3: timestamp rule — must be >= prevPayloadTime+1 and >= CometBFT block time.
	requiredTS := computeTimestamp(req.Time, app.state.LastPayloadTime)

	// Look up the proposer's registered reward address from the staking contract.
	// Falls back to cfg.RewardAddress for genesis validators not yet registered.
	rewardAddr := app.proposerRewardAddr(req.ProposerAddress)

	var payloadID string
	var payloadTS uint64

	// Pre-built payloads use cfg.RewardAddress. Discard if proposer has a different address.
	if app.prebuilt != nil && rewardAddr != app.cfg.RewardAddress {
		app.log.Debug("PrepareProposal: pre-built reward mismatch — fresh FCU",
			"height", height, "proposer_reward", rewardAddr)
		app.prebuilt = nil
	}

	// Try the pre-built payload from Commit's background FCU.
	if app.prebuilt != nil && app.prebuilt.forHeight == height && app.prebuilt.timestamp >= requiredTS {
		payloadID = app.prebuilt.payloadID
		payloadTS = app.prebuilt.timestamp
		app.prebuilt = nil
		app.log.Info("PrepareProposal: using pre-built payload", "height", height, "payloadID", payloadID)
	} else {
		if app.prebuilt != nil {
			app.log.Debug("PrepareProposal: pre-built payload not usable",
				"height", height,
				"prebuilt_for", app.prebuilt.forHeight,
				"prebuilt_ts", app.prebuilt.timestamp,
				"required_ts", requiredTS)
			app.prebuilt = nil
		}

		// Fallback: FCU + wait for payload.
		payloadTS = requiredTS

		prevRandao := zeroPrevRandao
		if height > 1 {
			prevRandao = execHashToPrevRandao(app.state.LastExecHash)
		}

		rewardGwei := consensus.BlockRewardGwei(uint64(height))
		withdrawal := engine.WithdrawalV1{
			Index:          consensus.QuantityHex(consensus.WithdrawalIndex(uint64(height))),
			ValidatorIndex: "0x0",
			Address:        rewardAddr,
			Amount:         consensus.QuantityHex(rewardGwei),
		}

		headHash := bytesToHash(app.state.LastExecHash)
		if headHash == zeroHash && app.cfg.GethRPCURL != "" {
			if gh, err := queryGethBlockByNumber(app.cfg.GethRPCURL, "0x0"); err == nil && gh != "" && gh != zeroHash {
				headHash = gh
			}
		}
		fcs := engine.ForkchoiceStateV1{
			HeadBlockHash:      headHash,
			SafeBlockHash:      headHash,
			FinalizedBlockHash: headHash,
		}
		attrs := &engine.PayloadAttributesV3{
			Timestamp:             consensus.QuantityHex(payloadTS),
			PrevRandao:            prevRandao,
			SuggestedFeeRecipient: rewardAddr,
			Withdrawals:           []engine.WithdrawalV1{withdrawal},
			ParentBeaconBlockRoot: zeroHash,
		}

		fcuCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		fcuResult, err := app.engine.ForkchoiceUpdatedV3(fcuCtx, fcs, attrs)
		cancel()
		if err != nil {
			// Engine API temporarily unavailable (e.g. geth restarting) — skip this
			// round instead of crashing. CometBFT will timeout and retry next round.
			app.log.Warn("PrepareProposal: FCU error — skipping proposal", "height", height, "err", err)
			return &abcitypes.ResponsePrepareProposal{Txs: [][]byte{}}, nil
		}
		if fcuResult.PayloadStatus.Status != "VALID" && fcuResult.PayloadStatus.Status != "SYNCING" {
			app.log.Warn("PrepareProposal: FCU bad status — skipping proposal", "height", height, "status", fcuResult.PayloadStatus.Status)
			return &abcitypes.ResponsePrepareProposal{Txs: [][]byte{}}, nil
		}
		if fcuResult.PayloadID == nil {
			app.log.Warn("PrepareProposal: FCU nil payloadID — skipping proposal", "height", height)
			return &abcitypes.ResponsePrepareProposal{Txs: [][]byte{}}, nil
		}
		payloadID = *fcuResult.PayloadID
	}

	// GetPayload — no fixed wait; the payload was either pre-built (2s ago) or
	// we poll briefly for the fallback path. Keep poll window at 1.5s so that
	// even in the fallback case geth has time to include pending txs.
	payload, err := app.pollGetPayload(ctx, payloadID, 1500*time.Millisecond)
	if err != nil {
		app.log.Warn("PrepareProposal: getPayload error — skipping proposal", "height", height, "err", err)
		return &abcitypes.ResponsePrepareProposal{Txs: [][]byte{}}, nil
	}

	payloadJSON, err := json.Marshal(payload.ExecutionPayload)
	if err != nil {
		return nil, fmt.Errorf("PrepareProposal: marshal payload: %w", err)
	}

	env := &ExecutionEnvelope{
		Version:               envelopeVersion,
		ExecutionPayload:      payloadJSON,
		VersionedHashes:       [][]byte{},
		ParentBeaconBlockRoot: make([]byte, 32),
	}
	envBytes, err := EncodeEnvelope(env)
	if err != nil {
		return nil, fmt.Errorf("PrepareProposal: encode envelope: %w", err)
	}

	// Cache for FinalizeBlock (proposer's own payload is pre-validated).
	app.pending[height] = &pendingBlock{
		envelope:  env,
		execHash:  payload.ExecutionPayload.BlockHash,
		validated: true,
	}

	app.log.Info("PrepareProposal done", "height", height, "exec_hash", payload.ExecutionPayload.BlockHash)
	return &abcitypes.ResponsePrepareProposal{Txs: [][]byte{envBytes}}, nil
}

// ── ABCI ProcessProposal ─────────────────────────────────────────────────────

// ProcessProposal validates the proposed block by calling newPayloadV3.
// Called on all validators (including the proposer for others' proposals).
func (app *ChakramApp) ProcessProposal(ctx context.Context, req *abcitypes.RequestProcessProposal) (*abcitypes.ResponseProcessProposal, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	height := req.Height
	accept := abcitypes.ResponseProcessProposal_ACCEPT
	reject := abcitypes.ResponseProcessProposal_REJECT

	// Exactly one tx (the envelope) is required.
	if len(req.Txs) != 1 {
		app.log.Warn("ProcessProposal: wrong tx count", "height", height, "count", len(req.Txs))
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil
	}

	env, err := DecodeEnvelope(req.Txs[0])
	if err != nil {
		app.log.Warn("ProcessProposal: bad envelope", "height", height, "err", err)
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil
	}

	var payload engine.ExecutionPayloadV3
	if err := json.Unmarshal(env.ExecutionPayload, &payload); err != nil {
		app.log.Warn("ProcessProposal: unmarshal payload", "height", height, "err", err)
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil
	}

	// Check parent hash matches our known head.
	expectedParent := bytesToHash(app.state.LastExecHash)
	if !hashEqual(payload.ParentHash, expectedParent) {
		app.log.Warn("ProcessProposal: wrong parent hash",
			"height", height, "got", payload.ParentHash, "want", expectedParent)
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil
	}

	// D3: timestamp rule.
	payloadTS, err := consensus.ParseQuantity(payload.Timestamp)
	if err != nil {
		app.log.Warn("ProcessProposal: bad timestamp", "height", height, "err", err)
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil
	}
	if payloadTS < app.state.LastPayloadTime+1 {
		app.log.Warn("ProcessProposal: timestamp violation",
			"height", height, "payload_ts", payloadTS, "min_required", app.state.LastPayloadTime+1)
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil
	}

	// Call newPayloadV3.
	npCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	status, err := app.engine.NewPayloadV3(npCtx, payload, []engine.Hash{}, zeroHash)
	if err != nil {
		app.log.Warn("ProcessProposal: newPayload error — rejecting", "height", height, "err", err)
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil
	}

	switch status.Status {
	case "VALID":
		app.log.Info("ProcessProposal: VALID", "height", height, "exec_hash", payload.BlockHash)
		app.cachePending(height, env, payload.BlockHash, true)
		return &abcitypes.ResponseProcessProposal{Status: accept}, nil

	case "INVALID":
		app.log.Warn("ProcessProposal: INVALID", "height", height,
			"validation_error", safeStr(status.ValidationError))
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil

	default:
		// F1: SYNCING or ACCEPTED → REJECT (no optimistic accept).
		app.log.Warn("ProcessProposal: geth not synced — rejecting",
			"height", height, "status", status.Status)
		return &abcitypes.ResponseProcessProposal{Status: reject}, nil
	}
}

// ── ABCI FinalizeBlock ───────────────────────────────────────────────────────

// FinalizeBlock is called after BFT commit. It ensures the execution payload
// is delivered to geth and advances geth's fork choice.
func (app *ChakramApp) FinalizeBlock(ctx context.Context, req *abcitypes.RequestFinalizeBlock) (*abcitypes.ResponseFinalizeBlock, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	height := req.Height
	app.log.Info("FinalizeBlock", "height", height)

	if len(req.Txs) != 1 {
		return nil, fmt.Errorf("FinalizeBlock: expected 1 tx, got %d", len(req.Txs))
	}

	pb, hasCached := app.pending[height]

	var execHash string

	if hasCached && pb.validated {
		// Fast path: payload already validated by ProcessProposal.
		execHash = pb.execHash
	} else {
		// Slow path (blocksync replay or missed ProcessProposal).
		var payload engine.ExecutionPayloadV3
		if hasCached && pb.envelope != nil {
			if err := json.Unmarshal(pb.envelope.ExecutionPayload, &payload); err != nil {
				return nil, fmt.Errorf("FinalizeBlock: unmarshal cached payload: %w", err)
			}
		} else {
			env, err := DecodeEnvelope(req.Txs[0])
			if err != nil {
				return nil, fmt.Errorf("FinalizeBlock: decode envelope: %w", err)
			}
			if err := json.Unmarshal(env.ExecutionPayload, &payload); err != nil {
				return nil, fmt.Errorf("FinalizeBlock: unmarshal payload: %w", err)
			}
		}

		// Poll until geth accepts the payload. During blocksync replay geth may
		// still be processing the previous block when this call arrives.
		// Also retries on transient RPC/connection errors (e.g. geth briefly restarting).
		var status engine.PayloadStatusV1
		deadline := time.Now().Add(30 * time.Second)
		for {
			npCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			var err error
			status, err = app.engine.NewPayloadV3(npCtx, payload, []engine.Hash{}, zeroHash)
			cancel()
			if err != nil {
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("FinalizeBlock: newPayload: %w", err)
				}
				app.log.Warn("FinalizeBlock: newPayload RPC error — retrying", "height", height, "err", err)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			if status.Status != "SYNCING" && status.Status != "ACCEPTED" {
				break
			}
			if time.Now().After(deadline) {
				app.log.Warn("FinalizeBlock: geth still SYNCING after 30s — proceeding", "height", height)
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if status.Status == "INVALID" {
			return nil, fmt.Errorf("FinalizeBlock: newPayload INVALID: %s", safeStr(status.ValidationError))
		}
		execHash = payload.BlockHash
	}

	// F3: head = safe = finalized = current block (instant BFT finality).
	fcs := engine.ForkchoiceStateV1{
		HeadBlockHash:      execHash,
		SafeBlockHash:      execHash,
		FinalizedBlockHash: execHash,
	}
	// Retry FCU for up to 60s so a briefly-down geth doesn't crash the node.
	// Uses context.Background() — we must not let CometBFT's context cancel finalization.
	var fcuErr error
	fcuDeadline := time.Now().Add(60 * time.Second)
	for {
		fcuCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, fcuErr = app.engine.ForkchoiceUpdatedV3(fcuCtx, fcs, nil)
		cancel()
		if fcuErr == nil {
			break
		}
		if time.Now().After(fcuDeadline) {
			break
		}
		app.log.Warn("FinalizeBlock: FCU error — retrying", "height", height, "err", fcuErr)
		time.Sleep(500 * time.Millisecond)
	}
	if fcuErr != nil {
		return nil, fmt.Errorf("FinalizeBlock: FCU: %w", fcuErr)
	}

	// F4: AppHash = raw 32-byte execution block hash (D7).
	execHashBytes, err := hexToBytes(execHash)
	if err != nil {
		return nil, fmt.Errorf("FinalizeBlock: parse exec hash: %w", err)
	}

	// Extract payload timestamp for the D3 state update.
	payloadTS := app.state.LastPayloadTime
	{
		var payloadJSON []byte
		if pb != nil && pb.envelope != nil {
			payloadJSON = pb.envelope.ExecutionPayload
		} else if env2, err2 := DecodeEnvelope(req.Txs[0]); err2 == nil {
			payloadJSON = env2.ExecutionPayload
		}
		if payloadJSON != nil {
			var p engine.ExecutionPayloadV3
			if err2 := json.Unmarshal(payloadJSON, &p); err2 == nil {
				if ts, err3 := consensus.ParseQuantity(p.Timestamp); err3 == nil {
					payloadTS = ts
				}
			}
		}
	}

	// Phase 3: read staking events and handle double-sign evidence.
	newPubKeys := copyPubKeys(app.state.ValidatorPubKeys)
	newRewardAddrs := copyRewardAddrs(app.state.ValidatorRewardAddrs)
	valUpdates := app.buildValidatorUpdates(ctx, execHash, newPubKeys, newRewardAddrs, req.Misbehavior)

	app.state = &AppState{
		LastHeight:           height,
		LastExecHash:         execHashBytes,
		LastPayloadTime:      payloadTS,
		ValidatorPubKeys:     newPubKeys,
		ValidatorRewardAddrs: newRewardAddrs,
	}
	delete(app.pending, height)

	app.log.Info("FinalizeBlock done", "height", height, "exec_hash", execHash, "val_updates", len(valUpdates))
	return &abcitypes.ResponseFinalizeBlock{
		TxResults:        []*abcitypes.ExecTxResult{{Code: abcitypes.CodeTypeOK}},
		AppHash:          execHashBytes,
		ValidatorUpdates: valUpdates,
	}, nil
}

// ── ABCI Commit ──────────────────────────────────────────────────────────────

// Commit persists the staged AppState to disk and kicks off a background FCU
// for the next height (Fix 3: optimistic payload pre-build). This gives geth
// a head start building the payload during the ~2s between blocks so
// PrepareProposal can call GetPayload immediately without a fixed wait.
func (app *ChakramApp) Commit(_ context.Context, _ *abcitypes.RequestCommit) (*abcitypes.ResponseCommit, error) {
	app.mu.Lock()

	if err := saveState(app.cfg.Home, app.state); err != nil {
		app.mu.Unlock()
		return nil, fmt.Errorf("Commit: save state: %w", err)
	}

	// Capture state snapshot for the pre-build goroutine before releasing lock.
	nextHeight := app.state.LastHeight + 1
	lastExecHash := append([]byte(nil), app.state.LastExecHash...)
	lastPayloadTime := app.state.LastPayloadTime

	app.mu.Unlock()

	app.log.Info("Commit", "height", nextHeight-1)

	// Start payload pre-build for the next height in the background so
	// PrepareProposal has a ready payloadID when it runs ~2s from now.
	go app.preBuildPayload(nextHeight, lastExecHash, lastPayloadTime)

	return &abcitypes.ResponseCommit{RetainHeight: 0}, nil
}

// preBuildPayload calls FCU with payload attributes for the next height
// immediately after Commit, giving geth time to build the payload before
// PrepareProposal is called. This is the optimistic pre-build (Fix 3/D15).
//
// The timestamp used is max(now, prevPayloadTime+1). Since CometBFT BFT time
// is normally >= wall clock, the pre-built timestamp will almost always satisfy
// the D3 rule in PrepareProposal. If it doesn't (CometBFT time jumped forward),
// PrepareProposal falls back to a fresh FCU.
func (app *ChakramApp) preBuildPayload(nextHeight int64, lastExecHash []byte, lastPayloadTime uint64) {
	payloadTS := lastPayloadTime + 1
	if now := uint64(time.Now().Unix()); now > payloadTS {
		payloadTS = now
	}

	prevRandao := zeroPrevRandao
	if nextHeight > 1 {
		prevRandao = execHashToPrevRandao(lastExecHash)
	}

	rewardGwei := consensus.BlockRewardGwei(uint64(nextHeight))
	withdrawal := engine.WithdrawalV1{
		Index:          consensus.QuantityHex(consensus.WithdrawalIndex(uint64(nextHeight))),
		ValidatorIndex: "0x0",
		Address:        app.cfg.RewardAddress,
		Amount:         consensus.QuantityHex(rewardGwei),
	}

	headHash := bytesToHash(lastExecHash)
	fcs := engine.ForkchoiceStateV1{
		HeadBlockHash:      headHash,
		SafeBlockHash:      headHash,
		FinalizedBlockHash: headHash,
	}
	attrs := &engine.PayloadAttributesV3{
		Timestamp:             consensus.QuantityHex(payloadTS),
		PrevRandao:            prevRandao,
		SuggestedFeeRecipient: app.cfg.RewardAddress,
		Withdrawals:           []engine.WithdrawalV1{withdrawal},
		ParentBeaconBlockRoot: zeroHash,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := app.engine.ForkchoiceUpdatedV3(ctx, fcs, attrs)
	if err != nil || result.PayloadID == nil {
		app.log.Debug("preBuildPayload: FCU failed or no payloadID",
			"height", nextHeight, "err", err)
		return
	}

	app.mu.Lock()
	// Only store if not superseded by a newer pre-build.
	if app.prebuilt == nil || app.prebuilt.forHeight <= nextHeight {
		app.prebuilt = &prebuiltPayload{
			forHeight: nextHeight,
			payloadID: *result.PayloadID,
			timestamp: payloadTS,
		}
	}
	app.mu.Unlock()
	app.log.Debug("preBuildPayload: done", "height", nextHeight, "payloadID", *result.PayloadID)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// computeTimestamp implements the D3 timestamp rule:
// max(cometBlockTime.Unix(), prevPayloadTimestamp + 1).
func computeTimestamp(cometTime time.Time, prevPayloadTime uint64) uint64 {
	ct := uint64(cometTime.Unix())
	if ct < prevPayloadTime+1 {
		return prevPayloadTime + 1
	}
	return ct
}

func bytesToHash(b []byte) string {
	if len(b) == 0 {
		return zeroHash
	}
	return "0x" + hex.EncodeToString(b)
}

func hexToBytes(s string) ([]byte, error) {
	if len(s) >= 2 && s[:2] == "0x" {
		s = s[2:]
	}
	return hex.DecodeString(s)
}

func hashEqual(a, b string) bool {
	return strings.EqualFold(normalizeHash(a), normalizeHash(b))
}

func normalizeHash(h string) string {
	if len(h) >= 2 && (h[:2] == "0x" || h[:2] == "0X") {
		h = h[2:]
	}
	return h
}

func execHashToPrevRandao(execHash []byte) string {
	if len(execHash) == 0 {
		return zeroPrevRandao
	}
	return "0x" + hex.EncodeToString(execHash)
}

// pollGetPayload polls engine_getPayloadV3 until the payload is ready or timeout.
func (app *ChakramApp) pollGetPayload(ctx context.Context, payloadID string, timeout time.Duration) (engine.GetPayloadResult, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return engine.GetPayloadResult{}, fmt.Errorf("getPayload timeout after %v", timeout)
		}
		pctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		result, err := app.engine.GetPayloadV3(pctx, payloadID)
		cancel()
		if err == nil {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return engine.GetPayloadResult{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (app *ChakramApp) cachePending(height int64, env *ExecutionEnvelope, execHash string, validated bool) {
	app.pending[height] = &pendingBlock{
		envelope:  env,
		execHash:  execHash,
		validated: validated,
	}
}

// queryGethBlockNumber calls eth_blockNumber on the plain geth HTTP RPC (D6 health gate).
func queryGethBlockNumber(rpcURL string) (int64, error) {
	body := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return 0, err
	}
	var result struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, err
	}
	if result.Result == "" || result.Result == "0x" {
		return 0, nil
	}
	var n int64
	_, err = fmt.Sscanf(result.Result, "0x%x", &n)
	return n, err
}

func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// queryGethBlockByNumber fetches the block hash for a given block number from
// geth's plain HTTP RPC. blockNumHex should be a 0x-prefixed hex string or
// "latest"/"finalized" etc.
func queryGethBlockByNumber(rpcURL, blockNumHex string) (string, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":[%q,false],"id":1}`, blockNumHex)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	var result struct {
		Result *struct {
			Hash string `json:"hash"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.Result == nil {
		return "", fmt.Errorf("block %s not found", blockNumHex)
	}
	return result.Result.Hash, nil
}

const (
	zeroHash       = "0x0000000000000000000000000000000000000000000000000000000000000000"
	zeroPrevRandao = "0x0000000000000000000000000000000000000000000000000000000000000000"
)

// ── Phase 3: staking helpers ─────────────────────────────────────────────────

// buildValidatorUpdates reads staking contract events from the finalized block
// and converts them + any double-sign evidence into CometBFT ValidatorUpdates.
// newPubKeys is mutated in-place as validators register/leave.
func (app *ChakramApp) buildValidatorUpdates(
	ctx context.Context,
	execHash string,
	newPubKeys map[string][]byte,
	newRewardAddrs map[string]string,
	byzantine []abcitypes.Misbehavior,
) []abcitypes.ValidatorUpdate {

	var updates []abcitypes.ValidatorUpdate

	// 1. Staking contract events (skipped when not in PoS mode).
	if app.cfg.StakingContractAddr != "" && app.cfg.GethRPCURL != "" {
		events, err := staking.ReadEvents(ctx, app.cfg.GethRPCURL, app.cfg.StakingContractAddr, execHash)
		if err != nil {
			app.log.Warn("buildValidatorUpdates: ReadEvents failed", "err", err)
		}
		for _, ev := range events {
			key := staking.NormalizeAddr(ev.Validator)
			switch ev.Type {
			case staking.EventRegistered:
				newPubKeys[key] = ev.PubKey
				// Map CometBFT address → EVM reward address so PrepareProposal can
				// route block rewards to the correct wallet when this validator proposes.
				if ev.RewardAddr != "" {
					h := sha256.Sum256(ev.PubKey)
					cometHex := strings.ToUpper(hex.EncodeToString(h[:20]))
					newRewardAddrs[cometHex] = ev.RewardAddr
					app.log.Info("staking: mapped reward address",
						"comet_addr", cometHex, "reward_addr", ev.RewardAddr)
				}
				updates = append(updates, validatorUpdate(ev.PubKey, staking.PowerPerValidator))
				app.log.Info("staking: validator registered", "addr", ev.Validator)

			case staking.EventJailed:
				if pubKey, ok := newPubKeys[key]; ok {
					updates = append(updates, validatorUpdate(pubKey, 0))
					app.log.Info("staking: validator jailed", "addr", ev.Validator)
				}

			case staking.EventUnjailed:
				if pubKey, ok := newPubKeys[key]; ok {
					updates = append(updates, validatorUpdate(pubKey, staking.PowerPerValidator))
					app.log.Info("staking: validator unjailed", "addr", ev.Validator)
				}
			}
		}
	}

	// 2. CometBFT double-sign evidence → automatic jail (Power = 0).
	for _, mis := range byzantine {
		pubKey := findPubKeyByCometAddr(newPubKeys, mis.Validator.Address)
		if pubKey == nil {
			app.log.Warn("buildValidatorUpdates: byzantine validator not in registry",
				"comet_addr", hex.EncodeToString(mis.Validator.Address))
			continue
		}
		updates = append(updates, validatorUpdate(pubKey, 0))
		app.log.Warn("staking: auto-jailed for double-sign",
			"comet_addr", hex.EncodeToString(mis.Validator.Address))
	}

	return updates
}

// validatorUpdate builds a CometBFT ValidatorUpdate for an Ed25519 key.
func validatorUpdate(ed25519PubKey []byte, power int64) abcitypes.ValidatorUpdate {
	return abcitypes.ValidatorUpdate{
		PubKey: crypto.PublicKey{
			Sum: &crypto.PublicKey_Ed25519{Ed25519: ed25519PubKey},
		},
		Power: power,
	}
}

// findPubKeyByCometAddr scans the pubkey map for the entry whose
// CometBFT address (sha256(pubkey)[0:20]) matches addr.
func findPubKeyByCometAddr(pubKeys map[string][]byte, addr []byte) []byte {
	for _, pubKey := range pubKeys {
		h := sha256.Sum256(pubKey)
		if bytes.Equal(h[:20], addr) {
			return pubKey
		}
	}
	return nil
}

// copyPubKeys makes a shallow copy of the pubkey map so FinalizeBlock
// can mutate it without touching the current app.state.
func copyPubKeys(src map[string][]byte) map[string][]byte {
	dst := make(map[string][]byte, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// copyRewardAddrs makes a shallow copy of the reward address map.
func copyRewardAddrs(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// proposerRewardAddr returns the EVM reward address for the given CometBFT proposer
// address (20-byte slice). Falls back to cfg.RewardAddress if the proposer has no
// registered staking entry (genesis validator not yet registered via contract).
func (app *ChakramApp) proposerRewardAddr(cometAddr []byte) string {
	if len(cometAddr) > 0 && app.cfg.StakingContractAddr != "" {
		key := strings.ToUpper(hex.EncodeToString(cometAddr))
		if addr, ok := app.state.ValidatorRewardAddrs[key]; ok && addr != "" {
			return addr
		}
	}
	return app.cfg.RewardAddress
}
