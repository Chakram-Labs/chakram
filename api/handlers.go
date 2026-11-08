package api

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// handleStatus handles GET /api/status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var cometStatus struct {
		NodeInfo struct {
			ID      string `json:"id"`
			Moniker string `json:"moniker"`
			Version string `json:"version"`
			Network string `json:"network"`
		} `json:"node_info"`
		SyncInfo struct {
			LatestBlockHeight string `json:"latest_block_height"`
			LatestBlockHash   string `json:"latest_block_hash"`
			LatestAppHash     string `json:"latest_app_hash"`
			CatchingUp        bool   `json:"catching_up"`
		} `json:"sync_info"`
		ValidatorInfo struct {
			Address     string `json:"address"`
			PubKey      any    `json:"pub_key"`
			VotingPower string `json:"voting_power"`
		} `json:"validator_info"`
	}
	if err := s.cometGet("/status", &cometStatus); err != nil {
		writeError(w, http.StatusBadGateway, "comet unavailable: "+err.Error())
		return
	}

	var netInfo struct {
		NPeers string `json:"n_peers"`
	}
	_ = s.cometGet("/net_info", &netInfo)

	height, _ := strconv.ParseInt(cometStatus.SyncInfo.LatestBlockHeight, 10, 64)
	peers, _ := strconv.Atoi(netInfo.NPeers)
	power, _ := strconv.ParseInt(cometStatus.ValidatorInfo.VotingPower, 10, 64)

	catchingUp := cometStatus.SyncInfo.CatchingUp
	var catchVal float64
	if catchingUp {
		catchVal = 1
	}
	s.metrics.ChainHeight.Set(float64(height))
	s.metrics.PeerCount.Set(float64(peers))
	s.metrics.CatchingUp.Set(catchVal)

	// Query geth for execution layer heights.
	var gethHeadHex string
	gethAvailable := s.gethRPC("eth_blockNumber", []any{}, &gethHeadHex) == nil
	gethHeight, _ := hexToInt64(gethHeadHex)

	var gethFinalBlock struct {
		Number string `json:"number"`
	}
	_ = s.gethRPC("eth_getBlockByNumber", []any{"finalized", false}, &gethFinalBlock)
	gethFinalized, _ := hexToInt64(gethFinalBlock.Number)

	// Health gate is blocked when geth head is >2 blocks behind CometBFT.
	healthGateBlocked := gethAvailable && height > 0 && (height-gethHeight) > 2

	writeJSON(w, http.StatusOK, StatusResponse{
		NodeID:            cometStatus.NodeInfo.ID,
		Moniker:           cometStatus.NodeInfo.Moniker,
		ChainID:           cometStatus.NodeInfo.Network,
		Height:            height,
		LatestHash:        cometStatus.SyncInfo.LatestBlockHash,
		AppHash:           cometStatus.SyncInfo.LatestAppHash,
		CatchingUp:        catchingUp,
		Peers:             peers,
		CometVersion:      cometStatus.NodeInfo.Version,
		ValidatorAddr:     cometStatus.ValidatorInfo.Address,
		VotingPower:       power,
		GethHeight:        gethHeight,
		GethFinalized:     gethFinalized,
		GethAvailable:     gethAvailable,
		HealthGateBlocked: healthGateBlocked,
	})
}

// handleValidator handles GET /api/validator.
func (s *Server) handleValidator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var cometStatus struct {
		SyncInfo struct {
			LatestBlockHeight string `json:"latest_block_height"`
		} `json:"sync_info"`
		ValidatorInfo struct {
			Address     string `json:"address"`
			PubKey      any    `json:"pub_key"`
			VotingPower string `json:"voting_power"`
		} `json:"validator_info"`
	}
	if err := s.cometGet("/status", &cometStatus); err != nil {
		writeError(w, http.StatusBadGateway, "comet unavailable: "+err.Error())
		return
	}

	power, _ := strconv.ParseInt(cometStatus.ValidatorInfo.VotingPower, 10, 64)
	valAddr := cometStatus.ValidatorInfo.Address
	currentHeight, _ := strconv.ParseInt(cometStatus.SyncInfo.LatestBlockHeight, 10, 64)

	// Count signed/missed in the last 100 blocks by checking last_commit.signatures.
	signed, missed := countSignedMissed(s, valAddr, currentHeight, 100)

	// Reward address and balance from config.
	rewardAddr := s.cfg.RewardAddress
	var rewardBalance string
	if rewardAddr != "" {
		var balHex string
		if err := s.gethRPC("eth_getBalance", []any{rewardAddr, "latest"}, &balHex); err == nil {
			rewardBalance = hexToDecStr(balHex)
		}
	}

	writeJSON(w, http.StatusOK, ValidatorResponse{
		Address:       valAddr,
		PubKey:        pubKeyValue(cometStatus.ValidatorInfo.PubKey),
		Power:         power,
		IsActive:      power > 0,
		SignedLast100: signed,
		MissedLast100: missed,
		RewardAddress: rewardAddr,
		RewardBalance: rewardBalance,
	})
}

// countSignedMissed counts how many of the last n blocks the validator at valAddr signed.
// Fetches blocks concurrently (up to 20 in-flight) to avoid N sequential RPC calls.
func countSignedMissed(s *Server, valAddr string, currentHeight int64, n int) (signed, missed int) {
	if valAddr == "" || currentHeight <= 1 {
		return
	}
	limit := n
	if int(currentHeight-1) < limit {
		limit = int(currentHeight - 1)
	}
	if limit <= 0 {
		return
	}

	type commitSig struct {
		BlockIDFlag      int    `json:"block_id_flag"`
		ValidatorAddress string `json:"validator_address"`
	}
	type blockResult struct {
		Block struct {
			LastCommit struct {
				Signatures []commitSig `json:"signatures"`
			} `json:"last_commit"`
		} `json:"block"`
	}

	results := make([]bool, limit) // true = signed
	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)

	for i := 0; i < limit; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			h := currentHeight - int64(i)
			var r blockResult
			if err := s.cometGet(fmt.Sprintf("/block?height=%d", h), &r); err != nil {
				return
			}
			for _, sig := range r.Block.LastCommit.Signatures {
				if sig.ValidatorAddress == valAddr && sig.BlockIDFlag == 2 {
					results[i] = true
					break
				}
			}
		}(i)
	}
	wg.Wait()

	for _, s := range results {
		if s {
			signed++
		} else {
			missed++
		}
	}
	return
}

// handleBlocks handles GET /api/blocks?limit=20&offset=0.
func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := 20
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	// Get current height.
	var statusResult struct {
		SyncInfo struct {
			LatestBlockHeight string `json:"latest_block_height"`
		} `json:"sync_info"`
	}
	if err := s.cometGet("/status", &statusResult); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	maxHeight, _ := strconv.ParseInt(statusResult.SyncInfo.LatestBlockHeight, 10, 64)
	if maxHeight == 0 {
		writeJSON(w, http.StatusOK, BlocksResponse{Blocks: []BlockSummary{}, Total: 0})
		return
	}

	// Fetch blocks from maxHeight downward.
	blocks := make([]BlockSummary, 0, limit)
	startHeight := maxHeight - int64(offset)
	for i := 0; i < limit && startHeight > 0; i++ {
		b, err := s.fetchBlock(startHeight)
		if err == nil {
			blocks = append(blocks, BlockSummary{
				Height:   b.Height,
				Hash:     b.Hash,
				Time:     b.Time,
				Proposer: b.Proposer,
				TxCount:  len(b.Txs),
				ExecHash: b.ExecHash,
			})
		}
		startHeight--
	}

	writeJSON(w, http.StatusOK, BlocksResponse{Blocks: blocks, Total: maxHeight})
}

// handleBlock handles GET /api/block/{height}.
func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	parts := strings.TrimPrefix(r.URL.Path, "/api/block/")
	height, err := strconv.ParseInt(parts, 10, 64)
	if err != nil || height <= 0 {
		writeError(w, http.StatusBadRequest, "invalid height")
		return
	}

	b, err := s.fetchBlock(height)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// fetchBlock fetches a block from CometBFT and enriches it with geth data.
func (s *Server) fetchBlock(height int64) (*BlockResponse, error) {
	var result struct {
		BlockID struct {
			Hash string `json:"hash"`
		} `json:"block_id"`
		Block struct {
			Header struct {
				Height          string `json:"height"`
				Time            string `json:"time"`
				ProposerAddress string `json:"proposer_address"`
				AppHash         string `json:"app_hash"`
			} `json:"header"`
			Data struct {
				Txs []string `json:"txs"`
			} `json:"data"`
		} `json:"block"`
	}
	if err := s.cometGet(fmt.Sprintf("/block?height=%d", height), &result); err != nil {
		return nil, err
	}

	txs := result.Block.Data.Txs
	if txs == nil {
		txs = []string{}
	}

	// Fetch EVM block from geth to get real tx hashes, gas, etc.
	var gethBlock struct {
		Hash         string   `json:"hash"`
		GasUsed      string   `json:"gasUsed"`
		GasLimit     string   `json:"gasLimit"`
		Transactions []string `json:"transactions"`
	}
	execHash := ""
	var evmTxs []string
	heightHex := fmt.Sprintf("0x%x", height)
	if err := s.gethRPC("eth_getBlockByNumber", []any{heightHex, false}, &gethBlock); err == nil && gethBlock.Hash != "" {
		execHash = gethBlock.Hash
		if gethBlock.Transactions != nil {
			evmTxs = gethBlock.Transactions
		}
	}
	if evmTxs == nil {
		evmTxs = []string{}
	}
	_ = txs // CometBFT txs contain the CHKV execution payload, not user txs

	return &BlockResponse{
		Height:   height,
		Hash:     result.BlockID.Hash,
		Time:     result.Block.Header.Time,
		Proposer: result.Block.Header.ProposerAddress,
		Txs:      evmTxs,
		ExecHash: execHash,
		GasUsed:  gethBlock.GasUsed,
		GasLimit: gethBlock.GasLimit,
	}, nil
}

// handleTx handles GET /api/tx/{hash}.
func (s *Server) handleTx(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	hash := strings.TrimPrefix(r.URL.Path, "/api/tx/")
	if hash == "" {
		writeError(w, http.StatusBadRequest, "missing tx hash")
		return
	}

	// Normalize to 0x-prefixed.
	if !strings.HasPrefix(hash, "0x") {
		hash = "0x" + hash
	}

	var txResult struct {
		BlockNumber      string `json:"blockNumber"`
		From             string `json:"from"`
		To               string `json:"to"`
		Value            string `json:"value"`
		GasPrice         string `json:"gasPrice"`
		Nonce            string `json:"nonce"`
		TransactionIndex string `json:"transactionIndex"`
	}
	if err := s.gethRPC("eth_getTransactionByHash", []any{hash}, &txResult); err != nil || txResult.BlockNumber == "" {
		writeError(w, http.StatusNotFound, "tx not found")
		return
	}

	var receipt struct {
		Status  string `json:"status"`
		GasUsed string `json:"gasUsed"`
	}
	_ = s.gethRPC("eth_getTransactionReceipt", []any{hash}, &receipt)

	blockNum, _ := hexToInt64(txResult.BlockNumber)
	nonce, _ := hexToUint64(txResult.Nonce)
	status := "success"
	if receipt.Status == "0x0" {
		status = "reverted"
	}

	writeJSON(w, http.StatusOK, TxResponse{
		Hash:        hash,
		BlockHeight: blockNum,
		Status:      status,
		From:        txResult.From,
		To:          txResult.To,
		Value:       hexToDecStr(txResult.Value),
		GasUsed:     hexToDecStr(receipt.GasUsed),
		GasPrice:    hexToDecStr(txResult.GasPrice),
		Nonce:       nonce,
	})
}

// handleAddress handles GET /api/address/{addr}.
func (s *Server) handleAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	addr := strings.TrimPrefix(r.URL.Path, "/api/address/")
	if addr == "" || len(addr) < 40 {
		writeError(w, http.StatusBadRequest, "invalid address")
		return
	}
	if !strings.HasPrefix(addr, "0x") {
		addr = "0x" + addr
	}

	var balance string
	if err := s.gethRPC("eth_getBalance", []any{addr, "latest"}, &balance); err != nil {
		writeError(w, http.StatusBadGateway, "geth unavailable")
		return
	}
	var nonceHex string
	_ = s.gethRPC("eth_getTransactionCount", []any{addr, "latest"}, &nonceHex)
	var code string
	_ = s.gethRPC("eth_getCode", []any{addr, "latest"}, &code)

	nonce, _ := hexToUint64(nonceHex)
	writeJSON(w, http.StatusOK, AddressResponse{
		Address: addr,
		Balance: hexToDecStr(balance),
		Nonce:   nonce,
		Code:    code,
	})
}

// handlePeers handles GET /api/peers.
func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var netInfo struct {
		Peers []struct {
			NodeInfo struct {
				ID      string `json:"id"`
				Moniker string `json:"moniker"`
			} `json:"node_info"`
			RemoteIP   string `json:"remote_ip"`
			IsOutbound bool   `json:"is_outbound"`
		} `json:"peers"`
	}
	if err := s.cometGet("/net_info", &netInfo); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	peers := make([]Peer, 0, len(netInfo.Peers))
	for _, p := range netInfo.Peers {
		peers = append(peers, Peer{
			ID:         p.NodeInfo.ID,
			RemoteIP:   p.RemoteIP,
			IsOutbound: p.IsOutbound,
			Moniker:    p.NodeInfo.Moniker,
		})
	}
	writeJSON(w, http.StatusOK, PeersResponse{Peers: peers})
}

// handleLogs handles GET /api/logs with Server-Sent Events streaming.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	if s.cfg.LogFile == "" {
		fmt.Fprintf(w, "data: {\"msg\":\"no log file configured\"}\n\n")
		flusher.Flush()
		return
	}

	// Send last 4KB of existing log as initial state.
	if tail, err := tailFile(s.cfg.LogFile, 4096); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(tail))
		for scanner.Scan() {
			fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
		}
		flusher.Flush()
	}

	// Tail the file by polling every 500ms.
	f, err := os.Open(s.cfg.LogFile)
	if err != nil {
		fmt.Fprintf(w, "data: {\"error\":\"cannot open log: %s\"}\n\n", err)
		flusher.Flush()
		return
	}
	defer f.Close()
	// Seek to end.
	_, _ = f.Seek(0, 2)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scanner := bufio.NewScanner(f)
			any := false
			for scanner.Scan() {
				line := scanner.Text()
				fmt.Fprintf(w, "data: %s\n\n", jsonEscape(line))
				any = true
			}
			if any {
				flusher.Flush()
			}
		}
	}
}

// handleFaucet handles POST /api/faucet (dev mode only).
// Signs a transfer with the Hardhat dev key and sends it via eth_sendRawTransaction.
// Rate limited to one request per IP per minute.
func (s *Server) handleFaucet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ip := clientIP(r)
	if !s.rl.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "rate limited: 1 request per minute per IP")
		return
	}

	var req FaucetRequest
	if err := jsonDecode(r.Body, &req); err != nil || req.Address == "" {
		writeError(w, http.StatusBadRequest, "body must be {\"address\":\"0x...\"}")
		return
	}
	if !strings.HasPrefix(req.Address, "0x") || len(req.Address) != 42 {
		writeError(w, http.StatusBadRequest, "invalid address")
		return
	}

	// Get the EVM chain ID.
	var chainIDHex string
	if err := s.gethRPC("eth_chainId", []any{}, &chainIDHex); err != nil {
		writeError(w, http.StatusInternalServerError, "could not get chain ID: "+err.Error())
		return
	}
	chainID, err := hexToUint64(chainIDHex)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bad chain ID: "+err.Error())
		return
	}

	faucetAddr := s.cfg.FaucetAddr
	faucetKey := s.cfg.FaucetPrivKey
	if faucetAddr == "" || faucetKey == "" {
		writeError(w, http.StatusServiceUnavailable, "faucet not configured")
		return
	}

	var nonceHex string
	if err := s.gethRPC("eth_getTransactionCount", []any{faucetAddr, "pending"}, &nonceHex); err != nil {
		writeError(w, http.StatusInternalServerError, "could not get nonce: "+err.Error())
		return
	}
	nonce, err := hexToUint64(nonceHex)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bad nonce: "+err.Error())
		return
	}

	// Get current gas price.
	var gasPriceHex string
	if err := s.gethRPC("eth_gasPrice", []any{}, &gasPriceHex); err != nil {
		writeError(w, http.StatusInternalServerError, "could not get gas price: "+err.Error())
		return
	}
	gasPrice := new(bigInt)
	gasPriceHex = strings.TrimPrefix(gasPriceHex, "0x")
	if _, ok := gasPrice.SetString(gasPriceHex, 16); !ok {
		writeError(w, http.StatusInternalServerError, "bad gas price")
		return
	}

	// 1 CHK = 1e18 wei
	value := new(bigInt).SetUint64(1_000_000_000_000_000_000)

	rawTx, err := buildSignedTx(faucetKey, chainID, nonce, gasPrice, req.Address, value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sign failed: "+err.Error())
		return
	}

	var txHash string
	if err := s.gethRPC("eth_sendRawTransaction", []any{rawTx}, &txHash); err != nil {
		writeError(w, http.StatusInternalServerError, "faucet send failed: "+err.Error())
		return
	}

	s.metrics.FaucetDrips.Inc()
	writeJSON(w, http.StatusOK, FaucetResponse{
		TxHash:  txHash,
		Amount:  "1000000000000000000",
		Message: "1 CHK sent",
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func hexToInt64(h string) (int64, error) {
	h = strings.TrimPrefix(h, "0x")
	if h == "" {
		return 0, fmt.Errorf("empty hex")
	}
	b, err := strconv.ParseInt(h, 16, 64)
	return b, err
}

func hexToUint64(h string) (uint64, error) {
	h = strings.TrimPrefix(h, "0x")
	if h == "" {
		return 0, fmt.Errorf("empty hex")
	}
	return strconv.ParseUint(h, 16, 64)
}

func hexToDecStr(h string) string {
	if h == "" || h == "0x" || h == "0x0" {
		return "0"
	}
	h = strings.TrimPrefix(h, "0x")
	b, err := hex.DecodeString(padEven(h))
	if err != nil {
		return "0"
	}
	// Convert big-endian bytes to decimal string.
	n := new(bigInt).SetBytes(b)
	return n.String()
}

func padEven(s string) string {
	if len(s)%2 != 0 {
		return "0" + s
	}
	return s
}

func jsonEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

// pubKeyValue extracts the base64 value from a CometBFT pub_key map,
// e.g. {"type":"tendermint/PubKeyEd25519","value":"JP84d9..."} → "JP84d9..."
func pubKeyValue(pk any) string {
	if m, ok := pk.(map[string]any); ok {
		if v, ok := m["value"].(string); ok {
			return v
		}
	}
	return fmt.Sprintf("%v", pk)
}

// handleGenesis serves GET /api/genesis — returns both genesis files so fullnodes can bootstrap.
func (s *Server) handleGenesis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.cfg.HomeDir == "" {
		writeError(w, http.StatusServiceUnavailable, "home dir not configured")
		return
	}

	cometPath := filepath.Join(s.cfg.HomeDir, "config", "genesis.json")
	gethPath := filepath.Join(s.cfg.HomeDir, "config", "geth-genesis.json")

	cometRaw, err := os.ReadFile(filepath.Clean(cometPath))
	if err != nil {
		writeError(w, http.StatusNotFound, "comet genesis not found")
		return
	}
	gethRaw, err := os.ReadFile(filepath.Clean(gethPath))
	if err != nil {
		writeError(w, http.StatusNotFound, "geth genesis not found")
		return
	}

	writeJSON(w, http.StatusOK, GenesisResponse{
		CometGenesis: json.RawMessage(cometRaw),
		GethGenesis:  json.RawMessage(gethRaw),
	})
}

// handleNodeInfo serves GET /api/node-info — returns peer/enode info for fullnode setup.
func (s *Server) handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Get CometBFT node ID.
	var status struct {
		NodeInfo struct {
			ID string `json:"id"`
		} `json:"node_info"`
	}
	if err := s.cometGet("/status", &status); err != nil {
		writeError(w, http.StatusBadGateway, "comet unavailable")
		return
	}

	// Get geth enode via admin_nodeInfo.
	var nodeInfo struct {
		Enode string `json:"enode"`
	}
	if err := s.gethRPC("admin_nodeInfo", []any{}, &nodeInfo); err != nil || nodeInfo.Enode == "" {
		writeError(w, http.StatusBadGateway, "geth admin api unavailable — ensure admin is in --http.api")
		return
	}

	host := s.p2pHost(r)

	// Extract enode pubkey and rebuild with the external host.
	enodePubkey := extractEnodePubkey(nodeInfo.Enode)
	gethEnode := fmt.Sprintf("enode://%s@%s:30303", enodePubkey, host)
	cometPeer := fmt.Sprintf("%s@%s:26656", status.NodeInfo.ID, host)

	scheme := "https"
	if r.TLS == nil && !strings.Contains(r.Host, "chakram.one") {
		scheme = "http"
	}
	genesisURL := fmt.Sprintf("%s://%s/api/genesis", scheme, r.Host)

	writeJSON(w, http.StatusOK, NodeInfoResponse{
		CometPeer:       cometPeer,
		GethEnode:       gethEnode,
		GenesisURL:      genesisURL,
		StakingContract: s.cfg.StakingContractAddr,
		ValidatorPubkey: s.validatorPubkeyB64(),
	})
}

// validatorPubkeyB64 reads the node's Ed25519 validator pubkey (base64) from
// priv_validator_key.json. Returns "" if the file is missing or unreadable.
func (s *Server) validatorPubkeyB64() string {
	keyPath := filepath.Join(s.cfg.HomeDir, "config", "priv_validator_key.json")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return ""
	}
	var key struct {
		PubKey struct {
			Value string `json:"value"`
		} `json:"pub_key"`
	}
	if json.Unmarshal(data, &key) != nil {
		return ""
	}
	return key.PubKey.Value
}

// p2pHost returns the configured P2P host, falling back to the request host.
func (s *Server) p2pHost(r *http.Request) string {
	if s.cfg.P2PHost != "" {
		return s.cfg.P2PHost
	}
	host := r.Host
	if h, _, err := strings.Cut(host, ":"); err {
		host = h
	}
	return host
}

var enodeRe = regexp.MustCompile(`^enode://([0-9a-fA-F]+)@`)

func extractEnodePubkey(enode string) string {
	if m := enodeRe.FindStringSubmatch(enode); len(m) == 2 {
		return m[1]
	}
	return enode
}
