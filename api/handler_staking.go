package api

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/sha3"
)

// ── ABI helpers ───────────────────────────────────────────────────────────────

func keccak4(sig string) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(sig))
	return h.Sum(nil)[:4]
}

type ethCallParam struct {
	To   string `json:"to"`
	Data string `json:"data"`
}

func (s *Server) ethCallRaw(to, data string) ([]byte, error) {
	var result string
	if err := s.gethRPC("eth_call", []any{ethCallParam{To: to, Data: data}, "latest"}, &result); err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimPrefix(result, "0x"))
}

func (s *Server) ethCall0(to, sig string) ([]byte, error) {
	return s.ethCallRaw(to, "0x"+hex.EncodeToString(keccak4(sig)))
}

func (s *Server) ethCall1Uint(to, sig string, n uint64) ([]byte, error) {
	sel := keccak4(sig)
	arg := make([]byte, 32)
	binary.BigEndian.PutUint64(arg[24:], n)
	return s.ethCallRaw(to, "0x"+hex.EncodeToString(append(sel, arg...)))
}

func (s *Server) ethCall1Addr(to, sig string, addr string) ([]byte, error) {
	addrBytes, err := hex.DecodeString(strings.TrimPrefix(addr, "0x"))
	if err != nil || len(addrBytes) != 20 {
		return nil, fmt.Errorf("invalid address %q", addr)
	}
	sel := keccak4(sig)
	arg := make([]byte, 32)
	copy(arg[12:], addrBytes)
	return s.ethCallRaw(to, "0x"+hex.EncodeToString(append(sel, arg...)))
}

// ABI slot decoders (each slot is 32 bytes at the given byte offset).

func abiAddress(b []byte, off int) string {
	if len(b) < off+32 {
		return ""
	}
	return "0x" + hex.EncodeToString(b[off+12:off+32])
}

func abiUint256Str(b []byte, off int) string {
	if len(b) < off+32 {
		return "0"
	}
	return new(big.Int).SetBytes(b[off : off+32]).String()
}

func abiUint64(b []byte, off int) uint64 {
	if len(b) < off+32 {
		return 0
	}
	return binary.BigEndian.Uint64(b[off+24 : off+32])
}

func abiBool(b []byte, off int) bool {
	if len(b) < off+32 {
		return false
	}
	return b[off+31] != 0
}

func abiBytes32(b []byte, off int) []byte {
	if len(b) < off+32 {
		return make([]byte, 32)
	}
	out := make([]byte, 32)
	copy(out, b[off:off+32])
	return out
}

// abiString decodes a dynamic string from an ABI-encoded return value.
// Layout: 32-byte offset | 32-byte length | utf-8 bytes (padded to 32).
func abiString(b []byte) string {
	if len(b) < 64 {
		return ""
	}
	length := int(binary.BigEndian.Uint64(b[56:64]))
	if length <= 0 || len(b) < 64+length {
		return ""
	}
	return string(b[64 : 64+length])
}

// abiStringCalldata builds calldata for a function that takes a single string arg.
// Layout: 4-byte selector | 32-byte offset (0x20) | 32-byte length | utf-8 bytes (padded).
func abiStringCalldata(sig, s string) []byte {
	sel := keccak4(sig)
	raw := []byte(s)
	n := len(raw)
	offset := make([]byte, 32)
	offset[31] = 0x20
	length := make([]byte, 32)
	binary.BigEndian.PutUint64(length[24:], uint64(n))
	padded := make([]byte, ((n+31)/32)*32)
	copy(padded, raw)
	out := make([]byte, 0, 4+32+32+len(padded))
	out = append(out, sel...)
	out = append(out, offset...)
	out = append(out, length...)
	out = append(out, padded...)
	return out
}

// pubKeyToCometAddr converts a 32-byte Ed25519 public key to a CometBFT validator
// address: uppercase hex of SHA256(pubKey)[0:20].
func pubKeyToCometAddr(pubKey32 []byte) string {
	h := sha256.Sum256(pubKey32)
	return strings.ToUpper(hex.EncodeToString(h[:20]))
}

// ── CometBFT power map ────────────────────────────────────────────────────────

func (s *Server) cometValidatorPowers() map[string]int64 {
	var result struct {
		Validators []struct {
			Address     string `json:"address"`
			VotingPower string `json:"voting_power"`
		} `json:"validators"`
	}
	if err := s.cometGet("/validators", &result); err != nil {
		return nil
	}
	m := make(map[string]int64, len(result.Validators))
	for _, v := range result.Validators {
		power, _ := strconv.ParseInt(v.VotingPower, 10, 64)
		m[strings.ToUpper(v.Address)] = power
	}
	return m
}

// ── Handler ───────────────────────────────────────────────────────────────────

func (s *Server) handleStaking(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if s.cfg.StakingContractAddr == "" {
		writeJSON(w, http.StatusOK, StakingResponse{Enabled: false, Validators: []StakingValidator{}})
		return
	}

	contract := s.cfg.StakingContractAddr

	ownerB, _ := s.ethCall0(contract, "owner()")
	minStakeB, _ := s.ethCall0(contract, "minStake()")
	unbondingB, _ := s.ethCall0(contract, "unbondingPeriod()")
	countB, _ := s.ethCall0(contract, "getValidatorCount()")

	owner := abiAddress(ownerB, 0)
	minStake := abiUint256Str(minStakeB, 0)
	unbondingPeriod := abiUint64(unbondingB, 0)
	count := abiUint64(countB, 0)

	powerMap := s.cometValidatorPowers()

	validators := make([]StakingValidator, 0, count)
	for i := uint64(0); i < count; i++ {
		listB, err := s.ethCall1Uint(contract, "validatorList(uint256)", i)
		if err != nil {
			continue
		}
		evmAddr := abiAddress(listB, 0)
		if evmAddr == "" || strings.TrimPrefix(evmAddr, "0x") == strings.Repeat("0", 40) {
			continue
		}

		// validators(address) returns 9 × 32-byte slots:
		// [0]  ed25519PubKey (bytes32)        offset   0
		// [1]  rewardAddress (address)        offset  32
		// [2]  stake (uint256)                offset  64
		// [3]  whitelisted (bool)             offset  96
		// [4]  active (bool)                  offset 128
		// [5]  jailed (bool)                  offset 160
		// [6]  registered (bool)              offset 192
		// [7]  commissionBps (uint16)         offset 224
		// [8]  unbondingUnlockBlock (uint256) offset 256
		infoB, err := s.ethCall1Addr(contract, "validators(address)", evmAddr)
		if err != nil || len(infoB) < 288 {
			continue
		}

		pubKey32 := abiBytes32(infoB, 0)
		rewardAddr := abiAddress(infoB, 32)
		stake := abiUint256Str(infoB, 64)
		whitelisted := abiBool(infoB, 96)
		active := abiBool(infoB, 128)
		jailed := abiBool(infoB, 160)
		unlockBlock := abiUint64(infoB, 256)

		cometAddr := pubKeyToCometAddr(pubKey32)
		var power int64
		if powerMap != nil {
			power = powerMap[cometAddr]
		}

		monikerB, _ := s.ethCall1Addr(contract, "monikers(address)", evmAddr)
		websiteB, _ := s.ethCall1Addr(contract, "websites(address)", evmAddr)

		validators = append(validators, StakingValidator{
			EVMAddr:              evmAddr,
			CometAddr:            cometAddr,
			Ed25519PubKey:        hex.EncodeToString(pubKey32),
			RewardAddr:           rewardAddr,
			Stake:                stake,
			Whitelisted:          whitelisted,
			Active:               active,
			Jailed:               jailed,
			UnbondingUnlockBlock: unlockBlock,
			VotingPower:          power,
			Moniker:              abiString(monikerB),
			Website:              abiString(websiteB),
		})
	}

	writeJSON(w, http.StatusOK, StakingResponse{
		Enabled:         true,
		ContractAddr:    contract,
		Owner:           owner,
		MinStake:        minStake,
		UnbondingPeriod: unbondingPeriod,
		Validators:      validators,
	})
}
