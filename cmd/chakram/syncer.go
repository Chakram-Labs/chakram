package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"chakram/consensus"
)

// SyncerStore is the subset of consensus.Store the Syncer needs.
type SyncerStore interface {
	GetTip() ([32]byte, uint64, error)
	GetBlock(hash [32]byte) (*consensus.ConsensusBlock, error)
	GetBlockByHeight(height uint64) (*consensus.ConsensusBlock, error)
	StoreBlock(b *consensus.ConsensusBlock) error
	StoreBlockOnly(b *consensus.ConsensusBlock) error
	SetTip(hash [32]byte, height uint64) error
	StorePayload(hash [32]byte, payload []byte) error
	GetPayload(hash [32]byte) ([]byte, error)
	HeaderFetcher() consensus.BlockHeaderFetcher
}

// AcceptFunc is called when the syncer decides a block should be applied to
// the execution layer (newPayloadV3 + forkchoiceUpdatedV3). It is called once
// per new canonical block, in order.
type AcceptFunc func(ctx context.Context, b *consensus.ConsensusBlock) error

// FetchFunc fetches up to limit blocks starting from height from a peer.
type FetchFunc func(ctx context.Context, from, limit uint64) ([]*consensus.ConsensusBlock, error)

// RelayFunc sends a block to all peers except the one it came from.
type RelayFunc func(b *consensus.ConsensusBlock)

// MaxReorgDepth is the maximum number of canonical blocks the syncer will unwind
// for a reorg. Reorgs deeper than this are rejected to prevent a hidden long-chain
// attack from rolling back a deeply confirmed section of the chain.
const MaxReorgDepth = 100

// Syncer handles incoming blocks from peers: validates, decides chain tip, reorgs.
type Syncer struct {
	store  SyncerStore
	accept AcceptFunc
	relay  RelayFunc

	mu        sync.Mutex
	orphans   map[[32]byte]*consensus.ConsensusBlock // parentHash → block
	ibdActive int32                                  // atomic: 1 while SyncFrom is running
}

// NewSyncer creates a Syncer. relay may be nil (no gossip relay).
func NewSyncer(store SyncerStore, accept AcceptFunc, relay RelayFunc) *Syncer {
	return &Syncer{
		store:   store,
		accept:  accept,
		relay:   relay,
		orphans: make(map[[32]byte]*consensus.ConsensusBlock),
	}
}

// Receive processes a block received from a peer.
//
// Rules (in order):
//  1. Block must have an execution payload (we need it to call newPayloadV3).
//  2. Duplicate: block hash already known — ignore silently.
//  3. Extends canonical tip (height = tip+1, parentHash = tipHash): apply immediately.
//  4. Higher height than tip: look up the fork chain and reorg if the fork is longer.
//  5. Parent unknown: stash as orphan for later.
//  6. Same or lower height than tip with known parent: stash as potential fork base.
func (s *Syncer) Receive(ctx context.Context, b *consensus.ConsensusBlock) error {
	if len(b.ExecutionPayload) == 0 {
		return fmt.Errorf("syncer: block %s has no execution payload", consensus.HashToHex(b.Hash))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Duplicate check.
	if _, err := s.store.GetBlock(b.Hash); err == nil {
		return nil // already known
	}

	tipHash, tipHeight, err := s.store.GetTip()
	if err != nil {
		return fmt.Errorf("syncer: get tip: %w", err)
	}

	// Store the block (without moving tip) so parent lookups work.
	if err := s.store.StoreBlockOnly(b); err != nil {
		return fmt.Errorf("syncer: store block: %w", err)
	}
	if err := s.store.StorePayload(b.Hash, b.ExecutionPayload); err != nil {
		return fmt.Errorf("syncer: store payload: %w", err)
	}

	// Case 1: block directly extends the canonical tip.
	if b.Header.Height == tipHeight+1 && b.Header.ParentHash == tipHash {
		if err := s.applyBlock(ctx, b); err != nil {
			return err
		}
		// Try to apply any orphans that were waiting for this block.
		s.drainOrphans(ctx, b.Hash, b.Header.Height)
		return nil
	}

	// Case 2: block could trigger a reorg (chain is longer).
	if b.Header.Height > tipHeight {
		// Walk back from b to find the fork point, then check if the fork chain
		// is indeed longer before reorging.
		forkLen, forkTip, err := s.longestForkChain(b)
		if err == nil && forkLen > tipHeight {
			log.Printf("[syncer] reorg: switching to fork head=%s height=%d (was %d)",
				consensus.HashToHex(forkTip.Hash), forkLen, tipHeight)
			return s.reorg(ctx, forkTip, forkLen)
		}
		return nil
	}

	// Case 3: parent unknown — stash as orphan.
	if _, err := s.store.GetBlock(b.Header.ParentHash); errors.Is(err, consensus.ErrBlockNotFound) {
		s.orphans[b.Header.ParentHash] = b
		log.Printf("[syncer] orphan stashed: height=%d hash=%s (parent unknown)",
			b.Header.Height, consensus.HashToHex(b.Hash)[:10])
		return nil
	}

	// Case 4: parent known, but not longer than current tip — stash as fork base.
	// drainOrphans will pick it up if a longer chain extends from it later.
	return nil
}

// applyBlock calls the AcceptFunc (which calls newPayloadV3 + forkchoiceUpdatedV3)
// and, if accepted, advances the canonical tip.
func (s *Syncer) applyBlock(ctx context.Context, b *consensus.ConsensusBlock) error {
	// Blocks loaded from BadgerDB don't carry the execution payload (it's stored
	// separately). Load it here so AcceptFunc can call newPayloadV3.
	if len(b.ExecutionPayload) == 0 {
		payload, err := s.store.GetPayload(b.Hash)
		if err != nil {
			return fmt.Errorf("syncer: get payload for block height=%d: %w", b.Header.Height, err)
		}
		b.ExecutionPayload = payload
	}

	if err := s.accept(ctx, b); err != nil {
		return fmt.Errorf("syncer: accept block height=%d: %w", b.Header.Height, err)
	}
	if err := s.store.SetTip(b.Hash, b.Header.Height); err != nil {
		return fmt.Errorf("syncer: set tip: %w", err)
	}
	log.Printf("[syncer] applied block height=%d hash=%s", b.Header.Height, consensus.HashToHex(b.Hash)[:10])
	if s.relay != nil && atomic.LoadInt32(&s.ibdActive) == 0 {
		s.relay(b)
	}
	return nil
}

// reorg switches the canonical tip to the given fork head.
// It walks back from forkTip to the common ancestor, then applies the fork
// chain forward.
func (s *Syncer) reorg(ctx context.Context, forkTip *consensus.ConsensusBlock, forkHeight uint64) error {
	// Collect the fork chain from fork tip back to the common ancestor.
	chain := []*consensus.ConsensusBlock{forkTip}
	cur := forkTip
	for {
		parent, err := s.store.GetBlock(cur.Header.ParentHash)
		if err != nil {
			// Common ancestor must already be on the canonical chain.
			break
		}
		// Stop once we find a block that's already canonical at its height.
		if canonical, err := s.store.GetBlockByHeight(parent.Header.Height); err == nil && canonical.Hash == parent.Hash {
			break
		}
		chain = append(chain, parent)
		cur = parent
	}

	// Reverse so we apply from lowest height to highest.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	// Reject reorgs deeper than the cap. chain[0] is the first fork block;
	// its parent is the common ancestor at height chain[0].Height-1.
	if len(chain) > 0 {
		_, currentTipHeight, _ := s.store.GetTip()
		forkPointHeight := chain[0].Header.Height - 1
		depth := currentTipHeight - forkPointHeight
		if depth > MaxReorgDepth {
			return fmt.Errorf("syncer: reorg depth %d exceeds cap of %d blocks", depth, MaxReorgDepth)
		}
	}

	for _, b := range chain {
		if err := s.applyBlock(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

// longestForkChain walks from b back toward genesis, following ParentHash,
// until it finds a block on the canonical chain. Returns the fork height and tip.
func (s *Syncer) longestForkChain(b *consensus.ConsensusBlock) (uint64, *consensus.ConsensusBlock, error) {
	cur := b
	for {
		// If cur's parent is on the canonical chain at cur's height-1, we found
		// the fork point. Return the tip of the fork (b) and its height.
		parent, err := s.store.GetBlock(cur.Header.ParentHash)
		if err != nil {
			return 0, nil, err
		}
		if canonical, err := s.store.GetBlockByHeight(parent.Header.Height); err == nil && canonical.Hash == parent.Hash {
			return b.Header.Height, b, nil
		}
		cur = parent
	}
}

// drainOrphans checks if any stashed orphan has parentHash as its parent, and
// if so, applies it and recurses.
func (s *Syncer) drainOrphans(ctx context.Context, parentHash [32]byte, parentHeight uint64) {
	orphan, ok := s.orphans[parentHash]
	if !ok {
		return
	}
	delete(s.orphans, parentHash)
	if err := s.applyBlock(ctx, orphan); err != nil {
		log.Printf("[syncer] orphan apply failed: %v", err)
		return
	}
	s.drainOrphans(ctx, orphan.Hash, orphan.Header.Height)
}

// SyncFrom fetches blocks from a peer in [fromHeight, targetHeight] and processes
// each through Receive. fromHeight must be low enough to include the common
// ancestor's child when syncing across a fork; callers on the same chain can pass
// localTip+1. Blocks already in the store are skipped by Receive's duplicate check.
// Relay is suppressed during IBD to avoid bouncing blocks back to the source peer.
func (s *Syncer) SyncFrom(ctx context.Context, fromHeight, targetHeight uint64, fetch FetchFunc) error {
	if fromHeight > targetHeight {
		return nil
	}
	atomic.StoreInt32(&s.ibdActive, 1)
	defer atomic.StoreInt32(&s.ibdActive, 0)

	const batchSize = 100
	for from := fromHeight; from <= targetHeight; {
		limit := uint64(batchSize)
		if from+limit-1 > targetHeight {
			limit = targetHeight - from + 1
		}
		blocks, err := fetch(ctx, from, limit)
		if err != nil {
			return fmt.Errorf("syncer: fetch blocks from=%d: %w", from, err)
		}
		if len(blocks) == 0 {
			break
		}
		for _, b := range blocks {
			if len(b.ExecutionPayload) == 0 {
				return fmt.Errorf("syncer: peer sent block %d with no payload", b.Header.Height)
			}
			if err := s.Receive(ctx, b); err != nil {
				return fmt.Errorf("syncer: receive block %d: %w", b.Header.Height, err)
			}
		}
		from += uint64(len(blocks))
	}
	return nil
}
