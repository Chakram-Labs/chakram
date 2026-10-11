package consensus

import (
	"math/big"
)

// Difficulty parameters for Chakram 2.0.
const (
	TargetBlockTime  int64  = 15  // seconds
	DifficultyWindow uint64 = 90  // LWMA window; tune during Phase 3 devnet
	MTPWindow        int    = 11  // blocks used for median-time-past
)

// BlockHeaderFetcher retrieves the header at the given canonical height.
// Returns an error if the height is not yet stored.
type BlockHeaderFetcher func(height uint64) (ConsensusBlockHeader, error)

// NextDifficulty returns the compact-encoded PoW target for the block at height
// whose immediate parent is parent.
//
// For the first DifficultyWindow blocks (bootstrap) it returns InitialCompactTargetV2.
// Afterwards it runs LWMA-3 over the last DifficultyWindow headers.
//
// fetch provides header lookup; it is called for heights [height-1-N … height-1]
// where N = DifficultyWindow.
func NextDifficulty(fetch BlockHeaderFetcher, height uint64, parent ConsensusBlockHeader) uint64 {
	if height <= DifficultyWindow {
		// During bootstrap, carry forward whatever target the genesis block started
		// with.  Mainnet uses InitialCompactTargetV2; devnet uses
		// DevnetInitialCompactTarget.  Using parent.Difficulty (not a hardcoded
		// constant) means both chains converge correctly after the window.
		return parent.Difficulty
	}

	N := int(DifficultyWindow)
	headers := make([]ConsensusBlockHeader, N+1)

	// Index N = parent (newest), index 0 = oldest.
	headers[N] = parent
	cur := parent
	for i := N - 1; i >= 0; i-- {
		h, err := fetch(cur.Height - 1)
		if err != nil {
			return InitialCompactTargetV2
		}
		headers[i] = h
		cur = h
	}

	currentTarget := DecodeCompact(headers[N].Difficulty)
	if currentTarget.Sign() == 0 {
		currentTarget = DecodeCompact(InitialCompactTargetV2)
	}

	avg := lwma3(headers, N)
	return EncodeCompact(adjustTarget(currentTarget, avg))
}

// MedianTimePast returns the median of the last MTPWindow timestamps ending at
// the block at parentHeight (inclusive). Returns 0 if not enough history.
func MedianTimePast(fetch BlockHeaderFetcher, parentHeight uint64) int64 {
	count := MTPWindow
	if int(parentHeight)+1 < count {
		count = int(parentHeight) + 1
	}
	if count == 0 {
		return 0
	}

	timestamps := make([]int64, count)
	h := parentHeight
	for i := count - 1; i >= 0; i-- {
		hdr, err := fetch(h)
		if err != nil {
			return 0
		}
		timestamps[i] = hdr.Timestamp
		if h == 0 {
			// Genesis — fill remaining slots with genesis timestamp.
			for j := i - 1; j >= 0; j-- {
				timestamps[j] = hdr.Timestamp
			}
			break
		}
		h--
	}

	sorted := make([]int64, count)
	copy(sorted, timestamps)
	sortInt64s(sorted)
	return sorted[count/2]
}

// MiningTimestamp computes the timestamp a miner should use for the block at
// height, whose parent is parent. Implements the clock-based rule from spec §5.3.
func MiningTimestamp(now int64, parentHeight uint64, parentTimestamp int64, fetch BlockHeaderFetcher) int64 {
	mtp := MedianTimePast(fetch, parentHeight)

	height := parentHeight + 1
	minGap := int64(1)
	if height <= DifficultyWindow {
		minGap = TargetBlockTime
	}

	ts := now
	if mtp+1 > ts {
		ts = mtp + 1
	}
	if parentTimestamp+minGap > ts {
		ts = parentTimestamp + minGap
	}
	return ts
}

// ── internal ──────────────────────────────────────────────────────────────────

func lwma3(headers []ConsensusBlockHeader, N int) int64 {
	var weightedSum, divisor int64
	for i := 1; i <= N; i++ {
		solveTime := headers[i].Timestamp - headers[i-1].Timestamp
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

func adjustTarget(current *big.Int, avg int64) *big.Int {
	// new_target = current × avg / TargetBlockTime
	// Larger avg (slow blocks) → larger target → easier.
	newTarget := new(big.Int).Mul(current, big.NewInt(avg))
	newTarget.Div(newTarget, big.NewInt(TargetBlockTime))

	// Cap: difficulty cannot increase by more than 4× per window
	// (target cannot fall below current/4).
	minTarget := new(big.Int).Div(current, big.NewInt(4))
	if newTarget.Cmp(minTarget) < 0 {
		newTarget = minTarget
	}

	// Floor: target must not exceed InitialCompactTargetV2 (minimum work).
	maxTarget := DecodeCompact(InitialCompactTargetV2)
	if newTarget.Cmp(maxTarget) > 0 {
		newTarget = maxTarget
	}
	return newTarget
}

func sortInt64s(a []int64) {
	// Simple insertion sort — MTPWindow = 11, so O(n²) is fine.
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}
