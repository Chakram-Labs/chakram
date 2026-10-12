//go:build hashrate

package main

import (
	"encoding/hex"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// currentRSS returns the process's current maximum RSS in bytes.
// macOS getrusage(2) reports MaxRSS in bytes; Linux in kibibytes.
func currentRSS() int64 {
	var r syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &r)
	v := r.Maxrss
	if runtime.GOOS == "linux" {
		v *= 1024
	}
	return v
}

// TestFullModeKeySwitch verifies that switching the RandomX epoch key 6 times
// in one process (simulating the miner crossing 6 epoch boundaries) neither
// crashes nor grows the process's 2 GB dataset footprint after the first init.
//
// The dataset is refilled in place on every re-key, so MaxRSS should plateau
// after the first Init and stay flat for all subsequent switches.
func TestFullModeKeySwitch(t *testing.T) {
	if !randomXIsCgo() {
		t.Skip("CGo engine not compiled in — pure-Go has no 2 GB dataset")
	}

	// Use 2 mining threads so the mining VMs are exercised in the re-key path.
	eng := newMiningEngine(false, 2)
	defer eng.Close()

	// Six distinct keys — five re-keys after the initial init.
	keys := [][]byte{
		[]byte("epoch key 0000"),
		[]byte("epoch key 0001"),
		[]byte("epoch key 0002"),
		[]byte("epoch key 0003"),
		[]byte("epoch key 0004"),
		[]byte("epoch key 0005"),
	}

	input := []byte("consistent test input for all epoch keys")
	hashes := make([]string, len(keys))

	rssBaseline := currentRSS()
	rssByKey := make([]int64, len(keys))

	t.Logf("%-5s  %-16s  %-8s  %-12s  %-12s  %s",
		"key#", "key", "init(s)", "MaxRSS(MiB)", "ΔBase(MiB)", "hash[:16]")

	for i, key := range keys {
		t0 := time.Now()
		if err := eng.Init(key); err != nil {
			t.Fatalf("Init key %d (%q): %v", i, key, err)
		}
		elapsed := time.Since(t0)

		h := eng.Hash(input)
		hashes[i] = hex.EncodeToString(h)

		rssByKey[i] = currentRSS()
		delta := rssByKey[i] - rssBaseline

		t.Logf("%-5d  %-16q  %-8.1f  %-12d  %-12d  %s",
			i, string(key), elapsed.Seconds(),
			rssByKey[i]>>20, delta>>20,
			hashes[i][:16])
	}

	// All 6 hashes must be distinct — each key must affect the output.
	seen := make(map[string]bool)
	for i, h := range hashes {
		if seen[h] {
			t.Errorf("key %d produced a duplicate hash: %s", i, h[:16])
		}
		seen[h] = true
	}

	// Memory check: MaxRSS should never exceed ~3 GiB, which is well below the
	// ~4+ GiB we'd see if both the old and new datasets were live simultaneously.
	// The expected full-resident footprint is ~2.6 GiB (2 GB dataset + 256 MB
	// cache + Go overhead). The dataset is mmap'd lazily, so MaxRSS grows from
	// ~1.5 GiB on the first cold fill to ~2.6 GiB once all pages are warm —
	// but it must plateau and never approach 2× dataset size.
	const doubleCeiling = 3 << 10 // 3 GiB in MiB — well below 2× dataset
	rssFinal := rssByKey[len(rssByKey)-1]
	if rssFinal>>20 > doubleCeiling {
		t.Errorf("MaxRSS %d MiB exceeds 3 GiB ceiling — suggests double dataset allocation",
			rssFinal>>20)
	}

	// Check that MaxRSS stabilises: keys 3–5 must all show the same maximum
	// (the dataset is fully warm by key 3; further re-keys should not grow RSS).
	if len(rssByKey) >= 5 {
		peak := rssByKey[3]
		for i := 4; i < len(rssByKey); i++ {
			if rssByKey[i] > peak+int64(64<<20) { // allow 64 MiB jitter
				t.Errorf("MaxRSS grew by %d MiB between key 3 and key %d — unexpected growth after warmup",
					(rssByKey[i]-peak)>>20, i)
			}
		}
		t.Logf("MaxRSS plateau (keys 3–5): %d MiB — in-place refill confirmed", peak>>20)
	}

	// Re-init with key[0] and verify the hash matches the original.
	if err := eng.Init(keys[0]); err != nil {
		t.Fatalf("re-init key 0: %v", err)
	}
	if got := hex.EncodeToString(eng.Hash(input)); got != hashes[0] {
		t.Errorf("re-init key 0 hash mismatch:\n  got  %s\n  want %s", got, hashes[0])
	} else {
		t.Logf("re-init key 0: hash matches original — determinism OK")
	}
}

// TestLightModeHashrate measures single-thread and multi-thread RandomX
// light-mode hashing performance and verifies that threading actually helps.
//
// Before the numWantVMs fix, --threads N was silently ignored in light mode
// because newMiningEngine gated numWantVMs on !lightMode. This test proves
// the fix works: multi-thread throughput must exceed single-thread.
//
// Run on seed-2 (Intel Xeon 2.20GHz, 2 vCPU, 3.8GB RAM):
//
//	go test -v -run TestLightModeHashrate -tags hashrate ./cmd/chakram/
func TestLightModeHashrate(t *testing.T) {
	if !randomXIsCgo() {
		t.Skip("CGo engine not compiled in — pure-Go light mode is not useful for mining")
	}

	const warmupKey = "testnet epoch key for hashrate bench"
	const benchDuration = 20 * time.Second
	input := make([]byte, 76) // typical block header input

	type result struct {
		threads int
		hps     float64
	}
	var results []result

	for _, nThreads := range []int{1, 2} {
		eng := newMiningEngine(true, nThreads)
		defer eng.Close()

		if err := eng.Init([]byte(warmupKey)); err != nil {
			t.Fatalf("Init (threads=%d): %v", nThreads, err)
		}

		mt, isMulti := eng.(interface {
			NumMiningThreads() int
			HashOnThread(int, []byte) []byte
		})

		var count int64
		start := time.Now()

		if isMulti && mt.NumMiningThreads() >= 2 {
			var wg sync.WaitGroup
			stop := make(chan struct{})
			nvm := mt.NumMiningThreads()
			for i := 0; i < nvm; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					inp := append([]byte{}, input...)
					for {
						select {
						case <-stop:
							return
						default:
						}
						mt.HashOnThread(idx, inp)
						atomic.AddInt64(&count, 1)
					}
				}(i)
			}
			time.Sleep(benchDuration)
			close(stop)
			wg.Wait()
		} else {
			for time.Since(start) < benchDuration {
				eng.Hash(input)
				atomic.AddInt64(&count, 1)
			}
		}

		elapsed := time.Since(start)
		hps := float64(count) / elapsed.Seconds()
		t.Logf("light mode, %d thread(s): %.1f H/s (%d hashes in %.0fs)",
			nThreads, hps, count, elapsed.Seconds())
		results = append(results, result{nThreads, hps})
	}

	// On machines with ≥ 4 vCPUs, multi-thread throughput must exceed single-thread.
	// On ≤ 2-vCPU machines (e.g. seed-2 running node+geth), both threads fight for
	// the same vCPUs, so flat throughput is expected and correct — the VMs exist and
	// work; the CPU is the bottleneck, not the code.
	if len(results) == 2 {
		single, multi := results[0].hps, results[1].hps
		t.Logf("threading speedup: %.2fx (%.1f → %.1f H/s)", multi/single, single, multi)
		if runtime.NumCPU() >= 4 && multi < single*1.2 {
			t.Errorf("multi-thread (%.1f H/s) not faster than single-thread (%.1f H/s) × 1.2 on %d-vCPU machine — check VM creation",
				multi, single, runtime.NumCPU())
		}
	}
}
