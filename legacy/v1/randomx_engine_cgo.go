//go:build cgo && ((darwin && amd64) || (darwin && arm64) || (linux && amd64) || (windows && amd64))

// randomx_engine_cgo.go — CGo RandomX engine using the C reference implementation.
// Runs in full mode (2 GB dataset) for ~10x the hashrate of light mode.
// Falls back to light mode automatically if the system has insufficient RAM.
// Cross-compiled Linux builds fall back to randomx_engine_pure.go.
package main

/*
#cgo CFLAGS: -I${SRCDIR}/lib
#cgo darwin,amd64  LDFLAGS: ${SRCDIR}/lib/darwin_amd64/librandomx.a  -lstdc++ -lpthread
#cgo darwin,arm64  LDFLAGS: ${SRCDIR}/lib/darwin_arm64/librandomx.a  -lstdc++ -lpthread
#cgo linux,amd64   LDFLAGS: ${SRCDIR}/lib/linux_amd64/librandomx.a   -lstdc++ -lpthread
#cgo windows,amd64 LDFLAGS: ${SRCDIR}/lib/windows_amd64/librandomx.a -lstdc++ -lpthread

#include "randomx.h"
#include <stdlib.h>
*/
import "C"
import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// RandomXEngine wraps the C RandomX VM. One instance is shared between the
// miner and the block verifier (via Blockchain.SetVerifyEngine) so the 2 GB
// dataset is only computed once per epoch boundary instead of twice.
type RandomXEngine struct {
	mu      sync.Mutex
	vm      *C.randomx_vm
	cache   *C.randomx_cache
	dataset *C.randomx_dataset
	lastKey []byte
}

// NewRandomXEngine returns an uninitialised CGo RandomX engine.
func NewRandomXEngine() *RandomXEngine {
	return &RandomXEngine{}
}

// Init initialises (or re-keys) the RandomX engine for the given epoch key.
// Attempts full mode (2 GB dataset, ~10x faster). Falls back to light mode
// (256 MB cache) if the system cannot allocate the dataset.
// No-op when the key matches the last call — epoch boundaries are infrequent.
func (e *RandomXEngine) Init(key []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if bytes.Equal(e.lastKey, key) {
		return nil
	}

	baseFlags := C.randomx_get_flags() | C.RANDOMX_FLAG_JIT
	fullFlags := baseFlags | C.RANDOMX_FLAG_FULL_MEM

	// Cache is always required — used to initialise the dataset.
	newCache := C.randomx_alloc_cache(baseFlags)
	if newCache == nil {
		return errors.New("randomx: failed to allocate cache")
	}
	C.randomx_init_cache(newCache, unsafe.Pointer(&key[0]), C.size_t(len(key)))

	// ── Full mode: allocate 2 GB dataset and initialise with all CPU threads ──
	var newDataset *C.randomx_dataset
	var newVM *C.randomx_vm

	newDataset = C.randomx_alloc_dataset(fullFlags)
	if newDataset != nil {
		nThreads := runtime.NumCPU()
		itemCount := uint64(C.randomx_dataset_item_count())
		perThread := itemCount / uint64(nThreads)

		fmt.Printf("[MINER] Initialising RandomX dataset (%d threads, ~2 min)…\n", nThreads)

		var wg sync.WaitGroup
		for i := 0; i < nThreads; i++ {
			start := uint64(i) * perThread
			count := perThread
			if i == nThreads-1 {
				count = itemCount - start
			}
			wg.Add(1)
			go func(s, c uint64) {
				defer wg.Done()
				C.randomx_init_dataset(newDataset, newCache, C.ulong(s), C.ulong(c))
			}(start, count)
		}
		wg.Wait()

		newVM = C.randomx_create_vm(fullFlags, nil, newDataset)
		if newVM == nil {
			// Dataset allocated but VM creation failed — release and fall through.
			C.randomx_release_dataset(newDataset)
			newDataset = nil
		} else {
			fmt.Printf("[MINER] RandomX full mode active\n")
		}
	}

	// ── Light mode fallback (256 MB cache, ~10x slower than full mode) ────────
	if newVM == nil {
		fmt.Printf("[MINER] RandomX light mode (full mode unavailable — low RAM?)\n")
		newVM = C.randomx_create_vm(baseFlags, newCache, nil)
		if newVM == nil {
			C.randomx_release_cache(newCache)
			return errors.New("randomx: failed to create VM")
		}
	}

	// Release previous resources before swapping in the new ones.
	if e.vm != nil {
		C.randomx_destroy_vm(e.vm)
	}
	if e.dataset != nil {
		C.randomx_release_dataset(e.dataset)
	}
	if e.cache != nil {
		C.randomx_release_cache(e.cache)
	}

	e.vm = newVM
	e.dataset = newDataset
	e.cache = newCache
	e.lastKey = make([]byte, len(key))
	copy(e.lastKey, key)
	return nil
}

// Hash runs the RandomX hash and returns a 32-byte digest.
func (e *RandomXEngine) Hash(input []byte) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vm == nil {
		return make([]byte, 32)
	}
	out := make([]byte, 32)
	C.randomx_calculate_hash(e.vm,
		unsafe.Pointer(&input[0]), C.size_t(len(input)),
		unsafe.Pointer(&out[0]))
	return out
}

// Close frees all C RandomX resources.
func (e *RandomXEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vm != nil {
		C.randomx_destroy_vm(e.vm)
		e.vm = nil
	}
	if e.dataset != nil {
		C.randomx_release_dataset(e.dataset)
		e.dataset = nil
	}
	if e.cache != nil {
		C.randomx_release_cache(e.cache)
		e.cache = nil
	}
}
