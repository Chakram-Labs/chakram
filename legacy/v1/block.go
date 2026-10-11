// block.go — Block and BlockHeader data structures for the Chakram blockchain.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"time"
)

// ComputeMerkleRoot builds a Merkle tree over the transaction IDs and returns
// the 32-byte root. Pairs are combined as SHA256(SHA256(left‖right)).
// An odd number of leaves duplicates the last leaf (standard Merkle behaviour).
func ComputeMerkleRoot(txs []*Transaction) []byte {
	if len(txs) == 0 {
		return make([]byte, 32)
	}
	hashes := make([][]byte, len(txs))
	for i, tx := range txs {
		hashes[i] = tx.TxID
	}
	for len(hashes) > 1 {
		if len(hashes)%2 != 0 {
			hashes = append(hashes, hashes[len(hashes)-1])
		}
		next := make([][]byte, len(hashes)/2)
		for i := 0; i < len(hashes); i += 2 {
			combined := append(hashes[i], hashes[i+1]...)
			h := sha256.Sum256(combined)
			h2 := sha256.Sum256(h[:])
			next[i/2] = h2[:]
		}
		hashes = next
	}
	return hashes[0]
}

// BlockHeader contains the metadata committed to by the block hash.
type BlockHeader struct {
	Version      uint32
	Height       uint64
	PreviousHash []byte // 32 bytes: hash of the preceding block header
	MerkleRoot   []byte // 32 bytes: merkle root of all transactions in this block
	Timestamp    int64  // unix seconds
	Difficulty   uint64 // current proof-of-work difficulty target
	Nonce        uint64 // miners increment this until HashIsValid returns true
}

// Block is a full block: header, transactions, and the block's own hash.
type Block struct {
	Header       BlockHeader
	Transactions []*Transaction
	Hash         []byte // 32 bytes, set after mining via SetHash
}

// NewBlock creates a new unmined block with header fields populated and
// Timestamp set to the current time. The caller must mine (increment Nonce
// and call SetHash) before the block can be added to the chain.
func NewBlock(previousHash []byte, height uint64, difficulty uint64, transactions []*Transaction) *Block {
	b := &Block{
		Header: BlockHeader{
			Version:      ProtocolVersion,
			Height:       height,
			PreviousHash: previousHash,
			MerkleRoot:   ComputeMerkleRoot(transactions),
			Timestamp:    time.Now().Unix(),
			Difficulty:   difficulty,
			Nonce:        0,
		},
		Transactions: transactions,
		Hash:         make([]byte, 32),
	}
	return b
}

// ComputeHash serialises every field of the block header in little-endian order
// and returns SHA256(SHA256(data)) — the same double-hash scheme Bitcoin uses.
// NOTE: this is used only for the genesis block, which is hashed before RandomX
// is initialised. All subsequent blocks are hashed via MineBlock in pow.go.
func (b *Block) ComputeHash() []byte {
	buf := new(bytes.Buffer)

	binary.Write(buf, binary.LittleEndian, b.Header.Version)
	binary.Write(buf, binary.LittleEndian, b.Header.Height)
	buf.Write(b.Header.PreviousHash)
	buf.Write(b.Header.MerkleRoot)
	binary.Write(buf, binary.LittleEndian, b.Header.Timestamp)
	binary.Write(buf, binary.LittleEndian, b.Header.Difficulty)
	binary.Write(buf, binary.LittleEndian, b.Header.Nonce)

	first := sha256.Sum256(buf.Bytes())
	second := sha256.Sum256(first[:])
	return second[:]
}

// SetHash computes the block's hash from the current header state and stores it
// in b.Hash. Call this after each Nonce increment during mining.
func (b *Block) SetHash() {
	b.Hash = b.ComputeHash()
}

// decodeCompact unpacks a compact uint64 PoW target (protocol v2+) to a *big.Int.
// Upper 8 bits = byte-length of the target value; lower 56 bits = mantissa.
func decodeCompact(compact uint64) *big.Int {
	byteLen := int(compact >> 56)
	mantissa := compact & 0x00FFFFFFFFFFFFFF
	result := new(big.Int).SetUint64(mantissa)
	if byteLen > 7 {
		result.Lsh(result, uint(8*(byteLen-7)))
	} else if byteLen < 7 {
		result.Rsh(result, uint(8*(7-byteLen)))
	}
	return result
}

// encodeCompact packs a *big.Int target into compact uint64 form.
func encodeCompact(target *big.Int) uint64 {
	if target.Sign() <= 0 {
		return 0
	}
	byteLen := (target.BitLen() + 7) / 8
	if byteLen == 0 {
		return 0
	}
	var mantissa uint64
	if byteLen >= 7 {
		tmp := new(big.Int).Rsh(target, uint(8*(byteLen-7)))
		mantissa = tmp.Uint64() & 0x00FFFFFFFFFFFFFF
	} else {
		tmp := new(big.Int).Lsh(target, uint(8*(7-byteLen)))
		mantissa = tmp.Uint64() & 0x00FFFFFFFFFFFFFF
	}
	return (uint64(byteLen) << 56) | mantissa
}

// HashIsValid reports whether the block's hash meets the proof-of-work target.
// Protocol v1: target = 2^(256−difficulty)  (integer bit-count difficulty).
// Protocol v2: target = decodeCompact(difficulty)  (continuous compact target).
func (b *Block) HashIsValid() bool {
	var target *big.Int
	if ProtocolVersionAt(b.Header.Height) >= 2 {
		target = decodeCompact(b.Header.Difficulty)
	} else {
		target = new(big.Int).Lsh(big.NewInt(1), uint(256-b.Header.Difficulty))
	}
	hashInt := new(big.Int).SetBytes(b.Hash)
	return hashInt.Cmp(target) < 0
}

// NewGenesisBlock creates the genesis block — the immutable first block of the
// Chakram chain. The genesis coinbase is built via NewCoinbaseTransaction so
// that CoinbaseData encodes height 0 and a unique TxID is guaranteed.
// PreviousHash is 32 zero bytes (no parent exists).
func NewGenesisBlock() *Block {
	zeroPKH := make([]byte, 20) // genesis reward is unspendable (zero public-key hash)
	cb := NewCoinbaseTransaction(zeroPKH, 0, 0)
	txs := []*Transaction{cb}
	genesis := &Block{
		Header: BlockHeader{
			Version:      ProtocolVersion,
			Height:       0,
			PreviousHash: make([]byte, 32), // all zeros: no previous block
			MerkleRoot:   ComputeMerkleRoot(txs),
			Timestamp:    GenesisTimestamp,
			Difficulty:   InitialCompactTargetV2, // v2 active from height 0
			Nonce:        0,
		},
		Transactions: txs,
		Hash:         make([]byte, 32),
	}
	genesis.SetHash()
	return genesis
}
