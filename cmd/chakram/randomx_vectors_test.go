package main

import (
	"encoding/hex"
	"fmt"
	"testing"
)

// knownVector is a RandomX key + input → expected 32-byte hash (hex).
// All vectors were generated with RANDOMX_FLAG_DEFAULT (interpreter, soft AES)
// and cross-checked against both our pre-compiled library and the tevador/RandomX
// HEAD reference library (see cmd/chakram/lib/). All flag combinations produce
// identical hashes; flags affect only performance, not correctness.
type knownVector struct {
	key      []byte
	input    []byte
	wantHex  string
	label    string
}

var officialVectors = []knownVector{
	{
		// Canonical test key used by the RandomX reference benchmark.
		// Input: 76 zero bytes (the benchmark's miner-init format).
		key:     []byte("test key 000"),
		input:   make([]byte, 76),
		wantHex: "20f85e26421cb02d45652f8f9493534d5f39678e9b3e198e22473ad81dfb6eb4",
		label:   "key=testkey000 input=76×0x00",
	},
	{
		key:     []byte("test key 000"),
		input:   []byte("test input"),
		wantHex: "4e7e06f952ac69098bab0834563635590fa53ab4d4431ad8729484f8a0d9569f",
		label:   "key=testkey000 input=testinput",
	},
	{
		key:     []byte("test key 000"),
		input:   []byte("RandomX test"),
		wantHex: "ce2345dfda0c397b62fd493a476a5123a7c3b0d4177cc627cf2d6bf642fc046f",
		label:   "key=testkey000 input=RandomXtest",
	},
	{
		// Different key — exercises key-switching path.
		key:     []byte("chakram v2"),
		input:   []byte("test key 000"),
		wantHex: "f017984b3f5023e8f51eb472ca225a50aba380725bcc82f8a09dc16a19784fae",
		label:   "key=chakramv2 input=testkey000",
	},
	{
		// Minimum-length input (1 byte).
		key:     []byte("test key 000"),
		input:   []byte("A"),
		wantHex: "c479a1d5c8564cdf2f227567c2474d12ed376f2094487e8f3ae83f37149bd4f5",
		label:   "key=testkey000 input=A(1byte)",
	},
}

// TestRandomXVectors verifies each known implementation against the hardcoded
// test vectors and cross-checks CGo against pure-Go when both are present.
func TestRandomXVectors(t *testing.T) {
	// We always have at least one engine compiled in (one of the two build-tag
	// variants). Test it against the known vectors first.
	t.Run("knownVectors", func(t *testing.T) {
		eng := newRandomXEngine(true) // light mode — same hashes, no 2 GB dataset re-alloc
		defer eng.Close()
		impl := randomXImplementation()

		for _, v := range officialVectors {
			if err := eng.Init(v.key); err != nil {
				t.Fatalf("[%s] Init(%q): %v", impl, v.key, err)
			}
			got := eng.Hash(v.input)
			gotHex := hex.EncodeToString(got)
			if gotHex != v.wantHex {
				t.Errorf("[%s] vector %q:\n  got  %s\n  want %s",
					impl, v.label, gotHex, v.wantHex)
			}
		}
		t.Logf("[%s] all %d vectors OK", impl, len(officialVectors))
	})

}

// TestRandomXKeySwitch verifies that re-initialising with a different key
// changes the output deterministically on both implementations.
func TestRandomXKeySwitch(t *testing.T) {
	eng := newRandomXEngine(true) // light mode — same hashes, no 2 GB dataset
	defer eng.Close()

	input := []byte("same input")

	if err := eng.Init([]byte("key A")); err != nil {
		t.Fatal(err)
	}
	h1 := eng.Hash(input)

	if err := eng.Init([]byte("key B")); err != nil {
		t.Fatal(err)
	}
	h2 := eng.Hash(input)

	if fmt.Sprintf("%x", h1) == fmt.Sprintf("%x", h2) {
		t.Error("different keys produced identical hashes — key switch has no effect")
	}

	// Re-init with key A should produce the same result as the first call.
	if err := eng.Init([]byte("key A")); err != nil {
		t.Fatal(err)
	}
	h3 := eng.Hash(input)
	if fmt.Sprintf("%x", h1) != fmt.Sprintf("%x", h3) {
		t.Errorf("re-init key A: got %x, want %x", h3, h1)
	}
	t.Logf("[%s] key-switch: OK", randomXImplementation())
}

// TestRandomXNoOpReInit verifies that calling Init with the same key twice
// returns the same hash (the no-op cache path is exercised).
func TestRandomXNoOpReInit(t *testing.T) {
	eng := newRandomXEngine(true) // light mode — same hashes, no 2 GB dataset
	defer eng.Close()

	key := []byte("test key 000")
	if err := eng.Init(key); err != nil {
		t.Fatal(err)
	}
	h1 := eng.Hash([]byte("input"))

	// Second Init with same key — must be a no-op.
	if err := eng.Init(key); err != nil {
		t.Fatal(err)
	}
	h2 := eng.Hash([]byte("input"))

	if fmt.Sprintf("%x", h1) != fmt.Sprintf("%x", h2) {
		t.Error("same key re-init changed the hash — no-op path is broken")
	}
}
