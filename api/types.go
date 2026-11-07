package api

import "encoding/json"

// StatusResponse is returned by GET /api/status.
type StatusResponse struct {
	NodeID             string `json:"node_id"`
	Moniker            string `json:"moniker"`
	ChainID            string `json:"chain_id"`
	Height             int64  `json:"height"`
	LatestHash         string `json:"latest_block_hash"`
	AppHash            string `json:"latest_app_hash"`
	CatchingUp         bool   `json:"catching_up"`
	Peers              int    `json:"peers"`
	CometVersion       string `json:"comet_version"`
	ValidatorAddr      string `json:"validator_address,omitempty"`
	VotingPower        int64  `json:"voting_power,omitempty"`
	// Geth execution layer fields
	GethHeight         int64  `json:"geth_height"`
	GethFinalized      int64  `json:"geth_finalized"`
	GethAvailable      bool   `json:"geth_available"`
	HealthGateBlocked  bool   `json:"health_gate_blocked"`
}

// ValidatorResponse is returned by GET /api/validator.
type ValidatorResponse struct {
	Address        string `json:"address"`
	PubKey         string `json:"pub_key"`
	Power          int64  `json:"voting_power"`
	IsActive       bool   `json:"is_active"`
	SignedLast100  int    `json:"signed_last_100"`
	MissedLast100  int    `json:"missed_last_100"`
	RewardAddress  string `json:"reward_address,omitempty"`
	RewardBalance  string `json:"reward_balance,omitempty"`
}

// BlockSummary is one entry in GET /api/blocks.
type BlockSummary struct {
	Height   int64  `json:"height"`
	Hash     string `json:"hash"`
	Time     string `json:"time"`
	Proposer string `json:"proposer"`
	TxCount  int    `json:"tx_count"`
	ExecHash string `json:"exec_hash"`
}

// BlocksResponse is returned by GET /api/blocks.
type BlocksResponse struct {
	Blocks []BlockSummary `json:"blocks"`
	Total  int64          `json:"total"`
}

// BlockResponse is returned by GET /api/block/{height}.
type BlockResponse struct {
	Height   int64    `json:"height"`
	Hash     string   `json:"hash"`
	Time     string   `json:"time"`
	Proposer string   `json:"proposer"`
	Txs      []string `json:"txs"`
	ExecHash string   `json:"exec_hash"`
	GasUsed  string   `json:"gas_used"`
	GasLimit string   `json:"gas_limit"`
}

// TxResponse is returned by GET /api/tx/{hash}.
type TxResponse struct {
	Hash        string `json:"hash"`
	BlockHeight int64  `json:"block_height"`
	Status      string `json:"status"`
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	GasUsed     string `json:"gas_used"`
	GasPrice    string `json:"gas_price"`
	Nonce       uint64 `json:"nonce"`
}

// AddressResponse is returned by GET /api/address/{addr}.
type AddressResponse struct {
	Address string `json:"address"`
	Balance string `json:"balance"`
	Nonce   uint64 `json:"nonce"`
	Code    string `json:"code"`
}

// Peer is one entry in PeersResponse.
type Peer struct {
	ID         string `json:"id"`
	RemoteIP   string `json:"remote_ip"`
	IsOutbound bool   `json:"is_outbound"`
	Moniker    string `json:"moniker,omitempty"`
}

// PeersResponse is returned by GET /api/peers.
type PeersResponse struct {
	Peers []Peer `json:"peers"`
}

// FaucetRequest is the body for POST /api/faucet.
type FaucetRequest struct {
	Address string `json:"address"`
}

// FaucetResponse is returned by POST /api/faucet.
type FaucetResponse struct {
	TxHash  string `json:"tx_hash"`
	Amount  string `json:"amount"`
	Message string `json:"message,omitempty"`
}

// ErrorResponse wraps API errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

// NodeInfoResponse is returned by GET /api/node-info.
type NodeInfoResponse struct {
	CometPeer       string `json:"comet_peer"`               // nodeID@host:26656
	GethEnode       string `json:"geth_enode"`               // enode://pubkey@host:30303
	GenesisURL      string `json:"genesis_url"`              // URL to download genesis files
	StakingContract string `json:"staking_contract,omitempty"` // StakingRegistry contract address
}

// GenesisResponse is returned by GET /api/genesis.
type GenesisResponse struct {
	CometGenesis json.RawMessage `json:"comet_genesis"`
	GethGenesis  json.RawMessage `json:"geth_genesis"`
}

// StakingValidator is one entry in StakingResponse.
type StakingValidator struct {
	EVMAddr             string `json:"evm_addr"`
	CometAddr           string `json:"comet_addr"`
	Ed25519PubKey       string `json:"ed25519_pubkey"`
	RewardAddr          string `json:"reward_addr"`
	Stake               string `json:"stake"`
	Whitelisted         bool   `json:"whitelisted"`
	Active              bool   `json:"active"`
	Jailed              bool   `json:"jailed"`
	UnbondingUnlockTime uint64 `json:"unbonding_unlock_time"`
	VotingPower         int64  `json:"voting_power"`
}

// StakingResponse is returned by GET /api/staking.
type StakingResponse struct {
	Enabled         bool               `json:"enabled"`
	ContractAddr    string             `json:"contract_addr,omitempty"`
	Owner           string             `json:"owner,omitempty"`
	MinStake        string             `json:"min_stake,omitempty"`
	UnbondingPeriod uint64             `json:"unbonding_period,omitempty"`
	Validators      []StakingValidator `json:"validators"`
}

// NodeWalletResponse is returned by GET /api/node-wallet.
type NodeWalletResponse struct {
	Address    string `json:"address"`
	BalanceWei string `json:"balance_wei"`
	HasKey     bool   `json:"has_key"`
}

// NodeWalletExportResponse is returned by GET /api/node-wallet/export-key (localhost only).
type NodeWalletExportResponse struct {
	PrivateKey string `json:"private_key"`
}

// StakingTxResponse is returned by POST /api/staking/{register,unbond,withdraw}.
type StakingTxResponse struct {
	TxHash string `json:"tx_hash"`
}

// StakingRegisterRequest is the body for POST /api/staking/register.
type StakingRegisterRequest struct {
	RewardAddress string `json:"reward_address"` // optional; defaults to node's EVM address
	StakeWei      string `json:"stake_wei"`      // optional; defaults to minStake
}
