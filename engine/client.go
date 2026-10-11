// Package engine wraps the Engine API (EIP-3675) calls used by the Chakram
// consensus client to drive geth. Only the three V3 methods needed for
// Cancun-era payloads are implemented here.
package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Hash is a 32-byte Ethereum hash encoded as a 0x-prefixed hex string in JSON.
type Hash = string

// Address is a 20-byte Ethereum address encoded as 0x-prefixed hex in JSON.
type Address = string

// Quantity is a uint64 encoded as a 0x-prefixed hex string (no leading zeros
// except for the value zero itself, which is "0x0").
type Quantity = string

// WithdrawalV1 represents an EIP-4895 withdrawal entry inside an execution payload.
type WithdrawalV1 struct {
	Index          Quantity `json:"index"`
	ValidatorIndex Quantity `json:"validatorIndex"`
	Address        Address  `json:"address"`
	Amount         Quantity `json:"amount"` // Gwei
}

// ExecutionPayloadV3 is the JSON structure returned by engine_getPayloadV3
// and accepted by engine_newPayloadV3. Fields follow the Engine API spec
// (EIP-4844 / Cancun).
type ExecutionPayloadV3 struct {
	ParentHash    Hash         `json:"parentHash"`
	FeeRecipient  Address      `json:"feeRecipient"`
	StateRoot     Hash         `json:"stateRoot"`
	ReceiptsRoot  Hash         `json:"receiptsRoot"`
	LogsBloom     string       `json:"logsBloom"`
	PrevRandao    Hash         `json:"prevRandao"`
	BlockNumber   Quantity     `json:"blockNumber"`
	GasLimit      Quantity     `json:"gasLimit"`
	GasUsed       Quantity     `json:"gasUsed"`
	Timestamp     Quantity     `json:"timestamp"`
	ExtraData     string       `json:"extraData"`
	BaseFeePerGas Quantity     `json:"baseFeePerGas"`
	BlockHash     Hash         `json:"blockHash"`
	Transactions  []string     `json:"transactions"`
	Withdrawals   []WithdrawalV1 `json:"withdrawals"`
	BlobGasUsed   Quantity     `json:"blobGasUsed"`
	ExcessBlobGas Quantity     `json:"excessBlobGas"`
}

// PayloadAttributesV3 is sent inside forkchoiceUpdated when the consensus
// client wants geth to start building a new payload.
type PayloadAttributesV3 struct {
	Timestamp             Quantity       `json:"timestamp"`
	PrevRandao            Hash           `json:"prevRandao"`
	SuggestedFeeRecipient Address        `json:"suggestedFeeRecipient"`
	Withdrawals           []WithdrawalV1 `json:"withdrawals"`
	ParentBeaconBlockRoot Hash           `json:"parentBeaconBlockRoot"`
}

// ForkchoiceStateV1 describes the current view of the chain from the
// consensus client's perspective.
type ForkchoiceStateV1 struct {
	HeadBlockHash      Hash `json:"headBlockHash"`
	SafeBlockHash      Hash `json:"safeBlockHash"`
	FinalizedBlockHash Hash `json:"finalizedBlockHash"`
}

// ForkchoiceUpdatedResult is the response from engine_forkchoiceUpdatedV3.
type ForkchoiceUpdatedResult struct {
	PayloadStatus PayloadStatusV1 `json:"payloadStatus"`
	PayloadID     *string         `json:"payloadId"` // nil when no attributes were sent
}

// PayloadStatusV1 is the status sub-object inside forkchoiceUpdated and
// newPayload responses.
type PayloadStatusV1 struct {
	Status          string  `json:"status"` // "VALID", "INVALID", "SYNCING", "ACCEPTED"
	LatestValidHash *string `json:"latestValidHash"`
	ValidationError *string `json:"validationError"`
}

// GetPayloadResult is the response from engine_getPayloadV3.
type GetPayloadResult struct {
	ExecutionPayload ExecutionPayloadV3 `json:"executionPayload"`
	BlockValue       Quantity           `json:"blockValue"`
	// BlobsBundle and ShouldOverrideBuilder omitted — blobs are disabled.
}

// Client is a JSON-RPC client for geth's authenticated Engine API.
// It is safe to use from multiple goroutines.
type Client struct {
	url    string
	secret []byte        // raw JWT secret bytes (32 bytes)
	http   *http.Client
}

// NewClientFromFile creates a Client using the JWT secret stored in secretFile
// (a hex-encoded 32-byte secret as written by `openssl rand -hex 32`).
// url should be the geth authrpc endpoint, e.g. "http://localhost:8551".
func NewClientFromFile(url, secretFile string) (*Client, error) {
	raw, err := os.ReadFile(secretFile)
	if err != nil {
		return nil, fmt.Errorf("engine: read jwt secret: %w", err)
	}
	hexStr := strings.TrimSpace(string(raw))
	hexStr = strings.TrimPrefix(hexStr, "0x")
	secret, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("engine: decode jwt secret: %w", err)
	}
	if len(secret) != 32 {
		return nil, fmt.Errorf("engine: jwt secret must be 32 bytes, got %d", len(secret))
	}
	return newClient(url, secret), nil
}

// NewClientFromSecret creates a Client directly from a 32-byte secret.
func NewClientFromSecret(url string, secret []byte) (*Client, error) {
	if len(secret) != 32 {
		return nil, fmt.Errorf("engine: jwt secret must be 32 bytes, got %d", len(secret))
	}
	cp := make([]byte, 32)
	copy(cp, secret)
	return newClient(url, cp), nil
}

// GenerateSecret generates a random 32-byte JWT secret and writes it as a
// 0x-prefixed hex string to path. Used during devnet setup.
func GenerateSecret(path string) ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	content := "0x" + hex.EncodeToString(secret) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return nil, err
	}
	return secret, nil
}

func newClient(url string, secret []byte) *Client {
	return &Client{
		url:    url,
		secret: secret,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// ForkchoiceUpdatedV3 calls engine_forkchoiceUpdatedV3. attrs may be nil when
// the caller only wants to update the fork choice without starting a new build.
func (c *Client) ForkchoiceUpdatedV3(
	ctx context.Context,
	state ForkchoiceStateV1,
	attrs *PayloadAttributesV3,
) (ForkchoiceUpdatedResult, error) {
	params := []any{state}
	if attrs != nil {
		params = append(params, attrs)
	} else {
		params = append(params, nil)
	}
	var result ForkchoiceUpdatedResult
	if err := c.call(ctx, "engine_forkchoiceUpdatedV3", params, &result); err != nil {
		return ForkchoiceUpdatedResult{}, err
	}
	return result, nil
}

// GetPayloadV3 calls engine_getPayloadV3 with the given payloadId (0x-prefixed
// hex string as returned by ForkchoiceUpdatedV3).
func (c *Client) GetPayloadV3(ctx context.Context, payloadID string) (GetPayloadResult, error) {
	var result GetPayloadResult
	if err := c.call(ctx, "engine_getPayloadV3", []any{payloadID}, &result); err != nil {
		return GetPayloadResult{}, err
	}
	return result, nil
}

// NewPayloadV3 calls engine_newPayloadV3. blobVersionedHashes must be an
// empty slice (blobs are disabled). parentBeaconBlockRoot is the consensus
// client's parent block hash (EIP-4788).
func (c *Client) NewPayloadV3(
	ctx context.Context,
	payload ExecutionPayloadV3,
	blobVersionedHashes []Hash,
	parentBeaconBlockRoot Hash,
) (PayloadStatusV1, error) {
	params := []any{payload, blobVersionedHashes, parentBeaconBlockRoot}
	var result PayloadStatusV1
	if err := c.call(ctx, "engine_newPayloadV3", params, &result); err != nil {
		return PayloadStatusV1{}, err
	}
	return result, nil
}

// ---- JSON-RPC plumbing ----

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("engine RPC error %d: %s", e.Code, e.Message)
}

func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	})
	if err != nil {
		return fmt.Errorf("engine: marshal %s: %w", method, err)
	}

	token, err := c.makeJWT()
	if err != nil {
		return fmt.Errorf("engine: jwt: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("engine: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("engine: %s: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("engine: read response: %w", err)
	}

	var rpc rpcResponse
	if err := json.Unmarshal(respBody, &rpc); err != nil {
		return fmt.Errorf("engine: decode response: %w", err)
	}
	if rpc.Error != nil {
		return rpc.Error
	}
	if out != nil {
		if err := json.Unmarshal(rpc.Result, out); err != nil {
			return fmt.Errorf("engine: decode result for %s: %w", method, err)
		}
	}
	return nil
}

func (c *Client) makeJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt: jwt.NewNumericDate(now),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(c.secret)
}
