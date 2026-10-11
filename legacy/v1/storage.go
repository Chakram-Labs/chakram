// storage.go — The entire storage layer for the Chakram blockchain.
// All reads and writes to BadgerDB go through this file.
// Nothing else in the codebase should import or touch BadgerDB directly.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	badger "github.com/dgraph-io/badger/v4"
)

// ── Key scheme ────────────────────────────────────────────────────────────────
//
//  block:hash:<hex-hash>          — full block JSON, keyed by 32-byte hash
//  block:height:<8-byte-BE-uint>  — 32-byte hash of block at that height
//  meta:tip                       — 32-byte hash of the current chain tip
//  meta:height                    — 8-byte big-endian current chain height
//  utxo:<txid>:<index>            — reserved for Phase 3 UTXO set

var (
	keyMetaTip    = []byte("meta:tip")
	keyMetaHeight = []byte("meta:height")
)

func blockHashKey(hash []byte) []byte {
	return []byte("block:hash:" + hex.EncodeToString(hash))
}

func blockHeightKey(height uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], height)
	return append([]byte("block:height:"), buf[:]...)
}

func blockUndoKey(hash []byte) []byte {
	return []byte("block:undo:" + hex.EncodeToString(hash))
}

// ── Storage-local DTOs ────────────────────────────────────────────────────────
// All []byte fields are stored as hex strings for human-readable JSON.

type txInputJSON struct {
	TxID        string `json:"txid"`
	OutputIndex uint32 `json:"output_index"`
	Signature   string `json:"signature"`
	PublicKey   string `json:"public_key"`
}

type txOutputJSON struct {
	Value      uint64 `json:"value"`
	PubKeyHash string `json:"pub_key_hash"`
}

type txFullJSON struct {
	TxID         string         `json:"txid"`
	Inputs       []txInputJSON  `json:"inputs"`
	Outputs      []txOutputJSON `json:"outputs"`
	Timestamp    int64          `json:"timestamp"`
	IsCoinbase   bool           `json:"is_coinbase"`
	CoinbaseData string         `json:"coinbase_data,omitempty"` // hex-encoded; only set for coinbase txs
}

type headerJSON struct {
	Version      uint32 `json:"version"`
	Height       uint64 `json:"height"`
	PreviousHash string `json:"previous_hash"`
	MerkleRoot   string `json:"merkle_root"`
	Timestamp    int64  `json:"timestamp"`
	Difficulty   uint64 `json:"difficulty"`
	Nonce        uint64 `json:"nonce"`
}

type blockJSON struct {
	Header       headerJSON   `json:"header"`
	Transactions []txFullJSON `json:"transactions"`
	Hash         string       `json:"hash"`
}

func txToFullJSON(tx *Transaction) txFullJSON {
	inputs := make([]txInputJSON, len(tx.Inputs))
	for i, in := range tx.Inputs {
		inputs[i] = txInputJSON{
			TxID:        hex.EncodeToString(in.TxID),
			OutputIndex: in.OutputIndex,
			Signature:   hex.EncodeToString(in.Signature),
			PublicKey:   hex.EncodeToString(in.PublicKey),
		}
	}
	outputs := make([]txOutputJSON, len(tx.Outputs))
	for i, out := range tx.Outputs {
		outputs[i] = txOutputJSON{
			Value:      out.Value,
			PubKeyHash: hex.EncodeToString(out.PublicKeyHash),
		}
	}
	return txFullJSON{
		TxID:         hex.EncodeToString(tx.TxID),
		Inputs:       inputs,
		Outputs:      outputs,
		Timestamp:    tx.Timestamp,
		IsCoinbase:   tx.IsCoinbase,
		CoinbaseData: hex.EncodeToString(tx.CoinbaseData),
	}
}

func txFromFullJSON(tj txFullJSON) (*Transaction, error) {
	txID, err := hex.DecodeString(tj.TxID)
	if err != nil {
		return nil, fmt.Errorf("decode txid: %w", err)
	}
	inputs := make([]TxInput, len(tj.Inputs))
	for i, inj := range tj.Inputs {
		inTxID, _ := hex.DecodeString(inj.TxID)
		sig, _ := hex.DecodeString(inj.Signature)
		pk, _ := hex.DecodeString(inj.PublicKey)
		inputs[i] = TxInput{
			TxID:        inTxID,
			OutputIndex: inj.OutputIndex,
			Signature:   sig,
			PublicKey:   pk,
		}
	}
	outputs := make([]TxOutput, len(tj.Outputs))
	for i, outj := range tj.Outputs {
		pkh, _ := hex.DecodeString(outj.PubKeyHash)
		outputs[i] = TxOutput{
			Value:         outj.Value,
			PublicKeyHash: pkh,
		}
	}
	cbData, _ := hex.DecodeString(tj.CoinbaseData)
	return &Transaction{
		TxID:         txID,
		Inputs:       inputs,
		Outputs:      outputs,
		Timestamp:    tj.Timestamp,
		IsCoinbase:   tj.IsCoinbase,
		CoinbaseData: cbData,
	}, nil
}

func blockToJSON(b *Block) blockJSON {
	txs := make([]txFullJSON, len(b.Transactions))
	for i, tx := range b.Transactions {
		txs[i] = txToFullJSON(tx)
	}
	return blockJSON{
		Header: headerJSON{
			Version:      b.Header.Version,
			Height:       b.Header.Height,
			PreviousHash: hex.EncodeToString(b.Header.PreviousHash),
			MerkleRoot:   hex.EncodeToString(b.Header.MerkleRoot),
			Timestamp:    b.Header.Timestamp,
			Difficulty:   b.Header.Difficulty,
			Nonce:        b.Header.Nonce,
		},
		Transactions: txs,
		Hash:         hex.EncodeToString(b.Hash),
	}
}

func blockFromJSON(bj blockJSON) (*Block, error) {
	prevHash, err := hex.DecodeString(bj.Header.PreviousHash)
	if err != nil {
		return nil, fmt.Errorf("decode previous_hash: %w", err)
	}
	merkle, err := hex.DecodeString(bj.Header.MerkleRoot)
	if err != nil {
		return nil, fmt.Errorf("decode merkle_root: %w", err)
	}
	hash, err := hex.DecodeString(bj.Hash)
	if err != nil {
		return nil, fmt.Errorf("decode hash: %w", err)
	}
	txs := make([]*Transaction, len(bj.Transactions))
	for i, tj := range bj.Transactions {
		tx, err := txFromFullJSON(tj)
		if err != nil {
			return nil, fmt.Errorf("tx[%d]: %w", i, err)
		}
		txs[i] = tx
	}
	return &Block{
		Header: BlockHeader{
			Version:      bj.Header.Version,
			Height:       bj.Header.Height,
			PreviousHash: prevHash,
			MerkleRoot:   merkle,
			Timestamp:    bj.Header.Timestamp,
			Difficulty:   bj.Header.Difficulty,
			Nonce:        bj.Header.Nonce,
		},
		Transactions: txs,
		Hash:         hash,
	}, nil
}

// ── Storage ───────────────────────────────────────────────────────────────────

// Storage is the single access point for all on-disk blockchain data.
type Storage struct {
	DB *badger.DB
}

// NewStorage opens (or creates) a BadgerDB database at the given directory path.
// The caller is responsible for calling Close when done.
func NewStorage(path string) (*Storage, error) {
	opts := badger.DefaultOptions(path).
		WithLogger(nil).
		WithValueLogFileSize(64 << 20) // 64 MB per vlog file (default is 1 GB)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open badger at %s: %w", path, err)
	}
	s := &Storage{DB: db}
	go s.runGC()
	return s, nil
}

// runGC periodically reclaims space from BadgerDB's append-only value log.
// Without this, deleted keys (spent UTXOs, old blocks) occupy disk forever.
func (s *Storage) runGC() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		for {
			if err := s.DB.RunValueLogGC(0.5); err != nil {
				break // ErrNoRewrite means nothing left to reclaim
			}
		}
	}
}

// Close flushes pending writes and cleanly shuts down the BadgerDB instance.
// Always defer this after a successful NewStorage call.
func (s *Storage) Close() error {
	return s.DB.Close()
}

// SaveBlock serialises b to JSON and persists it under two keys.
// Used only for the genesis block; all other blocks should use SaveBlockData
// followed by UpdateHeightIndex once the block is on the main chain.
func (s *Storage) SaveBlock(b *Block) error {
	data, err := json.Marshal(blockToJSON(b))
	if err != nil {
		return fmt.Errorf("marshal block: %w", err)
	}
	return s.DB.Update(func(txn *badger.Txn) error {
		if err := txn.Set(blockHashKey(b.Hash), data); err != nil {
			return err
		}
		return txn.Set(blockHeightKey(b.Header.Height), b.Hash)
	})
}

// SaveBlockData stores a block by its hash only, without updating the height
// index. Call this for all received blocks (main chain and side chain alike);
// call UpdateHeightIndex separately once the block is confirmed on the main chain.
func (s *Storage) SaveBlockData(b *Block) error {
	data, err := json.Marshal(blockToJSON(b))
	if err != nil {
		return fmt.Errorf("marshal block: %w", err)
	}
	return s.DB.Update(func(txn *badger.Txn) error {
		return txn.Set(blockHashKey(b.Hash), data)
	})
}

// UpdateHeightIndex maps height → block hash for the main chain.
// Call after a block is accepted onto the main chain (or after a reorg).
func (s *Storage) UpdateHeightIndex(b *Block) error {
	return s.DB.Update(func(txn *badger.Txn) error {
		return txn.Set(blockHeightKey(b.Header.Height), b.Hash)
	})
}

// ── Undo data storage ─────────────────────────────────────────────────────────

// blockUndoDTO mirrors BlockUndo for JSON serialisation in storage.
type undoUTXOJSON struct {
	TxID        string `json:"txid"`
	OutputIndex uint32 `json:"output_index"`
	Value       uint64 `json:"value"`
	PubKeyHash  string `json:"pub_key_hash"`
	BlockHeight uint64 `json:"block_height"`
	IsCoinbase  bool   `json:"is_coinbase"`
}

type undoRefJSON struct {
	TxID        string `json:"txid"`
	OutputIndex uint32 `json:"output_index"`
}

type blockUndoDTO struct {
	SpentUTXOs  []undoUTXOJSON `json:"spent_utxos"`
	CreatedRefs []undoRefJSON  `json:"created_refs"`
}

// SaveBlockUndo serialises and stores the undo record for a main-chain block.
func (s *Storage) SaveBlockUndo(blockHash []byte, undo *BlockUndo) error {
	spent := make([]undoUTXOJSON, len(undo.SpentUTXOs))
	for i, u := range undo.SpentUTXOs {
		spent[i] = undoUTXOJSON{
			TxID:        hex.EncodeToString(u.TxID),
			OutputIndex: u.OutputIndex,
			Value:       u.Value,
			PubKeyHash:  hex.EncodeToString(u.PublicKeyHash),
			BlockHeight: u.BlockHeight,
			IsCoinbase:  u.IsCoinbase,
		}
	}
	refs := make([]undoRefJSON, len(undo.CreatedRefs))
	for i, r := range undo.CreatedRefs {
		refs[i] = undoRefJSON{
			TxID:        hex.EncodeToString(r.TxID),
			OutputIndex: r.OutputIndex,
		}
	}
	data, err := json.Marshal(blockUndoDTO{SpentUTXOs: spent, CreatedRefs: refs})
	if err != nil {
		return fmt.Errorf("marshal undo: %w", err)
	}
	return s.DB.Update(func(txn *badger.Txn) error {
		return txn.Set(blockUndoKey(blockHash), data)
	})
}

// GetBlockUndo retrieves the undo record for the block with the given hash.
func (s *Storage) GetBlockUndo(blockHash []byte) (*BlockUndo, error) {
	var dto blockUndoDTO
	err := s.DB.View(func(txn *badger.Txn) error {
		item, err := txn.Get(blockUndoKey(blockHash))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &dto)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("get block undo: %w", err)
	}
	spent := make([]UTXO, len(dto.SpentUTXOs))
	for i, u := range dto.SpentUTXOs {
		txID, _ := hex.DecodeString(u.TxID)
		pkh, _ := hex.DecodeString(u.PubKeyHash)
		spent[i] = UTXO{
			TxID:          txID,
			OutputIndex:   u.OutputIndex,
			Value:         u.Value,
			PublicKeyHash: pkh,
			BlockHeight:   u.BlockHeight,
			IsCoinbase:    u.IsCoinbase,
		}
	}
	refs := make([]UTXORef, len(dto.CreatedRefs))
	for i, r := range dto.CreatedRefs {
		txID, _ := hex.DecodeString(r.TxID)
		refs[i] = UTXORef{TxID: txID, OutputIndex: r.OutputIndex}
	}
	return &BlockUndo{SpentUTXOs: spent, CreatedRefs: refs}, nil
}

// GetBlockByHash retrieves and deserialises the block stored under the given hash.
// Returns badger.ErrKeyNotFound (wrapped) if no such block exists.
func (s *Storage) GetBlockByHash(hash []byte) (*Block, error) {
	var b *Block
	err := s.DB.View(func(txn *badger.Txn) error {
		item, err := txn.Get(blockHashKey(hash))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			var bj blockJSON
			if err := json.Unmarshal(val, &bj); err != nil {
				return err
			}
			b, err = blockFromJSON(bj)
			return err
		})
	})
	if err != nil {
		return nil, fmt.Errorf("get block by hash: %w", err)
	}
	return b, nil
}

// GetBlockByHeight looks up the hash stored at the given height, then fetches
// the full block by that hash.
func (s *Storage) GetBlockByHeight(height uint64) (*Block, error) {
	var hash []byte
	err := s.DB.View(func(txn *badger.Txn) error {
		item, err := txn.Get(blockHeightKey(height))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			hash = make([]byte, len(val))
			copy(hash, val)
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("get hash at height %d: %w", height, err)
	}
	return s.GetBlockByHash(hash)
}

// SaveChainTip atomically persists the current tip hash and chain height.
// Both values are written in a single BadgerDB transaction so they stay consistent.
func (s *Storage) SaveChainTip(hash []byte, height uint64) error {
	var heightBuf [8]byte
	binary.BigEndian.PutUint64(heightBuf[:], height)

	return s.DB.Update(func(txn *badger.Txn) error {
		if err := txn.Set(keyMetaTip, hash); err != nil {
			return err
		}
		return txn.Set(keyMetaHeight, heightBuf[:])
	})
}

// GetChainTip returns the hash and height of the current best chain tip.
// Returns an error if the tip has not been initialised yet (fresh database).
func (s *Storage) GetChainTip() (hash []byte, height uint64, err error) {
	err = s.DB.View(func(txn *badger.Txn) error {
		// tip hash
		item, err := txn.Get(keyMetaTip)
		if err != nil {
			return fmt.Errorf("meta:tip not found: %w", err)
		}
		if err = item.Value(func(val []byte) error {
			hash = make([]byte, len(val))
			copy(hash, val)
			return nil
		}); err != nil {
			return err
		}

		// tip height
		item, err = txn.Get(keyMetaHeight)
		if err != nil {
			return fmt.Errorf("meta:height not found: %w", err)
		}
		return item.Value(func(val []byte) error {
			if len(val) < 8 {
				return fmt.Errorf("meta:height value too short")
			}
			height = binary.BigEndian.Uint64(val)
			return nil
		})
	})
	return hash, height, err
}

// HasBlock reports whether a block with the given hash is already stored.
// Used by the network layer to skip duplicate block processing.
func (s *Storage) HasBlock(hash []byte) bool {
	err := s.DB.View(func(txn *badger.Txn) error {
		_, err := txn.Get(blockHashKey(hash))
		return err
	})
	return err == nil
}

// ── UTXO storage ─────────────────────────────────────────────────────────────
//
// Address index key scheme:
//   aidx:<pubKeyHash_hex>:<txid_hex>:<outputIndex>
// Value is empty — all information is in the key.

func addressIndexKey(pubKeyHash, txID []byte, outputIndex uint32) string {
	return fmt.Sprintf("aidx:%s:%s:%d", hex.EncodeToString(pubKeyHash), hex.EncodeToString(txID), outputIndex)
}

// SaveUTXOAndIndex stores the UTXO data and writes the corresponding address
// index entry in a single atomic transaction.
func (s *Storage) SaveUTXOAndIndex(key string, data []byte, pubKeyHash, txID []byte, outputIndex uint32) error {
	idxKey := addressIndexKey(pubKeyHash, txID, outputIndex)
	return s.DB.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte(key), data); err != nil {
			return err
		}
		return txn.Set([]byte(idxKey), []byte{})
	})
}

// ApplyUTXOBatch atomically applies all UTXO changes for a single block in one
// BadgerDB transaction. A crash mid-flight either commits everything or nothing,
// preventing a partially-applied UTXO set after an unclean shutdown.
// spends are deleted first, then creates are written, all in the same txn.
func (s *Storage) ApplyUTXOBatch(spends []UTXO, creates []UTXO) error {
	return s.DB.Update(func(txn *badger.Txn) error {
		for _, u := range spends {
			k := utxoKey(u.TxID, u.OutputIndex)
			idx := addressIndexKey(u.PublicKeyHash, u.TxID, u.OutputIndex)
			if err := txn.Delete([]byte(k)); err != nil {
				return fmt.Errorf("delete utxo %s: %w", k, err)
			}
			if err := txn.Delete([]byte(idx)); err != nil {
				return fmt.Errorf("delete utxo index %s: %w", idx, err)
			}
		}
		for _, u := range creates {
			data, err := json.Marshal(utxoToJSON(u))
			if err != nil {
				return fmt.Errorf("marshal utxo: %w", err)
			}
			k := utxoKey(u.TxID, u.OutputIndex)
			idx := addressIndexKey(u.PublicKeyHash, u.TxID, u.OutputIndex)
			if err := txn.Set([]byte(k), data); err != nil {
				return fmt.Errorf("set utxo %s: %w", k, err)
			}
			if err := txn.Set([]byte(idx), []byte{}); err != nil {
				return fmt.Errorf("set utxo index %s: %w", idx, err)
			}
		}
		return nil
	})
}

// ApplyBlockAtomicFull writes all per-block state changes in a single Badger
// transaction: UTXO deletes, UTXO creates, height index, undo data, transaction
// indexes, and chain tip. A crash commits either everything or nothing.
//
// Blocks are capped at MaxBlockSize (1 MB) so ErrTxnTooBig can never occur in
// practice; it is treated as a hard error to preserve the atomicity guarantee.
func (s *Storage) ApplyBlockAtomicFull(b *Block, undo *BlockUndo, spends []UTXO, creates []UTXO) error {
	undoData, err := marshalUndoData(undo)
	if err != nil {
		return fmt.Errorf("marshal undo: %w", err)
	}

	var heightBuf [8]byte
	binary.BigEndian.PutUint64(heightBuf[:], b.Header.Height)

	return s.DB.Update(func(txn *badger.Txn) error {
		for _, u := range spends {
			if err := txn.Delete([]byte(utxoKey(u.TxID, u.OutputIndex))); err != nil {
				return fmt.Errorf("delete utxo: %w", err)
			}
			if err := txn.Delete([]byte(addressIndexKey(u.PublicKeyHash, u.TxID, u.OutputIndex))); err != nil {
				return fmt.Errorf("delete utxo index: %w", err)
			}
		}
		for _, u := range creates {
			data, err := json.Marshal(utxoToJSON(u))
			if err != nil {
				return fmt.Errorf("marshal utxo: %w", err)
			}
			if err := txn.Set([]byte(utxoKey(u.TxID, u.OutputIndex)), data); err != nil {
				return fmt.Errorf("set utxo: %w", err)
			}
			if err := txn.Set([]byte(addressIndexKey(u.PublicKeyHash, u.TxID, u.OutputIndex)), []byte{}); err != nil {
				return fmt.Errorf("set utxo index: %w", err)
			}
		}
		if err := txn.Set(blockHeightKey(b.Header.Height), b.Hash); err != nil {
			return fmt.Errorf("set height index: %w", err)
		}
		if err := txn.Set(blockUndoKey(b.Hash), undoData); err != nil {
			return fmt.Errorf("set undo: %w", err)
		}
		for _, tx := range b.Transactions {
			if err := txn.Set(txIndexKey(tx.TxID), heightBuf[:]); err != nil {
				return fmt.Errorf("set tx index: %w", err)
			}
		}
		if err := txn.Set(keyMetaTip, b.Hash); err != nil {
			return fmt.Errorf("set chain tip: %w", err)
		}
		return txn.Set(keyMetaHeight, heightBuf[:])
	})
}

// RollbackBlockAtomic reverses one block's UTXO changes, height index, tx
// indexes, and chain tip in a single Badger transaction. A crash mid-rollback
// is impossible — the transaction either commits everything or nothing.
//
// After a successful call the chain tip points to parentHash / parentHeight,
// all UTXOs created by b are deleted, and all UTXOs spent by b are restored.
func (s *Storage) RollbackBlockAtomic(b *Block, undo *BlockUndo, parentHash []byte, parentHeight uint64) error {
	var heightBuf [8]byte
	binary.BigEndian.PutUint64(heightBuf[:], parentHeight)

	return s.DB.Update(func(txn *badger.Txn) error {
		// 1. Delete UTXOs created by this block.
		for _, ref := range undo.CreatedRefs {
			k := utxoKey(ref.TxID, ref.OutputIndex)
			item, err := txn.Get([]byte(k))
			if err == badger.ErrKeyNotFound {
				continue // already absent — nothing to do
			}
			if err != nil {
				return fmt.Errorf("get created utxo %x:%d: %w", ref.TxID, ref.OutputIndex, err)
			}
			var j utxoJSON
			if err := item.Value(func(val []byte) error { return json.Unmarshal(val, &j) }); err != nil {
				return fmt.Errorf("unmarshal created utxo: %w", err)
			}
			pkh, _ := hex.DecodeString(j.PubKeyHash)
			if err := txn.Delete([]byte(k)); err != nil {
				return fmt.Errorf("delete created utxo: %w", err)
			}
			if err := txn.Delete([]byte(addressIndexKey(pkh, ref.TxID, ref.OutputIndex))); err != nil {
				return fmt.Errorf("delete created utxo addr index: %w", err)
			}
		}

		// 2. Restore UTXOs that this block spent.
		for _, u := range undo.SpentUTXOs {
			data, err := json.Marshal(utxoToJSON(u))
			if err != nil {
				return fmt.Errorf("marshal spent utxo: %w", err)
			}
			if err := txn.Set([]byte(utxoKey(u.TxID, u.OutputIndex)), data); err != nil {
				return fmt.Errorf("restore spent utxo: %w", err)
			}
			if err := txn.Set([]byte(addressIndexKey(u.PublicKeyHash, u.TxID, u.OutputIndex)), []byte{}); err != nil {
				return fmt.Errorf("restore spent utxo addr index: %w", err)
			}
		}

		// 3. Remove this block's height index entry. The new chain overwrites it on apply.
		if err := txn.Delete(blockHeightKey(b.Header.Height)); err != nil {
			return fmt.Errorf("delete height index h=%d: %w", b.Header.Height, err)
		}

		// 4. Remove tx index entries for all transactions in this block.
		for _, tx := range b.Transactions {
			if err := txn.Delete(txIndexKey(tx.TxID)); err != nil {
				return fmt.Errorf("delete tx index %x: %w", tx.TxID, err)
			}
		}

		// 5. Update chain tip to the parent — this is the final, atomic commit point.
		if err := txn.Set(keyMetaTip, parentHash); err != nil {
			return fmt.Errorf("set chain tip: %w", err)
		}
		return txn.Set(keyMetaHeight, heightBuf[:])
	})
}

// marshalUndoData serialises a BlockUndo to JSON for storage.
func marshalUndoData(undo *BlockUndo) ([]byte, error) {
	spent := make([]undoUTXOJSON, len(undo.SpentUTXOs))
	for i, u := range undo.SpentUTXOs {
		spent[i] = undoUTXOJSON{
			TxID:        hex.EncodeToString(u.TxID),
			OutputIndex: u.OutputIndex,
			Value:       u.Value,
			PubKeyHash:  hex.EncodeToString(u.PublicKeyHash),
			BlockHeight: u.BlockHeight,
			IsCoinbase:  u.IsCoinbase,
		}
	}
	refs := make([]undoRefJSON, len(undo.CreatedRefs))
	for i, r := range undo.CreatedRefs {
		refs[i] = undoRefJSON{
			TxID:        hex.EncodeToString(r.TxID),
			OutputIndex: r.OutputIndex,
		}
	}
	return json.Marshal(blockUndoDTO{SpentUTXOs: spent, CreatedRefs: refs})
}

// DeleteUTXOAndIndex removes the UTXO and its address index entry atomically.
// Returns an error if the UTXO does not exist (double-spend attempt).
func (s *Storage) DeleteUTXOAndIndex(key string, pubKeyHash, txID []byte, outputIndex uint32) error {
	idxKey := addressIndexKey(pubKeyHash, txID, outputIndex)
	return s.DB.Update(func(txn *badger.Txn) error {
		if _, err := txn.Get([]byte(key)); err != nil {
			return fmt.Errorf("utxo not found: %w", err)
		}
		if err := txn.Delete([]byte(key)); err != nil {
			return err
		}
		return txn.Delete([]byte(idxKey))
	})
}

// GetUTXO returns the raw bytes stored at key, or an error if not found.
func (s *Storage) GetUTXO(key string) ([]byte, error) {
	var data []byte
	err := s.DB.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			data = make([]byte, len(val))
			copy(data, val)
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("get utxo %s: %w", key, err)
	}
	return data, nil
}

// ── Transaction index ─────────────────────────────────────────────────────────
//
//  txidx:<txid_hex>  →  8-byte big-endian block height
// Enables O(1) lookup of the confirmed block containing a given transaction.

func txIndexKey(txID []byte) []byte {
	return []byte("txidx:" + hex.EncodeToString(txID))
}

// SaveTxIndex records the block height that confirmed txID.
func (s *Storage) SaveTxIndex(txID []byte, height uint64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], height)
	return s.DB.Update(func(txn *badger.Txn) error {
		return txn.Set(txIndexKey(txID), buf[:])
	})
}

// GetTxHeight returns the block height of the confirmed block containing txID.
// Returns an error if the transaction is not indexed (unconfirmed or unknown).
func (s *Storage) GetTxHeight(txID []byte) (uint64, error) {
	var height uint64
	err := s.DB.View(func(txn *badger.Txn) error {
		item, err := txn.Get(txIndexKey(txID))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			if len(val) < 8 {
				return fmt.Errorf("txidx value too short")
			}
			height = binary.BigEndian.Uint64(val)
			return nil
		})
	})
	if err != nil {
		return 0, fmt.Errorf("tx not indexed: %w", err)
	}
	return height, nil
}

// DeleteTxIndex removes the index entry for txID.
// Called during chain reorganisation when a block is rolled off the main chain.
func (s *Storage) DeleteTxIndex(txID []byte) error {
	return s.DB.Update(func(txn *badger.Txn) error {
		return txn.Delete(txIndexKey(txID))
	})
}

// IteratePrefix calls fn for every key-value pair whose key starts with prefix.
// Used by UTXOSet.GetUTXOsForAddress to scan the full UTXO set for an owner.
func (s *Storage) IteratePrefix(prefix string, fn func(key, value []byte) error) error {
	return s.DB.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)
		it := txn.NewIterator(opts)
		defer it.Close()

		pfx := []byte(prefix)
		for it.Seek(pfx); it.ValidForPrefix(pfx); it.Next() {
			item := it.Item()
			key := item.KeyCopy(nil)
			if err := item.Value(func(val []byte) error {
				return fn(key, val)
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
