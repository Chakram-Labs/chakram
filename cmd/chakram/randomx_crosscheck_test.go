//go:build cgo && ((darwin && amd64) || (darwin && arm64) || (linux && amd64) || (windows && amd64))

package main

import (
	"encoding/hex"
	"testing"
)

// TestRandomXCGoPureGoAgreement runs all official vectors through both the
// CGo engine and the pure-Go engine and fails if any result differs.
// This ensures the two implementations are byte-for-byte identical.
func TestRandomXCGoPureGoAgreement(t *testing.T) {
	cgoEng := newRandomXEngine(true) // light mode — same hashes, avoids 2 GB re-alloc crash
	defer cgoEng.Close()

	pureEng := newPureGoEngine()
	defer pureEng.Close()

	for _, v := range officialVectors {
		if err := cgoEng.Init(v.key); err != nil {
			t.Fatalf("CGo Init(%q): %v", v.key, err)
		}
		if err := pureEng.Init(v.key); err != nil {
			t.Fatalf("pure-Go Init(%q): %v", v.key, err)
		}
		cgoHex := hex.EncodeToString(cgoEng.Hash(v.input))
		pureHex := hex.EncodeToString(pureEng.Hash(v.input))
		if cgoHex != pureHex {
			t.Errorf("hash mismatch — vector %q:\n  CGo    %s\n  pureGo %s",
				v.label, cgoHex, pureHex)
		}
	}
	t.Logf("CGo ↔ pure-Go: all %d vectors agree", len(officialVectors))
}
