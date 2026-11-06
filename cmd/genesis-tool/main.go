// genesis-tool generates all configuration files for a Chakram devnet.
//
// Usage:
//
//	genesis-tool --out scripts/devnet-4node --chain-id chakram-devnet-4 --evm-chain-id 11642
//	genesis-tool --out docker/genesis --validators 1  # single-validator dev mode
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cmtcrypto "github.com/cometbft/cometbft/crypto/ed25519"
	cmtp2p "github.com/cometbft/cometbft/p2p"
	cmtprivval "github.com/cometbft/cometbft/privval"
	cmttypes "github.com/cometbft/cometbft/types"
	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
)

// Port base values; node i gets base + i*100.
const (
	gethHTTPBase  = 8545
	gethAuthBase  = 8551
	gethP2PBase   = 30303
	cometP2PBase  = 26656
	cometRPCBase  = 26657
)

// genEVMKey generates a fresh secp256k1 keypair and returns the hex private key
// and checksummed EVM address. The address is derived as keccak256(pubkey[1:])[12:].
func genEVMKey() (privHex, addr string, err error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return "", "", err
	}
	privHex = hex.EncodeToString(privKey.Serialize())

	pub := privKey.PubKey().SerializeUncompressed() // 65 bytes: 04 || x || y
	h := sha3.NewLegacyKeccak256()
	h.Write(pub[1:]) // skip the 04 prefix
	hash := h.Sum(nil)
	addr = "0x" + hex.EncodeToString(hash[12:]) // last 20 bytes
	return privHex, addr, nil
}

func main() {
	outDir := flag.String("out", "scripts/devnet-4node", "output directory")
	chainID := flag.String("chain-id", "chakram-devnet-4", "CometBFT chain ID")
	evmChainID := flag.Int("evm-chain-id", 11640, "geth chain ID")
	numVal := flag.Int("validators", 4, "number of validators to generate (1-4)")
	flag.Parse()

	if *numVal < 1 || *numVal > 4 {
		fmt.Fprintln(os.Stderr, "genesis-tool: --validators must be 1-4")
		os.Exit(1)
	}

	if err := run(*outDir, *chainID, *evmChainID, *numVal); err != nil {
		fmt.Fprintf(os.Stderr, "genesis-tool: %v\n", err)
		os.Exit(1)
	}
}

func run(outDir, chainID string, evmChainID, numValidators int) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}

	genesisTime := time.Now().UTC().Truncate(time.Second)

	// Generate validator keys.
	type validatorInfo struct {
		index       int
		privKey     cmtcrypto.PrivKey
		address     string
		pubKeyB64   string
		privKeyB64  string
		evmAddress  string
		evmPrivKey  string
		nodeID      string
		nodeKeyJSON map[string]any
	}

	validators := make([]validatorInfo, numValidators)
	cometValidators := make([]cmttypes.GenesisValidator, numValidators)
	validatorMap := make([]map[string]any, numValidators)

	for i := 0; i < numValidators; i++ {
		privKey := cmtcrypto.GenPrivKey()
		pubKey := privKey.PubKey()

		// Encode keys as CometBFT expects.
		privKeyFull := make([]byte, 64)
		copy(privKeyFull[:32], privKey[:])
		copy(privKeyFull[32:], pubKey.Bytes())

		evmPrivHex, evmAddr, err := genEVMKey()
		if err != nil {
			return fmt.Errorf("gen evm key for validator %d: %w", i, err)
		}

		// Generate node key here so nodeID is available for peer wiring.
		nodeKeyJSON, nodeID := generateNodeKey()

		validators[i] = validatorInfo{
			index:       i,
			privKey:     privKey,
			address:     pubKey.Address().String(),
			pubKeyB64:   base64.StdEncoding.EncodeToString(pubKey.Bytes()),
			privKeyB64:  base64.StdEncoding.EncodeToString(privKeyFull),
			evmAddress:  evmAddr,
			evmPrivKey:  evmPrivHex,
			nodeID:      nodeID,
			nodeKeyJSON: nodeKeyJSON,
		}

		cometValidators[i] = cmttypes.GenesisValidator{
			Address: pubKey.Address(),
			PubKey:  pubKey,
			Power:   10,
			Name:    fmt.Sprintf("validator-%d", i),
		}

		validatorMap[i] = map[string]any{
			"node":          i,
			"comet_address": pubKey.Address().String(),
			"evm_address":   evmAddr,
			"node_id":       nodeID,
		}
	}

	// Write validator-map.json.
	if err := writeJSON(filepath.Join(outDir, "validator-map.json"), validatorMap); err != nil {
		return fmt.Errorf("validator-map.json: %w", err)
	}
	fmt.Println("wrote validator-map.json")

	// Write CometBFT genesis.
	cometGenesis := &cmttypes.GenesisDoc{
		GenesisTime:   genesisTime,
		ChainID:       chainID,
		InitialHeight: 1,
		ConsensusParams: &cmttypes.ConsensusParams{
			Block: cmttypes.DefaultBlockParams(),
			Evidence: cmttypes.EvidenceParams{
				MaxAgeNumBlocks: 100000,
				MaxAgeDuration:  172800 * time.Second,
			},
			Validator: cmttypes.ValidatorParams{
				PubKeyTypes: []string{cmttypes.ABCIPubKeyTypeEd25519},
			},
			Version: cmttypes.VersionParams{App: 3},
		},
		Validators: cometValidators,
		AppHash:    []byte{},
	}
	cometGenesis.ConsensusParams.Block.MaxBytes = 4194304
	cometGenesis.ConsensusParams.Block.MaxGas = -1

	cometGenesisPath := filepath.Join(outDir, "comet-genesis.json")
	if err := cometGenesis.SaveAs(cometGenesisPath); err != nil {
		return fmt.Errorf("comet-genesis.json: %w", err)
	}
	fmt.Println("wrote comet-genesis.json")

	// Write geth genesis.
	alloc := map[string]map[string]string{}
	// Fund each validator's generated EVM address with 1M CHK —
	// enough to deploy the StakingRegistry, pay gas, and register as validator.
	for _, v := range validators {
		alloc[v.evmAddress] = map[string]string{"balance": "1000000000000000000000000000"}
	}

	gethGenesis := map[string]any{
		"config": map[string]any{
			"chainId":                       evmChainID,
			"homesteadBlock":                0,
			"eip150Block":                   0,
			"eip155Block":                   0,
			"eip158Block":                   0,
			"byzantiumBlock":                0,
			"constantinopleBlock":           0,
			"petersburgBlock":               0,
			"istanbulBlock":                 0,
			"berlinBlock":                   0,
			"londonBlock":                   0,
			"mergeNetsplitBlock":            0,
			"shanghaiTime":                  0,
			"cancunTime":                    0,
			"terminalTotalDifficulty":       0,
			"terminalTotalDifficultyPassed": true,
			"blobSchedule": map[string]any{
				"cancun": map[string]any{
					"target":               3,
					"max":                  6,
					"baseFeeUpdateFraction": 3338477,
				},
			},
		},
		"alloc":      alloc,
		"difficulty": "0x0",
		"gasLimit":   "0x1C9C380",
		"extraData":  "0x",
		"mixHash":    "0x0000000000000000000000000000000000000000000000000000000000000000",
		"nonce":      "0x0",
		"timestamp":  "0x0",
	}

	if err := writeJSON(filepath.Join(outDir, "geth-genesis.json"), gethGenesis); err != nil {
		return fmt.Errorf("geth-genesis.json: %w", err)
	}
	fmt.Println("wrote geth-genesis.json")

	// Write per-node directories.
	for i, v := range validators {
		nodeDir := filepath.Join(outDir, fmt.Sprintf("node%d", i))
		if err := os.MkdirAll(nodeDir, 0755); err != nil {
			return err
		}

		// priv_validator_key.json
		privValKey := map[string]any{
			"address": v.address,
			"pub_key": map[string]any{
				"type":  "tendermint/PubKeyEd25519",
				"value": v.pubKeyB64,
			},
			"priv_key": map[string]any{
				"type":  "tendermint/PrivKeyEd25519",
				"value": v.privKeyB64,
			},
		}
		if err := writeJSON(filepath.Join(nodeDir, "priv_validator_key.json"), privValKey); err != nil {
			return fmt.Errorf("node%d priv_validator_key.json: %w", i, err)
		}

		// priv_validator_state.json (initial state)
		pvState := map[string]any{
			"height": "0",
			"round":  0,
			"step":   0,
		}
		if err := writeJSON(filepath.Join(nodeDir, "priv_validator_state.json"), pvState); err != nil {
			return fmt.Errorf("node%d priv_validator_state.json: %w", i, err)
		}

		// node_key.json — generated in first loop; write stored key.
		if err := writeJSON(filepath.Join(nodeDir, "node_key.json"), v.nodeKeyJSON); err != nil {
			return fmt.Errorf("node%d node_key.json: %w", i, err)
		}

		// config.toml — include persistent_peers for all other validators,
		// using Docker service names (chakram-node{j}:26656) for localnet.
		var peers []string
		for j, peer := range validators {
			if j == i {
				continue
			}
			peers = append(peers, fmt.Sprintf("%s@chakram-node%d:%d", peer.nodeID, j, cometP2PBase))
		}
		configContent := generateConfigTOML(i, chainID, strings.Join(peers, ","))
		if err := os.WriteFile(filepath.Join(nodeDir, "config.toml"), []byte(configContent), 0644); err != nil {
			return fmt.Errorf("node%d config.toml: %w", i, err)
		}

		// evm-key.json — reward address private key, stored for operator use.
		evmKey := map[string]string{
			"address":     v.evmAddress,
			"private_key": "0x" + v.evmPrivKey,
		}
		if err := writeJSON(filepath.Join(nodeDir, "evm-key.json"), evmKey); err != nil {
			return fmt.Errorf("node%d evm-key.json: %w", i, err)
		}

		fmt.Printf("wrote node%d/ (address=%s, evm=%s)\n", i, v.address, v.evmAddress)
	}

	fmt.Printf("\nDone. %d validator(s) generated in %s\n", numValidators, outDir)
	return nil
}

// generateNodeKey creates an Ed25519 node key and returns the JSON representation
// and the CometBFT node ID (hex(sha256(pubkey)[0:20])).
func generateNodeKey() (keyJSON map[string]any, nodeID string) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	privKeyFull := make([]byte, 64)
	copy(privKeyFull, priv)
	// Ed25519 key from Go stdlib: priv[32:] is the public key.
	hash := sha256.Sum256(priv[32:])
	nodeID = hex.EncodeToString(hash[:20])
	keyJSON = map[string]any{
		"priv_key": map[string]any{
			"type":  "tendermint/PrivKeyEd25519",
			"value": base64.StdEncoding.EncodeToString(privKeyFull),
		},
	}
	return
}

func generateConfigTOML(nodeIdx int, chainID, persistentPeers string) string {
	cometP2P := cometP2PBase + nodeIdx*100
	cometRPC := cometRPCBase + nodeIdx*100

	peersLine := ""
	if persistentPeers != "" {
		peersLine = fmt.Sprintf("\npersistent_peers = %q", persistentPeers)
	}
	return fmt.Sprintf(`# CometBFT config for node%d — generated by genesis-tool

[p2p]
laddr = "tcp://0.0.0.0:%d"
allow_duplicate_ip = true%s

[rpc]
laddr = "tcp://0.0.0.0:%d"

[consensus]
timeout_propose = "2s"
timeout_prevote = "1s"
timeout_precommit = "1s"
timeout_commit = "500ms"
create_empty_blocks = true

[mempool]
type = "nop"

[blocksync]
enable = true
`, nodeIdx, cometP2P, peersLine, cometRPC)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Ensure cmtp2p is used (imported for node key generation reference).
var _ = cmtp2p.NodeKey{}
var _ = cmtprivval.FilePV{}
