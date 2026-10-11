//go:build cgo && ((darwin && amd64) || (darwin && arm64) || (linux && amd64) || (windows && amd64))

// randomx_pure_fortest.go is compiled on CGo-enabled platforms alongside
// randomx_cgo.go to provide a pure-Go engine instance for cross-checking in
// randomx_vectors_test.go.  It is not compiled on platforms that fall back to
// randomx_pure.go (where only the pure-Go engine exists).
package main

import (
	"bytes"
	"errors"
	"sync"

	randomx "git.gammaspectra.live/P2Pool/go-randomx"

	"chakram/consensus"
)

type pureGoRandomXEngine struct {
	mu      sync.RWMutex
	cache   *randomx.Randomx_Cache
	vm      *randomx.VM
	lastKey []byte
}

// newPureGoEngine returns a pure-Go RandomX engine for cross-checking.
// Only available when CGo is compiled in (complementary to randomx_pure.go).
func newPureGoEngine() consensus.PoWEngine {
	return &pureGoRandomXEngine{}
}

func (e *pureGoRandomXEngine) Init(key []byte) error {
	e.mu.RLock()
	same := bytes.Equal(e.lastKey, key)
	e.mu.RUnlock()
	if same {
		return nil
	}

	newCache := randomx.Randomx_alloc_cache(randomx.RANDOMX_FLAG_DEFAULT)
	if newCache == nil {
		return errors.New("pureGo randomx: failed to allocate cache")
	}
	newCache.Randomx_init_cache(key)

	gen := randomx.Init_Blake2Generator(key, 0)
	for i := 0; i < randomx.RANDOMX_PROGRAM_COUNT; i++ {
		newCache.Programs[i] = randomx.Build_SuperScalar_Program(gen)
	}
	newVM := newCache.VM_Initialize()
	if newVM == nil {
		return errors.New("pureGo randomx: failed to initialise VM")
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

func (e *pureGoRandomXEngine) Hash(input []byte) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vm == nil {
		return make([]byte, 32)
	}
	out := make([]byte, 32)
	e.vm.CalculateHash(input, out)
	return out
}

func (e *pureGoRandomXEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vm = nil
	e.cache = nil
}
