package consensus

import (
	"fmt"
	"math/big"
)

// PoWEngine is the proof-of-work abstraction. The consensus package defines
// the interface; cmd/chakram provides the RandomX implementation.
type PoWEngine interface {
	// Init prepares the engine for the given epoch key. No-op when the key
	// matches the last call (epoch boundaries are infrequent).
	Init(key []byte) error
	// Hash returns the 32-byte RandomX digest of input.
	Hash(input []byte) []byte
	// Close releases all engine resources.
	Close()
}

// RandomX epoch constants.
const (
	RandomXEpochLen uint64 = 2048 // blocks per epoch (~8.5 h at 15 s)
	RandomXKeyLag   uint64 = 64   // blocks of lag; gives miners ~16 min of lead time
)

// EpochKey derives the 32-byte RandomX epoch key for a block at height.
// The key is the consensus block hash at keyHeight = max(0, epochStart - RandomXKeyLag).
// blockHashAt must return the [32]byte hash of the consensus block at the given height.
func EpochKey(height uint64, blockHashAt func(uint64) ([32]byte, error)) ([]byte, error) {
	epochNum := height / RandomXEpochLen
	epochStart := epochNum * RandomXEpochLen
	keyHeight := uint64(0)
	if epochStart > RandomXKeyLag {
		keyHeight = epochStart - RandomXKeyLag
	}
	h, err := blockHashAt(keyHeight)
	if err != nil {
		return nil, fmt.Errorf("epochKey: get hash at height %d: %w", keyHeight, err)
	}
	key := make([]byte, 32)
	copy(key, h[:])
	return key, nil
}

// HashMeetsTarget reports whether the 32-byte RandomX output satisfies the
// compact difficulty target encoded in header.Difficulty.
func HashMeetsTarget(hash [32]byte, compactTarget uint64) bool {
	target := DecodeCompact(compactTarget)
	hashInt := new(big.Int).SetBytes(hash[:])
	return hashInt.Cmp(target) < 0
}

// DecodeCompact decodes a compact uint64 target: upper 8 bits = byte-length of
// mantissa, lower 56 bits = mantissa. Returns the target as a big.Int.
// Identical to v1.1's decodeCompact.
func DecodeCompact(c uint64) *big.Int {
	byteLen := c >> 56
	mantissa := c & 0x00FFFFFFFFFFFFFF
	t := new(big.Int).SetUint64(mantissa)
	if byteLen > 7 {
		t.Lsh(t, uint(8*(byteLen-7)))
	} else if byteLen < 7 {
		t.Rsh(t, uint(8*(7-byteLen)))
	}
	return t
}

// EncodeCompact encodes a big.Int target into the compact uint64 format.
// Identical to v1.1's encodeCompact.
func EncodeCompact(t *big.Int) uint64 {
	b := t.Bytes()
	byteLen := uint64(len(b))
	if byteLen == 0 {
		return 0
	}
	var mantissa uint64
	if byteLen >= 7 {
		mantissa = new(big.Int).Rsh(t, uint(8*(byteLen-7))).Uint64()
	} else {
		mantissa = new(big.Int).Lsh(t, uint(8*(7-byteLen))).Uint64()
	}
	mantissa &= 0x00FFFFFFFFFFFFFF
	return (byteLen << 56) | mantissa
}
