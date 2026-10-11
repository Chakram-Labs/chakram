package consensus

import (
	"encoding/binary"
	"testing"
)

func TestSerializeSize(t *testing.T) {
	h := ConsensusBlockHeader{}
	got := h.Serialize()
	if len(got) != HeaderSize {
		t.Fatalf("Serialize returned %d bytes, want %d", len(got), HeaderSize)
	}
}

func TestSerializeFieldOrder(t *testing.T) {
	h := ConsensusBlockHeader{
		Version:    2,
		Height:     1,
		Timestamp:  0x1122334455667788,
		Difficulty: 0xCAFEBABE,
		Nonce:      0xDEADBEEF,
	}
	h.ParentHash[0] = 0xAA
	h.ExecutionPayloadHash[0] = 0xBB
	h.MinerAddress[0] = 0xCC

	buf := h.Serialize()

	checks := []struct {
		name   string
		offset int
		size   int
		want   uint64
	}{
		{"Version", 0, 4, 2},
		{"Height", 4, 8, 1},
		{"Timestamp", 76, 8, 0x1122334455667788},
		{"Difficulty", 104, 8, 0xCAFEBABE},
		{"Nonce", 112, 8, 0xDEADBEEF},
	}
	for _, c := range checks {
		var got uint64
		switch c.size {
		case 4:
			got = uint64(binary.LittleEndian.Uint32(buf[c.offset:]))
		case 8:
			got = binary.LittleEndian.Uint64(buf[c.offset:])
		}
		if got != c.want {
			t.Errorf("%s at offset %d: got %x, want %x", c.name, c.offset, got, c.want)
		}
	}

	byteChecks := []struct {
		name   string
		offset int
		want   byte
	}{
		{"ParentHash[0]", 12, 0xAA},
		{"ExecutionPayloadHash[0]", 44, 0xBB},
		{"MinerAddress[0]", 84, 0xCC},
	}
	for _, c := range byteChecks {
		if buf[c.offset] != c.want {
			t.Errorf("%s at offset %d: got %02x, want %02x", c.name, c.offset, buf[c.offset], c.want)
		}
	}
}

func TestSerializeIsDeterministic(t *testing.T) {
	h := ConsensusBlockHeader{Version: 2, Height: 100, Difficulty: 0xFFFF}
	if h.Serialize() != h.Serialize() {
		t.Fatal("Serialize is not deterministic")
	}
}

func TestGenesisHashIsDeterministic(t *testing.T) {
	h := &ConsensusBlockHeader{Version: 2}
	a := GenesisHash(h)
	b := GenesisHash(h)
	if a != b {
		t.Fatal("GenesisHash is not deterministic")
	}
	var zero [32]byte
	if a == zero {
		t.Fatal("GenesisHash returned all-zero result")
	}
}

func TestHashRoundTrip(t *testing.T) {
	var original [32]byte
	for i := range original {
		original[i] = byte(i)
	}
	decoded, err := HashFromHex(HashToHex(original))
	if err != nil {
		t.Fatalf("HashFromHex: %v", err)
	}
	if decoded != original {
		t.Errorf("round-trip mismatch: got %x, want %x", decoded, original)
	}
}

func TestHashFromHexBareOk(t *testing.T) {
	_, err := HashFromHex("0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("bare hex without 0x should be accepted: %v", err)
	}
}

func TestAddrFromHex(t *testing.T) {
	addr, err := AddrFromHex("0xDeaDbeefdEAdbeefdEadbEEFdeadbeEFdEaDbeeF")
	if err != nil {
		t.Fatalf("AddrFromHex: %v", err)
	}
	if addr[0] != 0xDe {
		t.Errorf("addr[0]: got %02x, want de", addr[0])
	}
	if addr[19] != 0xeF {
		t.Errorf("addr[19]: got %02x, want ef", addr[19])
	}
}

func TestAddrFromHexRejectsBadLength(t *testing.T) {
	_, err := AddrFromHex("0xDEAD")
	if err == nil {
		t.Fatal("expected error for short address, got nil")
	}
}
