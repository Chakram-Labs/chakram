package process

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type pidEntry struct {
	PID int    `json:"pid"`
	Bin string `json:"bin"`
}

type pidRecord struct {
	Geth    pidEntry `json:"geth"`
	Chakram pidEntry `json:"chakram"`
}

func pidFilePath(dataDir string) string {
	return filepath.Join(dataDir, "pids.json")
}

func loadPidRecord(dataDir string) pidRecord {
	data, err := os.ReadFile(pidFilePath(dataDir))
	if err != nil {
		return pidRecord{}
	}
	var r pidRecord
	json.Unmarshal(data, &r) //nolint:errcheck
	return r
}

func savePidRecord(dataDir string, r pidRecord) {
	data, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(pidFilePath(dataDir), data, 0644) //nolint:errcheck
}

func clearPidRecord(dataDir string) {
	os.Remove(pidFilePath(dataDir)) //nolint:errcheck
}
