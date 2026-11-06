package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"
)

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	projectDir := projectDirFlag(fs)
	apiAddr := fs.String("api", "http://127.0.0.1:8080", "Node API address")
	_ = fs.Parse(args)

	_ = projectDir // may be used for compose ps in future

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(*apiAddr + "/api/status")
	if err != nil {
		// Fall back to docker compose ps.
		fmt.Println("Node API not reachable. Showing container status:")
		_ = composeRun(*projectDir, "ps")
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var status struct {
		NodeID       string `json:"node_id"`
		Moniker      string `json:"moniker"`
		ChainID      string `json:"chain_id"`
		Height       int64  `json:"height"`
		CatchingUp   bool   `json:"catching_up"`
		Peers        int    `json:"peers"`
		CometVersion string `json:"comet_version"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		fmt.Printf("Raw response: %s\n", body)
		return
	}

	syncStr := "synced"
	if status.CatchingUp {
		syncStr = "catching up"
	}

	fmt.Printf("Node:     %s (%s)\n", status.Moniker, status.NodeID[:min(8, len(status.NodeID))])
	fmt.Printf("Chain:    %s\n", status.ChainID)
	fmt.Printf("Height:   %d\n", status.Height)
	fmt.Printf("Status:   %s\n", syncStr)
	fmt.Printf("Peers:    %d\n", status.Peers)
	fmt.Printf("Comet:    v%s\n", status.CometVersion)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
