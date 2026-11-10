package abci

import (
	"bytes"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	original := &ExecutionEnvelope{
		Version:               envelopeVersion,
		ExecutionPayload:      []byte(`{"blockHash":"0xabc"}`),
		VersionedHashes:       nil,
		ParentBeaconBlockRoot: make([]byte, 32),
	}
	encoded, err := EncodeEnvelope(original)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	decoded, err := DecodeEnvelope(encoded)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if decoded.Version != original.Version {
		t.Errorf("Version: got %d, want %d", decoded.Version, original.Version)
	}
	if !bytes.Equal(decoded.ExecutionPayload, original.ExecutionPayload) {
		t.Errorf("ExecutionPayload mismatch")
	}
	if !bytes.Equal(decoded.ParentBeaconBlockRoot, original.ParentBeaconBlockRoot) {
		t.Errorf("ParentBeaconBlockRoot mismatch")
	}
}

func TestEnvelopeBadMagic(t *testing.T) {
	valid, _ := EncodeEnvelope(&ExecutionEnvelope{ExecutionPayload: []byte(`{}`)})
	valid[0] = 'X'
	_, err := DecodeEnvelope(valid)
	if err == nil {
		t.Fatal("expected error for bad magic, got nil")
	}
}

func TestEnvelopeBadVersion(t *testing.T) {
	valid, _ := EncodeEnvelope(&ExecutionEnvelope{ExecutionPayload: []byte(`{}`)})
	valid[4] = 0x02
	_, err := DecodeEnvelope(valid)
	if err == nil {
		t.Fatal("expected error for bad version, got nil")
	}
}

func TestEnvelopeTruncated(t *testing.T) {
	_, err := DecodeEnvelope([]byte("CHK"))
	if err == nil {
		t.Fatal("expected error for truncated input, got nil")
	}
}

func TestEnvelopeNilInput(t *testing.T) {
	_, err := EncodeEnvelope(nil)
	if err == nil {
		t.Fatal("expected error for nil envelope, got nil")
	}
}

func TestEnvelopeEmptyInput(t *testing.T) {
	_, err := DecodeEnvelope([]byte{})
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}
