//go:build hashrate

// Run manually to measure full-mode RandomX throughput:
//
//	go test -tags hashrate ./cmd/chakram/ -run=TestFullModeHashrate -v -timeout=300s

package main

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// TestFullModeHashrate measures full-mode RandomX throughput the same way the
// miner does: single-thread path with runtime.Gosched() after each hash.
//
// Run: go test ./cmd/chakram/ -run=TestFullModeHashrate -v -timeout=300s
func TestFullModeHashrate(t *testing.T) {
	const warmupSec = 5
	const measureSec = 60

	eng := newRandomXEngine(false) // full mode — same as production miner
	defer eng.Close()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Log("initialising RandomX dataset (full mode)…")
	if err := eng.Init(key); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Log("dataset ready — warming up…")

	input := make([]byte, 76) // standard header serialisation length

	warmupEnd := time.Now().Add(warmupSec * time.Second)
	for time.Now().Before(warmupEnd) {
		eng.Hash(input)
		runtime.Gosched()
		input[0]++
	}

	t.Logf("measuring for %d s…", measureSec)
	var hashes uint64
	start := time.Now()
	end := start.Add(measureSec * time.Second)
	for time.Now().Before(end) {
		eng.Hash(input)
		runtime.Gosched()
		input[0]++
		hashes++
	}
	elapsed := time.Since(start)

	hps := float64(hashes) / elapsed.Seconds()
	fmt.Printf("\n--- RandomX full-mode hashrate (single thread, %d-core M1) ---\n", runtime.NumCPU())
	fmt.Printf("  hashes in %.1f s : %d\n", elapsed.Seconds(), hashes)
	fmt.Printf("  single-thread   : %.1f H/s\n", hps)
	fmt.Printf("------------------------------------------------------------\n\n")
	t.Logf("single-thread hashrate: %.1f H/s (%d hashes / %.1fs)", hps, hashes, elapsed.Seconds())
}

// TestMultiThreadHashrate measures the aggregate hashrate with NumCPU-1 threads,
// matching production miner configuration.
func TestMultiThreadHashrate(t *testing.T) {
	const warmupSec = 5
	const measureSec = 60

	n := runtime.NumCPU() - 1
	if n < 1 {
		n = 1
	}
	t.Logf("using %d mining threads", n)

	eng := newMiningEngine(false, n)
	defer eng.Close()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Log("initialising RandomX dataset (full mode)…")
	if err := eng.Init(key); err != nil {
		t.Fatalf("Init: %v", err)
	}

	mt, ok := eng.(multiThreadPoW)
	if !ok || mt.NumMiningThreads() < 2 {
		t.Skip("multi-thread path not available (CGo not built or single-core machine)")
	}
	t.Logf("mining VMs ready: %d", mt.NumMiningThreads())

	t.Log("warming up…")
	warmupEnd := time.Now().Add(warmupSec * time.Second)
	for i := 0; time.Now().Before(warmupEnd); i++ {
		input := make([]byte, 76)
		input[0] = byte(i)
		mt.HashOnThread(i%mt.NumMiningThreads(), input)
	}

	t.Logf("measuring %d threads for %d s…", mt.NumMiningThreads(), measureSec)
	nThreads := mt.NumMiningThreads()
	done := make(chan uint64, nThreads)
	start := time.Now()
	end := start.Add(measureSec * time.Second)

	for i := 0; i < nThreads; i++ {
		go func(tidx int) {
			var count uint64
			input := make([]byte, 76)
			input[0] = byte(tidx)
			for time.Now().Before(end) {
				mt.HashOnThread(tidx, input)
				input[0]++
				count++
			}
			done <- count
		}(i)
	}

	var total uint64
	for i := 0; i < nThreads; i++ {
		total += <-done
	}
	elapsed := time.Since(start)

	hps := float64(total) / elapsed.Seconds()
	fmt.Printf("\n--- RandomX full-mode hashrate (%d threads, %d-core M1) ---\n", nThreads, runtime.NumCPU())
	fmt.Printf("  hashes in %.1f s : %d\n", elapsed.Seconds(), total)
	fmt.Printf("  total hashrate  : %.1f H/s\n", hps)
	fmt.Printf("  per-thread avg  : %.1f H/s\n", hps/float64(nThreads))
	fmt.Printf("----------------------------------------------------------\n\n")
	t.Logf("multi-thread hashrate: %.1f H/s (%d threads, %d hashes / %.1fs)", hps, nThreads, total, elapsed.Seconds())
}
