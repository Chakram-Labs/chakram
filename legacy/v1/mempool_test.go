// mempool_test.go — Tests for issues 5 (duplicate outpoint), 9 (TxID recompute), 11 (fee eviction).
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

// buildSignedTx creates a transaction that spends utxo, with a known output value,
// and signs it with priv/pub.
func buildSignedTx(t *testing.T, utxo UTXO, outputValue uint64, pub ed25519.PublicKey, priv ed25519.PrivateKey) *Transaction {
	t.Helper()
	pkh := PubKeyToHash(pub)
	tx := &Transaction{
		IsCoinbase: false,
		Inputs:     []TxInput{{TxID: utxo.TxID, OutputIndex: utxo.OutputIndex}},
		Outputs:    []TxOutput{{Value: outputValue, PublicKeyHash: pkh}},
		Timestamp:  time.Now().Unix(),
	}
	tx.SetTxID()
	msg := signedMessage(MainnetChainID, tx, 0)
	tx.Inputs[0].Signature = ed25519.Sign(priv, msg)
	tx.Inputs[0].PublicKey = []byte(pub)
	return tx
}

// plantUTXO inserts a UTXO directly into bc.UTXOSet and returns it.
func plantUTXO(t *testing.T, bc *Blockchain, seed byte, value uint64, pkh []byte) UTXO {
	t.Helper()
	u := UTXO{
		TxID:          bytes.Repeat([]byte{seed}, 32),
		OutputIndex:   0,
		Value:         value,
		PublicKeyHash: pkh,
		BlockHeight:   0,
		IsCoinbase:    false,
	}
	if err := bc.UTXOSet.AddUTXO(u); err != nil {
		t.Fatalf("plantUTXO: %v", err)
	}
	return u
}

// TestMempoolRejectsDuplicateOutpoint verifies that a transaction spending an
// outpoint that another mempool transaction already spends is rejected.
//
// Before the fix: the mempool tracks transactions by TxID only, so two different
// transactions both spending the same outpoint can coexist — the first-confirmed
// one would invalidate the second, but both waste relay bandwidth.
//
// After the fix: the spentOutpoints map catches the conflict on Add.
func TestMempoolRejectsDuplicateOutpoint(t *testing.T) {
	bc := newTestBC(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	pkh := PubKeyToHash(pub)

	utxoA := plantUTXO(t, bc, 0xAA, 5_000_000, pkh)

	// Build two different transactions that both spend utxoA.
	tx1 := buildSignedTx(t, utxoA, 4_000_000, pub, priv)
	tx2 := buildSignedTx(t, utxoA, 3_500_000, pub, priv) // same input, different output

	mp := NewMempool()
	mp.SetBlockchain(bc)

	if err := mp.Add(tx1); err != nil {
		t.Fatalf("add tx1 (first spender): %v", err)
	}
	if err := mp.Add(tx2); err == nil {
		t.Fatal("mempool accepted a second transaction spending the same outpoint — duplicate-outpoint check missing")
	}
}

// TestMempoolRecomputesTxID verifies that Mempool.Add recomputes the canonical
// TxID and ignores any TxID supplied on the wire.
//
// Before the fix: the mempool trusts the caller-provided TxID. A peer could send a
// transaction with a tampered TxID, causing the mempool to store it under the wrong
// key and making it unrelayable.
//
// After the fix: Add() calls tx.ComputeTxID() and overwrites tx.TxID, then indexes
// the transaction under the canonical key.
func TestMempoolRecomputesTxID(t *testing.T) {
	bc := newTestBC(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	pkh := PubKeyToHash(pub)

	utxoA := plantUTXO(t, bc, 0xBB, 5_000_000, pkh)
	tx := buildSignedTx(t, utxoA, 4_000_000, pub, priv)

	canonical := make([]byte, len(tx.TxID))
	copy(canonical, tx.TxID)

	// Tamper the TxID on the wire.
	tx.TxID = bytes.Repeat([]byte{0xFF}, 32)

	mp := NewMempool()
	mp.SetBlockchain(bc)

	if err := mp.Add(tx); err != nil {
		t.Fatalf("mempool rejected the transaction: %v", err)
	}

	// After Add, tx.TxID must have been restored to the canonical value.
	if !bytes.Equal(tx.TxID, canonical) {
		t.Fatalf("mempool did not recompute TxID: got %x, want %x", tx.TxID, canonical)
	}

	// Must be retrievable under the canonical TxID.
	if _, err := mp.Get(canonical); err != nil {
		t.Fatalf("transaction not found under canonical TxID after Add: %v", err)
	}
}

// TestMempoolFeeEviction verifies that when the mempool is full and a higher-fee
// transaction arrives, the lowest-fee existing transaction is evicted.
//
// Before the fix: the mempool has no fee tracking; when full it returns "mempool: full"
// regardless of the new transaction's fee.
//
// After the fix: the fees map enables fee-based replacement.
func TestMempoolFeeEviction(t *testing.T) {
	bc := newTestBC(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	pkh := PubKeyToHash(pub)

	// Three UTXOs, each with 10_000 Cash.
	utxoLow := plantUTXO(t, bc, 0xC1, 10_000, pkh) // will produce fee=500
	utxoMid := plantUTXO(t, bc, 0xC2, 10_000, pkh) // will produce fee=1000
	utxoHigh := plantUTXO(t, bc, 0xC3, 10_000, pkh) // will produce fee=2000

	txLow := buildSignedTx(t, utxoLow, 9_500, pub, priv)  // fee=500
	txMid := buildSignedTx(t, utxoMid, 9_000, pub, priv)  // fee=1000
	txHigh := buildSignedTx(t, utxoHigh, 8_000, pub, priv) // fee=2000

	// Restrict the pool to 2 transactions.
	origMax := maxMempoolSize
	maxMempoolSize = 2
	defer func() { maxMempoolSize = origMax }()

	mp := NewMempool()
	mp.SetBlockchain(bc)

	if err := mp.Add(txLow); err != nil {
		t.Fatalf("add txLow: %v", err)
	}
	if err := mp.Add(txMid); err != nil {
		t.Fatalf("add txMid: %v", err)
	}
	// Pool is now full (size=2).

	// Adding txHigh (fee=2000 > lowest-fee=500) should succeed via eviction.
	if err := mp.Add(txHigh); err != nil {
		t.Fatalf("add txHigh should succeed by evicting txLow: %v", err)
	}

	if mp.Size() != 2 {
		t.Fatalf("pool size should remain 2 after eviction, got %d", mp.Size())
	}

	// txLow must be gone; txMid and txHigh must remain.
	if _, err := mp.Get(txLow.TxID); err == nil {
		t.Fatal("txLow should have been evicted but is still in the pool")
	}
	if _, err := mp.Get(txMid.TxID); err != nil {
		t.Fatalf("txMid should remain in pool: %v", err)
	}
	if _, err := mp.Get(txHigh.TxID); err != nil {
		t.Fatalf("txHigh should be in pool after eviction: %v", err)
	}
}
