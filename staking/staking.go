// Package staking reads StakingRegistry.sol events from geth logs and converts
// them into CometBFT ValidatorUpdates for FinalizeBlock.
package staking

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

// weiPerCHK is 1e18 — dividing stake wei by this gives CHK units (= CometBFT power).
var weiPerCHK = new(big.Int).SetUint64(1_000_000_000_000_000_000)

// maxValidatorPower caps voting power so no single validator can dominate
// consensus by staking an unlimited amount. 1 CHK = 1 power, cap = 100,000 CHK.
const maxValidatorPower = int64(100_000)

// StakeToPower converts stake in wei to a CometBFT voting power unit.
// 1 CHK (1e18 wei) = power 1. Capped at maxValidatorPower. Minimum is 1
// so registered validators always participate even if staked below 1 CHK.
func StakeToPower(stakeWei *big.Int) int64 {
	if stakeWei == nil || stakeWei.Sign() <= 0 {
		return 1
	}
	p := new(big.Int).Div(stakeWei, weiPerCHK)
	if !p.IsInt64() {
		return maxValidatorPower
	}
	v := p.Int64()
	if v <= 0 {
		return 1 // stake < 1 CHK → minimum power
	}
	if v > maxValidatorPower {
		return maxValidatorPower
	}
	return v
}

// EventType classifies a staking contract log entry.
type EventType int

const (
	EventRegistered EventType = iota // validator joined
	EventJailed                      // validator removed — Power = 0
	EventUnjailed                    // validator restored
)

// Event is a parsed log entry from StakingRegistry.sol.
type Event struct {
	Type       EventType
	Validator  string   // 0x-prefixed EVM address (lowercase)
	PubKey     []byte   // 32-byte Ed25519 pubkey; non-empty only for EventRegistered
	RewardAddr string   // 0x-prefixed EVM address; non-empty only for EventRegistered
	StakeWei   *big.Int // stake in wei; non-nil for EventRegistered
}

// eventTopics holds the keccak256(eventSignature) for each watched event,
// computed once at package init.
var eventTopics struct {
	registered string
	jailed     string
	unjailed   string
}

func init() {
	eventTopics.registered = keccak256Topic("ValidatorRegistered(address,bytes32,address,uint256)")
	eventTopics.jailed = keccak256Topic("ValidatorJailed(address)")
	eventTopics.unjailed = keccak256Topic("ValidatorUnjailed(address)")
}

func keccak256Topic(sig string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(sig))
	return "0x" + hex.EncodeToString(h.Sum(nil))
}

// ReadEvents queries geth for StakingRegistry logs in the given execution block
// and returns parsed validator set change events in emission order.
func ReadEvents(ctx context.Context, gethRPCURL, contractAddr, blockHashHex string) ([]Event, error) {
	type logEntry struct {
		Topics []string `json:"topics"`
		Data   string   `json:"data"`
	}

	filter := map[string]any{
		"blockHash": blockHashHex,
		"address":   contractAddr,
		// OR-filter: any of the three event topics.
		"topics": [][]string{{
			eventTopics.registered,
			eventTopics.jailed,
			eventTopics.unjailed,
		}},
	}
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "eth_getLogs",
		"params":  []any{filter},
		"id":      1,
	})

	httpCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, gethRPCURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("staking: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("staking: eth_getLogs: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("staking: read response: %w", err)
	}

	var rpc struct {
		Result []logEntry `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil {
		return nil, fmt.Errorf("staking: decode response: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("staking: eth_getLogs error: %s", rpc.Error.Message)
	}

	var events []Event
	for _, log := range rpc.Result {
		ev, err := parseLog(log.Topics, log.Data)
		if err != nil {
			continue // skip malformed logs
		}
		events = append(events, ev)
	}
	return events, nil
}

// parseLog converts a raw log entry into a typed Event.
func parseLog(logTopics []string, data string) (Event, error) {
	if len(logTopics) < 2 {
		return Event{}, fmt.Errorf("too few topics: %d", len(logTopics))
	}

	// topics[1] is the indexed validator address: 0x + 12-zero-bytes + 20-byte-addr
	validatorAddr, err := topicToAddress(logTopics[1])
	if err != nil {
		return Event{}, fmt.Errorf("parse validator address: %w", err)
	}

	switch logTopics[0] {
	case eventTopics.registered:
		// data = ABI-encoded (bytes32 pubKey, address rewardAddr, uint256 stake)
		// bytes  0-31: ed25519 pubkey
		// bytes 32-63: reward address (right-aligned, 12 zero pad + 20 byte addr)
		// bytes 64-95: stake in wei
		dataBytes, err := hexDecode(data)
		if err != nil || len(dataBytes) < 32 {
			return Event{}, fmt.Errorf("bad ValidatorRegistered data")
		}
		pubKey := make([]byte, 32)
		copy(pubKey, dataBytes[:32])
		var rewardAddr string
		var stakeWei *big.Int
		if len(dataBytes) >= 64 {
			rewardAddr = "0x" + strings.ToLower(hex.EncodeToString(dataBytes[44:64]))
		}
		if len(dataBytes) >= 96 {
			stakeWei = new(big.Int).SetBytes(dataBytes[64:96])
		}
		return Event{Type: EventRegistered, Validator: validatorAddr, PubKey: pubKey, RewardAddr: rewardAddr, StakeWei: stakeWei}, nil

	case eventTopics.jailed:
		return Event{Type: EventJailed, Validator: validatorAddr}, nil

	case eventTopics.unjailed:
		return Event{Type: EventUnjailed, Validator: validatorAddr}, nil

	default:
		return Event{}, fmt.Errorf("unknown topic %s", logTopics[0])
	}
}

// topicToAddress extracts a 0x-prefixed lowercase EVM address from an ABI-encoded
// address topic (32 bytes, address right-aligned, left-padded with zeros).
func topicToAddress(topic string) (string, error) {
	topic = strings.TrimPrefix(topic, "0x")
	if len(topic) != 64 {
		return "", fmt.Errorf("bad topic length %d", len(topic))
	}
	return "0x" + strings.ToLower(topic[24:]), nil
}

// hexDecode decodes a 0x-prefixed hex string.
func hexDecode(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	return hex.DecodeString(s)
}

// NormalizeAddr returns a lowercase hex EVM address without the 0x prefix,
// suitable for use as a map key.
func NormalizeAddr(addr string) string {
	return strings.ToLower(strings.TrimPrefix(addr, "0x"))
}

// QueryValidatorStake calls validators(address) on the staking contract and
// returns the validator's current stake in wei. Used for unjail power recovery.
func QueryValidatorStake(ctx context.Context, gethRPCURL, contractAddr, validatorAddr string) (*big.Int, error) {
	addrBytes, err := hexDecode(validatorAddr)
	if err != nil || len(addrBytes) != 20 {
		return nil, fmt.Errorf("invalid validator address: %s", validatorAddr)
	}
	// ABI: selector(4) + zero-padded address(32)
	var calldata [36]byte
	copy(calldata[:4], keccak4("validators(address)"))
	copy(calldata[16:36], addrBytes) // right-align 20-byte addr in 32-byte slot
	result, err := ethCall(ctx, gethRPCURL, contractAddr, hex.EncodeToString(calldata[:]))
	if err != nil {
		return nil, err
	}
	if len(result) < 96 {
		return nil, fmt.Errorf("response too short: %d bytes", len(result))
	}
	return new(big.Int).SetBytes(result[64:96]), nil // stake is slot [2] at offset 64
}

// keccak4 returns the first 4 bytes of keccak256(sig) — the ABI function selector.
func keccak4(sig string) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(sig))
	return h.Sum(nil)[:4]
}

// ethCall performs a raw eth_call against the given contract with hex-encoded calldata.
func ethCall(ctx context.Context, gethRPCURL, contractAddr, dataHex string) ([]byte, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": "eth_call",
		"params": []any{map[string]string{"to": contractAddr, "data": "0x" + dataHex}, "latest"},
		"id":     1,
	})
	httpCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, gethRPCURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var rpc struct {
		Result string  `json:"result"`
		Error  *struct{ Message string `json:"message"` } `json:"error"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil {
		return nil, err
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("%s", rpc.Error.Message)
	}
	return hexDecode(rpc.Result)
}
