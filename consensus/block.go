// Package consensus defines the Chakram 2.0 consensus-layer block types and
// the canonical 120-byte header serialization that RandomX hashes over.
package consensus

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// HeaderSize is the exact byte length of the serialized ConsensusBlockHeader.
	HeaderSize = 120

	// GenesisVersion is the protocol version written into the genesis block.
	GenesisVersion uint32 = 2
)

// ConsensusBlockHeader is the 120-byte header that RandomX hashes (§1.3 of
// docs/spec.md). The order of fields matches the serialization order exactly.
type ConsensusBlockHeader struct {
	Version             uint32   // 4 bytes LE
	Height              uint64   // 8 bytes LE
	ParentHash          [32]byte // 32 bytes
	ExecutionPayloadHash [32]byte // 32 bytes — geth ExecutionPayloadV3.blockHash
	Timestamp           int64    // 8 bytes LE (cast to uint64)
	MinerAddress        [20]byte // 20 bytes
	Difficulty          uint64   // 8 bytes LE — compact target (v1.1 encoding)
	Nonce               uint64   // 8 bytes LE
}

// Serialize returns the canonical 120-byte little-endian serialization used
// as RandomX input. The output is deterministic for a given header.
func (h *ConsensusBlockHeader) Serialize() [HeaderSize]byte {
	var buf [HeaderSize]byte
	off := 0

	binary.LittleEndian.PutUint32(buf[off:], h.Version)
	off += 4

	binary.LittleEndian.PutUint64(buf[off:], h.Height)
	off += 8

	copy(buf[off:], h.ParentHash[:])
	off += 32

	copy(buf[off:], h.ExecutionPayloadHash[:])
	off += 32

	binary.LittleEndian.PutUint64(buf[off:], uint64(h.Timestamp))
	off += 8

	copy(buf[off:], h.MinerAddress[:])
	off += 20

	binary.LittleEndian.PutUint64(buf[off:], h.Difficulty)
	off += 8

	binary.LittleEndian.PutUint64(buf[off:], h.Nonce)
	off += 8

	// Compile-time guard expressed at runtime during testing.
	if off != HeaderSize {
		panic(fmt.Sprintf("consensus: Serialize wrote %d bytes, want %d", off, HeaderSize))
	}

	return buf
}

// ConsensusBlock is the full block gossiped over the Chakram P2P layer.
// ExecutionPayload is stored as raw JSON (the verbatim geth response) so it
// can be forwarded to peers and to geth without re-encoding.
type ConsensusBlock struct {
	Header          ConsensusBlockHeader
	ExecutionPayload []byte // raw JSON of ExecutionPayloadV3
	Hash            [32]byte
}

// GenesisHash computes the genesis block hash as SHA256(SHA256(header bytes)).
// All other blocks are hashed by the RandomX engine (pow.go).
func GenesisHash(h *ConsensusBlockHeader) [32]byte {
	serialized := h.Serialize()
	first := sha256.Sum256(serialized[:])
	return sha256.Sum256(first[:])
}

// HashFromHex decodes a 0x-prefixed or bare 64-char hex string into a [32]byte.
func HashFromHex(s string) ([32]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		return [32]byte{}, err
	}
	if len(b) != 32 {
		return [32]byte{}, fmt.Errorf("consensus: hash must be 32 bytes, got %d", len(b))
	}
	var out [32]byte
	copy(out[:], b)
	return out, nil
}

// HashToHex encodes a [32]byte as a 0x-prefixed lowercase hex string.
func HashToHex(h [32]byte) string {
	return "0x" + hex.EncodeToString(h[:])
}

// AddrFromHex decodes a 0x-prefixed or bare 40-char hex string into a [20]byte.
func AddrFromHex(s string) ([20]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		return [20]byte{}, err
	}
	if len(b) != 20 {
		return [20]byte{}, fmt.Errorf("consensus: address must be 20 bytes, got %d", len(b))
	}
	var out [20]byte
	copy(out[:], b)
	return out, nil
}
