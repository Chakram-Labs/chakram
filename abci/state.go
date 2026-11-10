package abci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AppState is the ABCI application state persisted between restarts.
// It is written to app_state.json in the CometBFT home directory on Commit
// and read back in Info.
type AppState struct {
	LastHeight      int64  `json:"last_height"`
	LastExecHash    []byte `json:"last_exec_hash"`    // geth block hash (32 bytes)
	LastPayloadTime uint64 `json:"last_payload_time"` // unix seconds

	// Phase 3: staking — rebuilt from FinalizeBlock replay (D5), never manually edited.
	// Key: lowercase EVM address without 0x. Value: 32-byte Ed25519 pubkey.
	ValidatorPubKeys map[string][]byte `json:"validator_pub_keys,omitempty"`
	// CometBFT address (uppercase hex, no 0x) → EVM reward address (0x-prefixed lowercase).
	// Used by PrepareProposal to route block rewards to the correct validator wallet.
	ValidatorRewardAddrs map[string]string `json:"validator_reward_addrs,omitempty"`

	// LockerStartHeight is the first block height at which dual withdrawals
	// (20 % validator + 80 % ChakramLocker) are used. Zero means the locker
	// is not yet configured. Set once when ChakramLockerAddr is first seen on
	// startup; never changed after that.
	LockerStartHeight int64 `json:"locker_start_height,omitempty"`
}

// stateFile returns the canonical path for the app state file.
func stateFile(home string) string {
	return filepath.Join(home, "app_state.json")
}

func loadState(home string) (*AppState, error) {
	data, err := os.ReadFile(stateFile(home))
	if os.IsNotExist(err) {
		return &AppState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loadState: read: %w", err)
	}
	var s AppState
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupted file from a crash mid-write. Back it up and return empty
		// state so CometBFT triggers a full FinalizeBlock replay from block 1.
		corrupt := stateFile(home) + ".corrupt"
		_ = os.Rename(stateFile(home), corrupt)
		return &AppState{}, nil
	}
	return &s, nil
}

// saveState writes app state atomically via a temp-file + rename so that a
// crash mid-write never leaves a partially-written file.
func saveState(home string, s *AppState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("saveState: marshal: %w", err)
	}
	tmp := stateFile(home) + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("saveState: write tmp: %w", err)
	}
	if err := os.Rename(tmp, stateFile(home)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("saveState: rename: %w", err)
	}
	return nil
}
