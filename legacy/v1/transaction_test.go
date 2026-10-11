// transaction_test.go — Tests for transaction validation.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"math"
	"testing"
)

// TestValidateRejectsDuplicateInputs verifies that a transaction with two
// inputs that reference the same outpoint is rejected by Validate().
//
// Before the fix: Validate() iterates inputs checking for empty fields but
// never checks for duplicates — the tx passes structural validation.
//
// After the fix: Validate() detects the duplicate outpoint and returns an error.
func TestValidateRejectsDuplicateInputs(t *testing.T) {
	txid := bytes.Repeat([]byte{0xAB}, 32)

	// Both inputs reference the same outpoint: txid:0
	dup := TxInput{
		TxID:        txid,
		OutputIndex: 0,
		Signature:   bytes.Repeat([]byte{0x01}, 64),
		PublicKey:   bytes.Repeat([]byte{0x02}, 32),
	}

	tx := &Transaction{
		IsCoinbase: false,
		Inputs:     []TxInput{dup, dup},
		Outputs:    []TxOutput{{Value: 1_000, PublicKeyHash: bytes.Repeat([]byte{0x03}, 20)}},
		Timestamp:  1_000_000,
	}
	tx.SetTxID()

	if err := tx.Validate(); err == nil {
		t.Fatal("Validate() accepted a transaction with duplicate inputs — duplicate-input check missing")
	}
}

// TestValidateRejectsOverflowingOutputSum verifies that a transaction whose
// output values sum to more than MaxUint64 is rejected by Validate().
//
// Before the fix: Validate() checks each output's value individually (non-zero)
// but never checks the sum, so the overflow wraps silently and the tx passes.
//
// After the fix: TotalOutput() uses math/bits.Add64 overflow detection and
// Validate() calls it — the overflow is caught before the tx reaches the UTXO set.
func TestValidateRejectsOverflowingOutputSum(t *testing.T) {
	tx := &Transaction{
		IsCoinbase: false,
		Inputs: []TxInput{{
			TxID:        bytes.Repeat([]byte{0x01}, 32),
			OutputIndex: 0,
			Signature:   bytes.Repeat([]byte{0x01}, 64),
			PublicKey:   bytes.Repeat([]byte{0x02}, 32),
		}},
		Outputs: []TxOutput{
			{Value: math.MaxUint64 - 1, PublicKeyHash: bytes.Repeat([]byte{0x03}, 20)},
			{Value: 2, PublicKeyHash: bytes.Repeat([]byte{0x03}, 20)},
		},
		Timestamp: 1_000_000,
	}
	tx.SetTxID()
	if err := tx.Validate(); err == nil {
		t.Fatal("Validate() must reject a transaction whose output values overflow uint64")
	}
}

// makeTx is a test helper that builds a minimal unsigned transaction.
func makeTx(inputs []TxInput, outputs []TxOutput, ts int64) *Transaction {
	tx := &Transaction{
		IsCoinbase: false,
		Inputs:     inputs,
		Outputs:    outputs,
		Timestamp:  ts,
	}
	tx.SetTxID()
	return tx
}

// TestSighashIncludesChainID verifies that the signed message differs between
// mainnet and testnet. A mainnet signature must not be replayable on testnet.
func TestSighashIncludesChainID(t *testing.T) {
	tx := makeTx(
		[]TxInput{{TxID: bytes.Repeat([]byte{0x01}, 32), OutputIndex: 0}},
		[]TxOutput{{Value: 1_000, PublicKeyHash: bytes.Repeat([]byte{0xAA}, 20)}},
		1_000_000,
	)
	msg1 := signedMessage(MainnetChainID, tx, 0)
	msg2 := signedMessage(TestnetChainID, tx, 0)
	if bytes.Equal(msg1, msg2) {
		t.Fatal("sighash must differ between mainnet and testnet — cross-chain replay possible")
	}
}

// TestSighashIncludesOutputs verifies that the signed message differs when the
// spending transaction's outputs differ. Prevents output-substitution: a
// signature authorising payment to Alice cannot be repurposed to pay Eve.
func TestSighashIncludesOutputs(t *testing.T) {
	in := TxInput{TxID: bytes.Repeat([]byte{0x01}, 32), OutputIndex: 0}
	txAlice := makeTx([]TxInput{in},
		[]TxOutput{{Value: 1_000, PublicKeyHash: bytes.Repeat([]byte{0xAA}, 20)}}, 1_000_000)
	txEve := makeTx([]TxInput{in},
		[]TxOutput{{Value: 1_000, PublicKeyHash: bytes.Repeat([]byte{0xBB}, 20)}}, 1_000_000)

	msg1 := signedMessage(MainnetChainID, txAlice, 0)
	msg2 := signedMessage(MainnetChainID, txEve, 0)
	if bytes.Equal(msg1, msg2) {
		t.Fatal("sighash must differ when outputs differ — output-substitution attack possible")
	}
}

// TestSighashIncludesAllInputs verifies that adding an extra input to a
// transaction changes the sighash of the original input. Without this, an
// attacker could graft additional inputs onto an already-signed transaction
// and the original signatures would still verify.
func TestSighashIncludesAllInputs(t *testing.T) {
	in0 := TxInput{TxID: bytes.Repeat([]byte{0x01}, 32), OutputIndex: 0}
	in1 := TxInput{TxID: bytes.Repeat([]byte{0x02}, 32), OutputIndex: 0}
	out := TxOutput{Value: 1_000, PublicKeyHash: bytes.Repeat([]byte{0xAA}, 20)}

	tx1 := makeTx([]TxInput{in0}, []TxOutput{out}, 1_000_000)
	tx2 := makeTx([]TxInput{in0, in1}, []TxOutput{out}, 1_000_000)

	msg1 := signedMessage(MainnetChainID, tx1, 0)
	msg2 := signedMessage(MainnetChainID, tx2, 0)
	if bytes.Equal(msg1, msg2) {
		t.Fatal("sighash must differ when input set differs — input-grafting attack possible")
	}
}

// TestSighashIncludesTimestamp verifies that changing the transaction timestamp
// changes the sighash. Without this, a timestamp could be altered post-signing,
// changing the TxID while keeping all signatures valid.
func TestSighashIncludesTimestamp(t *testing.T) {
	in := TxInput{TxID: bytes.Repeat([]byte{0x01}, 32), OutputIndex: 0}
	out := TxOutput{Value: 1_000, PublicKeyHash: bytes.Repeat([]byte{0xAA}, 20)}

	tx1 := makeTx([]TxInput{in}, []TxOutput{out}, 1_000_000)
	tx2 := makeTx([]TxInput{in}, []TxOutput{out}, 2_000_000)

	msg1 := signedMessage(MainnetChainID, tx1, 0)
	msg2 := signedMessage(MainnetChainID, tx2, 0)
	if bytes.Equal(msg1, msg2) {
		t.Fatal("sighash must differ when timestamp differs — timestamp can be altered post-signing")
	}
}

// TestSwappedOutputsFailVerification proves end-to-end that swapping a
// transaction's outputs after signing causes Ed25519 verification to fail.
// This is the full-circuit test: sign → tamper → verify → must reject.
func TestSwappedOutputsFailVerification(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	alice := bytes.Repeat([]byte{0xAA}, 20)
	bob := bytes.Repeat([]byte{0xBB}, 20)

	tx := makeTx(
		[]TxInput{{TxID: bytes.Repeat([]byte{0x01}, 32), OutputIndex: 0}},
		[]TxOutput{
			{Value: 3_000, PublicKeyHash: alice}, // Alice gets 3000
			{Value: 1_000, PublicKeyHash: bob},   // Bob gets 1000
		},
		1_000_000,
	)

	// Sign input 0 over the original output order.
	msg := signedMessage(MainnetChainID, tx, 0)
	sig := ed25519.Sign(priv, msg)
	tx.Inputs[0].Signature = sig
	tx.Inputs[0].PublicKey = pub

	// Attacker swaps outputs: Bob now gets 3000, Alice gets 1000.
	tx.Outputs[0], tx.Outputs[1] = tx.Outputs[1], tx.Outputs[0]
	tx.SetTxID()

	msgAfter := signedMessage(MainnetChainID, tx, 0)
	if bytes.Equal(msg, msgAfter) {
		t.Fatal("sighash did not change when outputs were swapped — swap is undetectable")
	}
	if ed25519.Verify(pub, msgAfter, sig) {
		t.Fatal("Ed25519 still verifies after output swap — output-substitution attack succeeds")
	}
}

// TestCoinbaseHeightIsUniqueInTxID verifies that two coinbase transactions at
// different block heights (but otherwise identical) have different TxIDs.
//
// Before the fix: ComputeTxID commits only to outputs + timestamp. Two coinbase
// transactions with the same miner address, same reward amount, and the same
// timestamp (possible on real hardware) produce the same TxID → duplicate TxID
// across blocks, breaking the UTXO index.
//
// After the fix: CoinbaseData encodes blockHeight, ComputeTxID includes it →
// TxIDs diverge even when everything else is identical.
func TestCoinbaseHeightIsUniqueInTxID(t *testing.T) {
	pkh := bytes.Repeat([]byte{0x42}, 20)
	cb1 := NewCoinbaseTransaction(pkh, 1, 0)
	cb2 := NewCoinbaseTransaction(pkh, 2, 0)

	// Force identical timestamps so the only difference is height.
	cb1.Timestamp = 1_000_000
	cb2.Timestamp = 1_000_000
	cb1.SetTxID()
	cb2.SetTxID()

	if bytes.Equal(cb1.TxID, cb2.TxID) {
		t.Fatal("coinbase transactions at different heights must have different TxIDs — height not committed in ComputeTxID")
	}
}
