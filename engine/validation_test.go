package engine

import (
	"strings"
	"testing"

	"chakram/consensus"
)

// goodPayload returns a ValidateBuiltPayload-ready ExecutionPayloadV3 for height 1.
func goodPayload() (ExecutionPayloadV3, uint64, int64, string, string, WithdrawalV1) {
	height := uint64(1)
	timestamp := int64(1_000_000_015)
	parentExecHash := "0x" + strings.Repeat("aa", 32)
	prevRandao := "0x" + strings.Repeat("bb", 32)
	withdrawal := WithdrawalV1{
		Index:          consensus.QuantityHex(consensus.WithdrawalIndex(height)),
		ValidatorIndex: "0x0",
		Address:        "0x" + strings.Repeat("cc", 20),
		Amount:         consensus.QuantityHex(consensus.BlockRewardGwei(height)),
	}
	p := ExecutionPayloadV3{
		ParentHash:  parentExecHash,
		PrevRandao:  prevRandao,
		BlockNumber: consensus.QuantityHex(height),
		Timestamp:   consensus.QuantityHex(uint64(timestamp)),
		BlobGasUsed: "0x0",
		Withdrawals: []WithdrawalV1{withdrawal},
	}
	return p, height, timestamp, parentExecHash, prevRandao, withdrawal
}

func TestValidateBuiltPayloadHappy(t *testing.T) {
	p, height, ts, parentHash, prevRandao, w := goodPayload()
	if err := ValidateBuiltPayload(p, height, ts, parentHash, prevRandao, w); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateBuiltPayloadWrongBlockNumber(t *testing.T) {
	p, height, ts, parentHash, prevRandao, w := goodPayload()
	p.BlockNumber = consensus.QuantityHex(height + 1)
	if err := ValidateBuiltPayload(p, height, ts, parentHash, prevRandao, w); err == nil {
		t.Error("expected error for wrong blockNumber")
	}
}

func TestValidateBuiltPayloadWrongTimestamp(t *testing.T) {
	p, height, ts, parentHash, prevRandao, w := goodPayload()
	p.Timestamp = consensus.QuantityHex(uint64(ts + 1))
	if err := ValidateBuiltPayload(p, height, ts, parentHash, prevRandao, w); err == nil {
		t.Error("expected error for wrong timestamp")
	}
}

func TestValidateBuiltPayloadWrongParentHash(t *testing.T) {
	p, height, ts, _, prevRandao, w := goodPayload()
	if err := ValidateBuiltPayload(p, height, ts, "0x"+strings.Repeat("11", 32), prevRandao, w); err == nil {
		t.Error("expected error for wrong parentHash")
	}
}

func TestValidateBuiltPayloadBlobGasNonZero(t *testing.T) {
	p, height, ts, parentHash, prevRandao, w := goodPayload()
	p.BlobGasUsed = "0x1"
	if err := ValidateBuiltPayload(p, height, ts, parentHash, prevRandao, w); err == nil {
		t.Error("expected error for non-zero blobGasUsed")
	}
}

func TestValidateBuiltPayloadNoWithdrawals(t *testing.T) {
	p, height, ts, parentHash, prevRandao, w := goodPayload()
	p.Withdrawals = nil
	if err := ValidateBuiltPayload(p, height, ts, parentHash, prevRandao, w); err == nil {
		t.Error("expected error for missing withdrawal")
	}
}

func TestValidateBuiltPayloadWrongWithdrawalAmount(t *testing.T) {
	p, height, ts, parentHash, prevRandao, w := goodPayload()
	p.Withdrawals[0].Amount = "0x1"
	if err := ValidateBuiltPayload(p, height, ts, parentHash, prevRandao, w); err == nil {
		t.Error("expected error for wrong withdrawal amount")
	}
}

// ── ValidatePeerPayload ───────────────────────────────────────────────────────

func goodPeerPayloadComponents() (ExecutionPayloadV3, consensus.ConsensusBlockHeader, string, string) {
	height := uint64(3)
	execHashBytes := [32]byte{0xde, 0xad}
	parentExecHash := "0x" + strings.Repeat("aa", 32)
	prevRandao := "0x" + strings.Repeat("bb", 32)
	minerAddr := [20]byte{0xcc}

	header := consensus.ConsensusBlockHeader{
		Height:               height,
		ExecutionPayloadHash: execHashBytes,
		Timestamp:            1_000_000_045,
		MinerAddress:         minerAddr,
	}
	p := ExecutionPayloadV3{
		BlockHash:   consensus.HashToHex(execHashBytes),
		BlockNumber: consensus.QuantityHex(height),
		Timestamp:   consensus.QuantityHex(uint64(header.Timestamp)),
		ParentHash:  parentExecHash,
		PrevRandao:  prevRandao,
		BlobGasUsed: "0x0",
		Withdrawals: []WithdrawalV1{
			{
				Index:          consensus.QuantityHex(consensus.WithdrawalIndex(height)),
				ValidatorIndex: "0x0",
				Address:        "0xcc" + strings.Repeat("00", 19),
				Amount:         consensus.QuantityHex(consensus.BlockRewardGwei(height)),
			},
		},
	}
	return p, header, parentExecHash, prevRandao
}

func TestValidatePeerPayloadHappy(t *testing.T) {
	p, hdr, parentHash, prevRandao := goodPeerPayloadComponents()
	if err := ValidatePeerPayload(p, hdr, parentHash, prevRandao, hdr.Height-1); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidatePeerPayloadWrongBlockHash(t *testing.T) {
	p, hdr, parentHash, prevRandao := goodPeerPayloadComponents()
	p.BlockHash = "0x" + strings.Repeat("ff", 32)
	if err := ValidatePeerPayload(p, hdr, parentHash, prevRandao, hdr.Height-1); err == nil {
		t.Error("expected error for wrong blockHash")
	}
}

func TestValidatePeerPayloadBlobGasNonZero(t *testing.T) {
	p, hdr, parentHash, prevRandao := goodPeerPayloadComponents()
	p.BlobGasUsed = "0x1"
	if err := ValidatePeerPayload(p, hdr, parentHash, prevRandao, hdr.Height-1); err == nil {
		t.Error("expected error for non-zero blobGasUsed")
	}
}

func TestValidatePeerPayloadWrongWithdrawalIndex(t *testing.T) {
	p, hdr, parentHash, prevRandao := goodPeerPayloadComponents()
	p.Withdrawals[0].Index = "0x9999"
	if err := ValidatePeerPayload(p, hdr, parentHash, prevRandao, hdr.Height-1); err == nil {
		t.Error("expected error for wrong withdrawal index")
	}
}
