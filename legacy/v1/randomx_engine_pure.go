//go:build !cgo || !((darwin && amd64) || (darwin && arm64) || (linux && amd64) || (windows && amd64))

// randomx_engine_pure.go — Pure-Go RandomX fallback engine.
// Used automatically when CGo is unavailable (cross-compiled Linux builds).
// Produces correct hashes at ~1 H/s — sufficient for block verification
// on relay-only seed nodes that do not mine.
package main

import (
	"bytes"
	"errors"
	"sync"

	randomx "git.gammaspectra.live/P2Pool/go-randomx"
)

// RandomXEngine wraps the pure-Go P2Pool RandomX library.
type RandomXEngine struct {
	mu      sync.RWMutex
	cache   *randomx.Randomx_Cache
	vm      *randomx.VM
	lastKey []byte
}

// NewRandomXEngine returns an uninitialised pure-Go RandomX engine.
func NewRandomXEngine() *RandomXEngine {
	return &RandomXEngine{}
}

// Init initialises the RandomX cache and VM. Uses double-checked locking so
// Argon2d runs without holding any lock, and the key is re-checked under a
// write lock before swapping — safe for concurrent callers.
func (e *RandomXEngine) Init(key []byte) error {
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

// Hash runs the pure-Go RandomX hash and returns a 32-byte digest.
func (e *RandomXEngine) Hash(input []byte) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vm == nil {
		return make([]byte, 32)
	}
	out := make([]byte, 32)
	e.vm.CalculateHash(input, out)
	return out
}

// Close releases pure-Go RandomX resources (GC handles memory).
func (e *RandomXEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vm = nil
	e.cache = nil
}
