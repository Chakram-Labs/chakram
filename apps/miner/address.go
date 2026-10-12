package main

import (
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/sha3"
)

// validateAddress returns true for valid Ethereum addresses:
// - 0x prefix + 40 hex chars
// - Must match EIP-55 checksum, OR be all-lowercase, OR be all-uppercase.
func validateAddress(addr string) bool {
	if !strings.HasPrefix(addr, "0x") && !strings.HasPrefix(addr, "0X") {
		return false
	}
	raw := addr[2:]
	if len(raw) != 40 {
		return false
	}
	lower := strings.ToLower(raw)
	if _, err := hex.DecodeString(lower); err != nil {
		return false
	}
	// Accept all-lowercase or all-uppercase.
	if raw == lower || raw == strings.ToUpper(raw) {
		return true
	}
	// Verify EIP-55 checksum.
	return raw == eip55Checksum(lower)
}

func eip55Checksum(lowerHex string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(lowerHex))
	hash := h.Sum(nil)

	out := []byte(lowerHex)
	for i := 0; i < len(out); i++ {
		nibble := hash[i/2]
		if i%2 == 0 {
			nibble >>= 4
		} else {
			nibble &= 0x0f
		}
		if nibble >= 8 && out[i] >= 'a' {
			out[i] -= 32 // to uppercase
		}
	}
	return string(out)
}
