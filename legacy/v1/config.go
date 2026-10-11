// config.go — Core constants for the Chakram (CHK) cryptocurrency.
// Inspired by the ancient Travancore kingdom coins of Kerala, India.
package main

// ── Coin Identity ────────────────────────────────────────────────────────────

const (
	CoinName      = "Chakram"
	Ticker        = "CHK"
	AddressPrefix = "CK1"

	// ProtocolVersion is the current network protocol version, embedded in
	// every block header and P2P handshake. Increment this for each hard fork.
	// Old nodes will be rejected once MinProtocolVersion is raised to match.
	ProtocolVersion uint32 = 2

	// MinProtocolVersion is the lowest peer protocol version this node accepts
	// during the handshake. Still 1 — will be bumped to 2 in the release after
	// block 10000 activates, once all miners have upgraded.
	MinProtocolVersion uint32 = 1

	// SoftwareVersion is the human-readable release string. Bumped by release.sh.
	SoftwareVersion = "v1.1.0"
)

// ForkActivations maps each protocol version to the block height at which its
// consensus rules activate. Version 1 always starts at genesis (height 0).
//
// v1.1 relaunch: all protocol versions activate at genesis — no mid-chain fork
// transitions on the new chain.
var ForkActivations = map[uint32]uint64{
	1: 0, // genesis rules, always active
	2: 0, // compact-target difficulty + 20s block floor — active from genesis on v1.1
}

// Checkpoints are immutable (height → hex-encoded hash) entries committed to
// by this binary. Any block at a checkpointed height must exactly match.
// Reorganizations may never roll back past the highest checkpoint.
// Add a new entry with each major release after the chain has stabilised.
// v1.1 relaunch: cleared — new genesis, new chain history.
var Checkpoints = map[uint64]string{}

// ── Supply & Economics ───────────────────────────────────────────────────────

const (
	// CashPerCHK is the number of indivisible Cash units in one CHK (like Satoshis in Bitcoin).
	CashPerCHK uint64 = 1_000_000

	// MaxSupply is the hard cap in Cash units (44,800,000 CHK).
	MaxSupply uint64 = 44_800_000 * CashPerCHK

	// InitialBlockReward is the reward for mining the first era of blocks, in Cash.
	InitialBlockReward uint64 = 50 * CashPerCHK

	// HalvingInterval is the number of blocks between each block-reward halving
	// (~2 years at a 30-second block time).
	HalvingInterval uint64 = 2_102_400

	// MinTxFee is the minimum transaction fee in Cash units.
	MinTxFee uint64 = 1_000
)

// ── Network ──────────────────────────────────────────────────────────────────

const (
	DefaultPortMainnet = 8338
	DefaultPortTestnet = 18338
	RPCPortMainnet     = 8339
	RPCPortTestnet     = 18339

	MaxPeers = 12
	MinPeers = 3
)

// MagicMainnet and MagicTestnet are the 4-byte network identifiers prepended to
// all peer-to-peer messages to prevent cross-network communication.
// v1.1: new magic bytes disconnect all v1.0.x nodes from the relaunch network.
var (
	MagicMainnet = [4]byte{0x43, 0x48, 0x31, 0x31} // C H 1 1
	MagicTestnet = [4]byte{0x54, 0x48, 0x31, 0x31} // T H 1 1
)

// MainnetChainID and TestnetChainID are committed to in every transaction
// signature. Including the chain ID in the sighash prevents a valid mainnet
// signature from being replayed on testnet or on any future fork.
var (
	MainnetChainID = []byte("chakram-mainnet-v1")
	TestnetChainID = []byte("chakram-testnet-v1")
)

// NetworkChainID returns the chain ID for the given network.
func NetworkChainID(testnet bool) []byte {
	if testnet {
		return TestnetChainID
	}
	return MainnetChainID
}

// ── Blockchain ───────────────────────────────────────────────────────────────

const (
	// TargetBlockTime is the desired seconds between blocks.
	TargetBlockTime int64 = 30

	// DifficultyWindow is the number of recent blocks used to compute the next
	// difficulty target (sliding-window retarget).
	DifficultyWindow uint64 = 60

	// InitialDifficulty is the PoW baseline during the bootstrap window.
	// With timestamp-enforced bootstrap (TEB), block rate is controlled by the
	// 60-second time floor, not by this value. This only needs to be high enough
	// to prevent trivial block forgery. 2^4 = 16 expected hashes — achievable in
	// seconds on any hardware, so the time floor always controls the actual rate.
	InitialDifficulty uint64 = 4

	// DifficultyAdjustmentInterval is kept for reference / future batch retarget.
	DifficultyAdjustmentInterval uint64 = 2016

	// MaxBlockSize is the maximum serialised block size in bytes (1 MB).
	MaxBlockSize uint64 = 1 * 1024 * 1024

	// CoinbaseMaturity is the number of confirmations required before a mined
	// reward can be spent. 10 blocks × 60 s = ~10 min wait before spending rewards.
	CoinbaseMaturity uint64 = 10

	// RandomXEpochLen is how many blocks share the same RandomX cache seed.
	// Argon2d is only re-run when the epoch boundary changes.
	RandomXEpochLen uint64 = 64
)

// ── Genesis ───────────────────────────────────────────────────────────────────

const (
	// GenesisMessage is the coinbase message embedded in the genesis block.
	// Translation: "Chakram — The heritage of Kerala, reborn in the digital age."
	GenesisMessage = "ചക്രം — കേരളത്തിന്റെ പൈതൃകം ഡിജിറ്റൽ യുഗത്തിൽ പുനർജനിക്കുന്നു"

	// GenesisTimestamp is the fixed Unix timestamp of the genesis block (2026-10-09 00:00:00 UTC).
	GenesisTimestamp int64 = 1_791_504_000
)

// ── Mining ────────────────────────────────────────────────────────────────────

const (
	// MiningAlgorithm identifies the proof-of-work algorithm used by Chakram.
	MiningAlgorithm = "RandomX"

	// MinDifficulty is the lowest allowed network difficulty target.
	MinDifficulty uint64 = 4

	// PostBootstrapMinGap is the minimum seconds between consecutive blocks
	// after the TEB bootstrap window ends (protocol v1). Set above TargetBlockTime
	// to keep difficulty pinned at MinDifficulty while the network bootstraps.
	PostBootstrapMinGap int64 = 45

	// PostBootstrapMinGapV2 replaces PostBootstrapMinGap from protocol v2 onward
	// (activates at block 10000). Below TargetBlockTime so LWMA can raise
	// difficulty as more miners join and hashrate grows.
	PostBootstrapMinGapV2 int64 = 20

	// InitialCompactTargetV2 is the compact-encoded PoW target for the first
	// protocol-v2 block (height 10000). Encodes target = 2^252, which is exactly
	// equivalent to the old integer difficulty of 4 (hash < 2^(256-4) = 2^252).
	// Compact format: upper 8 bits = byte-length (32), lower 56 bits = mantissa (2^52).
	InitialCompactTargetV2 uint64 = 0x2010000000000000

	// RandomXLightMode is retained for reference. The CGo engine uses full mode
	// (2 GB dataset, ~10x faster) on all supported platforms and falls back to
	// light mode automatically if the system cannot allocate the dataset.
	RandomXLightMode = false

	// MiningThreads is the default number of threads used during mining.
	// Can be overridden via the node config file in a later phase.
	MiningThreads = 1
)

// ── Seed Nodes ────────────────────────────────────────────────────────────────

// DNSSeeds is the list of DNS seed hostnames resolved at startup.
// Each entry is operated independently — community members can run their own
// crawler (similar to bitcoin-seeder) and submit a PR to add their hostname.
// New seed VMs can be added by the operator without a code release by updating
// the A records behind their hostname.
var DNSSeeds = []string{
	"seeds.chakram.one", // operated by Chakram core team
}

// Hardcoded fallback seeds — used when DNS is unreachable.
var TestnetSeeds = []string{
	"35.207.229.32:18338",
	"34.1.166.49:18338",
}

var MainnetSeeds = []string{
	"35.207.229.32:8338",
	"34.1.166.49:8338",
	"35.207.217.64:8338",
}

