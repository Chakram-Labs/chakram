package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	gethVersion   = "1.17.6"
	gethBlobStore = "https://gethstore.blob.core.windows.net/builds"
)

// bootstrap ensures all required files are present in dataDir.
// It's idempotent — safe to call on every startup.
// apiURLs is tried in order for genesis fetch; first success wins.
func bootstrap(dataDir string, apiURLs []string) error {
	dirs := []string{
		filepath.Join(dataDir, "geth-data"),
		filepath.Join(dataDir, "comet"),
		filepath.Join(dataDir, "comet", "config"),
		filepath.Join(dataDir, "comet", "data"),
		filepath.Join(dataDir, "bin"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	if err := ensureGeth(filepath.Join(dataDir, "bin")); err != nil {
		return fmt.Errorf("geth download: %w", err)
	}

	genesisChanged, err := ensureGenesis(dataDir, apiURLs)
	if err != nil {
		return fmt.Errorf("genesis: %w", err)
	}

	if err := ensureJWT(filepath.Join(dataDir, "jwt.hex")); err != nil {
		return fmt.Errorf("jwt: %w", err)
	}

	if err := ensureGethInit(dataDir, genesisChanged); err != nil {
		return fmt.Errorf("geth init: %w", err)
	}

	return nil
}

// bootstrapGenesisNode sets up a fresh chain without fetching genesis from the
// network. It creates the directory structure, downloads geth, generates a JWT,
// writes a fresh geth genesis (funding the owner address), and runs geth init.
// ensureChakramKeys (called later in startConsensus) will create the CometBFT
// genesis with the local validator key.
func bootstrapGenesisNode(dataDir, ownerAddr string) error {
	dirs := []string{
		filepath.Join(dataDir, "geth-data"),
		filepath.Join(dataDir, "comet"),
		filepath.Join(dataDir, "comet", "config"),
		filepath.Join(dataDir, "comet", "data"),
		filepath.Join(dataDir, "bin"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	if err := ensureGeth(filepath.Join(dataDir, "bin")); err != nil {
		return fmt.Errorf("geth download: %w", err)
	}

	if err := ensureJWT(filepath.Join(dataDir, "jwt.hex")); err != nil {
		return fmt.Errorf("jwt: %w", err)
	}

	gethGenesisPath := filepath.Join(dataDir, "comet", "config", "geth-genesis.json")
	if !fileExists(gethGenesisPath) {
		data, err := json.MarshalIndent(buildGenesisNodeGethGenesis(ownerAddr), "", "  ")
		if err != nil {
			return fmt.Errorf("marshal geth genesis: %w", err)
		}
		if err := os.WriteFile(gethGenesisPath, data, 0644); err != nil {
			return fmt.Errorf("write geth genesis: %w", err)
		}
		fmt.Printf("  Created geth genesis (chainId 1164)\n")
	}

	if err := ensureGethInit(dataDir, false); err != nil {
		return fmt.Errorf("geth init: %w", err)
	}

	fmt.Printf("  Genesis node bootstrap complete.\n")
	return nil
}

// buildGenesisNodeGethGenesis returns the geth genesis for a fresh Chakram chain.
// ownerAddr receives 100 CHK at genesis — bootstrap gas for deploying StakingRegistry.
// All remaining CHK supply is emitted purely as block rewards.
func buildGenesisNodeGethGenesis(ownerAddr string) map[string]any {
	if ownerAddr == "" {
		ownerAddr = "0xdeF22dBC999aD395527205C90d7922357378CAe0"
	}
	return map[string]any{
		"config": map[string]any{
			"chainId":                       1164,
			"homesteadBlock":                0,
			"eip150Block":                   0,
			"eip155Block":                   0,
			"eip158Block":                   0,
			"byzantiumBlock":                0,
			"constantinopleBlock":           0,
			"petersburgBlock":               0,
			"istanbulBlock":                 0,
			"berlinBlock":                   0,
			"londonBlock":                   0,
			"mergeNetsplitBlock":            0,
			"shanghaiTime":                  0,
			"cancunTime":                    0,
			"terminalTotalDifficulty":       0,
			"terminalTotalDifficultyPassed": true,
			"blobSchedule": map[string]any{
				"cancun": map[string]any{
					"baseFeeUpdateFraction": 3338477,
					"max":                  6,
					"target":               3,
				},
			},
		},
		"alloc": map[string]any{
			ownerAddr: map[string]any{
				"balance": "100000000000000000000", // 100 CHK — bootstrap gas only
			},
		},
		"difficulty": "0x0",
		"extraData":  "0x",
		"gasLimit":   "0x1C9C380",
		"mixHash":    "0x0000000000000000000000000000000000000000000000000000000000000000",
		"nonce":      "0x0",
		"timestamp":  "0x0",
	}
}

// ── geth binary ────────────────────────────────────────────────────────────

func gethBinPath(binDir string) string {
	name := "geth"
	if runtime.GOOS == "windows" {
		name = "geth.exe"
	}
	return filepath.Join(binDir, name)
}

func ensureGeth(binDir string) error {
	bin := gethBinPath(binDir)
	if _, err := os.Stat(bin); err == nil {
		return nil // already present
	}

	fmt.Printf("  Downloading geth v%s for %s/%s...\n", gethVersion, runtime.GOOS, runtime.GOARCH)

	assetURL, archiveName, err := resolveGethAsset()
	if err != nil {
		// On macOS, geth pre-builts stop at v1.15.x — fall back to system geth if available.
		if runtime.GOOS == "darwin" {
			if sysGeth, lerr := exec.LookPath("geth"); lerr == nil {
				fmt.Printf("  No pre-built geth v%s for darwin — using system geth at %s\n", gethVersion, sysGeth)
				return copyExecutable(sysGeth, bin)
			}
			return fmt.Errorf("%w\n\n  Install geth on macOS with: brew install ethereum", err)
		}
		return err
	}

	archivePath := filepath.Join(binDir, archiveName)
	if err := downloadFile(assetURL, archivePath); err != nil {
		return fmt.Errorf("download %s: %w", assetURL, err)
	}
	defer os.Remove(archivePath)

	if err := extractGeth(archivePath, bin); err != nil {
		return fmt.Errorf("extract geth: %w", err)
	}
	if err := os.Chmod(bin, 0755); err != nil {
		return err
	}
	fmt.Printf("  geth ready at %s\n", bin)
	return nil
}

// resolveGethAsset queries the gethstore blob CDN to find the download URL
// for the geth binary matching the current OS/arch and gethVersion.
func resolveGethAsset() (dlURL, name string, err error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	// geth doesn't publish Windows arm64 builds; treat as amd64.
	if goos == "windows" && goarch == "arm64" {
		goarch = "amd64"
	}

	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}

	prefix := fmt.Sprintf("geth-%s-%s-%s-", goos, goarch, gethVersion)
	listURL := fmt.Sprintf("%s?restype=container&comp=list&prefix=%s", gethBlobStore, prefix)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(listURL)
	if err != nil {
		return "", "", fmt.Errorf("blob store listing: %w", err)
	}
	defer resp.Body.Close()

	var listing struct {
		Blobs []struct {
			Name string `xml:"Name"`
		} `xml:"Blobs>Blob"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return "", "", fmt.Errorf("parse blob listing: %w", err)
	}

	for _, blob := range listing.Blobs {
		n := blob.Name
		if strings.HasSuffix(n, ext) && !strings.Contains(n, "unstable") && !strings.Contains(n, "alltools") {
			return fmt.Sprintf("%s/%s", gethBlobStore, n), n, nil
		}
	}
	return "", "", fmt.Errorf("geth v%s not available for %s/%s on the blob CDN", gethVersion, goos, goarch)
}

// copyExecutable copies src to dst and marks dst executable.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, 0755)
}

func extractGeth(archivePath, destBin string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractGethZip(archivePath, destBin)
	}
	return extractGethTarGz(archivePath, destBin)
}

func extractGethTarGz(archivePath, destBin string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Find the geth binary — it lives inside a subdir: geth-linux-amd64-X.Y.Z-hash/geth
		base := filepath.Base(hdr.Name)
		if base == "geth" && hdr.Typeflag == tar.TypeReg {
			out, err := os.Create(destBin)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			return err
		}
	}
	return fmt.Errorf("geth binary not found in archive")
}

func extractGethZip(archivePath, destBin string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if base == "geth.exe" {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			out, err := os.Create(destBin)
			if err != nil {
				rc.Close()
				return err
			}
			_, err = io.Copy(out, rc)
			rc.Close()
			out.Close()
			return err
		}
	}
	return fmt.Errorf("geth.exe not found in zip archive")
}

// ── genesis files ──────────────────────────────────────────────────────────

// ensureGenesis fetches genesis files from the first responsive API URL.
func ensureGenesis(dataDir string, apiURLs []string) (changed bool, err error) {
	cometGenesis := filepath.Join(dataDir, "comet", "config", "genesis.json")
	gethGenesis := filepath.Join(dataDir, "comet", "config", "geth-genesis.json")

	if fileExists(cometGenesis) && fileExists(gethGenesis) {
		return false, nil
	}

	type genesisResp struct {
		CometGenesis json.RawMessage `json:"comet_genesis"`
		GethGenesis  json.RawMessage `json:"geth_genesis"`
	}

	var lastErr error
	for _, apiURL := range apiURLs {
		fmt.Printf("  Fetching genesis from %s ...\n", apiURL)
		resp, err := httpGetJSON(apiURL + "/api/genesis")
		if err != nil {
			lastErr = err
			continue
		}
		var gr genesisResp
		body, _ := json.Marshal(resp)
		if err := json.Unmarshal(body, &gr); err != nil {
			lastErr = err
			continue
		}
		if len(gr.CometGenesis) == 0 || len(gr.GethGenesis) == 0 {
			lastErr = fmt.Errorf("incomplete genesis from %s", apiURL)
			continue
		}

		cometPretty, _ := json.MarshalIndent(json.RawMessage(gr.CometGenesis), "", "  ")
		gethPretty, _ := json.MarshalIndent(json.RawMessage(gr.GethGenesis), "", "  ")

		if err := os.WriteFile(cometGenesis, cometPretty, 0644); err != nil {
			return false, err
		}
		if err := os.WriteFile(gethGenesis, gethPretty, 0644); err != nil {
			return false, err
		}
		fmt.Printf("  Genesis files written.\n")
		return true, nil
	}

	if lastErr != nil {
		return false, fmt.Errorf("genesis fetch failed from all %d endpoint(s): %w", len(apiURLs), lastErr)
	}
	return false, fmt.Errorf("no API endpoints available for genesis fetch")
}

// fetchNetworkInfo queries the first responsive API URL for peer + staking info.
// It enriches the provided seedList in-place (adds comet peer + geth enode from
// each API's /api/node-info) and returns the staking contract address.
func fetchNetworkInfo(apiURLs []string, seeds *seedList) (stakingContract string) {
	type nodeInfo struct {
		CometPeer       string `json:"comet_peer"`
		GethEnode       string `json:"geth_enode"`
		StakingContract string `json:"staking_contract"`
	}
	for _, apiURL := range apiURLs {
		resp, err := httpGetJSON(apiURL + "/api/node-info")
		if err != nil {
			continue
		}
		var ni nodeInfo
		body, _ := json.Marshal(resp)
		if json.Unmarshal(body, &ni) != nil {
			continue
		}
		if ni.CometPeer != "" {
			seeds.CometPeers = appendUniq(seeds.CometPeers, ni.CometPeer)
		}
		if ni.GethEnode != "" {
			seeds.GethEnodes = appendUniq(seeds.GethEnodes, ni.GethEnode)
		}
		if stakingContract == "" {
			stakingContract = ni.StakingContract
		}
	}
	return stakingContract
}

// ── JWT secret ─────────────────────────────────────────────────────────────

func ensureJWT(jwtPath string) error {
	if fileExists(jwtPath) {
		return nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	secret := hex.EncodeToString(buf)
	return os.WriteFile(jwtPath, []byte(secret), 0600)
}

// ── geth init ──────────────────────────────────────────────────────────────

func ensureGethInit(dataDir string, force bool) error {
	chaindata := filepath.Join(dataDir, "geth-data", "geth", "chaindata")
	if !force && fileExists(chaindata) {
		return nil
	}

	gethBin := gethBinPath(filepath.Join(dataDir, "bin"))
	genesisFile := filepath.Join(dataDir, "comet", "config", "geth-genesis.json")

	fmt.Printf("  Initializing geth chain data...\n")
	return runCmd(gethBin,
		"--datadir", filepath.Join(dataDir, "geth-data"),
		"init", genesisFile,
	)
}

// ── http helpers ──────────────────────────────────────────────────────────

func httpGetJSON(url string) (any, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 32*1024)
	lastPct := -1
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			downloaded += int64(n)
			if total > 0 {
				pct := int(downloaded * 100 / total)
				if pct/10 != lastPct/10 {
					fmt.Printf("    %d%%...\n", pct)
					lastPct = pct
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
