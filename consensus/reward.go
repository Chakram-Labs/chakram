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
func WithdrawalIndex(height uint64) uint64 {
	if height == 0 {
		panic("consensus: WithdrawalIndex called for genesis (height 0)")
	}
	return height - 1
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
