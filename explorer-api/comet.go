package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type CometClient struct {
	url    string
	client http.Client
}

func newCometClient(url string) *CometClient {
	return &CometClient{url: url, client: http.Client{Timeout: 10 * time.Second}}
}

type CometBlockInfo struct {
	Hash     string
	Proposer string
	AppHash  string
}

type CometValidator struct {
	Address     string `json:"address"`
	VotingPower string `json:"voting_power"`
	PubKey      struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"pub_key"`
}

func (c *CometClient) GetBlockInfo(height int64) (*CometBlockInfo, error) {
	url := fmt.Sprintf("%s/block?height=%d", c.url, height)
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			BlockID struct {
				Hash string `json:"hash"`
			} `json:"block_id"`
			Block struct {
				Header struct {
					ProposerAddress string `json:"proposer_address"`
					AppHash         string `json:"app_hash"`
				} `json:"header"`
			} `json:"block"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &CometBlockInfo{
		Hash:     result.Result.BlockID.Hash,
		Proposer: result.Result.Block.Header.ProposerAddress,
		AppHash:  result.Result.Block.Header.AppHash,
	}, nil
}

func (c *CometClient) GetValidators() ([]CometValidator, int64, error) {
	url := fmt.Sprintf("%s/validators?per_page=100", c.url)
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			BlockHeight string           `json:"block_height"`
			Validators  []CometValidator `json:"validators"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, 0, err
	}
	height := hexToInt64(result.Result.BlockHeight)
	if height == 0 {
		// BlockHeight is decimal string from CometBFT
		fmt.Sscanf(result.Result.BlockHeight, "%d", &height)
	}
	return result.Result.Validators, height, nil
}

func (c *CometClient) LatestHeight() (int64, error) {
	url := fmt.Sprintf("%s/status", c.url)
	resp, err := c.client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			SyncInfo struct {
				LatestBlockHeight string `json:"latest_block_height"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	var h int64
	fmt.Sscanf(result.Result.SyncInfo.LatestBlockHeight, "%d", &h)
	return h, nil
}
