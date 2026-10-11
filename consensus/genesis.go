package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// Chain IDs for the three Chakram 2.0 networks.
// The devnet (11641) is unregistered and distinct from the testnet (11640) so
// that EIP-155 signatures cannot be replayed across the two networks.
const (
	DevnetChainID  = 11641 // local development only; not registered
	TestnetChainID = 11640 // registered in ethereum-lists/chains as of 2026-10-09
	MainnetChainID = 1164  // Chakram mainnet (matches v1 network ID)
)

// GenesisExecutionPayloadHashHex is the keccak256 block hash of the devnet
// genesis execution block produced by geth from config/genesis.json
// (chainId DevnetChainID, Cancun at t=0, timestamp=0x0,
//  extraData="chakram-v2-devnet", no alloc).
//
// Verified via:
//   geth init --datadir <dir> config/genesis.json
//   geth --datadir <dir> --authrpc.port <free> --ipcdisable --exec "eth.getBlock(0).hash" console
const GenesisExecutionPayloadHashHex = "0xd985bc867eb1489a4bcf2e8069db06631d2ca9357471221c0bb7a59d8897109f"

// GenesisExecutionPayloadHashBytes is the byte form of the constant above.
var GenesisExecutionPayloadHashBytes [32]byte

func init() {
	h, err := HashFromHex(GenesisExecutionPayloadHashHex)
	if err != nil {
		panic("consensus: invalid GenesisExecutionPayloadHashHex: " + err.Error())
	}
	GenesisExecutionPayloadHashBytes = h
}

// DevnetGenesisTimestamp is the consensus genesis timestamp used for the
// Phase 2 devnet. It is a fixed value so the consensus genesis block hash
// is reproducible without running geth.
//
// Value: 2026-10-09 00:00:00 UTC — the date Phase 2 work began.
const DevnetGenesisTimestamp int64 = 1760486400

// TestnetGenesisTimestamp is the consensus genesis timestamp for the public
// testnet (chain ID 11640). Fixed to midnight UTC on launch day so the
// consensus genesis block hash is stable and reproducible.
//
// Value: 2026-10-11 00:00:00 UTC — testnet launch date.
const TestnetGenesisTimestamp int64 = 1791676800

// NewGenesisConsensusBlock builds the genesis ConsensusBlock for the devnet.
// The ExecutionPayloadHash is the hardcoded constant above; geth must already
// be initialized from config/genesis.json before this is used.
func NewGenesisConsensusBlock() *ConsensusBlock {
	header := ConsensusBlockHeader{
		Version:              GenesisVersion,
		Height:               0,
		ParentHash:           [32]byte{},                        // 32 zero bytes
		ExecutionPayloadHash: GenesisExecutionPayloadHashBytes,
		Timestamp:            DevnetGenesisTimestamp,
		MinerAddress:         [20]byte{},                        // 20 zero bytes — no reward at genesis
		Difficulty:           InitialCompactTargetV2,
		Nonce:                0,
	}
	hash := GenesisHash(&header)
	return &ConsensusBlock{
		Header:           header,
		ExecutionPayload: nil, // genesis execution payload is not stored; geth owns it
		Hash:             hash,
	}
}

// MainnetGenesisTimestamp is the consensus genesis timestamp for Chakram 2.0
// mainnet. Fixed at midnight UTC on launch day so the genesis block hash is
// stable and reproducible.
//
// Value: 2026-10-11 00:00:00 UTC — same day as testnet, distinct by chain ID.
const MainnetGenesisTimestamp int64 = 1791676800

// NewMainnetGenesisConsensusBlock builds the genesis ConsensusBlock for the
// Chakram 2.0 mainnet (chain ID 1164). Uses MainnetGenesisExecutionPayloadHashBytes
// (the geth block 0 hash from config/genesis.mainnet.json) and InitialCompactTargetV2.
// Geth must be initialized from config/genesis.mainnet.json before this is used.
func NewMainnetGenesisConsensusBlock() *ConsensusBlock {
	header := ConsensusBlockHeader{
		Version:              GenesisVersion,
		Height:               0,
		ParentHash:           [32]byte{},
		ExecutionPayloadHash: MainnetGenesisExecutionPayloadHashBytes,
		Timestamp:            MainnetGenesisTimestamp,
		MinerAddress:         [20]byte{},
		Difficulty:           InitialCompactTargetV2,
		Nonce:                0,
	}
	hash := GenesisHash(&header)
	return &ConsensusBlock{
		Header:           header,
		ExecutionPayload: nil,
		Hash:             hash,
	}
}

// NewTestnetGenesisConsensusBlock builds the genesis ConsensusBlock for the
// public testnet (chain ID 11640). Uses TestnetGenesisExecutionPayloadHashBytes
// (the geth block 0 hash from TestnetGenesisJSON) and TestnetInitialCompactTarget.
// Geth must be initialized from TestnetGenesisJSON before this is used.
func NewTestnetGenesisConsensusBlock() *ConsensusBlock {
	header := ConsensusBlockHeader{
		Version:              GenesisVersion,
		Height:               0,
		ParentHash:           [32]byte{},
		ExecutionPayloadHash: TestnetGenesisExecutionPayloadHashBytes,
		Timestamp:            TestnetGenesisTimestamp,
		MinerAddress:         [20]byte{},
		Difficulty:           TestnetInitialCompactTarget,
		Nonce:                0,
	}
	hash := GenesisHash(&header)
	return &ConsensusBlock{
		Header:           header,
		ExecutionPayload: nil,
		Hash:             hash,
	}
}

// InitialCompactTargetV2 is the mainnet starting compact target (reused from v1.1).
// Decodes to 2^252 — calibrated for production-scale RandomX hardware (>1000 H/s).
// Upper 8 bits = mantissa byte-length, lower 56 bits = mantissa.
const InitialCompactTargetV2 uint64 = 0x2010000000000000

// DevnetInitialCompactTarget is the devnet starting compact target.
// Decodes to 2^248 (256 expected hashes) — calibrated for light-mode RandomX
// on ARM64 MacBook Air (~30 H/s per miner × 3 nodes ≈ 90 H/s total), giving
// ~2.8 s/block during the 90-block LWMA bootstrap window. After 90 blocks
// LWMA-3 adjusts toward the 15 s target. Used only by cmd/chakram; never for mainnet.
const DevnetInitialCompactTarget uint64 = 0x2001000000000000

// DevnetCP5CompactTarget is the compact target used by the CP5 orphan-rate test.
// Decodes to 2^246 (1024 expected hashes) — gives ~11 s/block at 90 H/s total
// (3 light-mode miners on ARM64), close enough to the 15 s production target
// that orphan-rate measurements reflect realistic propagation behaviour.
// This constant is pinned; do not change it without regenerating CP5 baselines.
const DevnetCP5CompactTarget uint64 = 0x1f40000000000000

// TestnetInitialCompactTarget is the starting compact target for the public
// testnet (chain ID 11640). Decodes to 2^244 (~4,096 expected hashes per block).
//
// Calibration (Apple M1 Pro 8-core, full-mode RandomX, 7 threads, 60 s measured):
//   per-thread hashrate  : 153.8 H/s
//   7-thread total       : 1,076 H/s (memory-bandwidth limited; 7 = NumCPU-1)
//   assumed miners at launch: 3 similar M1 nodes = 3 × 1,076 = 3,228 H/s
//   solve time at 3 miners (4,096 hashes): 4,096 / 3,228 ≈ 1.27 s  << 15 s
//   solve time at 1 miner  (4,096 hashes): 4,096 / 1,076 ≈ 3.8 s   << 15 s
//   → bootstrap gap rule (height ≤ DifficultyWindow) is always binding at 15 s ✓
//   After bootstrap: LWMA-3 adjusts toward 15 s within one 90-block window.
//
// This constant is baked into the testnet genesis; do not change it.
const TestnetInitialCompactTarget uint64 = 0x1F10000000000000

// TestnetFaucetAddr is the well-known testnet faucet address.
// All testnet CHK originates here; this address must never appear in the
// mainnet genesis (enforced by TestMainnetGenesisNoAlloc).
//
// Generated 2026-10-11 via scripts/gen-faucet-key.py; private key is stored
// outside the repo at ~/.chakram/testnet-faucet.key (permissions 0600).
var TestnetFaucetAddr = [20]byte{
	0x14, 0x20, 0x14, 0x99, 0x42, 0x50, 0xF6, 0xBA, 0x76, 0x8c,
	0xF0, 0x8c, 0x6a, 0x98, 0x22, 0x94, 0x8E, 0x2F, 0x75, 0xd7,
}

// TestnetFaucetWei is the initial testnet faucet balance (1 billion CHK in wei).
var TestnetFaucetWei = new(big.Int).Mul(
	big.NewInt(1_000_000_000),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),
)

// TestnetGenesisExecutionPayloadHashHex is the keccak256 block hash of the
// testnet genesis execution block produced by geth from TestnetGenesisJSON
// (chainId TestnetChainID, Cancun at t=0, timestamp=0x1,
//  extraData="chakram-v2-testnet", TestnetFaucetAddr funded with TestnetFaucetWei).
//
// Verified via:
//
//	python3 scripts/gen-faucet-key.py   # generates TestnetFaucetAddr
//	go run scripts/gen-testnet-genesis.go > /tmp/testnet-genesis.json
//	geth init --state.scheme hash --datadir /tmp/testnet-geth /tmp/testnet-genesis.json
//	geth --datadir /tmp/testnet-geth --authrpc.port <free> --ipcdisable \
//	     --exec "eth.getBlock(0).hash" console
const TestnetGenesisExecutionPayloadHashHex = "0x0baac5aad5891749e9e55a03f0bb17805edf487ebb39be07394edd0457121bea"

// MainnetGenesisExecutionPayloadHashHex is the keccak256 block hash of the
// mainnet genesis execution block produced by geth from config/genesis.mainnet.json
// (chainId MainnetChainID, Cancun at t=0, timestamp=0x2,
//  extraData="chakram-v2-mainnet", no alloc).
//
// Verified via:
//
//	geth init --datadir <dir> config/genesis.mainnet.json
//	geth --datadir <dir> --authrpc.port <free> --ipcdisable --exec "eth.getBlock(0).hash" console
const MainnetGenesisExecutionPayloadHashHex = "0x785b386c4243e58494d4614aed7d5413a154bc869abbc238bd493df5733d83cf"

// MainnetGenesisExecutionPayloadHashBytes is the byte form of the constant above.
var MainnetGenesisExecutionPayloadHashBytes [32]byte

// TestnetGenesisExecutionPayloadHashBytes is the byte form of the constant above.
var TestnetGenesisExecutionPayloadHashBytes [32]byte

func init() {
	h, err := HashFromHex(TestnetGenesisExecutionPayloadHashHex)
	if err != nil {
		panic("consensus: invalid TestnetGenesisExecutionPayloadHashHex: " + err.Error())
	}
	TestnetGenesisExecutionPayloadHashBytes = h

	m, err := HashFromHex(MainnetGenesisExecutionPayloadHashHex)
	if err != nil {
		panic("consensus: invalid MainnetGenesisExecutionPayloadHashHex: " + err.Error())
	}
	MainnetGenesisExecutionPayloadHashBytes = m
}

// TestnetGenesisJSON returns a geth-compatible genesis JSON for the testnet.
// It is identical to config/genesis.json except that the faucet address is
// funded with faucetWei in the alloc section. The faucet must never appear in
// the mainnet genesis (enforced by TestMainnetGenesisNoAlloc).
//
// faucetAddr is the 20-byte EVM address of the testnet faucet wallet.
// faucetWei is the initial balance in wei (e.g. 1e27 for 1 billion CHK).
func TestnetGenesisJSON(faucetAddr [20]byte, faucetWei *big.Int) ([]byte, error) {
	type blobCancun struct {
		Target               int `json:"target"`
		Max                  int `json:"max"`
		BaseFeeUpdateFraction int `json:"baseFeeUpdateFraction"`
	}
	type blobSched struct {
		Cancun blobCancun `json:"cancun"`
	}
	type chainConfig struct {
		ChainID                       int       `json:"chainId"`
		HomesteadBlock                int       `json:"homesteadBlock"`
		EIP150Block                   int       `json:"eip150Block"`
		EIP155Block                   int       `json:"eip155Block"`
		EIP158Block                   int       `json:"eip158Block"`
		ByzantiumBlock                int       `json:"byzantiumBlock"`
		ConstantinopleBlock           int       `json:"constantinopleBlock"`
		PetersburgBlock               int       `json:"petersburgBlock"`
		IstanbulBlock                 int       `json:"istanbulBlock"`
		MuirGlacierBlock              int       `json:"muirGlacierBlock"`
		BerlinBlock                   int       `json:"berlinBlock"`
		LondonBlock                   int       `json:"londonBlock"`
		ArrowGlacierBlock             int       `json:"arrowGlacierBlock"`
		GrayGlacierBlock              int       `json:"grayGlacierBlock"`
		MergeNetsplitBlock            int       `json:"mergeNetsplitBlock"`
		ShanghaiTime                  int       `json:"shanghaiTime"`
		CancunTime                    int       `json:"cancunTime"`
		TerminalTotalDifficulty       int       `json:"terminalTotalDifficulty"`
		TerminalTotalDifficultyPassed bool      `json:"terminalTotalDifficultyPassed"`
		BlobSchedule                  blobSched `json:"blobSchedule"`
	}
	type allocEntry struct {
		Balance string `json:"balance"`
	}
	type genesisDoc struct {
		Config       chainConfig            `json:"config"`
		Nonce        string                 `json:"nonce"`
		Timestamp    string                 `json:"timestamp"`
		ExtraData    string                 `json:"extraData"`
		GasLimit     string                 `json:"gasLimit"`
		Difficulty   string                 `json:"difficulty"`
		MixHash      string                 `json:"mixHash"`
		Coinbase     string                 `json:"coinbase"`
		Alloc        map[string]allocEntry  `json:"alloc"`
		Number       string                 `json:"number"`
		GasUsed      string                 `json:"gasUsed"`
		ParentHash   string                 `json:"parentHash"`
		BaseFeePerGas string                `json:"baseFeePerGas"`
		Withdrawals  []any                  `json:"withdrawals"`
		BlobGasUsed  string                 `json:"blobGasUsed"`
		ExcessBlobGas string               `json:"excessBlobGas"`
	}

	addrHex := "0x"
	for _, b := range faucetAddr {
		addrHex += fmt.Sprintf("%02x", b)
	}

	doc := genesisDoc{
		Config: chainConfig{
			ChainID:                       TestnetChainID,
			BlobSchedule:                  blobSched{Cancun: blobCancun{Target: 0, Max: 0, BaseFeeUpdateFraction: 3338477}},
			TerminalTotalDifficultyPassed: true,
		},
		Nonce:         "0x0",
		Timestamp:     "0x1",
		ExtraData:     "0x6368616b72616d2d76322d746573746e6574",
		GasLimit:      "0x1C9C380",
		Difficulty:    "0x0",
		MixHash:       "0x0000000000000000000000000000000000000000000000000000000000000000",
		Coinbase:      "0x0000000000000000000000000000000000000000",
		Number:        "0x0",
		GasUsed:       "0x0",
		ParentHash:    "0x0000000000000000000000000000000000000000000000000000000000000000",
		BaseFeePerGas: "0x3B9ACA00",
		Withdrawals:   []any{},
		BlobGasUsed:   "0x0",
		ExcessBlobGas: "0x0",
		Alloc: map[string]allocEntry{
			addrHex: {Balance: "0x" + faucetWei.Text(16)},
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

// VerifyGenesisMatch fetches block 0 from geth via the plain JSON-RPC endpoint
// and checks that its hash matches the given expectedHashHex. Call it with the
// network-appropriate constant (GenesisExecutionPayloadHashHex,
// TestnetGenesisExecutionPayloadHashHex, or MainnetGenesisExecutionPayloadHashHex).
func VerifyGenesisMatch(ctx context.Context, ethRPCURL, expectedHashHex string) error {
	type blockResult struct {
		Hash string `json:"hash"`
	}
	type rpcResp struct {
		Result *blockResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	body := `{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x0",false],"id":1}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ethRPCURL,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("verifyGenesis: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("verifyGenesis: call geth: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("verifyGenesis: read response: %w", err)
	}
	var rpc rpcResp
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return fmt.Errorf("verifyGenesis: decode response: %w", err)
	}
	if rpc.Error != nil {
		return fmt.Errorf("verifyGenesis: geth error: %s", rpc.Error.Message)
	}
	if rpc.Result == nil {
		return fmt.Errorf("verifyGenesis: geth returned null block 0 — data directory not initialized?")
	}

	got := strings.ToLower(rpc.Result.Hash)
	want := strings.ToLower(expectedHashHex)
	if got != want {
		return fmt.Errorf(
			"genesis mismatch: geth block 0 hash is %s but expected %s\n"+
				"The data directory was initialized from the wrong genesis.json.\n"+
				"Re-run: geth removedb --datadir <dir> && geth init --datadir <dir> config/genesis.json",
			got, want,
		)
	}
	return nil
}
