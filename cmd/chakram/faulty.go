//go:build faulty

package main

import (
	"context"
	"math/rand"

	abcitypes "github.com/cometbft/cometbft/abci/types"

	chakramabci "chakram/abci"
)

// maybeFaultyApp wraps app with a faulty proposer when rate > 0.
// NEVER included in release builds (requires -tags faulty).
func maybeFaultyApp(app *chakramabci.ChakramApp, rate float64) chakramabci.Application {
	if rate <= 0 {
		return app
	}
	return &faultyApp{ChakramApp: app, rate: rate}
}

type faultyApp struct {
	*chakramabci.ChakramApp
	rate float64
}

// PrepareProposal occasionally returns a corrupted envelope to simulate a Byzantine proposer.
func (f *faultyApp) PrepareProposal(ctx context.Context, req *abcitypes.RequestPrepareProposal) (*abcitypes.ResponsePrepareProposal, error) {
	resp, err := f.ChakramApp.PrepareProposal(ctx, req)
	if err != nil || len(resp.Txs) == 0 {
		return resp, err
	}
	if rand.Float64() < f.rate {
		corrupted := make([]byte, len(resp.Txs[0]))
		copy(corrupted, resp.Txs[0])
		// Corrupt bytes in the JSON body (after 5-byte header) to produce an invalid envelope.
		for i := 10; i < 30 && i < len(corrupted); i++ {
			corrupted[i] ^= 0xFF
		}
		return &abcitypes.ResponsePrepareProposal{Txs: [][]byte{corrupted}}, nil
	}
	return resp, err
}
