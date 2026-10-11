// transaction.go — Transaction types and logic for the Chakram blockchain.
// Replaces the placeholder Transaction struct that was in block.go.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"time"
)

// TxOutput is a transaction output — it creates a new UTXO locked to a recipient.
type TxOutput struct {
	Value         uint64 // amount in Cash (smallest unit)
	PublicKeyHash []byte // 20 bytes: RIPEMD160(SHA256(recipientPublicKey))
}

// TxInput is a transaction input — it spends an existing UTXO.
type TxInput struct {
	TxID        []byte // 32 bytes: hash of the transaction whose output we are spending
	OutputIndex uint32 // which output index in that transaction
	Signature   []byte // Ed25519 signature proving ownership of the UTXO
	PublicKey   []byte // 32 bytes: sender's Ed25519 public key
}

// Transaction is a transfer of value on the Chakram blockchain.
// Coinbase transactions have no inputs and are created by miners to claim rewards.
type Transaction struct {
	TxID         []byte
	Inputs       []TxInput
	Outputs      []TxOutput
	Timestamp    int64
	IsCoinbase   bool
	CoinbaseData []byte // 8-byte big-endian block height for coinbase txs; nil for regular txs
}

// NewCoinbaseTransaction creates the block-reward transaction for a miner.
// fees is the total transaction fee collected from non-coinbase txs in the block.
// The reward halves every HalvingInterval blocks; it reaches 0 when the supply
// cap is exhausted.
func NewCoinbaseTransaction(minerPubKeyHash []byte, blockHeight uint64, fees uint64) *Transaction {
	halvings := blockHeight / HalvingInterval
	var reward uint64
	if halvings < 64 {
		reward = InitialBlockReward >> halvings
	}

	// Guard against overflow; cap at MaxSupply so validateBlockReadOnly can
	// catch and reject if the coinbase still exceeds the allowed maximum.
	total, carry := bits.Add64(reward, fees, 0)
	if carry != 0 {
		total = MaxSupply
	}

	heightBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(heightBytes, blockHeight)

	tx := &Transaction{
		IsCoinbase:   true,
		CoinbaseData: heightBytes,
		Inputs:       []TxInput{},
		Outputs: []TxOutput{
			{Value: total, PublicKeyHash: minerPubKeyHash},
		},
		Timestamp: time.Now().Unix(),
	}
	tx.SetTxID()
	return tx
}

// NewTransaction creates a regular (non-coinbase) transaction.
func NewTransaction(inputs []TxInput, outputs []TxOutput) *Transaction {
	tx := &Transaction{
		IsCoinbase: false,
		Inputs:     inputs,
		Outputs:    outputs,
		Timestamp:  time.Now().Unix(),
	}
	tx.SetTxID()
	return tx
}

// ComputeTxID serialises the transaction's identifying fields and returns
// SHA256(SHA256(data)). The coinbase flag, all input references, all output
// values and recipients, and the timestamp are included in the commitment.
func (tx *Transaction) ComputeTxID() []byte {
	buf := new(bytes.Buffer)

	// coinbase flag
	if tx.IsCoinbase {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}

	for _, in := range tx.Inputs {
		buf.Write(in.TxID)
		binary.Write(buf, binary.LittleEndian, in.OutputIndex)
	}

	for _, out := range tx.Outputs {
		binary.Write(buf, binary.LittleEndian, out.Value)
		buf.Write(out.PublicKeyHash)
	}

	binary.Write(buf, binary.LittleEndian, tx.Timestamp)

	// Always commit to CoinbaseData length + bytes so that coinbase transactions
	// at different heights can never produce the same TxID.
	binary.Write(buf, binary.LittleEndian, uint32(len(tx.CoinbaseData)))
	buf.Write(tx.CoinbaseData)

	first := sha256.Sum256(buf.Bytes())
	second := sha256.Sum256(first[:])
	return second[:]
}

// SetTxID computes and stores the transaction ID.
func (tx *Transaction) SetTxID() {
	tx.TxID = tx.ComputeTxID()
}

// TotalOutput returns the sum of all output values in Cash.
// Returns an error if the sum would overflow uint64 — a transaction with
// overflowing outputs is structurally invalid and must be rejected.
func (tx *Transaction) TotalOutput() (uint64, error) {
	var total uint64
	for _, out := range tx.Outputs {
		next, carry := bits.Add64(total, out.Value, 0)
		if carry != 0 {
			return 0, errors.New("output values overflow uint64")
		}
		total = next
	}
	return total, nil
}

// IsCoinbaseTx reports whether this is a coinbase transaction.
func (tx *Transaction) IsCoinbaseTx() bool {
	return tx.IsCoinbase
}

// Validate performs structural validation of the transaction.
// It does not check the UTXO set or verify signatures — those happen in utxo.go.
func (tx *Transaction) Validate() error {
	if len(tx.TxID) == 0 {
		return errors.New("transaction: TxID is empty")
	}
	if len(tx.Outputs) == 0 {
		return errors.New("transaction: must have at least one output")
	}
	for i, out := range tx.Outputs {
		if out.Value == 0 {
			return fmt.Errorf("transaction: output %d has zero value", i)
		}
		if out.Value > MaxSupply {
			return fmt.Errorf("transaction: output %d value %d exceeds MaxSupply %d", i, out.Value, MaxSupply)
		}
		if len(out.PublicKeyHash) != 20 {
			return fmt.Errorf("transaction: output %d PublicKeyHash must be 20 bytes, got %d", i, len(out.PublicKeyHash))
		}
	}
	total, err := tx.TotalOutput()
	if err != nil {
		return fmt.Errorf("transaction: %w", err)
	}
	if total > MaxSupply {
		return fmt.Errorf("transaction: total output %d exceeds MaxSupply %d", total, MaxSupply)
	}
	if !tx.IsCoinbase {
		if len(tx.Inputs) == 0 {
			return errors.New("transaction: non-coinbase must have at least one input")
		}
		seen := make(map[string]bool, len(tx.Inputs))
		for i, in := range tx.Inputs {
			if len(in.TxID) == 0 {
				return fmt.Errorf("transaction: input %d has empty TxID", i)
			}
			if len(in.Signature) == 0 {
				return fmt.Errorf("transaction: input %d has empty Signature", i)
			}
			if len(in.PublicKey) == 0 {
				return fmt.Errorf("transaction: input %d has empty PublicKey", i)
			}
			key := fmt.Sprintf("%x:%d", in.TxID, in.OutputIndex)
			if seen[key] {
				return fmt.Errorf("transaction: input %d duplicates outpoint %x:%d",
					i, in.TxID, in.OutputIndex)
			}
			seen[key] = true
		}
	}
	return nil
}
