package engine

import (
	"fmt"
	"strings"

	"chakram/consensus"
)

// ValidateBuiltPayload verifies that the ExecutionPayloadV3 returned by
// engine_getPayloadV3 matches exactly what the consensus client requested
// (spec §5.4 step 4a). Any mismatch is a consensus-client bug — abort mining.
func ValidateBuiltPayload(
	payload ExecutionPayloadV3,
	height uint64,
	timestamp int64,
	parentExecHashHex string,  // 0x-prefixed
	prevRandaoHex string,      // 0x-prefixed
	withdrawal WithdrawalV1,
) error {
	// blockNumber == height
	gotNum, err := consensus.ParseQuantity(payload.BlockNumber)
	if err != nil {
		return fmt.Errorf("blockNumber: %w", err)
	}
	if gotNum != height {
		return fmt.Errorf("blockNumber: got %d, want %d", gotNum, height)
	}

	// timestamp == header.Timestamp
	gotTS, err := consensus.ParseQuantity(payload.Timestamp)
	if err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	if int64(gotTS) != timestamp {
		return fmt.Errorf("timestamp: got %d, want %d", gotTS, timestamp)
	}

	// parentHash == parent.ExecutionPayloadHash
	if !strings.EqualFold(payload.ParentHash, parentExecHashHex) {
		return fmt.Errorf("parentHash: got %s, want %s", payload.ParentHash, parentExecHashHex)
	}

	// prevRandao == keccak256(parentConsensusBlockHash)
	if !strings.EqualFold(payload.PrevRandao, prevRandaoHex) {
		return fmt.Errorf("prevRandao: got %s, want %s", payload.PrevRandao, prevRandaoHex)
	}

	// blobGasUsed == 0
	blobGas, err := consensus.ParseQuantity(payload.BlobGasUsed)
	if err != nil {
		return fmt.Errorf("blobGasUsed: %w", err)
	}
	if blobGas != 0 {
		return fmt.Errorf("blobGasUsed: got %d, want 0 (blobs are disabled)", blobGas)
	}

	// exactly one withdrawal with the correct index, address, amount
	// withdrawal fields are already Quantity (hex string) — compare directly.
	if len(payload.Withdrawals) != 1 {
		return fmt.Errorf("withdrawals: got %d, want 1", len(payload.Withdrawals))
	}
	w := payload.Withdrawals[0]
	if !strings.EqualFold(w.Index, withdrawal.Index) {
		return fmt.Errorf("withdrawal.index: got %s, want %s", w.Index, withdrawal.Index)
	}
	if !strings.EqualFold(w.ValidatorIndex, withdrawal.ValidatorIndex) {
		return fmt.Errorf("withdrawal.validatorIndex: got %s, want %s", w.ValidatorIndex, withdrawal.ValidatorIndex)
	}
	if !strings.EqualFold(w.Address, withdrawal.Address) {
		return fmt.Errorf("withdrawal.address: got %s, want %s", w.Address, withdrawal.Address)
	}
	if !strings.EqualFold(w.Amount, withdrawal.Amount) {
		return fmt.Errorf("withdrawal.amount: got %s, want %s", w.Amount, withdrawal.Amount)
	}

	return nil
}

// ValidatePeerPayload performs pre-flight checks on an execution payload
// received from a peer before passing it to engine_newPayloadV3
// (spec §5.5 step 2a). Returns an error with a description suitable for
// logging a peer penalty.
func ValidatePeerPayload(
	payload ExecutionPayloadV3,
	header consensus.ConsensusBlockHeader,
	parentExecHashHex string,  // parent.ExecutionPayloadHash
	prevRandaoHex string,      // keccak256(parentConsensusHash)
	parentHeight uint64,
) error {
	height := header.Height

	// blockHash == header.ExecutionPayloadHash
	wantExecHash := consensus.HashToHex(header.ExecutionPayloadHash)
	if !strings.EqualFold(payload.BlockHash, wantExecHash) {
		return fmt.Errorf("blockHash: got %s, want %s (from consensus header)", payload.BlockHash, wantExecHash)
	}

	// blockNumber == header.Height
	gotNum, err := consensus.ParseQuantity(payload.BlockNumber)
	if err != nil {
		return fmt.Errorf("blockNumber: %w", err)
	}
	if gotNum != height {
		return fmt.Errorf("blockNumber: got %d, want %d", gotNum, height)
	}

	// timestamp == header.Timestamp
	gotTS, err := consensus.ParseQuantity(payload.Timestamp)
	if err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	if int64(gotTS) != header.Timestamp {
		return fmt.Errorf("timestamp: got %d, want %d", gotTS, header.Timestamp)
	}

	// parentHash == parent.ExecutionPayloadHash
	if !strings.EqualFold(payload.ParentHash, parentExecHashHex) {
		return fmt.Errorf("parentHash: got %s, want %s", payload.ParentHash, parentExecHashHex)
	}

	// prevRandao == keccak256(parentConsensusBlockHash)
	if !strings.EqualFold(payload.PrevRandao, prevRandaoHex) {
		return fmt.Errorf("prevRandao: got %s, want %s", payload.PrevRandao, prevRandaoHex)
	}

	// blobGasUsed == 0
	blobGas, err := consensus.ParseQuantity(payload.BlobGasUsed)
	if err != nil {
		return fmt.Errorf("blobGasUsed: %w", err)
	}
	if blobGas != 0 {
		return fmt.Errorf("blobGasUsed: got %d, want 0 (blobs disabled)", blobGas)
	}

	// withdrawal checks
	if height == 0 {
		if len(payload.Withdrawals) != 0 {
			return fmt.Errorf("genesis must have 0 withdrawals, got %d", len(payload.Withdrawals))
		}
		return nil
	}
	if len(payload.Withdrawals) != 1 {
		return fmt.Errorf("withdrawals: got %d, want 1", len(payload.Withdrawals))
	}
	w := payload.Withdrawals[0]

	wantIdx := consensus.QuantityHex(consensus.WithdrawalIndex(height))
	if !strings.EqualFold(w.Index, wantIdx) {
		return fmt.Errorf("withdrawal.index: got %s, want %s", w.Index, wantIdx)
	}
	if !strings.EqualFold(w.ValidatorIndex, "0x0") {
		return fmt.Errorf("withdrawal.validatorIndex: got %s, want 0x0", w.ValidatorIndex)
	}

	wantAddr := consensus.HashToHex([32]byte{}) // will be overridden below
	_ = wantAddr
	minerAddrHex := "0x" + fmt.Sprintf("%x", header.MinerAddress)
	if !strings.EqualFold(w.Address, minerAddrHex) {
		return fmt.Errorf("withdrawal.address: got %s, want %s (from consensus header)", w.Address, minerAddrHex)
	}

	wantAmt := consensus.QuantityHex(consensus.BlockRewardGwei(height))
	if !strings.EqualFold(w.Amount, wantAmt) {
		return fmt.Errorf("withdrawal.amount: got %s, want %s", w.Amount, wantAmt)
	}

	return nil
}
