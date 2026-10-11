// send.go — CHK transfer logic for the `chakram send` command.
// Talks to the running node via HTTP — never opens the blockchain directly.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"net/http"
	"strings"
)

// rpcUTXO is the JSON shape returned by GET /utxos/{address}.
type rpcUTXO struct {
	TxID        string  `json:"txid"`
	OutputIndex uint32  `json:"output_index"`
	Value       uint64  `json:"value"`
	ValueCHK    float64 `json:"value_chk"`
	BlockHeight uint64  `json:"block_height"`
	IsCoinbase  bool    `json:"is_coinbase"`
	Mature      bool    `json:"mature"`
}

// SendCHK builds, signs, and submits a CHK transfer via the local running node.
// Returns the hex TxID on success.
func SendCHK(cfg NodeConfig, toAddress string, amountCHK float64) (string, error) {
	// 1. Validate destination address.
	if !ValidateAddress(toAddress) {
		return "", fmt.Errorf("invalid address: %s", toAddress)
	}

	// 2. Convert to Cash units (round to nearest to avoid float64 truncation).
	amountCash := uint64(math.Round(amountCHK * float64(CashPerCHK)))
	if amountCash == 0 {
		return "", fmt.Errorf("amount must be greater than 0")
	}

	rpcPort := RPCPortMainnet
	if cfg.Testnet {
		rpcPort = RPCPortTestnet
	}
	base := fmt.Sprintf("http://localhost:%d", rpcPort)

	// 3. Load wallet (no blockchain — just the key file).
	password := cfg.Password
	if password == "" || password == "chakram" {
		return "", fmt.Errorf("FATAL: --password is required and must not be 'chakram'.\n  Example: chakram send <address> <amount> --password <secret>")
	}
	wallet, err := LoadWalletFromFile(cfg.WalletFile, password)
	if err != nil {
		return "", fmt.Errorf("load wallet: %w", err)
	}

	// 4. Fetch UTXOs from running node.
	utxos, err := fetchUTXOs(base, wallet.Address)
	if err != nil {
		return "", err
	}

	// 5. Coin selection — only mature UTXOs.
	fee := MinTxFee
	needed := amountCash + fee

	var selectedUTXOs []rpcUTXO
	var total uint64
	var matureBalance uint64

	for _, u := range utxos {
		if !u.Mature {
			continue
		}
		next, carry := bits.Add64(matureBalance, u.Value, 0)
		if carry != 0 {
			return "", fmt.Errorf("mature balance overflows uint64")
		}
		matureBalance = next
		if total < needed {
			selectedUTXOs = append(selectedUTXOs, u)
			selected, carry2 := bits.Add64(total, u.Value, 0)
			if carry2 != 0 {
				return "", fmt.Errorf("selected total overflows uint64")
			}
			total = selected
		}
	}

	if total < needed {
		return "", fmt.Errorf(
			"insufficient mature balance: have %.6f CHK, need %.6f CHK",
			float64(matureBalance)/float64(CashPerCHK),
			float64(needed)/float64(CashPerCHK),
		)
	}

	// 6. Build inputs.
	inputs := make([]TxInput, len(selectedUTXOs))
	for i, u := range selectedUTXOs {
		txid, err := hex.DecodeString(u.TxID)
		if err != nil {
			return "", fmt.Errorf("decode txid %s: %w", u.TxID, err)
		}
		inputs[i] = TxInput{TxID: txid, OutputIndex: u.OutputIndex}
	}

	// 7. Build outputs — payment + change.
	toPKH, err := AddressToPubKeyHash(toAddress)
	if err != nil {
		return "", fmt.Errorf("resolve to address: %w", err)
	}
	outputs := []TxOutput{
		{Value: amountCash, PublicKeyHash: toPKH},
	}
	if change := total - amountCash - fee; change > 0 {
		outputs = append(outputs, TxOutput{
			Value:         change,
			PublicKeyHash: wallet.GetPubKeyHash(),
		})
	}

	// 8. Create, sign, and validate transaction.
	tx := NewTransaction(inputs, outputs)
	chainID := NetworkChainID(cfg.Testnet)
	if err := SignTransaction(tx, wallet, chainID); err != nil {
		return "", fmt.Errorf("sign transaction: %w", err)
	}
	if err := tx.Validate(); err != nil {
		return "", fmt.Errorf("transaction invalid: %w", err)
	}

	// 9. Submit to running node.
	return submitTx(base, tx)
}

// fetchUTXOs calls GET /utxos/{address} on the local node.
func fetchUTXOs(base, address string) ([]rpcUTXO, error) {
	url := base + "/utxos/" + address
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, nodeOfflineErr(err)
	}
	defer resp.Body.Close()

	var utxos []rpcUTXO
	if err := json.NewDecoder(resp.Body).Decode(&utxos); err != nil {
		return nil, fmt.Errorf("decode UTXOs: %w", err)
	}
	return utxos, nil
}

// submitTx POSTs a transaction to POST /tx/submit on the local node.
func submitTx(base string, tx *Transaction) (string, error) {
	body, err := json.Marshal(tx)
	if err != nil {
		return "", fmt.Errorf("encode transaction: %w", err)
	}
	resp, err := http.Post(base+"/tx/submit", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return "", nodeOfflineErr(err)
	}
	defer resp.Body.Close()

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if errMsg, ok := result["error"]; ok {
		return "", fmt.Errorf("node rejected transaction: %s", errMsg)
	}
	return result["txid"], nil
}

// nodeOfflineErr converts a connection-refused error into a friendly message.
func nodeOfflineErr(err error) error {
	if strings.Contains(err.Error(), "connection refused") {
		return fmt.Errorf("node is not running. Start your node first with:\n  ./chakram node")
	}
	return err
}
