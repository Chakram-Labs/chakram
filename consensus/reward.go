// Package consensus provides utility functions shared across Chakram's engine
// and ABCI layers: reward schedule, quantity encoding, and withdrawal helpers.
// This package contains no PoW code.
package consensus

import (
	"fmt"
	"strconv"
	"strings"
)

// Reward schedule constants.
const (
	// HalvingInterval is the number of blocks between reward halvings.
	// At 2s/block: 8,409,600 blocks ≈ 4 years.
	HalvingInterval uint64 = 8_409_600
	BaseRewardWei   uint64 = 5_000_000_000_000_000_000 // 5 CHK in wei
	TailEmissionWei uint64 = 300_000_000_000_000_000   // 0.3 CHK in wei
)

// BlockRewardWei returns the block reward in wei for the block at height.
// Halves every HalvingInterval blocks; floor is TailEmissionWei.
func BlockRewardWei(height uint64) uint64 {
	halvings := height / HalvingInterval
	if halvings >= 64 {
		return TailEmissionWei
	}
	reward := BaseRewardWei >> halvings
	if reward < TailEmissionWei {
		return TailEmissionWei
	}
	return reward
}

// BlockRewardGwei returns the block reward in Gwei (wei / 1e9).
// The Engine API withdrawal `amount` field is in Gwei.
func BlockRewardGwei(height uint64) uint64 {
	return BlockRewardWei(height) / 1_000_000_000
}

// WithdrawalIndex returns the Engine API withdrawal index for a block at height.
// Genesis has no withdrawal; height 1 starts at index 0.
// Use only when no ChakramLocker is configured (single-withdrawal mode).
func WithdrawalIndex(height uint64) uint64 {
	if height == 0 {
		panic("consensus: WithdrawalIndex called for genesis (height 0)")
	}
	return height - 1
}

// LockerWithdrawalIndices returns the two EIP-4895 withdrawal indices for a
// block at height when ChakramLocker is enabled.
//
// lockerStartHeight is the first block that uses dual withdrawals (set once
// in AppState when the locker contract address is first configured).
//
// Before lockerStartHeight blocks used a single withdrawal (index = height-1),
// so the dual-withdrawal indices are offset to maintain strict monotonicity:
//
//	N = lockerStartHeight - 1  (last single-withdrawal height; 0 if locker from block 1)
//	validatorIdx = 2*height - N - 2
//	lockerIdx    = 2*height - N - 1
func LockerWithdrawalIndices(height uint64, lockerStartHeight int64) (validatorIdx, lockerIdx uint64) {
	if height == 0 {
		panic("consensus: LockerWithdrawalIndices called for genesis (height 0)")
	}
	n := uint64(lockerStartHeight - 1) // last single-withdrawal height (may be 0)
	validatorIdx = 2*height - n - 2
	lockerIdx = 2*height - n - 1
	return validatorIdx, lockerIdx
}

// BlockLockGwei returns the 80 % locker share of the block reward in Gwei.
func BlockLockGwei(height uint64) uint64 {
	full := BlockRewardGwei(height)
	return full - full/5 // 80% (avoid rounding loss vs validator share)
}

// BlockValidatorGwei returns the 20 % validator immediate share in Gwei.
func BlockValidatorGwei(height uint64) uint64 {
	return BlockRewardGwei(height) / 5
}

// QuantityHex encodes a uint64 as an Engine API QUANTITY string.
func QuantityHex(v uint64) string {
	if v == 0 {
		return "0x0"
	}
	return "0x" + strconv.FormatUint(v, 16)
}

// ParseQuantity decodes an Engine API QUANTITY hex string to uint64.
func ParseQuantity(s string) (uint64, error) {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parseQuantity %q: %w", s, err)
	}
	return v, nil
}
