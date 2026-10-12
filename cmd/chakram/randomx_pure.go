//go:build !cgo || !((darwin && amd64) || (darwin && arm64) || (linux && amd64) || (windows && amd64))

package main

import (
	"bytes"
	"errors"
	"sync"

	randomx "git.gammaspectra.live/P2Pool/go-randomx"

	"chakram/consensus"
)

// randomXImplementation returns a human-readable name for the active engine.
func randomXImplementation() string { return "pure-Go" }

// randomXIsCgo reports whether the production CGo engine is compiled in.
func randomXIsCgo() bool { return false }

// randomXEngine wraps the pure-Go P2Pool RandomX library.
// Implements consensus.PoWEngine. ~1 H/s — sufficient for devnet with low
// difficulty but not suitable for production mining.
type randomXEngine struct {
	mu      sync.RWMutex
	cache   *randomx.Randomx_Cache
	vm      *randomx.VM
	lastKey []byte
	label   string // unused in pure-Go (no logs), present for API parity with CGo
}

// newRandomXEngine returns a single-threaded pure-Go engine.
// label is accepted for API parity with the CGo build but is not used.
func newRandomXEngine(_ bool, label ...string) consensus.PoWEngine {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}
	return &randomXEngine{label: lbl}
}

// newMiningEngine is a single-threaded fallback on platforms without CGo.
func newMiningEngine(_ bool, _ int) consensus.PoWEngine {
	return &randomXEngine{label: "mining"}
}

func (e *randomXEngine) Init(key []byte) error {
	e.mu.RLock()
	sameKey := bytes.Equal(e.lastKey, key)
	e.mu.RUnlock()
	if sameKey {
		return nil
	}

	newCache := randomx.Randomx_alloc_cache(randomx.RANDOMX_FLAG_DEFAULT)
	if newCache == nil {
		return errors.New("randomx: failed to allocate cache")
	}
	newCache.Randomx_init_cache(key)

	gen := randomx.Init_Blake2Generator(key, 0)
	for i := 0; i < randomx.RANDOMX_PROGRAM_COUNT; i++ {
		newCache.Programs[i] = randomx.Build_SuperScalar_Program(gen)
	}

	newVM := newCache.VM_Initialize()
	if newVM == nil {
		return errors.New("randomx: failed to initialise VM")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if bytes.Equal(e.lastKey, key) {
		return nil
	}
	e.cache = newCache
	e.vm = newVM
	e.lastKey = make([]byte, len(key))
	copy(e.lastKey, key)
	return nil
}

func (e *randomXEngine) Hash(input []byte) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vm == nil {
		return make([]byte, 32)
	}
	out := make([]byte, 32)
	e.vm.CalculateHash(input, out)
	return out
}

func (e *randomXEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vm = nil
	e.cache = nil
}
