package abci

import abcitypes "github.com/cometbft/cometbft/abci/types"

// Application is an alias for the CometBFT ABCI Application interface.
// Used by cmd/chakram to allow wrapping with a faulty proposer for testing.
type Application = abcitypes.Application
