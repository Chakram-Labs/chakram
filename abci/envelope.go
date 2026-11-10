// Package abci implements the CometBFT ABCI application for Chakram.
// It bridges CometBFT BFT consensus to geth via the Engine API.
package abci

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	envelopeMagic   = "CHKV"
	envelopeVersion = byte(3)
)

// ExecutionEnvelope carries one geth ExecutionPayloadV3 through CometBFT consensus.
//
// Wire format: 4-byte magic ("CHKV") + 1-byte version (0x03) + JSON body.
// The JSON body uses base64 encoding for []byte fields (standard encoding/json behaviour).
// This is the Phase 1 encoding; protobuf is planned for Phase 2+ (D1).
type ExecutionEnvelope struct {
	Version               byte     `json:"version"`
	ExecutionPayload      []byte   `json:"execution_payload"`        // JSON-encoded ExecutionPayloadV3
	VersionedHashes       [][]byte `json:"versioned_hashes"`         // empty for PoA (no blobs)
	ParentBeaconBlockRoot []byte   `json:"parent_beacon_block_root"` // 32 zero bytes for PoA
}

// EncodeEnvelope serialises e into the CHKV wire format.
func EncodeEnvelope(e *ExecutionEnvelope) ([]byte, error) {
	if e == nil {
		return nil, errors.New("envelope: nil envelope")
	}
	body, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("envelope marshal: %w", err)
	}
	out := make([]byte, 0, 5+len(body))
	out = append(out, []byte(envelopeMagic)...)
	out = append(out, envelopeVersion)
	out = append(out, body...)
	return out, nil
}

// DecodeEnvelope parses b from the CHKV wire format.
// Returns an error if the magic bytes are wrong, the version is unsupported,
// or the JSON body is malformed.
func DecodeEnvelope(b []byte) (*ExecutionEnvelope, error) {
	if len(b) < 5 {
		return nil, errors.New("envelope: too short")
	}
	if string(b[:4]) != envelopeMagic {
		return nil, fmt.Errorf("envelope: bad magic %q (want %q)", string(b[:4]), envelopeMagic)
	}
	if b[4] != envelopeVersion {
		return nil, fmt.Errorf("envelope: unsupported version %d (want %d)", b[4], envelopeVersion)
	}
	var e ExecutionEnvelope
	if err := json.Unmarshal(b[5:], &e); err != nil {
		return nil, fmt.Errorf("envelope: unmarshal body: %w", err)
	}
	return &e, nil
}
