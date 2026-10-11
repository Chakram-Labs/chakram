// utxo.go — UTXO set: tracks all unspent transaction outputs on the Chakram chain.
// The UTXO set is the single source of truth for wallet balances and
// double-spend prevention. All reads/writes go through Storage.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
)

// ErrBadSignature is returned by ValidateInputsOnly when an input carries an
// invalid Ed25519 signature. Peers forwarding such transactions are penalized.
var ErrBadSignature = errors.New("invalid Ed25519 signature")

// outpointKey returns a string key that uniquely identifies a UTXO reference.
// Used by the mempool's spentOutpoints map and validateBlockReadOnly.
func outpointKey(txID []byte, idx uint32) string {
	return fmt.Sprintf("%x:%d", txID, idx)
}

// ── Types ─────────────────────────────────────────────────────────────────────

// UTXO represents a single unspent transaction output.
type UTXO struct {
	TxID          []byte
	OutputIndex   uint32
	Value         uint64 // in Cash
	PublicKeyHash []byte // 20 bytes: RIPEMD160(SHA256(ownerPublicKey))
	BlockHeight   uint64
	IsCoinbase    bool
}

// UTXORef identifies a UTXO by its location without carrying its full data.
type UTXORef struct {
	TxID        []byte
	OutputIndex uint32
}

// BlockUndo contains the data needed to undo the UTXO changes made by one block.
// SpentUTXOs are restored on rollback; CreatedRefs are deleted.
type BlockUndo struct {
	SpentUTXOs  []UTXO    // UTXOs consumed by this block (restore on rollback)
	CreatedRefs []UTXORef // UTXOs created by this block (delete on rollback)
}

// UTXOSet manages the full set of unspent outputs backed by Storage.
type UTXOSet struct {
	Storage *Storage
	chainID []byte // network chain ID, committed to in every signature
}

// NewUTXOSet creates a UTXOSet backed by the given storage instance.
// chainID is included in every sighash to prevent cross-network replay.
func NewUTXOSet(storage *Storage, chainID []byte) *UTXOSet {
	return &UTXOSet{Storage: storage, chainID: chainID}
}

// ── Storage-local DTO ─────────────────────────────────────────────────────────

type utxoJSON struct {
	TxID        string `json:"txid"`
	OutputIndex uint32 `json:"output_index"`
	Value       uint64 `json:"value"`
	PubKeyHash  string `json:"pub_key_hash"`
	BlockHeight uint64 `json:"block_height"`
	IsCoinbase  bool   `json:"is_coinbase"`
}

func utxoToJSON(u UTXO) utxoJSON {
	return utxoJSON{
		TxID:        hex.EncodeToString(u.TxID),
		OutputIndex: u.OutputIndex,
		Value:       u.Value,
		PubKeyHash:  hex.EncodeToString(u.PublicKeyHash),
		BlockHeight: u.BlockHeight,
		IsCoinbase:  u.IsCoinbase,
	}
}

func utxoFromJSON(j utxoJSON) (UTXO, error) {
	if j.Value > MaxSupply {
		return UTXO{}, fmt.Errorf("utxo value %d exceeds MaxSupply %d", j.Value, MaxSupply)
	}
	txID, err := hex.DecodeString(j.TxID)
	if err != nil {
		return UTXO{}, fmt.Errorf("decode txid: %w", err)
	}
	pkh, err := hex.DecodeString(j.PubKeyHash)
	if err != nil {
		return UTXO{}, fmt.Errorf("decode pub_key_hash: %w", err)
	}
	return UTXO{
		TxID:          txID,
		OutputIndex:   j.OutputIndex,
		Value:         j.Value,
		PublicKeyHash: pkh,
		BlockHeight:   j.BlockHeight,
		IsCoinbase:    j.IsCoinbase,
	}, nil
}

func utxoKey(txID []byte, outputIndex uint32) string {
	return fmt.Sprintf("utxo:%s:%d", hex.EncodeToString(txID), outputIndex)
}

// ── UTXO operations ───────────────────────────────────────────────────────────

// AddUTXO persists a new unspent output to BadgerDB and updates the address index.
func (u *UTXOSet) AddUTXO(utxo UTXO) error {
	data, err := json.Marshal(utxoToJSON(utxo))
	if err != nil {
		return fmt.Errorf("marshal utxo: %w", err)
	}
	return u.Storage.SaveUTXOAndIndex(utxoKey(utxo.TxID, utxo.OutputIndex), data, utxo.PublicKeyHash, utxo.TxID, utxo.OutputIndex)
}

// SpendUTXO removes a UTXO and its address index entry atomically.
// Returns an error if the UTXO does not exist (double-spend attempt).
func (u *UTXOSet) SpendUTXO(txID []byte, outputIndex uint32) error {
	utxo, err := u.GetUTXO(txID, outputIndex)
	if err != nil {
		return fmt.Errorf("spend utxo %s:%d: not found: %w", hex.EncodeToString(txID), outputIndex, err)
	}
	if err := u.Storage.DeleteUTXOAndIndex(utxoKey(txID, outputIndex), utxo.PublicKeyHash, txID, outputIndex); err != nil {
		return fmt.Errorf("spend utxo %s:%d: %w", hex.EncodeToString(txID), outputIndex, err)
	}
	return nil
}

// GetUTXO fetches a specific unspent output by transaction ID and output index.
func (u *UTXOSet) GetUTXO(txID []byte, outputIndex uint32) (*UTXO, error) {
	data, err := u.Storage.GetUTXO(utxoKey(txID, outputIndex))
	if err != nil {
		return nil, fmt.Errorf("get utxo %s:%d: %w", hex.EncodeToString(txID), outputIndex, err)
	}
	var j utxoJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("unmarshal utxo: %w", err)
	}
	utxo, err := utxoFromJSON(j)
	if err != nil {
		return nil, err
	}
	return &utxo, nil
}

// GetUTXOsForAddress returns all unspent outputs locked to the given public key hash.
// Uses the address index for O(k) lookup instead of a full UTXO set scan.
func (u *UTXOSet) GetUTXOsForAddress(pubKeyHash []byte) ([]UTXO, error) {
	prefix := fmt.Sprintf("aidx:%s:", hex.EncodeToString(pubKeyHash))
	var results []UTXO
	err := u.Storage.IteratePrefix(prefix, func(key, _ []byte) error {
		// key format: aidx:<pkhHex>:<txidHex>:<outputIndex>
		parts := strings.SplitN(string(key), ":", 4)
		if len(parts) != 4 {
			return fmt.Errorf("malformed address index key: %s", string(key))
		}
		txID, err := hex.DecodeString(parts[2])
		if err != nil {
			return fmt.Errorf("decode txid from address index: %w", err)
		}
		idx, err := strconv.ParseUint(parts[3], 10, 32)
		if err != nil {
			return fmt.Errorf("decode output index from address index: %w", err)
		}
		utxo, err := u.GetUTXO(txID, uint32(idx))
		if err != nil {
			return fmt.Errorf("get utxo for address index entry: %w", err)
		}
		results = append(results, *utxo)
		return nil
	})
	return results, err
}

// GetBalance returns the total spendable balance (in Cash) for an address.
func (u *UTXOSet) GetBalance(pubKeyHash []byte) (uint64, error) {
	utxos, err := u.GetUTXOsForAddress(pubKeyHash)
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, utxo := range utxos {
		next, carry := bits.Add64(total, utxo.Value, 0)
		if carry != 0 {
			return 0, errors.New("balance overflows uint64")
		}
		total = next
	}
	return total, nil
}

// ── Signature / ownership helpers ─────────────────────────────────────────────

// pubKeyToHash is a package-level alias for the exported PubKeyToHash in wallet.go.
func pubKeyToHash(publicKey []byte) []byte {
	return PubKeyToHash(publicKey)
}

// signedMessage returns the canonical sighash for authorising input inputIndex
// of tx. The pre-image commits to:
//   - chainID: prevents replay across networks and forks.
//   - All input outpoints (TxID + OutputIndex): prevents input-set manipulation —
//     an attacker cannot add or remove inputs without invalidating all signatures.
//   - All outputs: binds every coin destination; blocks output-substitution.
//   - tx.Timestamp: prevents the timestamp from being altered without
//     re-signing, which would produce a different TxID for the same content.
//   - inputIndex: makes each input's sighash unique within the same transaction.
func signedMessage(chainID []byte, tx *Transaction, inputIndex int) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, uint32(len(chainID)))
	buf.Write(chainID)
	binary.Write(buf, binary.LittleEndian, uint32(len(tx.Inputs)))
	for _, in := range tx.Inputs {
		buf.Write(in.TxID)
		binary.Write(buf, binary.LittleEndian, in.OutputIndex)
	}
	binary.Write(buf, binary.LittleEndian, uint32(len(tx.Outputs)))
	for _, out := range tx.Outputs {
		binary.Write(buf, binary.LittleEndian, out.Value)
		buf.Write(out.PublicKeyHash)
	}
	binary.Write(buf, binary.LittleEndian, tx.Timestamp)
	binary.Write(buf, binary.LittleEndian, uint32(inputIndex))
	h := sha256.Sum256(buf.Bytes())
	return h[:]
}

// ── Block processing ──────────────────────────────────────────────────────────

// ValidateInputsOnly performs the same checks as validateBlockReadOnly but
// does not spend any UTXOs. Used by the mempool to pre-validate transactions.
func (u *UTXOSet) ValidateInputsOnly(tx *Transaction, currentHeight uint64) error {
	var inputSum uint64
	for i, in := range tx.Inputs {
		utxo, err := u.GetUTXO(in.TxID, in.OutputIndex)
		if err != nil {
			return fmt.Errorf("input %d: utxo not found: %w", i, err)
		}
		if utxo.IsCoinbase && currentHeight < utxo.BlockHeight+CoinbaseMaturity {
			return fmt.Errorf("input %d: coinbase not yet mature (height %d, matures at %d)",
				i, currentHeight, utxo.BlockHeight+CoinbaseMaturity)
		}
		msg := signedMessage(u.chainID, tx, i)
		if !ed25519.Verify(ed25519.PublicKey(in.PublicKey), msg, in.Signature) {
			return fmt.Errorf("input %d: %w", i, ErrBadSignature)
		}
		derivedHash := pubKeyToHash(in.PublicKey)
		if !bytes.Equal(derivedHash, utxo.PublicKeyHash) {
			return fmt.Errorf("input %d: public key does not match UTXO owner", i)
		}
		next, carry := bits.Add64(inputSum, utxo.Value, 0)
		if carry != 0 {
			return fmt.Errorf("input %d: input values overflow uint64", i)
		}
		inputSum = next
	}
	outputSum, err := tx.TotalOutput()
	if err != nil {
		return fmt.Errorf("output sum overflow: %w", err)
	}
	if outputSum > inputSum {
		return fmt.Errorf("outputs (%d) exceed inputs (%d)", outputSum, inputSum)
	}
	return nil
}

// CalculateFee returns the fee (inputs − outputs) for a non-coinbase transaction.
// Returns an error if any input UTXO is missing, outputs exceed inputs, or any
// value sum would overflow uint64.
func (u *UTXOSet) CalculateFee(tx *Transaction) (uint64, error) {
	if tx.IsCoinbase {
		return 0, nil
	}
	var inputSum uint64
	for i, in := range tx.Inputs {
		utxo, err := u.GetUTXO(in.TxID, in.OutputIndex)
		if err != nil {
			return 0, fmt.Errorf("input utxo not found: %w", err)
		}
		next, carry := bits.Add64(inputSum, utxo.Value, 0)
		if carry != 0 {
			return 0, fmt.Errorf("input %d: input values overflow uint64", i)
		}
		inputSum = next
	}
	outputSum, err := tx.TotalOutput()
	if err != nil {
		return 0, fmt.Errorf("output sum overflow: %w", err)
	}
	if outputSum > inputSum {
		return 0, fmt.Errorf("outputs (%d) exceed inputs (%d)", outputSum, inputSum)
	}
	return inputSum - outputSum, nil
}

// validateBlockReadOnly checks all block transactions against the current UTXO
// set without making any mutations. It catches:
//   - missing or already-spent UTXOs
//   - duplicate outpoints within and across transactions
//   - coinbase maturity violations
//   - invalid Ed25519 signatures
//   - outputs exceeding inputs
//   - coinbase reward exceeding block reward + fees
//
// Returns the full UTXO data for every input spent by the block so that
// applyBlockMutations can batch-delete them without re-reading from DB.
// If this returns nil, applyBlockMutations is guaranteed to succeed (assuming
// no concurrent UTXO modifications between the two calls).
func (u *UTXOSet) validateBlockReadOnly(b *Block, height uint64) ([]UTXO, uint64, error) {
	pendingSpend := make(map[string]bool) // tracks outpoints consumed in this block
	var spentUTXOs []UTXO
	var totalFees uint64

	for _, tx := range b.Transactions {
		if tx.IsCoinbaseTx() {
			continue
		}
		var inputSum uint64
		for i, in := range tx.Inputs {
			key := outpointKey(in.TxID, in.OutputIndex)
			if pendingSpend[key] {
				return nil, 0, fmt.Errorf("tx %s: input %d is a duplicate outpoint (already consumed in this block)",
					hex.EncodeToString(tx.TxID), i)
			}
			pendingSpend[key] = true

			utxo, err := u.GetUTXO(in.TxID, in.OutputIndex)
			if err != nil {
				return nil, 0, fmt.Errorf("tx %s input %d: utxo not found: %w",
					hex.EncodeToString(tx.TxID), i, err)
			}
			if utxo.IsCoinbase && height < utxo.BlockHeight+CoinbaseMaturity {
				return nil, 0, fmt.Errorf("tx %s input %d: coinbase not yet mature (height %d, matures at %d)",
					hex.EncodeToString(tx.TxID), i, height, utxo.BlockHeight+CoinbaseMaturity)
			}
			msg := signedMessage(u.chainID, tx, i)
			if !ed25519.Verify(ed25519.PublicKey(in.PublicKey), msg, in.Signature) {
				return nil, 0, fmt.Errorf("tx %s input %d: invalid Ed25519 signature",
					hex.EncodeToString(tx.TxID), i)
			}
			if !bytes.Equal(pubKeyToHash(in.PublicKey), utxo.PublicKeyHash) {
				return nil, 0, fmt.Errorf("tx %s input %d: public key does not match UTXO owner",
					hex.EncodeToString(tx.TxID), i)
			}
			next, carry := bits.Add64(inputSum, utxo.Value, 0)
			if carry != 0 {
				return nil, 0, fmt.Errorf("tx %s input %d: input values overflow uint64",
					hex.EncodeToString(tx.TxID), i)
			}
			inputSum = next
			spentUTXOs = append(spentUTXOs, *utxo)
		}
		outputSum, err := tx.TotalOutput()
		if err != nil {
			return nil, 0, fmt.Errorf("tx %s: %w", hex.EncodeToString(tx.TxID), err)
		}
		if outputSum > inputSum {
			return nil, 0, fmt.Errorf("tx %s: outputs (%d) exceed inputs (%d)",
				hex.EncodeToString(tx.TxID), outputSum, inputSum)
		}
		fee := inputSum - outputSum
		nextFees, carry := bits.Add64(totalFees, fee, 0)
		if carry != 0 {
			return nil, 0, fmt.Errorf("tx %s: accumulated fees overflow uint64", hex.EncodeToString(tx.TxID))
		}
		totalFees = nextFees
	}

	// Validate the coinbase reward.
	for _, tx := range b.Transactions {
		if !tx.IsCoinbaseTx() {
			continue
		}
		halvings := height / HalvingInterval
		var blockReward uint64
		if halvings < 64 {
			blockReward = InitialBlockReward >> halvings
		}
		maxAllowed, carry := bits.Add64(blockReward, totalFees, 0)
		if carry != 0 {
			return nil, 0, fmt.Errorf("coinbase: blockReward+totalFees overflow uint64")
		}
		cbOutput, err := tx.TotalOutput()
		if err != nil {
			return nil, 0, fmt.Errorf("coinbase output sum overflow: %w", err)
		}
		if cbOutput > maxAllowed {
			return nil, 0, fmt.Errorf("coinbase output %d exceeds maximum allowed %d (reward=%d fees=%d)",
				cbOutput, maxAllowed, blockReward, totalFees)
		}
		break
	}
	return spentUTXOs, totalFees, nil
}

// ComputeBlockMutations validates the block and returns the full mutation set
// (spentUTXOs, creates, undo) without writing anything to storage.
// Pass the results to Storage.ApplyBlockAtomicFull to commit everything in one
// Badger transaction.
func (u *UTXOSet) ComputeBlockMutations(b *Block, height uint64) (spentUTXOs []UTXO, creates []UTXO, undo *BlockUndo, err error) {
	spentUTXOs, _, err = u.validateBlockReadOnly(b, height)
	if err != nil {
		return nil, nil, nil, err
	}
	var createdRefs []UTXORef
	for _, tx := range b.Transactions {
		isCoinbase := tx.IsCoinbaseTx()
		for idx, out := range tx.Outputs {
			creates = append(creates, UTXO{
				TxID:          tx.TxID,
				OutputIndex:   uint32(idx),
				Value:         out.Value,
				PublicKeyHash: out.PublicKeyHash,
				BlockHeight:   height,
				IsCoinbase:    isCoinbase,
			})
			createdRefs = append(createdRefs, UTXORef{TxID: tx.TxID, OutputIndex: uint32(idx)})
		}
	}
	undo = &BlockUndo{SpentUTXOs: spentUTXOs, CreatedRefs: createdRefs}
	return spentUTXOs, creates, undo, nil
}

// ProcessBlock validates then applies an entire block to the UTXO set atomically:
// validateBlockReadOnly runs first (no mutations); only if it passes does
// applyBlockMutations write to the database in a single Badger transaction.
// A failure in either phase leaves the UTXO set unchanged.
func (u *UTXOSet) ProcessBlock(b *Block, height uint64) (*BlockUndo, error) {
	spentUTXOs, _, err := u.validateBlockReadOnly(b, height)
	if err != nil {
		return nil, err
	}
	return u.applyBlockMutations(b, height, spentUTXOs)
}

// applyBlockMutations commits the UTXO changes for b in a single Badger
// transaction and returns undo data. Must only be called after
// validateBlockReadOnly has returned nil for the same block.
func (u *UTXOSet) applyBlockMutations(b *Block, height uint64, spentUTXOs []UTXO) (*BlockUndo, error) {
	var creates []UTXO
	var createdRefs []UTXORef

	for _, tx := range b.Transactions {
		isCoinbase := tx.IsCoinbaseTx()
		for idx, out := range tx.Outputs {
			creates = append(creates, UTXO{
				TxID:          tx.TxID,
				OutputIndex:   uint32(idx),
				Value:         out.Value,
				PublicKeyHash: out.PublicKeyHash,
				BlockHeight:   height,
				IsCoinbase:    isCoinbase,
			})
			createdRefs = append(createdRefs, UTXORef{TxID: tx.TxID, OutputIndex: uint32(idx)})
		}
	}

	if err := u.Storage.ApplyUTXOBatch(spentUTXOs, creates); err != nil {
		return nil, fmt.Errorf("apply utxo batch: %w", err)
	}

	return &BlockUndo{
		SpentUTXOs:  spentUTXOs,
		CreatedRefs: createdRefs,
	}, nil
}

// RollbackBlock reverses the UTXO changes made when a block was applied.
// Used during chain reorganisation to revert the old main chain before
// switching to the new one.
func (u *UTXOSet) RollbackBlock(undo *BlockUndo) error {
	// Delete all UTXOs created by the block. If a UTXO is already missing
	// (e.g. UTXO index was corrupted by a prior relay bug), treat it as
	// already deleted — the goal is for it to not exist, and it doesn't.
	for _, ref := range undo.CreatedRefs {
		err := u.SpendUTXO(ref.TxID, ref.OutputIndex)
		if err == nil {
			continue
		}
		// SpendUTXO failed — only a real error if the UTXO still exists.
		if _, getErr := u.GetUTXO(ref.TxID, ref.OutputIndex); getErr != nil {
			continue // already gone — goal achieved
		}
		return fmt.Errorf("rollback: delete created utxo %x:%d: %w",
			ref.TxID, ref.OutputIndex, err)
	}
	// Restore all UTXOs that the block spent.
	for _, utxo := range undo.SpentUTXOs {
		if err := u.AddUTXO(utxo); err != nil {
			return fmt.Errorf("rollback: restore spent utxo %x:%d: %w",
				utxo.TxID, utxo.OutputIndex, err)
		}
	}
	return nil
}
