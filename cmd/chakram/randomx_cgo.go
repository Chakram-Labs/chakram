//go:build cgo && ((darwin && amd64) || (darwin && arm64) || (linux && amd64) || (windows && amd64))

package main

/*
#cgo CFLAGS: -I${SRCDIR}/../../lib
#cgo darwin,amd64  LDFLAGS: ${SRCDIR}/../../lib/darwin_amd64/librandomx.a  -lstdc++ -lpthread
#cgo darwin,arm64  LDFLAGS: ${SRCDIR}/../../lib/darwin_arm64/librandomx.a  -lstdc++ -lpthread
#cgo linux,amd64   LDFLAGS: ${SRCDIR}/../../lib/linux_amd64/librandomx.a   -lstdc++ -lpthread
#cgo windows,amd64 LDFLAGS: ${SRCDIR}/../../lib/windows_amd64/librandomx.a -lstdc++ -lpthread

#include "randomx.h"
#include <stdlib.h>
*/
import "C"
import (
	"bytes"
	"errors"
	"log"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"chakram/consensus"
)

// randomXImplementation returns the active CGo engine with the flags that
// randomx_get_flags() detects at runtime on this CPU.
func randomXImplementation() string {
	flags := C.randomx_get_flags()
	var parts []string
	if flags&C.RANDOMX_FLAG_HARD_AES != 0 {
		parts = append(parts, "HARD_AES")
	}
	if flags&C.RANDOMX_FLAG_JIT != 0 {
		parts = append(parts, "JIT")
	}
	if flags&C.RANDOMX_FLAG_SECURE != 0 {
		parts = append(parts, "SECURE")
	}
	if flags&C.RANDOMX_FLAG_ARGON2_AVX2 != 0 {
		parts = append(parts, "AVX2")
	}
	if flags&C.RANDOMX_FLAG_ARGON2_SSSE3 != 0 {
		parts = append(parts, "SSSE3")
	}
	if len(parts) == 0 {
		parts = []string{"DEFAULT"}
	}
	return "CGo (" + strings.Join(parts, "|") + ")"
}

// randomXIsCgo reports whether the production CGo engine is compiled in.
// Returns false for the pure-Go fallback, which is ~1 H/s and unsuitable
// for mining on live networks.
func randomXIsCgo() bool { return true }

// randomXEngine wraps the C RandomX library.
// vm is the main verification VM (used by Hash, mutex-protected).
// miningVMs are additional VMs sharing the same dataset for parallel mining;
// each is owned exclusively by one goroutine and requires no mutex.
type randomXEngine struct {
	mu         sync.Mutex
	vm         *C.randomx_vm
	miningVMs  []*C.randomx_vm
	numWantVMs int // desired mining VM count set at construction
	cache      *C.randomx_cache
	dataset    *C.randomx_dataset
	lastKey    []byte
	lightMode  bool
	label      string // optional log prefix, e.g. "mining" or "verify"
}

// newRandomXEngine returns a single-threaded engine (verification / tests).
// label is an optional name shown in log messages to distinguish engine instances.
func newRandomXEngine(lightMode bool, label ...string) consensus.PoWEngine {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}
	return &randomXEngine{lightMode: lightMode, label: lbl}
}

// newMiningEngine returns a multi-threaded mining engine.
//
// Full mode (lightMode=false): numThreads VMs all share one 2 GB dataset;
// the main VM is kept separately for Hash(). Falls back to single-threaded
// when numThreads < 2 or dataset allocation fails.
//
// Light mode (lightMode=true): numThreads VMs each back a single 256 MB cache
// (read-only after Init, so sharing is safe). This is valid per the RandomX
// specification: "multiple VMs can share the same randomx_cache."
// Without this, --threads N is silently ignored in light mode.
func newMiningEngine(lightMode bool, numThreads int) consensus.PoWEngine {
	n := 0
	if numThreads >= 2 {
		n = numThreads
	}
	return &randomXEngine{lightMode: lightMode, numWantVMs: n, label: "mining"}
}

// NumMiningThreads returns the number of available parallel mining VMs.
// Implements the miner-internal multiThreadPoW interface.
func (e *randomXEngine) NumMiningThreads() int {
	return len(e.miningVMs)
}

// HashOnThread hashes input on mining VM slot threadIndex without locking.
// The caller must ensure exclusive ownership of the slot (one goroutine per index).
func (e *randomXEngine) HashOnThread(threadIndex int, input []byte) []byte {
	vm := e.miningVMs[threadIndex]
	if vm == nil {
		return make([]byte, 32)
	}
	out := make([]byte, 32)
	C.randomx_calculate_hash(vm,
		unsafe.Pointer(&input[0]), C.size_t(len(input)),
		unsafe.Pointer(&out[0]))
	return out
}

// fillDataset fills (or refills) all dataset items in parallel using cache.
// Shared by the first-init path and the in-place re-key path.
func fillDataset(dataset *C.randomx_dataset, cache *C.randomx_cache, label string) {
	nThreads := runtime.NumCPU()
	itemCount := uint64(C.randomx_dataset_item_count())
	perThread := itemCount / uint64(nThreads)
	log.Printf("[RandomX] %s (%d threads)…", label, nThreads)
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
			C.randomx_init_dataset(dataset, cache, C.ulong(s), C.ulong(c))
		}(start, count)
	}
	wg.Wait()
}

// Init initialises (or re-keys) all VMs for the given epoch key.
// No-op when the key matches the last call.
//
// On the first call the 2 GB dataset is allocated; on subsequent calls with a
// different key the dataset is refilled IN PLACE so that only one 2 GB region
// is ever live at a time. This avoids the macOS ARM64 crash that occurs when
// the C library tries to mmap a third 2 GB region in the same process.
func (e *randomXEngine) Init(key []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if bytes.Equal(e.lastKey, key) {
		return nil
	}

	flags := C.randomx_get_flags() | C.RANDOMX_FLAG_JIT
	fullFlags := flags | C.RANDOMX_FLAG_FULL_MEM

	newCache := C.randomx_alloc_cache(flags)
	if newCache == nil {
		return errors.New("randomx: failed to allocate cache")
	}
	C.randomx_init_cache(newCache, unsafe.Pointer(&key[0]), C.size_t(len(key)))

	if !e.lightMode {
		if e.dataset != nil {
			// RE-KEY PATH: refill the existing dataset in place.
			// No new mmap — avoids the macOS ARM64 crash on the 3rd allocation.
			fillDataset(e.dataset, newCache, "epoch key change — refilling dataset in place")
			// Re-set the dataset pointer in each VM so any cached VM state is invalidated.
			C.randomx_vm_set_dataset(e.vm, e.dataset)
			for _, mvm := range e.miningVMs {
				C.randomx_vm_set_dataset(mvm, e.dataset)
			}
			oldCache := e.cache
			e.cache = newCache
			e.lastKey = make([]byte, len(key))
			copy(e.lastKey, key)
			C.randomx_release_cache(oldCache)
			return nil
		}

		// FIRST INIT PATH: allocate dataset and VMs from scratch.
		newDataset := C.randomx_alloc_dataset(fullFlags)
		if newDataset != nil {
			fillDataset(newDataset, newCache, "initialising dataset")

			newVM := C.randomx_create_vm(fullFlags, nil, newDataset)
			if newVM != nil {
				var newMiningVMs []*C.randomx_vm
				if e.numWantVMs >= 2 {
					for i := 0; i < e.numWantVMs; i++ {
						mvm := C.randomx_create_vm(fullFlags, nil, newDataset)
						if mvm == nil {
							break
						}
						newMiningVMs = append(newMiningVMs, mvm)
					}
					log.Printf("[RandomX][%s] full mode: %d mining thread(s)", e.label, len(newMiningVMs))
				} else {
					log.Printf("[RandomX][%s] full mode active", e.label)
				}

				e.vm = newVM
				e.miningVMs = newMiningVMs
				e.dataset = newDataset
				e.cache = newCache
				e.lastKey = make([]byte, len(key))
				copy(e.lastKey, key)
				return nil
			}
			C.randomx_release_dataset(newDataset)
		}
		// fall through to light mode
	}

	// LIGHT MODE PATH: cache-backed VMs, no dataset.
	// Multiple VMs may share the same randomx_cache; after randomx_init_cache
	// the cache is read-only, so concurrent VM usage across goroutines is safe.
	lightVM := C.randomx_create_vm(flags, newCache, nil)
	if lightVM == nil {
		C.randomx_release_cache(newCache)
		return errors.New("randomx: failed to create VM")
	}

	// One extra VM per desired mining thread (each goroutine owns its slot).
	var lightMiningVMs []*C.randomx_vm
	if e.numWantVMs >= 2 {
		for i := 0; i < e.numWantVMs; i++ {
			mvm := C.randomx_create_vm(flags, newCache, nil)
			if mvm == nil {
				break
			}
			lightMiningVMs = append(lightMiningVMs, mvm)
		}
	}

	// Tear down old resources (light-mode re-key or fallback from failed full-mode).
	if e.vm != nil {
		C.randomx_destroy_vm(e.vm)
	}
	for _, mvm := range e.miningVMs {
		C.randomx_destroy_vm(mvm)
	}
	if e.dataset != nil {
		C.randomx_release_dataset(e.dataset)
		e.dataset = nil
	}
	if e.cache != nil {
		C.randomx_release_cache(e.cache)
	}

	e.vm = lightVM
	e.miningVMs = lightMiningVMs
	e.cache = newCache
	e.lastKey = make([]byte, len(key))
	copy(e.lastKey, key)

	if e.lightMode {
		if len(lightMiningVMs) >= 2 {
			log.Printf("[RandomX][%s] light mode: %d mining thread(s)", e.label, len(lightMiningVMs))
		} else {
			log.Printf("[RandomX][%s] light mode", e.label)
		}
	} else {
		log.Printf("[RandomX][%s] light mode (full mode unavailable)", e.label)
	}
	return nil
}

// Hash returns the 32-byte RandomX digest of input using the main VM (mutex-protected).
func (e *randomXEngine) Hash(input []byte) []byte {
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
func (e *randomXEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vm != nil {
		C.randomx_destroy_vm(e.vm)
		e.vm = nil
	}
	for _, mvm := range e.miningVMs {
		C.randomx_destroy_vm(mvm)
	}
	e.miningVMs = nil
	if e.dataset != nil {
		C.randomx_release_dataset(e.dataset)
		e.dataset = nil
	}
	if e.cache != nil {
		C.randomx_release_cache(e.cache)
		e.cache = nil
	}
}
