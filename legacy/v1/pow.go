// pow.go — Proof-of-work mining logic for Chakram.
// RandomXEngine is defined in randomx_engine_cgo.go (CGo build) or
// randomx_engine_pure.go (pure-Go fallback). This file contains only the
// algorithm-agnostic code: the PoWEngine interface, header serialisation,
// block verification, and the mining loop.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"time"
)

// PoWEngine is the proof-of-work abstraction used by the miner and verifier.
// Init must be called once per epoch key before calling Hash.
// Close releases any C or Go resources held by the implementation.
type PoWEngine interface {
	Init(key []byte) error    // initialises or re-keys the engine
	Hash(input []byte) []byte // returns 32-byte RandomX digest
	Close()                   // releases resources
}

// serializeHeader encodes all BlockHeader fields to bytes in little-endian order.
// The byte sequence is what gets fed into the PoW hash function.
// Field order must match ComputeHash in block.go exactly.
func serializeHeader(h BlockHeader) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, h.Version)    //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, h.Height)     //nolint:errcheck
	buf.Write(h.PreviousHash)
	buf.Write(h.MerkleRoot)
	binary.Write(buf, binary.LittleEndian, h.Timestamp)  //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, h.Difficulty) //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, h.Nonce)      //nolint:errcheck
	return buf.Bytes()
}

// VerifyBlock confirms that b.Hash is the authentic RandomX hash of b's header
// and that it satisfies the difficulty target.
func VerifyBlock(b *Block, engine PoWEngine, key []byte) error {
	if err := engine.Init(key); err != nil {
		return fmt.Errorf("engine init failed: %w", err)
	}
	data := serializeHeader(b.Header)
	expected := engine.Hash(data)
	if !bytes.Equal(expected, b.Hash) || !b.HashIsValid() {
		return ErrInvalidPoW
	}
	return nil
}

// MineBlock searches for a Nonce that makes b's hash satisfy the difficulty
// target. Prints hashrate every 30 seconds. Returns nil on success.
func MineBlock(b *Block, engine PoWEngine, key []byte, quit <-chan struct{}) error {
	if err := engine.Init(key); err != nil {
		return fmt.Errorf("mine: init engine: %w", err)
	}

	startNonce := b.Header.Nonce
	var hashCount uint64
	lastReport := time.Now()

	for {
		select {
		case <-quit:
			return errors.New("mining cancelled")
		default:
		}

		data := serializeHeader(b.Header)
		b.Hash = engine.Hash(data)
		hashCount++
		runtime.Gosched()

		if time.Since(lastReport) >= 30*time.Second {
			hps := float64(hashCount) / time.Since(lastReport).Seconds()
			fmt.Printf("[MINER] diff=%d hashrate=%.0f H/s\n", b.Header.Difficulty, hps)
			hashCount = 0
			lastReport = time.Now()
		}

		if b.HashIsValid() {
			return nil
		}

		b.Header.Nonce++
		if b.Header.Nonce == startNonce {
			return errors.New("mine: nonce exhausted")
		}
	}
}
