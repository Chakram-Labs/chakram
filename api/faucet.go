package api

// Faucet: sign an EIP-155 legacy transaction in pure Go and send it via
// eth_sendRawTransaction. Dev mode only. Rate limited to one request per IP
// per minute.
//
// Signing uses the well-known Hardhat/Foundry dev key #0. This key is
// pre-funded in every Chakram devnet genesis. Never use it in production.

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// hardhatPrivKeyHex is Hardhat/Foundry dev key #0.
// From address: 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
// Pre-funded in genesis; dev mode only, never production.
const hardhatPrivKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

// faucetRateWindow is the minimum gap between faucet requests from the same IP.
const faucetRateWindow = time.Minute

// rateLimiter tracks per-IP last-seen times (one request per window).
type rateLimiter struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
	window   time.Duration
}

func newRateLimiter(window time.Duration) *rateLimiter {
	return &rateLimiter{lastSeen: make(map[string]time.Time), window: window}
}

// allow returns true and records the time if the IP has not been seen within window.
func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if last, ok := rl.lastSeen[ip]; ok && time.Since(last) < rl.window {
		return false
	}
	rl.lastSeen[ip] = time.Now()
	return true
}

// burstLimiter allows up to maxReqs requests per IP within window.
type burstLimiter struct {
	mu      sync.Mutex
	times   map[string][]time.Time
	window  time.Duration
	maxReqs int
}

func newBurstLimiter(window time.Duration, maxReqs int) *burstLimiter {
	return &burstLimiter{times: make(map[string][]time.Time), window: window, maxReqs: maxReqs}
}

func (bl *burstLimiter) allow(ip string) bool {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-bl.window)
	prev := bl.times[ip]
	var kept []time.Time
	for _, t := range prev {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= bl.maxReqs {
		bl.times[ip] = kept
		return false
	}
	bl.times[ip] = append(kept, now)
	return true
}

// clientIP extracts the real IP from a request (respects X-Forwarded-For if behind proxy).
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.SplitN(fwd, ",", 2)
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// buildSignedTx builds an EIP-155 signed ETH transfer and returns its 0x-prefixed hex encoding.
func buildSignedTx(privKeyHex string, chainID, nonce uint64, gasPrice *big.Int, toHex string, value *big.Int) (string, error) {
	return buildSignedTxFull(privKeyHex, chainID, nonce, gasPrice, toHex, value, nil, 21000)
}

// buildSignedTxFull builds an EIP-155 signed transaction with arbitrary calldata and gasLimit.
func buildSignedTxFull(privKeyHex string, chainID, nonce uint64, gasPrice *big.Int, toHex string, value *big.Int, data []byte, gasLimit uint64) (string, error) {
	privBytes, err := hex.DecodeString(strings.TrimPrefix(privKeyHex, "0x"))
	if err != nil || len(privBytes) != 32 {
		return "", fmt.Errorf("invalid private key")
	}
	privKey := secp256k1.PrivKeyFromBytes(privBytes)

	toBytes, err := hex.DecodeString(strings.TrimPrefix(toHex, "0x"))
	if err != nil || len(toBytes) != 20 {
		return "", fmt.Errorf("invalid to address")
	}

	// EIP-155 signing hash: keccak256(rlp([nonce, gasPrice, gasLimit, to, value, data, chainID, 0, 0]))
	preSign := rlpList([][]byte{
		rlpUint64(nonce),
		rlpBigInt(gasPrice),
		rlpUint64(gasLimit),
		rlpFixedBytes(toBytes),
		rlpBigInt(value),
		rlpBytes(data),
		rlpUint64(chainID),
		rlpBytes(nil),
		rlpBytes(nil),
	})
	hash := keccak256Hash(preSign)

	// SignCompact returns [recovery_byte, r[32], s[32]] where recovery_byte = 27 + recid.
	sig := ecdsa.SignCompact(privKey, hash, false)
	recid := uint64(sig[0] - 27)
	r := new(big.Int).SetBytes(sig[1:33])
	s := new(big.Int).SetBytes(sig[33:65])
	v := new(big.Int).SetUint64(recid + chainID*2 + 35)

	signed := rlpList([][]byte{
		rlpUint64(nonce),
		rlpBigInt(gasPrice),
		rlpUint64(gasLimit),
		rlpFixedBytes(toBytes),
		rlpBigInt(value),
		rlpBytes(data),
		rlpBigInt(v),
		rlpBigInt(r),
		rlpBigInt(s),
	})
	return "0x" + hex.EncodeToString(signed), nil
}

// ── RLP encoding helpers ──────────────────────────────────────────────────────

// rlpBytes encodes a byte slice as an RLP string item.
func rlpBytes(b []byte) []byte {
	n := len(b)
	if n == 0 {
		return []byte{0x80}
	}
	if n == 1 && b[0] < 0x80 {
		return b
	}
	if n <= 55 {
		return append([]byte{byte(0x80 + n)}, b...)
	}
	lb := beMinBytes(uint64(n))
	return append(append([]byte{byte(0xb7 + len(lb))}, lb...), b...)
}

// rlpFixedBytes encodes a fixed-length byte slice (e.g. 20-byte address) — never stripped.
func rlpFixedBytes(b []byte) []byte {
	return rlpBytes(b)
}

// rlpUint64 encodes a uint64 as an RLP integer (big-endian, no leading zeros).
func rlpUint64(n uint64) []byte {
	if n == 0 {
		return []byte{0x80}
	}
	return rlpBytes(beMinBytes(n))
}

// rlpBigInt encodes a *big.Int as an RLP integer.
func rlpBigInt(n *big.Int) []byte {
	if n == nil || n.Sign() == 0 {
		return []byte{0x80}
	}
	return rlpBytes(n.Bytes())
}

// rlpList encodes a list of already-encoded RLP items as an RLP list.
func rlpList(items [][]byte) []byte {
	var content []byte
	for _, item := range items {
		content = append(content, item...)
	}
	n := len(content)
	if n <= 55 {
		return append([]byte{byte(0xc0 + n)}, content...)
	}
	lb := beMinBytes(uint64(n))
	return append(append([]byte{byte(0xf7 + len(lb))}, lb...), content...)
}

// beMinBytes returns the big-endian minimal (no leading zeros) encoding of n.
func beMinBytes(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	i := 0
	for i < 7 && b[i] == 0 {
		i++
	}
	return b[i:]
}

// keccak256Hash returns the Keccak-256 hash (Ethereum variant, not SHA3-256).
func keccak256Hash(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}
