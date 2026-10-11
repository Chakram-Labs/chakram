// Package p2p implements the Chakram 2.0 consensus-layer P2P protocol.
// It uses plain HTTP/JSON: each node runs a small HTTP server and clients
// push/pull consensus blocks between nodes.
package p2p

import (
	"chakram/consensus"
)

// BlockStore is the subset of consensus.Store that the P2P server needs.
type BlockStore interface {
	GetTip() ([32]byte, uint64, error)
	GetBlock(hash [32]byte) (*consensus.ConsensusBlock, error)
	GetBlockByHeight(height uint64) (*consensus.ConsensusBlock, error)
	GetPayload(hash [32]byte) ([]byte, error)
}

// StatusResponse is returned by GET /status.
type StatusResponse struct {
	Height      uint64 `json:"height"`
	TipHash     string `json:"tipHash"`
	ChainID     uint64 `json:"chainId"`     // EIP-155 chain ID
	GenesisHash string `json:"genesisHash"` // EVM genesis block hash (keccak256)
}

// WireBlock is the JSON encoding of a consensus block sent over the network.
// It includes the raw execution payload so the receiver can call newPayloadV3.
type WireBlock struct {
	Version              uint32 `json:"version"`
	Height               uint64 `json:"height"`
	ParentHash           string `json:"parentHash"`
	ExecutionPayloadHash string `json:"executionPayloadHash"`
	Timestamp            int64  `json:"timestamp"`
	MinerAddress         string `json:"minerAddress"`
	Difficulty           uint64 `json:"difficulty"`
	Nonce                uint64 `json:"nonce"`
	Hash                 string `json:"hash"`
	ExecutionPayload     []byte `json:"executionPayload"` // raw JSON of ExecutionPayloadV3
}

// BlockToWire converts a ConsensusBlock to its wire representation.
func BlockToWire(b *consensus.ConsensusBlock) WireBlock {
	addr := b.Header.MinerAddress
	return WireBlock{
		Version:              b.Header.Version,
		Height:               b.Header.Height,
		ParentHash:           consensus.HashToHex(b.Header.ParentHash),
		ExecutionPayloadHash: consensus.HashToHex(b.Header.ExecutionPayloadHash),
		Timestamp:            b.Header.Timestamp,
		MinerAddress:         "0x" + addrHex(addr),
		Difficulty:           b.Header.Difficulty,
		Nonce:                b.Header.Nonce,
		Hash:                 consensus.HashToHex(b.Hash),
		ExecutionPayload:     b.ExecutionPayload,
	}
}

// WireToBlock converts a WireBlock back to a ConsensusBlock.
// Returns an error if any field is malformed.
func WireToBlock(w WireBlock) (*consensus.ConsensusBlock, error) {
	parentHash, err := consensus.HashFromHex(w.ParentHash)
	if err != nil {
		return nil, err
	}
	execHash, err := consensus.HashFromHex(w.ExecutionPayloadHash)
	if err != nil {
		return nil, err
	}
	addr, err := consensus.AddrFromHex(w.MinerAddress)
	if err != nil {
		return nil, err
	}
	hash, err := consensus.HashFromHex(w.Hash)
	if err != nil {
		return nil, err
	}
	return &consensus.ConsensusBlock{
		Header: consensus.ConsensusBlockHeader{
			Version:              w.Version,
			Height:               w.Height,
			ParentHash:           parentHash,
			ExecutionPayloadHash: execHash,
			Timestamp:            w.Timestamp,
			MinerAddress:         addr,
			Difficulty:           w.Difficulty,
			Nonce:                w.Nonce,
		},
		Hash:             hash,
		ExecutionPayload: w.ExecutionPayload,
	}, nil
}

// LocatorEntry is one entry in a block locator: a (height, hash) pair.
type LocatorEntry struct {
	Height uint64 `json:"height"`
	Hash   string `json:"hash"` // 0x-prefixed hex
}

// LocatorRequest is sent to POST /locator to find a common ancestor.
type LocatorRequest struct {
	Entries []LocatorEntry `json:"entries"`
}

// LocatorResponse is returned by POST /locator.
type LocatorResponse struct {
	// CommonHeight is the height of the newest common ancestor found.
	// Zero means genesis is the best common point.
	CommonHeight uint64 `json:"commonHeight"`
}

// BuildLocator builds a Bitcoin-style block locator for the given tip height.
// hashAt must return the canonical block hash at the requested height.
//
// The locator contains the last 10 heights consecutively, then doubles the
// gap for each subsequent entry down to 0. Genesis (height 0) is always included.
// This gives O(log N) entries and guarantees finding a common ancestor within
// MaxReorgDepth quickly.
func BuildLocator(tip uint64, hashAt func(uint64) ([32]byte, error)) LocatorRequest {
	var entries []LocatorEntry
	seen := make(map[uint64]bool)
	step := uint64(1)

	// Collect up to 32 heights: first 10 are consecutive, then doubling gaps.
	for i := 0; len(entries) < 32; i++ {
		h := uint64(0)
		if i == 0 {
			h = tip
		} else if i <= 10 {
			if tip < uint64(i) {
				h = 0
			} else {
				h = tip - uint64(i)
			}
		} else {
			// After the 10th entry, use the running position variable.
			// entries[len(entries)-1].Height already set; just continue below.
			break
		}
		if seen[h] {
			break
		}
		seen[h] = true
		if hash, err := hashAt(h); err == nil {
			entries = append(entries, LocatorEntry{Height: h, Hash: consensus.HashToHex(hash)})
		}
		if h == 0 {
			break
		}
	}

	// After the 10 consecutive entries, add exponentially spaced entries.
	step = 2
	if len(entries) > 0 {
		last := entries[len(entries)-1].Height
		for len(entries) < 32 && last > 0 {
			var next uint64
			if last <= step {
				next = 0
			} else {
				next = last - step
			}
			if !seen[next] {
				seen[next] = true
				if hash, err := hashAt(next); err == nil {
					entries = append(entries, LocatorEntry{Height: next, Hash: consensus.HashToHex(hash)})
				}
			}
			last = next
			step *= 2
			if next == 0 {
				break
			}
		}
	}

	// Always include genesis if not already present.
	if len(entries) == 0 || entries[len(entries)-1].Height != 0 {
		if hash, err := hashAt(0); err == nil {
			entries = append(entries, LocatorEntry{Height: 0, Hash: consensus.HashToHex(hash)})
		}
	}

	return LocatorRequest{Entries: entries}
}

func addrHex(addr [20]byte) string {
	const hextable = "0123456789abcdef"
	buf := make([]byte, 40)
	for i, b := range addr {
		buf[i*2] = hextable[b>>4]
		buf[i*2+1] = hextable[b&0xf]
	}
	return string(buf)
}
