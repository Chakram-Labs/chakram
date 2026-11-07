package api

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// rewardKeyJSON is the on-disk format for reward-key.json.
type rewardKeyJSON struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

func (s *Server) loadNodeKey() (*rewardKeyJSON, error) {
	if s.cfg.NodeKeyPath == "" {
		return nil, fmt.Errorf("node key path not configured")
	}
	data, err := os.ReadFile(s.cfg.NodeKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read node key: %w", err)
	}
	var k rewardKeyJSON
	if err := json.Unmarshal(data, &k); err != nil || k.Address == "" || k.PrivateKey == "" {
		return nil, fmt.Errorf("invalid node key file")
	}
	return &k, nil
}

// handleNodeWallet serves GET /api/node-wallet.
func (s *Server) handleNodeWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	k, err := s.loadNodeKey()
	if err != nil {
		writeJSON(w, http.StatusOK, NodeWalletResponse{HasKey: false})
		return
	}
	var balHex string
	_ = s.gethRPC("eth_getBalance", []any{k.Address, "latest"}, &balHex)
	bal, _ := new(big.Int).SetString(strings.TrimPrefix(balHex, "0x"), 16)
	if bal == nil {
		bal = new(big.Int)
	}
	writeJSON(w, http.StatusOK, NodeWalletResponse{
		Address:    k.Address,
		BalanceWei: bal.String(),
		HasKey:     true,
	})
}

// handleNodeWalletExportKey serves GET /api/node-wallet/export-key.
// Returns the private key for the node's EVM wallet.
func (s *Server) handleNodeWalletExportKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	k, err := s.loadNodeKey()
	if err != nil {
		writeError(w, http.StatusNotFound, "node key not found — start chakram-node to generate it")
		return
	}
	writeJSON(w, http.StatusOK, NodeWalletExportResponse{PrivateKey: k.PrivateKey})
}

// loadCometPubKey reads the ed25519 public key from CometBFT's priv_validator_key.json.
func (s *Server) loadCometPubKey() ([]byte, error) {
	keyPath := filepath.Join(s.cfg.HomeDir, "config", "priv_validator_key.json")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read priv_validator_key: %w", err)
	}
	var pv struct {
		PubKey struct {
			Value string `json:"value"` // base64 of 32-byte ed25519 pubkey
		} `json:"pub_key"`
	}
	if err := json.Unmarshal(data, &pv); err != nil {
		return nil, fmt.Errorf("decode priv_validator_key: %w", err)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(pv.PubKey.Value)
	if err != nil || len(pubBytes) != 32 {
		return nil, fmt.Errorf("invalid ed25519 pubkey in priv_validator_key.json")
	}
	return pubBytes, nil
}

// evmAddrFromPrivKey derives the Ethereum address (0x-prefixed) from a secp256k1 private key.
func evmAddrFromPrivKey(privKeyHex string) (string, error) {
	privBytes, err := hex.DecodeString(strings.TrimPrefix(privKeyHex, "0x"))
	if err != nil || len(privBytes) != 32 {
		return "", fmt.Errorf("invalid private key")
	}
	privKey := secp256k1.PrivKeyFromBytes(privBytes)
	uncompressed := privKey.PubKey().SerializeUncompressed() // 65 bytes: 0x04 + X + Y
	hash := keccak256Hash(uncompressed[1:])
	return "0x" + hex.EncodeToString(hash[12:]), nil
}

func bigIntToHex(n *big.Int) string {
	if n == nil || n.Sign() == 0 {
		return "0x0"
	}
	return "0x" + n.Text(16)
}

type estGasCallParam struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value,omitempty"`
	Data  string `json:"data"`
}

// sendNodeTx signs and sends a transaction using the node's EVM private key.
// value may be nil (treated as zero). data is the ABI-encoded calldata.
func (s *Server) sendNodeTx(privKeyHex, toAddr string, value *big.Int, data []byte) (string, error) {
	if value == nil {
		value = new(big.Int)
	}

	// chainID
	var chainIDHex string
	if err := s.gethRPC("eth_chainId", []any{}, &chainIDHex); err != nil {
		return "", fmt.Errorf("get chainId: %w", err)
	}
	chainID, err := strconv.ParseUint(strings.TrimPrefix(chainIDHex, "0x"), 16, 64)
	if err != nil {
		return "", fmt.Errorf("parse chainId: %w", err)
	}

	// sender address
	senderAddr, err := evmAddrFromPrivKey(privKeyHex)
	if err != nil {
		return "", fmt.Errorf("derive sender: %w", err)
	}

	// nonce
	var nonceHex string
	if err := s.gethRPC("eth_getTransactionCount", []any{senderAddr, "pending"}, &nonceHex); err != nil {
		return "", fmt.Errorf("get nonce: %w", err)
	}
	nonce, _ := strconv.ParseUint(strings.TrimPrefix(nonceHex, "0x"), 16, 64)

	// gasPrice
	var gasPriceHex string
	if err := s.gethRPC("eth_gasPrice", []any{}, &gasPriceHex); err != nil {
		return "", fmt.Errorf("get gasPrice: %w", err)
	}
	gasPrice, _ := new(big.Int).SetString(strings.TrimPrefix(gasPriceHex, "0x"), 16)
	if gasPrice == nil {
		gasPrice = new(big.Int).SetInt64(1_000_000_000)
	}

	// estimateGas — if it fails the tx would revert; surface the error
	estParam := estGasCallParam{
		From: senderAddr,
		To:   toAddr,
		Data: "0x" + hex.EncodeToString(data),
	}
	if value.Sign() > 0 {
		estParam.Value = bigIntToHex(value)
	}
	var gasHex string
	if err := s.gethRPC("eth_estimateGas", []any{estParam, "latest"}, &gasHex); err != nil {
		return "", fmt.Errorf("tx would revert: %w", err)
	}
	gasLimit, _ := strconv.ParseUint(strings.TrimPrefix(gasHex, "0x"), 16, 64)
	gasLimit = gasLimit * 12 / 10 // 20% buffer

	rawTx, err := buildSignedTxFull(privKeyHex, chainID, nonce, gasPrice, toAddr, value, data, gasLimit)
	if err != nil {
		return "", fmt.Errorf("sign tx: %w", err)
	}

	var txHash string
	if err := s.gethRPC("eth_sendRawTransaction", []any{rawTx}, &txHash); err != nil {
		return "", fmt.Errorf("send tx: %w", err)
	}
	return txHash, nil
}

// handleStakingRegister serves POST /api/staking/register.
// Signs register(bytes32,address) with the node's EVM key.
func (s *Server) handleStakingRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.cfg.StakingContractAddr == "" {
		writeError(w, http.StatusServiceUnavailable, "staking not configured")
		return
	}
	nodeKey, err := s.loadNodeKey()
	if err != nil {
		writeError(w, http.StatusNotFound, "node key not found — start chakram-node to generate it")
		return
	}

	var req StakingRegisterRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	// CometBFT pubkey (bytes32 for the contract)
	pubKey32, err := s.loadCometPubKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read validator key: "+err.Error())
		return
	}

	// reward address (default: node's own EVM address)
	rewardAddr := req.RewardAddress
	if rewardAddr == "" {
		rewardAddr = nodeKey.Address
	}
	rewardBytes, err := hex.DecodeString(strings.TrimPrefix(rewardAddr, "0x"))
	if err != nil || len(rewardBytes) != 20 {
		writeError(w, http.StatusBadRequest, "invalid reward_address")
		return
	}

	// stake amount (default: minStake from contract)
	var stakeWei *big.Int
	if req.StakeWei != "" {
		stakeWei, _ = new(big.Int).SetString(req.StakeWei, 10)
	}
	if stakeWei == nil || stakeWei.Sign() == 0 {
		minStakeB, err := s.ethCall0(s.cfg.StakingContractAddr, "minStake()")
		if err != nil {
			writeError(w, http.StatusBadGateway, "cannot fetch minStake: "+err.Error())
			return
		}
		stakeStr := abiUint256Str(minStakeB, 0)
		stakeWei, _ = new(big.Int).SetString(stakeStr, 10)
		if stakeWei == nil || stakeWei.Sign() == 0 {
			writeError(w, http.StatusInternalServerError, "minStake is zero or unreadable")
			return
		}
	}

	// ABI encode: register(bytes32,address)
	// selector + 32-byte pubkey + 32-byte left-padded address
	var arg0 [32]byte
	copy(arg0[:], pubKey32)
	var arg1 [32]byte
	copy(arg1[12:], rewardBytes)
	calldata := append(keccak4("register(bytes32,address)"), arg0[:]...)
	calldata = append(calldata, arg1[:]...)

	txHash, err := s.sendNodeTx(nodeKey.PrivateKey, s.cfg.StakingContractAddr, stakeWei, calldata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, StakingTxResponse{TxHash: txHash})
}

// handleStakingUnbond serves POST /api/staking/unbond.
func (s *Server) handleStakingUnbond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.cfg.StakingContractAddr == "" {
		writeError(w, http.StatusServiceUnavailable, "staking not configured")
		return
	}
	nodeKey, err := s.loadNodeKey()
	if err != nil {
		writeError(w, http.StatusNotFound, "node key not found")
		return
	}
	calldata := keccak4("startUnbonding()")
	txHash, err := s.sendNodeTx(nodeKey.PrivateKey, s.cfg.StakingContractAddr, nil, calldata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, StakingTxResponse{TxHash: txHash})
}

// handleStakingWithdraw serves POST /api/staking/withdraw.
func (s *Server) handleStakingWithdraw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.cfg.StakingContractAddr == "" {
		writeError(w, http.StatusServiceUnavailable, "staking not configured")
		return
	}
	nodeKey, err := s.loadNodeKey()
	if err != nil {
		writeError(w, http.StatusNotFound, "node key not found")
		return
	}
	calldata := keccak4("withdraw()")
	txHash, err := s.sendNodeTx(nodeKey.PrivateKey, s.cfg.StakingContractAddr, nil, calldata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, StakingTxResponse{TxHash: txHash})
}
