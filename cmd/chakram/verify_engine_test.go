package main

import (
	"bytes"
	"encoding/hex"
	"testing"

	"chakram/consensus"
)

// countingPow wraps a consensus.PoWEngine and increments *count on every Init.
type countingPow struct {
	consensus.PoWEngine
	count *int
}

func (c *countingPow) Init(key []byte) error {
	*c.count++
	return c.PoWEngine.Init(key)
}

// TestEpochVerifierCacheHit verifies that switching between two epoch keys does
// not re-initialize the underlying engine when both keys are already in the
// 2-slot cache.
func TestEpochVerifierCacheHit(t *testing.T) {
	initCount := 0

	v := newEpochVerifier(func() consensus.PoWEngine {
		return &countingPow{PoWEngine: newRandomXEngine(true), count: &initCount}
	})
	defer v.Close()

	input := []byte("block header bytes for verify test")
	keyN := []byte("epoch N key for verify cache test")
	keyNm1 := []byte("epoch N-1 key for verify cache test")

	// First call: slot 1 is allocated and initialised with keyN.
	if err := v.Init(keyN); err != nil {
		t.Fatalf("Init(keyN): %v", err)
	}
	hashN := v.Hash(input)

	// Second call: different key, slot 0 is allocated and initialised with keyNm1.
	if err := v.Init(keyNm1); err != nil {
		t.Fatalf("Init(keyNm1): %v", err)
	}
	hashNm1 := v.Hash(input)

	// Both slots now filled — exactly 2 underlying Init calls.
	if initCount != 2 {
		t.Errorf("expected 2 underlying Init calls after filling both slots, got %d", initCount)
	}

	// Switch back to keyN — must be a CACHE HIT (no new Init).
	before := initCount
	if err := v.Init(keyN); err != nil {
		t.Fatalf("re-Init(keyN): %v", err)
	}
	if initCount != before {
		t.Errorf("re-Init(keyN) triggered an underlying Init — cache miss when hit expected")
	}
	if !bytes.Equal(v.Hash(input), hashN) {
		t.Error("epoch N hash changed after cache re-use — wrong slot activated")
	}

	// Switch back to keyNm1 — must also be a CACHE HIT.
	before = initCount
	if err := v.Init(keyNm1); err != nil {
		t.Fatalf("re-Init(keyNm1): %v", err)
	}
	if initCount != before {
		t.Errorf("re-Init(keyNm1) triggered an underlying Init — cache miss when hit expected")
	}
	if !bytes.Equal(v.Hash(input), hashNm1) {
		t.Error("epoch N-1 hash changed after cache re-use")
	}

	// Sanity: different keys must produce different hashes.
	if bytes.Equal(hashN, hashNm1) {
		t.Error("epoch N and N-1 produced identical hashes — key had no effect")
	}

	t.Logf("epoch N   hash: %s…", hex.EncodeToString(hashN)[:16])
	t.Logf("epoch N-1 hash: %s…", hex.EncodeToString(hashNm1)[:16])
	t.Logf("underlying Init calls: %d (expect 2; cache absorbed all further switches)", initCount)
}

// TestVerifyEpochNm1WhileMiningEpochN simulates the real scenario: miner
// advances to epoch N, then a reorg requires re-verifying a block from epoch
// N-1. The verifier must use its cached slot and produce a consistent hash.
func TestVerifyEpochNm1WhileMiningEpochN(t *testing.T) {
	// Mining engine (separate from verifier — models production layout).
	miningEng := newRandomXEngine(true)
	defer miningEng.Close()

	// Verifier with 2-slot cache.
	verifier := newEpochVerifier(func() consensus.PoWEngine {
		return newRandomXEngine(true)
	})
	defer verifier.Close()

	keyNm1 := []byte("epoch N-1 reorg test key 111")
	keyN := []byte("epoch N   current test key 222")
	block := []byte("simulated 120-byte consensus block header serialisation")

	// Step 1: verifier processes blocks at epoch N-1.
	if err := verifier.Init(keyNm1); err != nil {
		t.Fatalf("verifier Init(keyNm1): %v", err)
	}
	hashNm1 := verifier.Hash(block)

	// Step 2: miner advances to epoch N.
	if err := miningEng.Init(keyN); err != nil {
		t.Fatalf("miner Init(keyN): %v", err)
	}
	miningHashN := miningEng.Hash(block)

	// Step 3: verifier also sees epoch N.
	if err := verifier.Init(keyN); err != nil {
		t.Fatalf("verifier Init(keyN): %v", err)
	}
	verifyHashN := verifier.Hash(block)

	// Step 4: reorg — verifier must re-check an epoch N-1 block.
	// Both slots are populated (N-1 and N), so this must be a cache hit.
	if err := verifier.Init(keyNm1); err != nil {
		t.Fatalf("verifier re-Init(keyNm1) after reorg: %v", err)
	}
	reorgHashNm1 := verifier.Hash(block)

	// The re-verified hash must equal the original.
	if !bytes.Equal(hashNm1, reorgHashNm1) {
		t.Errorf("epoch N-1 hash inconsistent after reorg:\n  first  %s\n  reorg  %s",
			hex.EncodeToString(hashNm1), hex.EncodeToString(reorgHashNm1))
	}

	// Miner and verifier must agree on epoch N.
	if !bytes.Equal(miningHashN, verifyHashN) {
		t.Errorf("miner and verifier disagree on epoch N hash:\n  miner    %s\n  verifier %s",
			hex.EncodeToString(miningHashN), hex.EncodeToString(verifyHashN))
	}

	t.Logf("epoch N-1 hash: %s (consistent across reorg)", hex.EncodeToString(hashNm1)[:16])
	t.Logf("epoch N   hash: %s (miner == verifier)", hex.EncodeToString(verifyHashN)[:16])
}
