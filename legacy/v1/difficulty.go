// difficulty.go — LWMA-3 difficulty retarget and fork-version helpers for Chakram.
// Uses Linearly Weighted Moving Average (LWMA-3) giving more weight to recent
// blocks. Adopted by low-hashrate networks (Zcash, Beam) to resist timestamp
// manipulation and hash-rate volatility.
package main

import "math/big"

// NextDifficulty returns the proof-of-work difficulty for the block at height.
// Called by the miner and must produce identical results on every node.
//
// Algorithm (LWMA-3):
//   - For the first DifficultyWindow blocks return InitialDifficulty so the
//     chain starts at a sensible rate without a calibration period.
//   - Fetch the last DifficultyWindow+1 blocks to derive N solve times.
//   - Assign linearly increasing weights (oldest=1, newest=N) so recent blocks
//     influence the estimate more than old ones.
//   - Clamp each solve time to [1, 6×TargetBlockTime] to limit the damage a
//     manipulated timestamp can do to a single sample.
//   - Adjust current difficulty by TargetBlockTime / weighted_avg.
//   - Cap increases at 4× per window (prevents runaway escalation from a
//     sudden hashrate spike). No downward cap: if difficulty overshoots and
//     blocks slow down, the chain self-corrects in a single adjustment rather
//     than being permanently stuck.
//   - Result never falls below MinDifficulty.
func NextDifficulty(bc *Blockchain, height uint64) uint64 {
	if ProtocolVersionAt(height) >= 2 {
		return nextDifficultyV2(bc.Storage, height)
	}
	if height <= DifficultyWindow {
		return InitialDifficulty
	}

	N := int(DifficultyWindow)

	// Fetch N+1 consecutive blocks (oldest → newest).
	blocks := make([]*Block, N+1)
	for i := 0; i <= N; i++ {
		h := height - 1 - uint64(N-i)
		b, err := bc.Storage.GetBlockByHeight(h)
		if err != nil {
			return InitialDifficulty
		}
		blocks[i] = b
	}

	current := blocks[N].Header.Difficulty
	if current < InitialDifficulty {
		current = InitialDifficulty
	}

	// Linearly weighted sum of clamped solve times.
	var weightedSum, divisor int64
	for i := 1; i <= N; i++ {
		solveTime := blocks[i].Header.Timestamp - blocks[i-1].Header.Timestamp
		if solveTime < 1 {
			solveTime = 1
		}
		if solveTime > 6*TargetBlockTime {
			solveTime = 6 * TargetBlockTime
		}
		weight := int64(i)
		weightedSum += solveTime * weight
		divisor += weight
	}

	avg := weightedSum / divisor
	if avg < 1 {
		avg = 1
	}

	next := uint64(float64(current) * float64(TargetBlockTime) / float64(avg))

	// Cap increases at 4× per window; no downward cap so the chain can recover
	// from overshoots without getting permanently stuck.
	if next > current*4 {
		next = current * 4
	}
	if next < MinDifficulty {
		next = MinDifficulty
	}
	return next
}

// ProtocolVersionAt returns the consensus rule set that applies at height.
// Validation code gates new rules behind: ProtocolVersionAt(height) >= newVer.
// When a new fork is scheduled, add its entry to ForkActivations in config.go.
func ProtocolVersionAt(height uint64) uint32 {
	var best uint32 = 1
	for ver, actHeight := range ForkActivations {
		if height >= actHeight && ver > best {
			best = ver
		}
	}
	return best
}

// highestCheckpoint returns the highest checkpointed block height, or 0 if none.
func highestCheckpoint() uint64 {
	var h uint64
	for height := range Checkpoints {
		if height > h {
			h = height
		}
	}
	return h
}

// NextDifficultyFromParent computes the expected difficulty for a new block
// whose immediate parent is parent. Unlike NextDifficulty, it walks ancestor
// blocks via GetBlockByHash so it works correctly for blocks on a fork that
// has not yet been adopted as the canonical chain.
func NextDifficultyFromParent(s *Storage, height uint64, parent *Block) uint64 {
	if ProtocolVersionAt(height) >= 2 {
		return nextDifficultyFromParentV2(s, height, parent)
	}
	if height <= DifficultyWindow {
		return InitialDifficulty
	}

	N := int(DifficultyWindow)

	// Walk back N+1 blocks via parent hashes (oldest at index 0).
	blocks := make([]*Block, N+1)
	blocks[N] = parent
	cur := parent
	for i := N - 1; i >= 0; i-- {
		prev, err := s.GetBlockByHash(cur.Header.PreviousHash)
		if err != nil {
			return InitialDifficulty
		}
		blocks[i] = prev
		cur = prev
	}

	current := blocks[N].Header.Difficulty
	if current < InitialDifficulty {
		current = InitialDifficulty
	}

	var weightedSum, divisor int64
	for i := 1; i <= N; i++ {
		solveTime := blocks[i].Header.Timestamp - blocks[i-1].Header.Timestamp
		if solveTime < 1 {
			solveTime = 1
		}
		if solveTime > 6*TargetBlockTime {
			solveTime = 6 * TargetBlockTime
		}
		weight := int64(i)
		weightedSum += solveTime * weight
		divisor += weight
	}

	avg := weightedSum / divisor
	if avg < 1 {
		avg = 1
	}

	next := uint64(float64(current) * float64(TargetBlockTime) / float64(avg))

	if next > current*4 {
		next = current * 4
	}
	if next < MinDifficulty {
		next = MinDifficulty
	}
	return next
}

// ── Protocol v2 difficulty (compact continuous target) ────────────────────────
//
// From block 10000, difficulty is stored as a compact uint64 target instead of
// an integer bit-count. This lets LWMA make fractional adjustments so the chain
// smoothly tracks the true equilibrium difficulty rather than oscillating between
// coarse integer steps. See decodeCompact/encodeCompact in block.go.

func lwmaWindowV2(blocks []*Block, N int) int64 {
	var weightedSum, divisor int64
	for i := 1; i <= N; i++ {
		solveTime := blocks[i].Header.Timestamp - blocks[i-1].Header.Timestamp
		if solveTime < 1 {
			solveTime = 1
		}
		if solveTime > 6*TargetBlockTime {
			solveTime = 6 * TargetBlockTime
		}
		weight := int64(i)
		weightedSum += solveTime * weight
		divisor += weight
	}
	avg := weightedSum / divisor
	if avg < 1 {
		avg = 1
	}
	return avg
}

func adjustTargetV2(currentTarget *big.Int, avg int64) *big.Int {
	// new_target = current_target × avg / TargetBlockTime
	// Higher avg (slow blocks) → larger target → easier.
	// Lower avg (fast blocks) → smaller target → harder.
	newTarget := new(big.Int).Mul(currentTarget, big.NewInt(avg))
	newTarget.Div(newTarget, big.NewInt(TargetBlockTime))

	// Cap: difficulty must not increase by more than 4× per window
	// (target must not fall below current/4).
	minTarget := new(big.Int).Div(currentTarget, big.NewInt(4))
	if newTarget.Cmp(minTarget) < 0 {
		newTarget = minTarget
	}

	// Floor: target must not exceed InitialCompactTargetV2 (minimum difficulty).
	maxTarget := decodeCompact(InitialCompactTargetV2)
	if newTarget.Cmp(maxTarget) > 0 {
		newTarget = maxTarget
	}

	return newTarget
}

func nextDifficultyV2(s *Storage, height uint64) uint64 {
	actHeight := ForkActivations[2]
	if height <= actHeight {
		return InitialCompactTargetV2
	}

	N := int(DifficultyWindow)
	blocks := make([]*Block, N+1)
	for i := 0; i <= N; i++ {
		h := height - 1 - uint64(N-i)
		b, err := s.GetBlockByHeight(h)
		if err != nil {
			return InitialCompactTargetV2
		}
		blocks[i] = b
	}

	var currentTarget *big.Int
	if blocks[N].Header.Height >= actHeight {
		currentTarget = decodeCompact(blocks[N].Header.Difficulty)
	} else {
		currentTarget = decodeCompact(InitialCompactTargetV2)
	}

	avg := lwmaWindowV2(blocks, N)
	return encodeCompact(adjustTargetV2(currentTarget, avg))
}

func nextDifficultyFromParentV2(s *Storage, height uint64, parent *Block) uint64 {
	actHeight := ForkActivations[2]
	if height <= actHeight {
		return InitialCompactTargetV2
	}

	N := int(DifficultyWindow)
	blocks := make([]*Block, N+1)
	blocks[N] = parent
	cur := parent
	for i := N - 1; i >= 0; i-- {
		prev, err := s.GetBlockByHash(cur.Header.PreviousHash)
		if err != nil {
			return InitialCompactTargetV2
		}
		blocks[i] = prev
		cur = prev
	}

	var currentTarget *big.Int
	if blocks[N].Header.Height >= actHeight {
		currentTarget = decodeCompact(blocks[N].Header.Difficulty)
	} else {
		currentTarget = decodeCompact(InitialCompactTargetV2)
	}

	avg := lwmaWindowV2(blocks, N)
	return encodeCompact(adjustTargetV2(currentTarget, avg))
}
