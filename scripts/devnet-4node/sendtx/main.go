// sendtx is a devnet-only signing tool. Never use outside of test environments.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const privKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func main() {
	rpcURL := flag.String("rpc", "http://127.0.0.1:8545", "geth RPC URL")
	toAddr := flag.String("to", "0x1111111111111111111111111111111111111111", "recipient")
	count := flag.Int("count", 1, "number of txs to send")
	chainID := flag.Int64("chain-id", 11642, "EVM chain ID")
	flag.Parse()

	privKey := must(crypto.HexToECDSA(privKeyHex))
	from := crypto.PubkeyToAddress(privKey.PublicKey)

	nonce := must(getNonce(*rpcURL, from.Hex()))

	signer := types.NewCancunSigner(big.NewInt(*chainID))
	to := common.HexToAddress(*toAddr)

	for i := 0; i < *count; i++ {
		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   big.NewInt(*chainID),
			Nonce:     uint64(nonce + i),
			GasTipCap: big.NewInt(1e9),
			GasFeeCap: big.NewInt(2e9),
			Gas:       21000,
			To:        &to,
			Value:     big.NewInt(1e15), // 0.001 ETH
		})
		signed := must(types.SignTx(tx, signer, privKey))
		raw := must(signed.MarshalBinary())
		txHash, err := sendRaw(*rpcURL, fmt.Sprintf("%x", raw))
		if err != nil {
			fmt.Fprintf(os.Stderr, "send[%d]: %v\n", i, err)
		} else {
			fmt.Println(txHash)
		}
	}
}

// Suppress unused import of ecdsa
var _ *ecdsa.PrivateKey

func getNonce(rpc, addr string) (int, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getTransactionCount","params":[%q,"pending"],"id":1}`, addr)
	var resp struct {
		Result string `json:"result"`
	}
	if err := doRPC(rpc, body, &resp); err != nil {
		return 0, err
	}
	n := new(big.Int)
	n.SetString(resp.Result[2:], 16)
	return int(n.Int64()), nil
}

func sendRaw(rpc, rawHex string) (string, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":[%q],"id":1}`, "0x"+rawHex)
	var resp struct {
		Result string                 `json:"result"`
		Error  map[string]interface{} `json:"error"`
	}
	if err := doRPC(rpc, body, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%v", resp.Error)
	}
	return resp.Result, nil
}

func doRPC(rpc, body string, out interface{}) error {
	r, err := http.Post(rpc, "application/json", bytes.NewBufferString(body))
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(out)
}
