package consensus

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	badger "github.com/dgraph-io/badger/v4"
)

// Key scheme:
//   block:hash:<hex>   → consBlockJSON (header + hash)
//   block:height:<8B>  → 32-byte consensus block hash at that height
//   meta:tip           → 32-byte consensus block hash
//   meta:height        → 8-byte big-endian height

var (
	keyMetaTip    = []byte("meta:tip")
	keyMetaHeight = []byte("meta:height")
)

func blockHashKey(hash [32]byte) []byte {
	return []byte("block:hash:" + HashToHex(hash))
}

func blockHeightKey(height uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], height)
	return append([]byte("block:height:"), buf[:]...)
}

func blockPayloadKey(hash [32]byte) []byte {
	return []byte("block:payload:" + HashToHex(hash))
}

// consBlockJSON is the on-disk representation of a ConsensusBlock.
// The full execution payload is NOT stored here — geth owns it.
type consBlockJSON struct {
	Version              uint32 `json:"version"`
	Height               uint64 `json:"height"`
	ParentHash           string `json:"parent_hash"`
	ExecutionPayloadHash string `json:"execution_payload_hash"`
	Timestamp            int64  `json:"timestamp"`
	MinerAddress         string `json:"miner_address"`
	Difficulty           uint64 `json:"difficulty"`
	Nonce                uint64 `json:"nonce"`
	Hash                 string `json:"hash"`
}

func headerToJSON(b *ConsensusBlock) consBlockJSON {
	addr := b.Header.MinerAddress
	return consBlockJSON{
		Version:              b.Header.Version,
		Height:               b.Header.Height,
		ParentHash:           HashToHex(b.Header.ParentHash),
		ExecutionPayloadHash: HashToHex(b.Header.ExecutionPayloadHash),
		Timestamp:            b.Header.Timestamp,
		MinerAddress:         "0x" + fmt.Sprintf("%x", addr),
		Difficulty:           b.Header.Difficulty,
		Nonce:                b.Header.Nonce,
		Hash:                 HashToHex(b.Hash),
	}
}

func headerFromJSON(j consBlockJSON) (*ConsensusBlock, error) {
	parent, err := HashFromHex(j.ParentHash)
	if err != nil {
		return nil, fmt.Errorf("parent_hash: %w", err)
	}
	execHash, err := HashFromHex(j.ExecutionPayloadHash)
	if err != nil {
		return nil, fmt.Errorf("execution_payload_hash: %w", err)
	}
	addr, err := AddrFromHex(j.MinerAddress)
	if err != nil {
		return nil, fmt.Errorf("miner_address: %w", err)
	}
	hash, err := HashFromHex(j.Hash)
	if err != nil {
		return nil, fmt.Errorf("hash: %w", err)
	}
	return &ConsensusBlock{
		Header: ConsensusBlockHeader{
			Version:              j.Version,
			Height:               j.Height,
			ParentHash:           parent,
			ExecutionPayloadHash: execHash,
			Timestamp:            j.Timestamp,
			MinerAddress:         addr,
			Difficulty:           j.Difficulty,
			Nonce:                j.Nonce,
		},
		Hash: hash,
	}, nil
}

// Store persists consensus blocks and the chain tip in BadgerDB.
// It is safe to use from multiple goroutines.
type Store struct {
	db *badger.DB
}

// OpenStore opens (or creates) the consensus store at dataDir/consensus.
func OpenStore(dataDir string) (*Store, error) {
	opts := badger.DefaultOptions(dataDir + "/consensus").
		WithLogger(nil). // silence badger's own logging
		WithNumVersionsToKeep(1)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open consensus store: %w", err)
	}
	return &Store{db: db}, nil
}

// Close flushes and closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// StoreBlock writes the block atomically: persists the block JSON, the height
// index, and updates the chain tip — all in one Badger transaction.
func (s *Store) StoreBlock(b *ConsensusBlock) error {
	data, err := json.Marshal(headerToJSON(b))
	if err != nil {
		return fmt.Errorf("marshal block: %w", err)
	}
	var heightBuf [8]byte
	binary.BigEndian.PutUint64(heightBuf[:], b.Header.Height)

	return s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(blockHashKey(b.Hash), data); err != nil {
			return err
		}
		if err := txn.Set(blockHeightKey(b.Header.Height), b.Hash[:]); err != nil {
			return err
		}
		if err := txn.Set(keyMetaTip, b.Hash[:]); err != nil {
			return err
		}
		return txn.Set(keyMetaHeight, heightBuf[:])
	})
}

// GetBlock retrieves the consensus block with the given hash.
func (s *Store) GetBlock(hash [32]byte) (*ConsensusBlock, error) {
	var data []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(blockHashKey(hash))
		if err != nil {
			return err
		}
		data, err = item.ValueCopy(nil)
		return err
	})
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrBlockNotFound, HashToHex(hash))
		}
		return nil, err
	}
	var j consBlockJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("unmarshal block: %w", err)
	}
	return headerFromJSON(j)
}

// GetBlockByHeight retrieves the consensus block at the given canonical height.
func (s *Store) GetBlockByHeight(height uint64) (*ConsensusBlock, error) {
	var hash [32]byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(blockHeightKey(height))
		if err != nil {
			return err
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		if len(val) != 32 {
			return fmt.Errorf("height index corrupt: expected 32 bytes, got %d", len(val))
		}
		copy(hash[:], val)
		return nil
	})
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, fmt.Errorf("no block at height %d", height)
		}
		return nil, err
	}
	return s.GetBlock(hash)
}

// GetTip returns (hash, height) of the current chain tip.
// Returns ErrTipNotFound if no tip has been stored yet (fresh DB).
var ErrTipNotFound = errors.New("consensus store: no chain tip")

// ErrBlockNotFound is returned by GetBlock and GetBlockByHeight when the
// requested block is not in the store.
var ErrBlockNotFound = errors.New("consensus store: block not found")

func (s *Store) GetTip() ([32]byte, uint64, error) {
	var hash [32]byte
	var height uint64
	err := s.db.View(func(txn *badger.Txn) error {
		// hash
		item, err := txn.Get(keyMetaTip)
		if err != nil {
			return err
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		if len(val) != 32 {
			return fmt.Errorf("meta:tip corrupt")
		}
		copy(hash[:], val)

		// height
		item, err = txn.Get(keyMetaHeight)
		if err != nil {
			return err
		}
		val, err = item.ValueCopy(nil)
		if err != nil {
			return err
		}
		if len(val) != 8 {
			return fmt.Errorf("meta:height corrupt")
		}
		height = binary.BigEndian.Uint64(val)
		return nil
	})
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return [32]byte{}, 0, ErrTipNotFound
		}
		return [32]byte{}, 0, err
	}
	return hash, height, nil
}

// HeaderFetcher returns a BlockHeaderFetcher backed by this store, used by
// the difficulty and MTP functions.
func (s *Store) HeaderFetcher() BlockHeaderFetcher {
	return func(height uint64) (ConsensusBlockHeader, error) {
		b, err := s.GetBlockByHeight(height)
		if err != nil {
			return ConsensusBlockHeader{}, err
		}
		return b.Header, nil
	}
}

// StoreBlockOnly persists the block by hash but does NOT update the canonical
// tip or the height index. Used for Phase 3 to store fork/received blocks
// before deciding whether they become the new canonical head.
func (s *Store) StoreBlockOnly(b *ConsensusBlock) error {
	data, err := json.Marshal(headerToJSON(b))
	if err != nil {
		return fmt.Errorf("marshal block: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(blockHashKey(b.Hash), data)
	})
}

// SetTip explicitly updates the canonical chain tip and the height index.
// Used after a reorg: call StoreBlockOnly for all new-chain blocks first,
// then call SetTip for the new head.
func (s *Store) SetTip(hash [32]byte, height uint64) error {
	var heightBuf [8]byte
	binary.BigEndian.PutUint64(heightBuf[:], height)
	return s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(blockHeightKey(height), hash[:]); err != nil {
			return err
		}
		if err := txn.Set(keyMetaTip, hash[:]); err != nil {
			return err
		}
		return txn.Set(keyMetaHeight, heightBuf[:])
	})
}

// StorePayload persists the raw execution payload JSON for the block identified
// by hash. Called when a block is first mined or received so peers can fetch it.
func (s *Store) StorePayload(hash [32]byte, payload []byte) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(blockPayloadKey(hash), payload)
	})
}

// GetPayload retrieves the raw execution payload JSON for the given block hash.
func (s *Store) GetPayload(hash [32]byte) ([]byte, error) {
	var data []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(blockPayloadKey(hash))
		if err != nil {
			return err
		}
		data, err = item.ValueCopy(nil)
		return err
	})
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, fmt.Errorf("payload for block %s not found", HashToHex(hash))
		}
		return nil, err
	}
	return data, nil
}

// BlockHashAt returns a function compatible with EpochKey that looks up block
// hashes by canonical height.
func (s *Store) BlockHashAt() func(uint64) ([32]byte, error) {
	return func(height uint64) ([32]byte, error) {
		b, err := s.GetBlockByHeight(height)
		if err != nil {
			return [32]byte{}, err
		}
		return b.Hash, nil
	}
}
