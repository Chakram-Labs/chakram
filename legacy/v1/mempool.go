// mempool.go — In-memory pool of unconfirmed transactions waiting to be mined.
package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	maxMempoolSize = 10_000 // var so tests can lower it to exercise eviction
)

var (
	// ErrTxInvalid signals a structurally invalid transaction (malformed,
	// duplicate inputs, bad amounts). Peers sending these should be penalized.
	ErrTxInvalid = errors.New("mempool: structurally invalid transaction")
	// ErrTxDuplicateInput signals an outpoint that is already claimed by
	// another transaction in the pool. This is a clear protocol violation.
	ErrTxDuplicateInput = errors.New("mempool: duplicate input outpoint")
)

const mempoolTxTTL = 72 * time.Hour

// Mempool holds unconfirmed transactions, safe for concurrent access.
type Mempool struct {
	transactions   map[string]*Transaction
	addedAt        map[string]time.Time
	fees           map[string]uint64 // txID hex → fee in Cash
	spentOutpoints map[string]string // outpointKey → txID hex; prevents double-spend in pool
	mu             sync.RWMutex
	blockchain     *Blockchain
}

// NewMempool creates an empty mempool.
func NewMempool() *Mempool {
	return &Mempool{
		transactions:   make(map[string]*Transaction),
		addedAt:        make(map[string]time.Time),
		fees:           make(map[string]uint64),
		spentOutpoints: make(map[string]string),
	}
}

// SetBlockchain wires the blockchain into the mempool so that Add() can
// validate that referenced UTXOs exist and signatures are correct.
func (m *Mempool) SetBlockchain(bc *Blockchain) {
	m.blockchain = bc
}

// Add validates tx and inserts it into the mempool.
// Recomputes the canonical TxID, checks for outpoint conflicts in the existing
// pool, and evicts the lowest-fee transaction when the pool is full and the
// incoming fee is higher.
func (m *Mempool) Add(tx *Transaction) error {
	// Always recompute — reject any wire-provided TxID that doesn't match content.
	tx.TxID = tx.ComputeTxID()

	if err := tx.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrTxInvalid, err)
	}
	key := hex.EncodeToString(tx.TxID)

	// Quick existence check under read lock.
	m.mu.RLock()
	_, exists := m.transactions[key]
	m.mu.RUnlock()
	if exists {
		return errors.New("mempool: transaction already exists")
	}

	// UTXO validation — read-only, potentially slow; do it outside the write lock.
	if m.blockchain != nil && !tx.IsCoinbase {
		height := m.blockchain.GetHeight()
		if err := m.blockchain.UTXOSet.ValidateInputsOnly(tx, height); err != nil {
			return fmt.Errorf("mempool: %w", err)
		}
	}

	// Calculate fee for eviction ordering.
	var fee uint64
	if m.blockchain != nil && !tx.IsCoinbase {
		if f, err := m.blockchain.UTXOSet.CalculateFee(tx); err == nil {
			fee = f
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Evict TTL-expired transactions.
	cutoff := time.Now().Add(-mempoolTxTTL)
	for k, t := range m.addedAt {
		if t.Before(cutoff) {
			m.removeUnderLock(k)
		}
	}

	// Re-check existence under write lock (closes TOCTOU window).
	if _, exists := m.transactions[key]; exists {
		return errors.New("mempool: transaction already exists")
	}

	// Reject if any input outpoint is already claimed by a pooled transaction.
	if !tx.IsCoinbase {
		for i, in := range tx.Inputs {
			op := outpointKey(in.TxID, in.OutputIndex)
			if conflict, ok := m.spentOutpoints[op]; ok {
				return fmt.Errorf("%w: input %d conflicts with in-pool tx %s", ErrTxDuplicateInput, i, conflict)
			}
		}
	}

	// Fee-based eviction when the pool is full.
	if len(m.transactions) >= maxMempoolSize {
		lowestKey, lowestFee := m.lowestFeeEntry()
		if lowestKey == "" || fee <= lowestFee {
			return errors.New("mempool: full, transaction fee too low to replace existing entry")
		}
		m.removeUnderLock(lowestKey)
	}

	m.transactions[key] = tx
	m.addedAt[key] = time.Now()
	m.fees[key] = fee
	if !tx.IsCoinbase {
		for _, in := range tx.Inputs {
			m.spentOutpoints[outpointKey(in.TxID, in.OutputIndex)] = key
		}
	}
	return nil
}

// lowestFeeEntry returns the key and fee of the cheapest transaction in the pool.
// Must be called with m.mu held (at least RLock).
func (m *Mempool) lowestFeeEntry() (string, uint64) {
	var lowestKey string
	lowestFee := ^uint64(0)
	for k, f := range m.fees {
		if f < lowestFee {
			lowestFee = f
			lowestKey = k
		}
	}
	return lowestKey, lowestFee
}

// removeUnderLock removes the transaction keyed by txHex from all maps.
// Caller must hold m.mu (write).
func (m *Mempool) removeUnderLock(txHex string) {
	if tx, ok := m.transactions[txHex]; ok {
		for _, in := range tx.Inputs {
			delete(m.spentOutpoints, outpointKey(in.TxID, in.OutputIndex))
		}
	}
	delete(m.transactions, txHex)
	delete(m.addedAt, txHex)
	delete(m.fees, txHex)
}

// Remove deletes the transaction with the given TxID from the mempool.
// Called after a block confirms it. No-op if not present.
func (m *Mempool) Remove(txID []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeUnderLock(hex.EncodeToString(txID))
}

// Get returns the transaction with the given TxID, or an error if not found.
func (m *Mempool) Get(txID []byte) (*Transaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tx, ok := m.transactions[hex.EncodeToString(txID)]
	if !ok {
		return nil, errors.New("mempool: transaction not found")
	}
	return tx, nil
}

// GetAll returns a snapshot of all transactions as a slice.
// Used by the miner to select transactions for the next block.
func (m *Mempool) GetAll() []*Transaction {
	m.mu.RLock()
	defer m.mu.RUnlock()

	txs := make([]*Transaction, 0, len(m.transactions))
	for _, tx := range m.transactions {
		txs = append(txs, tx)
	}
	return txs
}

// Size returns the number of transactions currently in the mempool.
func (m *Mempool) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.transactions)
}

// ClearConfirmed removes every transaction in txs from the mempool.
// Call this after a new block is added to the chain.
func (m *Mempool) ClearConfirmed(txs []*Transaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tx := range txs {
		m.removeUnderLock(hex.EncodeToString(tx.TxID))
	}
}
