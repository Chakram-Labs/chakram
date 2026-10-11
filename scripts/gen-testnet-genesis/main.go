// gen-testnet-genesis prints the geth-compatible genesis JSON for the Chakram
// testnet to stdout.  Commit the output to config/genesis.testnet.json.
//
// Usage:
//
//	go run ./scripts/gen-testnet-genesis > config/genesis.testnet.json
package main

import (
	"fmt"
	"os"

	"chakram/consensus"
)

func main() {
	b, err := consensus.TestnetGenesisJSON(consensus.TestnetFaucetAddr, consensus.TestnetFaucetWei)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s\n", b)
}
