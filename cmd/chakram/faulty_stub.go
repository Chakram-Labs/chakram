//go:build !faulty

package main

import chakramabci "chakram/abci"

// maybeFaultyApp returns the app unchanged in normal builds.
// The faulty build tag enables a Byzantine proposer for testing only.
func maybeFaultyApp(app *chakramabci.ChakramApp, rate float64) chakramabci.Application {
	return app
}
