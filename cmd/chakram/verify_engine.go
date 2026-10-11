package main

import (
	"bytes"

	"chakram/consensus"
)

// compile-time check: epochVerifier must satisfy consensus.PoWEngine.
var _ consensus.PoWEngine = (*epochVerifier)(nil)

// verifierSlot holds one epoch's fully-initialised PoW engine.
type verifierSlot struct {
	key []byte
	eng consensus.PoWEngine
}

// epochVerifier caches the last 2 epoch PoW engines so that verifying blocks
// near an epoch boundary — or re-verifying epoch N-1 blocks during a reorg
// while the miner is on epoch N — requires no re-initialisation.
//
// LRU policy with 2 slots: on a cache miss the slot that was NOT used most
// recently is evicted and re-initialised with the new key.
//
// Callers must serialise access externally (the verifyMu in main.go does this).
// epochVerifier is not goroutine-safe.
type epochVerifier struct {
	factory func() consensus.PoWEngine // creates a fresh single-epoch engine
	slots   [2]verifierSlot
	current int // index (0 or 1) of the most recently used slot
}

// newEpochVerifier returns a verifier backed by engines produced by factory.
// factory is called at most twice over the lifetime (once per slot).
func newEpochVerifier(factory func() consensus.PoWEngine) *epochVerifier {
	return &epochVerifier{factory: factory}
}

// Init selects the engine slot for key, initialising it if necessary.
// If key is already loaded in one of the two slots the switch is O(1) —
// no RandomX re-initialisation occurs.
func (v *epochVerifier) Init(key []byte) error {
	// Fast path: key is already cached.
	for i := range v.slots {
		if bytes.Equal(v.slots[i].key, key) {
			v.current = i
			return nil
		}
	}

	// Slow path: evict the non-current (older) slot.
	evict := 1 - v.current
	if v.slots[evict].eng == nil {
		v.slots[evict].eng = v.factory()
	}
	if err := v.slots[evict].eng.Init(key); err != nil {
		return err
	}
	v.slots[evict].key = append(v.slots[evict].key[:0], key...)
	v.current = evict
	return nil
}

// Hash returns the 32-byte RandomX digest using the active epoch's engine.
// Must be called after a successful Init.
func (v *epochVerifier) Hash(input []byte) []byte {
	if v.slots[v.current].eng == nil {
		return make([]byte, 32)
	}
	return v.slots[v.current].eng.Hash(input)
}

// Close frees all engine resources.
func (v *epochVerifier) Close() {
	for i := range v.slots {
		if v.slots[i].eng != nil {
			v.slots[i].eng.Close()
			v.slots[i].eng = nil
		}
		v.slots[i].key = nil
	}
}
