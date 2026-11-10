package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Handler struct {
	db         *Database
	geth       *GethClient
	comet      *CometClient
	nodeAPIURL string
	solcPath   string
	adminKey   string
	log        *slog.Logger
}

func (h *Handler) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/stats", h.stats)
	mux.HandleFunc("GET /api/v1/indexer/status", h.indexerStatus)
	mux.HandleFunc("GET /api/v1/search", h.search)

	mux.HandleFunc("GET /api/v1/blocks", h.listBlocks)
	mux.HandleFunc("GET /api/v1/blocks/latest", h.latestBlock)
	mux.HandleFunc("GET /api/v1/blocks/{height}", h.getBlock)
	mux.HandleFunc("GET /api/v1/blocks/{height}/txs", h.blockTxs)

	mux.HandleFunc("GET /api/v1/txs", h.listTxs)
	mux.HandleFunc("GET /api/v1/txs/{hash}", h.getTx)
	mux.HandleFunc("GET /api/v1/txs/{hash}/logs", h.txLogs)

	mux.HandleFunc("GET /api/v1/addresses/{address}", h.getAddress)
	mux.HandleFunc("GET /api/v1/addresses/{address}/txs", h.addressTxs)
	mux.HandleFunc("GET /api/v1/addresses/{address}/token-transfers", h.addressTokenTransfers)
	mux.HandleFunc("GET /api/v1/addresses/{address}/tokens", h.addressTokens)

	mux.HandleFunc("GET /api/v1/tokens", h.listTokens)
	mux.HandleFunc("GET /api/v1/tokens/{address}", h.getToken)
	mux.HandleFunc("GET /api/v1/tokens/{address}/transfers", h.tokenTransfers)
	mux.HandleFunc("GET /api/v1/tokens/{address}/holders", h.tokenHolders)

	mux.HandleFunc("GET /api/v1/contracts/{address}", h.getContract)
	mux.HandleFunc("POST /api/v1/contracts/{address}/verify", h.verifyHandler)
	mux.HandleFunc("POST /api/v1/contracts/{address}/force-verify", h.forceVerifyHandler)
	mux.HandleFunc("GET /api/v1/contracts/{address}/abi", h.contractABI)

	mux.HandleFunc("GET /api/v1/validators", h.validators)

	return mux
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func parsePage(r *http.Request) (page, limit, offset int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 25
	}
	offset = (page - 1) * limit
	return
}

// ── Stats ─────────────────────────────────────────────────────────────────────

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	s := h.db.GetStats()
	jsonOK(w, s)
}

func (h *Handler) indexerStatus(w http.ResponseWriter, r *http.Request) {
	indexed := h.db.GetLastIndexedBlock()
	head, _ := h.geth.BlockNumber()
	jsonOK(w, map[string]any{
		"last_indexed_block": indexed,
		"chain_head":         head,
		"blocks_behind":      max64(0, head-indexed),
		"is_synced":          indexed >= head,
	})
}

// ── Blocks ────────────────────────────────────────────────────────────────────

func (h *Handler) listBlocks(w http.ResponseWriter, r *http.Request) {
	_, limit, offset := parsePage(r)
	blocks, total, err := h.db.ListBlocks(limit, offset)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"blocks": orEmpty(blocks), "total": total, "limit": limit})
}

func (h *Handler) latestBlock(w http.ResponseWriter, r *http.Request) {
	b, err := h.db.GetLatestBlock()
	if err != nil || b == nil {
		jsonErr(w, 404, "no blocks indexed")
		return
	}
	jsonOK(w, b)
}

func (h *Handler) getBlock(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("height")
	var b *DBBlock
	var err error

	// Support both height (integer) and hash (0x...)
	if strings.HasPrefix(raw, "0x") {
		b, err = h.db.GetBlockByHash(strings.ToLower(raw))
	} else {
		height, _ := strconv.ParseInt(raw, 10, 64)
		b, err = h.db.GetBlock(height)
	}
	if err != nil || b == nil {
		jsonErr(w, 404, "block not found")
		return
	}
	jsonOK(w, b)
}

func (h *Handler) blockTxs(w http.ResponseWriter, r *http.Request) {
	height, _ := strconv.ParseInt(r.PathValue("height"), 10, 64)
	txs, err := h.db.ListBlockTxs(height)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"txs": orEmpty(txs), "total": len(txs)})
}

// ── Transactions ──────────────────────────────────────────────────────────────

func (h *Handler) listTxs(w http.ResponseWriter, r *http.Request) {
	_, limit, offset := parsePage(r)
	txs, total, err := h.db.ListTxs(limit, offset)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"txs": orEmpty(txs), "total": total, "limit": limit})
}

func (h *Handler) getTx(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	tx, err := h.db.GetTx(hash)
	if err != nil || tx == nil {
		jsonErr(w, 404, "transaction not found")
		return
	}
	logs, _ := h.db.GetTxLogs(hash)
	jsonOK(w, map[string]any{
		"tx":   tx,
		"logs": orEmpty(logs),
	})
}

func (h *Handler) txLogs(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	logs, err := h.db.GetTxLogs(hash)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"logs": orEmpty(logs)})
}

// ── Addresses ─────────────────────────────────────────────────────────────────

func (h *Handler) getAddress(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))

	balance, _ := h.geth.GetBalance(addr)
	txCount, _ := h.geth.GetTxCount(addr)
	code, _ := h.geth.GetCode(addr)
	isContract := code != "" && code != "0x"

	resp := map[string]any{
		"address":     addr,
		"balance":     balance,
		"tx_count":    txCount,
		"is_contract": isContract,
	}

	if isContract {
		if c, _ := h.db.GetContract(addr); c != nil {
			resp["contract"] = map[string]any{
				"is_verified":      c.IsVerified,
				"name":             c.Name,
				"compiler_version": c.CompilerVersion,
				"creator":          c.Creator,
				"creation_tx":      c.CreationTx,
				"creation_block":   c.CreationBlock,
			}
		}
		if t, _ := h.db.GetToken(addr); t != nil {
			resp["token"] = t
		}
	}

	jsonOK(w, resp)
}

func (h *Handler) addressTxs(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))
	_, limit, offset := parsePage(r)
	txs, total, err := h.db.ListAddressTxs(addr, limit, offset)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"txs": orEmpty(txs), "total": total, "limit": limit})
}

func (h *Handler) addressTokenTransfers(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))
	_, limit, offset := parsePage(r)
	tts, total, err := h.db.ListAddressTokenTransfers(addr, limit, offset)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"transfers": orEmpty(tts), "total": total, "limit": limit})
}

func (h *Handler) addressTokens(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))
	tokens, err := h.db.ListAddressTokens(addr)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"tokens": orEmpty(tokens)})
}

// ── Tokens ────────────────────────────────────────────────────────────────────

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	_, limit, offset := parsePage(r)
	tokens, total, err := h.db.ListTokens(limit, offset)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"tokens": orEmpty(tokens), "total": total, "limit": limit})
}

func (h *Handler) getToken(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))
	t, err := h.db.GetToken(addr)
	if err != nil || t == nil {
		jsonErr(w, 404, "token not found")
		return
	}
	jsonOK(w, t)
}

func (h *Handler) tokenTransfers(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))
	_, limit, offset := parsePage(r)
	tts, total, err := h.db.ListTokenTransfers(addr, limit, offset)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"transfers": orEmpty(tts), "total": total, "limit": limit})
}

func (h *Handler) tokenHolders(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))
	_, limit, offset := parsePage(r)
	holders, total, err := h.db.ListTokenHolders(addr, limit, offset)
	if err != nil {
		jsonErr(w, 500, "db error")
		return
	}
	jsonOK(w, map[string]any{"holders": orEmpty(holders), "total": total, "limit": limit})
}

// ── Contracts ─────────────────────────────────────────────────────────────────

func (h *Handler) getContract(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))
	c, err := h.db.GetContract(addr)
	if err != nil || c == nil {
		// Check if it's a contract at all
		code, _ := h.geth.GetCode(addr)
		if code == "" || code == "0x" {
			jsonErr(w, 404, "not a contract")
			return
		}
		jsonOK(w, map[string]any{
			"address":     addr,
			"is_verified": false,
			"bytecode":    code,
		})
		return
	}
	jsonOK(w, c)
}

func (h *Handler) contractABI(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))
	c, _ := h.db.GetContract(addr)
	if c == nil || !c.IsVerified || c.ABI == "" {
		jsonErr(w, 404, "contract not verified or ABI unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(c.ABI))
}

func (h *Handler) verifyHandler(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(r.PathValue("address"))

	var req VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "invalid request body")
		return
	}

	result, err := verify(h.db, h.geth, h.solcPath, addr, req)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	jsonOK(w, result)
}

// forceVerifyHandler bypasses bytecode comparison and marks the contract as verified directly.
// Requires the X-Admin-Key header to match the --admin-key flag. Used when the deployed
// bytecode cannot be reproduced exactly (e.g. compilation environment differences).
func (h *Handler) forceVerifyHandler(w http.ResponseWriter, r *http.Request) {
	if h.adminKey == "" || r.Header.Get("X-Admin-Key") != h.adminKey {
		jsonErr(w, 403, "forbidden")
		return
	}

	addr := strings.ToLower(r.PathValue("address"))

	var req VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "invalid request body")
		return
	}
	if len(req.SourceFiles) == 0 || req.CompilerVersion == "" {
		jsonErr(w, 400, "source_files and compiler_version are required")
		return
	}
	if req.OptimizerRuns == 0 {
		req.OptimizerRuns = 200
	}

	// Compile to extract the ABI — we still need a valid compile, just skip bytecode check
	import_json, _ := json.Marshal(map[string]any{
		"language": "Solidity",
		"sources": func() map[string]map[string]string {
			m := make(map[string]map[string]string, len(req.SourceFiles))
			for name, content := range req.SourceFiles {
				m[name] = map[string]string{"content": content}
			}
			return m
		}(),
		"settings": map[string]any{
			"optimizer":       map[string]any{"enabled": req.OptimizerEnabled, "runs": req.OptimizerRuns},
			"outputSelection": map[string]any{"*": map[string]any{"*": []string{"abi"}}},
		},
	})

	import_cmd := exec.Command(h.solcPath, "--standard-json") //nolint
	import_cmd.Stdin = bytes.NewReader(import_json)
	out, err := import_cmd.Output()
	if err != nil {
		jsonErr(w, 400, "compilation failed: "+err.Error())
		return
	}

	var solcOut struct {
		Errors    []struct{ Severity, Message string } `json:"errors"`
		Contracts map[string]map[string]struct {
			ABI json.RawMessage `json:"abi"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(out, &solcOut); err != nil {
		jsonErr(w, 400, "parse solc output: "+err.Error())
		return
	}
	for _, e := range solcOut.Errors {
		if e.Severity == "error" {
			jsonErr(w, 400, "compilation error: "+e.Message)
			return
		}
	}

	// Find the ABI for the requested contract
	var abiStr string
	for _, contracts := range solcOut.Contracts {
		for name, c := range contracts {
			if req.ContractName == "" || name == req.ContractName {
				b, _ := json.Marshal(c.ABI)
				abiStr = string(b)
				if req.ContractName == "" {
					req.ContractName = name
				}
				break
			}
		}
	}
	if abiStr == "" {
		jsonErr(w, 400, "contract not found in compilation output")
		return
	}

	sourceJSON, _ := json.Marshal(req.SourceFiles)
	if err := h.db.UpdateVerification(addr, req.ContractName, string(sourceJSON), abiStr,
		req.CompilerVersion, req.ConstructorArgs, req.License,
		req.OptimizerEnabled, req.OptimizerRuns, time.Now().Unix()); err != nil {
		jsonErr(w, 500, "db error: "+err.Error())
		return
	}

	jsonOK(w, map[string]any{
		"success":          true,
		"message":          "Contract force-verified successfully",
		"contract_name":    req.ContractName,
		"compiler_version": req.CompilerVersion,
	})
}

// ── Validators ────────────────────────────────────────────────────────────────

// stakingProfile holds the staking contract profile for one validator.
type stakingProfile struct {
	CometAddr  string `json:"comet_addr"`
	EVMAddr    string `json:"evm_addr"`
	RewardAddr string `json:"reward_addr"`
	Stake      string `json:"stake"`
	Moniker    string `json:"moniker"`
	Website    string `json:"website"`
	Active     bool   `json:"active"`
	Jailed     bool   `json:"jailed"`
}

func (h *Handler) fetchStakingProfiles() map[string]stakingProfile {
	profiles := map[string]stakingProfile{}
	if h.nodeAPIURL == "" {
		return profiles
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(h.nodeAPIURL + "/api/staking")
	if err != nil {
		return profiles
	}
	defer resp.Body.Close()
	var result struct {
		Validators []stakingProfile `json:"validators"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) != nil {
		return profiles
	}
	for _, p := range result.Validators {
		if p.CometAddr != "" {
			profiles[strings.ToUpper(p.CometAddr)] = p
		}
	}
	return profiles
}

func (h *Handler) validators(w http.ResponseWriter, r *http.Request) {
	vals, height, err := h.comet.GetValidators()
	if err != nil {
		jsonErr(w, 502, "comet unavailable")
		return
	}

	profiles := h.fetchStakingProfiles()

	type enrichedValidator struct {
		CometValidator
		EVMAddr    string `json:"evm_addr,omitempty"`
		RewardAddr string `json:"reward_addr,omitempty"`
		Stake      string `json:"stake,omitempty"`
		Moniker    string `json:"moniker,omitempty"`
		Website    string `json:"website,omitempty"`
		Active     bool   `json:"active"`
		Jailed     bool   `json:"jailed"`
	}

	enriched := make([]enrichedValidator, len(vals))
	for i, v := range vals {
		ev := enrichedValidator{CometValidator: v}
		if p, ok := profiles[strings.ToUpper(v.Address)]; ok {
			ev.EVMAddr = p.EVMAddr
			ev.RewardAddr = p.RewardAddr
			ev.Stake = p.Stake
			ev.Moniker = p.Moniker
			ev.Website = p.Website
			ev.Active = p.Active
			ev.Jailed = p.Jailed
		}
		enriched[i] = ev
	}

	jsonOK(w, map[string]any{
		"height":     height,
		"validators": enriched,
	})
}

// ── Search ────────────────────────────────────────────────────────────────────

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		jsonErr(w, 400, "q is required")
		return
	}

	// Block height: pure integer
	if height, err := strconv.ParseInt(q, 10, 64); err == nil && height > 0 {
		if h.db.BlockExists(height) {
			jsonOK(w, map[string]any{"type": "block", "value": height})
			return
		}
	}

	norm := strings.ToLower(q)

	// Transaction hash: 0x + 64 hex chars
	if strings.HasPrefix(norm, "0x") && len(norm) == 66 {
		if h.db.TxExists(norm) {
			jsonOK(w, map[string]any{"type": "tx", "value": norm})
			return
		}
		// Also check as block hash
		if b, _ := h.db.GetBlockByHash(norm); b != nil {
			jsonOK(w, map[string]any{"type": "block", "value": b.Height})
			return
		}
	}

	// Address: 0x + 40 hex chars
	if strings.HasPrefix(norm, "0x") && len(norm) == 42 {
		code, _ := h.geth.GetCode(norm)
		addrType := "address"
		if code != "" && code != "0x" {
			addrType = "contract"
			if h.db.TokenExists(norm) {
				addrType = "token"
			}
		}
		jsonOK(w, map[string]any{"type": addrType, "value": norm})
		return
	}

	jsonOK(w, map[string]any{"type": "not_found", "value": q})
}

// ── Utilities ─────────────────────────────────────────────────────────────────

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
