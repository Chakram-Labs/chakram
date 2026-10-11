// fuzz_test.go — Go fuzz targets for network message, transaction, and block decoding.
// Run with: go test -fuzz=FuzzDecodeMessage -fuzztime=30s
package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// FuzzDecodeMessage feeds arbitrary bytes into the P2P message decoder.
// It must never panic regardless of input; malformed messages return errors.
func FuzzDecodeMessage(f *testing.F) {
	// Seed corpus: a valid-looking version message with mainnet magic.
	seed := func(magic [4]byte, msgType MessageType, payload []byte) []byte {
		msg := Message{Magic: magic, Type: msgType, Length: uint32(len(payload)), Payload: payload}
		b, _ := msg.Encode()
		return b
	}
	f.Add(seed(MagicMainnet, MsgVersion, []byte(`{"version":1,"height":0}`)))
	f.Add(seed(MagicTestnet, MsgPing, []byte(`{}`)))
	f.Add([]byte{0x00}) // too short
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		// Must not panic; errors are expected and fine.
		DecodeMessage(r, MagicMainnet) //nolint:errcheck
	})
}

// FuzzDecodeBlock feeds arbitrary JSON bytes into the block deserialiser.
// The deserialiser must never panic.
func FuzzDecodeBlock(f *testing.F) {
	// Seed with a valid minimal block JSON.
	validBlock := blockJSON{
		Header: headerJSON{
			Version:      1,
			Height:       0,
			PreviousHash: "0000000000000000000000000000000000000000000000000000000000000000",
			MerkleRoot:   "0000000000000000000000000000000000000000000000000000000000000000",
			Timestamp:    1_791_504_000,
			Difficulty:   InitialCompactTargetV2,
			Nonce:        0,
		},
		Transactions: []txFullJSON{},
		Hash:         "0000000000000000000000000000000000000000000000000000000000000000",
	}
	seed, _ := json.Marshal(validBlock)
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"header":{}}`))
	f.Add([]byte(`not json`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var bj blockJSON
		if err := json.Unmarshal(data, &bj); err != nil {
			return // malformed JSON is fine
		}
		blockFromJSON(bj) //nolint:errcheck — must not panic
	})
}

// FuzzDecodeTransaction feeds arbitrary JSON bytes into the transaction deserialiser.
func FuzzDecodeTransaction(f *testing.F) {
	validTx := txFullJSON{
		TxID:       "0000000000000000000000000000000000000000000000000000000000000000",
		Inputs:     []txInputJSON{},
		Outputs:    []txOutputJSON{{Value: 1000, PubKeyHash: "0000000000000000000000000000000000000000"}},
		Timestamp:  1_791_504_000,
		IsCoinbase: true,
	}
	seed, _ := json.Marshal(validTx)
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"inputs":null,"outputs":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var tj txFullJSON
		if err := json.Unmarshal(data, &tj); err != nil {
			return
		}
		txFromFullJSON(tj) //nolint:errcheck — must not panic
	})
}
