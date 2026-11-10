package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// VerifyRequest is the JSON body for POST /api/v1/contracts/:address/verify
type VerifyRequest struct {
	// Map of filename → Solidity source content.
	// Use flattened single-file or multiple files for imports.
	SourceFiles map[string]string `json:"source_files"`
	// e.g. "0.8.20", "0.8.19"
	CompilerVersion string `json:"compiler_version"`
	// Name of the contract to verify (e.g. "MyToken")
	ContractName     string `json:"contract_name"`
	OptimizerEnabled bool   `json:"optimizer_enabled"`
	OptimizerRuns    int    `json:"optimizer_runs"`
	// ABI-encoded constructor arguments (hex, with or without 0x prefix). Optional.
	ConstructorArgs string `json:"constructor_args"`
	License         string `json:"license"`
}

type VerifyResult struct {
	Success         bool   `json:"success"`
	Message         string `json:"message"`
	ContractName    string `json:"contract_name,omitempty"`
	CompilerVersion string `json:"compiler_version,omitempty"`
	ABI             string `json:"abi,omitempty"`
}

func verify(db *Database, geth *GethClient, solcPath string, addr string, req VerifyRequest) (*VerifyResult, error) {
	if len(req.SourceFiles) == 0 {
		return nil, fmt.Errorf("source_files is required")
	}
	if req.CompilerVersion == "" {
		return nil, fmt.Errorf("compiler_version is required")
	}
	if req.OptimizerRuns == 0 {
		req.OptimizerRuns = 200
	}

	// Fetch on-chain deployed bytecode
	onChain, err := geth.GetCode(addr)
	if err != nil {
		return nil, fmt.Errorf("cannot fetch bytecode: %w", err)
	}
	if onChain == "" || onChain == "0x" {
		return nil, fmt.Errorf("no contract deployed at %s", addr)
	}
	onChainBytes, err := hex.DecodeString(strings.TrimPrefix(onChain, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid on-chain bytecode: %w", err)
	}

	// Build solc Standard JSON input
	sources := make(map[string]map[string]string, len(req.SourceFiles))
	for name, content := range req.SourceFiles {
		sources[name] = map[string]string{"content": content}
	}
	solcInput := map[string]any{
		"language": "Solidity",
		"sources":  sources,
		"settings": map[string]any{
			"optimizer": map[string]any{
				"enabled": req.OptimizerEnabled,
				"runs":    req.OptimizerRuns,
			},
			"outputSelection": map[string]any{
				"*": map[string]any{
					"*": []string{"evm.deployedBytecode.object", "evm.deployedBytecode.immutableReferences", "abi"},
				},
			},
		},
	}

	inputJSON, err := json.Marshal(solcInput)
	if err != nil {
		return nil, fmt.Errorf("marshal solc input: %w", err)
	}

	// Run solc
	cmd := exec.Command(solcPath, "--standard-json")
	cmd.Stdin = bytes.NewReader(inputJSON)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("solc error: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("solc not found or failed: %w", err)
	}

	// Parse solc output
	var solcOut struct {
		Errors []struct {
			Severity string `json:"severity"`
			Message  string `json:"message"`
		} `json:"errors"`
		Contracts map[string]map[string]struct {
			ABI json.RawMessage `json:"abi"`
			EVM struct {
				DeployedBytecode struct {
					Object              string                        `json:"object"`
					ImmutableReferences map[string][]ImmutableRef     `json:"immutableReferences"`
				} `json:"deployedBytecode"`
			} `json:"evm"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(output, &solcOut); err != nil {
		return nil, fmt.Errorf("parse solc output: %w", err)
	}

	// Check for hard errors
	for _, e := range solcOut.Errors {
		if e.Severity == "error" {
			return nil, fmt.Errorf("compilation error: %s", e.Message)
		}
	}

	// Find matching contract — search by name, or try all
	type candidate struct {
		name     string
		bytecode string
		immRefs  map[string][]ImmutableRef
		abi      string
	}
	var candidates []candidate
	for _, contracts := range solcOut.Contracts {
		for name, c := range contracts {
			if req.ContractName != "" && name != req.ContractName {
				continue
			}
			abiBytes, _ := json.Marshal(c.ABI)
			candidates = append(candidates, candidate{
				name:     name,
				bytecode: c.EVM.DeployedBytecode.Object,
				immRefs:  c.EVM.DeployedBytecode.ImmutableReferences,
				abi:      string(abiBytes),
			})
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no matching contract found in compilation output")
	}

	// Try each candidate — compare with metadata and immutables stripped
	onChainStripped := stripMetadata(onChainBytes)
	for _, c := range candidates {
		compiled, err := hex.DecodeString(c.bytecode)
		if err != nil {
			continue
		}
		compiledStripped := stripMetadata(compiled)
		// Zero out immutable variable slots in both bytecodes before comparing.
		// Immutables are written into deployed bytecode by the constructor with
		// runtime values, so they differ between deployments.
		zeroImmutables(onChainStripped, c.immRefs)
		zeroImmutables(compiledStripped, c.immRefs)
		if bytes.Equal(onChainStripped, compiledStripped) {
			// Match! Store verification
			sourceJSON, _ := json.Marshal(req.SourceFiles)
			db.UpdateVerification(
				addr, c.name, string(sourceJSON), c.abi,
				req.CompilerVersion, req.ConstructorArgs, req.License,
				req.OptimizerEnabled, req.OptimizerRuns, time.Now().Unix(),
			)
			return &VerifyResult{
				Success:         true,
				Message:         "Contract verified successfully",
				ContractName:    c.name,
				CompilerVersion: req.CompilerVersion,
				ABI:             c.abi,
			}, nil
		}
	}

	return &VerifyResult{
		Success: false,
		Message: "Bytecode mismatch — check compiler version, optimizer settings, and source files",
	}, nil
}

// stripMetadata removes the CBOR-encoded metadata hash appended by the Solidity
// compiler. The last 2 bytes encode the metadata length; strip them + the metadata.
// This is necessary because metadata hashes differ between compilation environments.
func stripMetadata(bytecode []byte) []byte {
	if len(bytecode) < 2 {
		return bytecode
	}
	metaLen := int(bytecode[len(bytecode)-2])*256 + int(bytecode[len(bytecode)-1])
	end := len(bytecode) - 2 - metaLen
	if end < 0 || end > len(bytecode) {
		return bytecode
	}
	return bytecode[:end]
}

// ImmutableRef describes a byte range in deployed bytecode occupied by an immutable variable.
type ImmutableRef struct {
	Start  int `json:"start"`
	Length int `json:"length"`
}

// zeroImmutables overwrites immutable variable slots with zeros in-place.
// Immutables are filled by the constructor with deployment-specific values,
// so they must be excluded from bytecode comparison during verification.
func zeroImmutables(bytecode []byte, refs map[string][]ImmutableRef) {
	for _, slots := range refs {
		for _, slot := range slots {
			end := slot.Start + slot.Length
			if slot.Start < 0 || end > len(bytecode) {
				continue
			}
			for i := slot.Start; i < end; i++ {
				bytecode[i] = 0
			}
		}
	}
}
