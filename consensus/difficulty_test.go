package consensus

import (
	"fmt"
	"math/big"
	"testing"
)

// makeHeaders builds n+1 headers at regular solve-time intervals starting at
// timestamp 1_000_000_000, all carrying the given compact difficulty target.
func makeHeaders(n int, solveTimeSec int64, diff uint64) []ConsensusBlockHeader {
	out := make([]ConsensusBlockHeader, n+1)
	var ts int64 = 1_000_000_000
	for i := range out {
		out[i] = ConsensusBlockHeader{
			Height:     uint64(i),
			Timestamp:  ts,
			Difficulty: diff,
		}
		ts += solveTimeSec
	}
	return out
}

// memFetcher returns a BlockHeaderFetcher backed by a slice (linear scan, fine
// for unit tests).
func memFetcher(headers []ConsensusBlockHeader) BlockHeaderFetcher {
	return func(height uint64) (ConsensusBlockHeader, error) {
		for _, h := range headers {
			if h.Height == height {
				return h, nil
			}
		}
		return ConsensusBlockHeader{}, fmt.Errorf("height %d not found", height)
	}
}

// harderCompact is a compact target 4× harder than InitialCompactTargetV2 (¼ the
// target value). Used in tests that need room to adjust upward.
func harderCompact() uint64 {
	return EncodeCompact(new(big.Int).Rsh(DecodeCompact(InitialCompactTargetV2), 2))
}

func TestNextDifficultyBootstrap(t *testing.T) {
	fetch := memFetcher(nil)
	parent := ConsensusBlockHeader{Height: 0, Difficulty: InitialCompactTargetV2}
	for h := uint64(1); h <= DifficultyWindow; h++ {
		if got := NextDifficulty(fetch, h, parent); got != InitialCompactTargetV2 {
			t.Errorf("height=%d: got %016x, want %016x", h, got, InitialCompactTargetV2)
		}
	}
}

func TestNextDifficultySlowBlocksEasier(t *testing.T) {
	// Start from a target 4× harder than initial so slow blocks have room to
	// push the target upward (toward easier).
	harder := harderCompact()
	N := int(DifficultyWindow)
	headers := makeHeaders(N, TargetBlockTime*2, harder)
	got := DecodeCompact(NextDifficulty(memFetcher(headers), uint64(N)+1, headers[N]))
	current := DecodeCompact(harder)
	if got.Cmp(current) <= 0 {
		t.Errorf("slow blocks should raise target (easier); got %x <= current %x", got, current)
	}
}

func TestNextDifficultyFastBlocksHarder(t *testing.T) {
	// Fast blocks (½ target time) → smaller target → harder mining.
	N := int(DifficultyWindow)
	headers := makeHeaders(N, TargetBlockTime/2, InitialCompactTargetV2)
	got := DecodeCompact(NextDifficulty(memFetcher(headers), uint64(N)+1, headers[N]))
	current := DecodeCompact(InitialCompactTargetV2)
	if got.Cmp(current) >= 0 {
		t.Errorf("fast blocks should lower target (harder); got %x >= current %x", got, current)
	}
}

func TestNextDifficultyCap4x(t *testing.T) {
	// Extreme 1-second blocks: difficulty adjustment must not drop the target
	// below current/4 (the 4× per-window cap).
	N := int(DifficultyWindow)
	headers := makeHeaders(N, 1, InitialCompactTargetV2)
	got := DecodeCompact(NextDifficulty(memFetcher(headers), uint64(N)+1, headers[N]))
	current := DecodeCompact(InitialCompactTargetV2)
	cap4x := new(big.Int).Div(current, big.NewInt(4))
	if got.Cmp(cap4x) < 0 {
		t.Errorf("target dropped below current/4 cap: got %x, min allowed %x", got, cap4x)
	}
}

func TestMedianTimePastGenesis(t *testing.T) {
	genesis := ConsensusBlockHeader{Height: 0, Timestamp: 1_000_000}
	if mtp := MedianTimePast(memFetcher([]ConsensusBlockHeader{genesis}), 0); mtp != 1_000_000 {
		t.Errorf("MTP at genesis: got %d, want 1000000", mtp)
	}
}

func TestMedianTimePast11Blocks(t *testing.T) {
	// 11 blocks with timestamps 100, 200, …, 1100. Median = 600 (middle index 5).
	headers := make([]ConsensusBlockHeader, 11)
	for i := range headers {
		headers[i] = ConsensusBlockHeader{Height: uint64(i), Timestamp: int64((i + 1) * 100)}
	}
	if mtp := MedianTimePast(memFetcher(headers), 10); mtp != 600 {
		t.Errorf("MTP: got %d, want 600", mtp)
	}
}

func TestMiningTimestampBootstrapEnforced(t *testing.T) {
	// During bootstrap, timestamp must be ≥ parent + TargetBlockTime.
	genesis := ConsensusBlockHeader{Height: 0, Timestamp: 1_000_000}
	fetch := memFetcher([]ConsensusBlockHeader{genesis})
	ts := MiningTimestamp(genesis.Timestamp, 0, genesis.Timestamp, fetch)
	if ts < genesis.Timestamp+TargetBlockTime {
		t.Errorf("bootstrap gap not enforced: %d < %d", ts, genesis.Timestamp+TargetBlockTime)
	}
}

func TestMiningTimestampFollowsClock(t *testing.T) {
	// After bootstrap, if the clock is well ahead of MTP+1 and parent+1,
	// the timestamp should equal the clock.
	N := int(DifficultyWindow) + 1
	headers := make([]ConsensusBlockHeader, N)
	for i := range headers {
		headers[i] = ConsensusBlockHeader{Height: uint64(i), Timestamp: int64(1_000_000 + i*15)}
	}
	parent := headers[N-1]
	now := parent.Timestamp + 100
	ts := MiningTimestamp(now, parent.Height, parent.Timestamp, memFetcher(headers))
	if ts != now {
		t.Errorf("after bootstrap: got timestamp %d, want %d (clock)", ts, now)
	}
}

func TestDecodeEncodeCompactRoundTrip(t *testing.T) {
	t1 := DecodeCompact(InitialCompactTargetV2)
	t2 := DecodeCompact(EncodeCompact(t1))
	if t1.Cmp(t2) != 0 {
		t.Errorf("compact round-trip mismatch: %x vs %x", t1, t2)
	}
}
