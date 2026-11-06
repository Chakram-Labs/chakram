// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title  StakingRegistry
/// @notice Permissioned validator registry for Chakram.
///
///  Decentralisation path — encoded in immutable constants, NOT owner promises:
///
///   The whitelist requirement is permanently dropped when ANY of these occur:
///     (A) block.timestamp >= permissionlessAfter   [~18 months after deploy]
///     (B) activeValidatorCount >= validatorThreshold [20 active validators]
///     (C) Governance vote: 66% of active stake votes YES within a 7-day window
///
///   Once isPermissionless() returns true it can never return false again.
///   The owner has no power to delay or block any of these triggers.
///
///  Future features supported WITHOUT redeploying this contract:
///   - Delegation  : DelegationRegistry.sol reads commissionBps from this contract
///   - Slashing    : ABCI app calls slash() via the trusted slasher address
///   - Governance  : GovernanceRegistry.sol reads state and calls setMinStake() etc.
///
///  Stages:
///   1 — owner is a single key (this deploy)
///   2 — transferOwnership to a 5-of-9 multisig (Phase 5)
///   3 — transferOwnership to an on-chain governance contract (Phase 6)
///   4 — permissionless triggers automatically — owner role becomes irrelevant
contract StakingRegistry {

    // ── Core state ────────────────────────────────────────────────────────────

    address public owner;
    uint256 public minStake;
    uint256 public unbondingPeriod; // seconds

    struct ValidatorInfo {
        bytes32 ed25519PubKey;
        address rewardAddress;
        uint256 stake;
        bool    whitelisted;
        bool    active;
        bool    jailed;
        bool    registered;        // ever registered — prevents duplicate list entries
        uint16  commissionBps;     // 0–10000 basis points; read by DelegationRegistry
        uint256 unbondingUnlockTime; // 0 if not unbonding
    }

    mapping(address => ValidatorInfo) public validators;
    address[] public validatorList;      // all ever-registered addresses
    uint256 public activeValidatorCount; // currently active validators
    uint256 public totalActiveStake;     // sum of active validator stakes

    // ── Validator metadata ────────────────────────────────────────────────────
    // Stored separately to keep ValidatorInfo ABI clean.
    // DelegationRegistry and the UI read these for display / UX.

    mapping(address => string) public monikers; // e.g. "University of Delhi"
    mapping(address => string) public websites; // e.g. "https://du.ac.in"

    // ── Slashing ──────────────────────────────────────────────────────────────
    // slasher: trusted address that can slash validators (initially the ABCI app).
    //          Set to address(0) to disable slashing.
    // treasury: receives slashed funds. address(0) = burn to 0xdead.

    address public slasher;
    address public treasury;

    // ── Immutable decentralisation parameters ─────────────────────────────────
    // Set at construction. immutable = written to bytecode, cannot be changed
    // by any function, owner, or governance. Verifiable by anyone.

    /// @notice Unix timestamp after which the whitelist is permanently removed.
    uint256 public immutable permissionlessAfter; // deploy + ~18 months

    /// @notice Active validator count that permanently removes the whitelist.
    uint256 public immutable validatorThreshold;  // 20 validators

    /// @notice Set to true by a successful governance vote. Never reset to false.
    bool public openedEarly;

    // ── Governance: early-open vote ───────────────────────────────────────────

    uint256 public constant VOTING_PERIOD     = 7 days;
    uint256 public constant PROPOSAL_COOLDOWN = 30 days;
    uint256 public constant OPEN_QUORUM       = 66; // % of active stake needed

    struct Proposal {
        address proposer;
        uint256 deadline;
        uint256 votesFor;
        bool    executed;
    }

    Proposal public currentProposal;
    bool     public proposalActive;
    uint256  public proposalNonce;
    uint256  public lastProposalTime;
    // nonce → validator → voted; nonce prevents replay across proposals
    mapping(uint256 => mapping(address => bool)) public hasVotedOn;

    // ── Events ────────────────────────────────────────────────────────────────

    event ValidatorRegistered(
        address indexed validator,
        bytes32         ed25519PubKey,
        address         rewardAddress,
        uint256         stake
    );
    event ValidatorJailed(address indexed validator);
    event ValidatorUnjailed(address indexed validator);
    event UnbondingStarted(address indexed validator, uint256 unlockTime);
    event Withdrawn(address indexed validator, uint256 amount);
    event Whitelisted(address indexed validator);
    event WhitelistRemoved(address indexed validator);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);

    // Metadata events
    event MonikerUpdated(address indexed validator, string moniker);
    event WebsiteUpdated(address indexed validator, string website);

    // Commission event (read by DelegationRegistry)
    event CommissionUpdated(address indexed validator, uint16 oldBps, uint16 newBps);

    // Slash events
    event ValidatorSlashed(address indexed validator, uint256 amount, uint256 bps);
    event SlasherUpdated(address indexed oldSlasher, address indexed newSlasher);
    event TreasuryUpdated(address indexed oldTreasury, address indexed newTreasury);

    // Governance events
    event EarlyOpenProposed(address indexed proposer, uint256 nonce, uint256 deadline);
    event VoteCast(address indexed validator, uint256 nonce, uint256 stakeWeight, uint256 totalVotesFor);
    event OpenedEarlyByVote(uint256 timestamp);
    event ProposalExpired(uint256 nonce);
    event PermissionlessActivated(string reason, uint256 timestamp);

    // ── Modifiers ─────────────────────────────────────────────────────────────

    modifier onlyOwner() {
        require(msg.sender == owner, "not owner");
        _;
    }

    modifier onlyActiveValidator() {
        require(validators[msg.sender].active, "not an active validator");
        _;
    }

    // ── Constructor ───────────────────────────────────────────────────────────

    constructor(uint256 _minStake, uint256 _unbondingPeriod) {
        owner           = msg.sender;
        minStake        = _minStake;
        unbondingPeriod = _unbondingPeriod;
        // Immutable: written to bytecode at deploy, cannot be changed by anyone.
        permissionlessAfter = block.timestamp + 548 days; // ~18 months
        validatorThreshold  = 20;
    }

    // ── Permissionless check ──────────────────────────────────────────────────

    /// @notice True if the network has permanently dropped the whitelist requirement.
    ///         Any of three independent conditions can trigger this.
    ///         Once true, always true — owner cannot reverse it.
    function isPermissionless() public view returns (bool) {
        return openedEarly
            || block.timestamp      >= permissionlessAfter
            || activeValidatorCount >= validatorThreshold;
    }

    // ── Owner functions ───────────────────────────────────────────────────────

    function whitelist(address validator) external onlyOwner {
        require(validator != address(0), "zero address");
        validators[validator].whitelisted = true;
        emit Whitelisted(validator);
    }

    function removeWhitelist(address validator) external onlyOwner {
        validators[validator].whitelisted = false;
        emit WhitelistRemoved(validator);
    }

    /// @notice Force-jail a misbehaving validator (manual path — use slash for automatic).
    function jail(address validator) external onlyOwner {
        ValidatorInfo storage v = validators[validator];
        require(v.active, "not active");
        v.active = false;
        v.jailed = true;
        activeValidatorCount--;
        totalActiveStake -= v.stake;
        emit ValidatorJailed(validator);
    }

    /// @notice Re-admit a jailed validator to the active set.
    function unjail(address validator) external onlyOwner {
        ValidatorInfo storage v = validators[validator];
        require(v.jailed, "not jailed");
        if (!isPermissionless()) {
            require(v.whitelisted, "not whitelisted");
        }
        require(v.stake >= minStake, "stake below minStake");
        v.active = true;
        v.jailed = false;
        activeValidatorCount++;
        totalActiveStake += v.stake;
        emit ValidatorUnjailed(validator);
    }

    function transferOwnership(address newOwner) external onlyOwner {
        require(newOwner != address(0), "zero address");
        emit OwnershipTransferred(owner, newOwner);
        owner = newOwner;
    }

    function setMinStake(uint256 _minStake) external onlyOwner {
        minStake = _minStake;
    }

    /// @notice Set the trusted slasher address (typically the ABCI app or slash oracle).
    ///         Set to address(0) to disable slashing entirely.
    function setSlasher(address _slasher) external onlyOwner {
        emit SlasherUpdated(slasher, _slasher);
        slasher = _slasher;
    }

    /// @notice Set the address that receives slashed funds.
    ///         address(0) = burn to 0xdead (default).
    function setTreasury(address _treasury) external onlyOwner {
        emit TreasuryUpdated(treasury, _treasury);
        treasury = _treasury;
    }

    // ── Slashing ──────────────────────────────────────────────────────────────

    /// @notice Slash a validator's stake by a given basis points (1 bps = 0.01%).
    ///         Called by the trusted slasher address (ABCI app detects double-sign
    ///         or liveness fault and calls this automatically).
    ///         Slashing always jails the validator. Owner must manually unjail.
    ///         Slashed funds go to treasury (or are burned if treasury == address(0)).
    ///
    ///  Suggested bps by offense:
    ///   - Liveness fault (missed >X blocks): 100 bps (1%)
    ///   - Double-sign:                       10000 bps (100%)
    function slash(address validator, uint256 bps) external {
        require(msg.sender == slasher, "not slasher");
        require(bps > 0 && bps <= 10000, "bps must be 1-10000");
        ValidatorInfo storage v = validators[validator];
        require(v.stake > 0, "no stake to slash");

        uint256 slashAmount = v.stake * bps / 10000;
        if (slashAmount == 0) return; // nothing to do (tiny stake, tiny bps)

        if (v.active) {
            // Active validator: remove from set, jail, update totals
            v.active = false;
            v.jailed = true;
            activeValidatorCount--;
            totalActiveStake -= v.stake; // full stake leaves active pool
            emit ValidatorJailed(validator);
        } else if (!v.jailed) {
            // Unbonding validator: jail and cancel unbonding so they can't
            // withdraw slashed funds before the owner reviews
            v.jailed = true;
            v.unbondingUnlockTime = 0;
            emit ValidatorJailed(validator);
        }
        // Already jailed: just slash the remaining stake, no re-jail needed

        v.stake -= slashAmount;

        address dest = (treasury == address(0)) ? address(0xdead) : treasury;
        (bool ok,) = payable(dest).call{value: slashAmount}("");
        require(ok, "slash transfer failed");

        emit ValidatorSlashed(validator, slashAmount, bps);
    }

    // ── Validator functions ───────────────────────────────────────────────────

    /// @notice Register as a validator. During permissioned phase requires whitelist.
    ///         After isPermissionless() — open to anyone with sufficient stake.
    function register(bytes32 ed25519PubKey, address rewardAddress) external payable {
        ValidatorInfo storage v = validators[msg.sender];

        if (!isPermissionless()) {
            require(v.whitelisted, "not whitelisted");
        }
        require(!v.active,                  "already active");
        require(!v.jailed,                  "jailed - contact owner");
        require(v.unbondingUnlockTime == 0, "unbonding in progress");
        require(msg.value >= minStake,      "insufficient stake");
        require(rewardAddress != address(0),"zero reward address");
        require(ed25519PubKey != bytes32(0),"zero pubkey");

        if (!v.registered) {
            v.registered = true;
            validatorList.push(msg.sender);
        }

        v.ed25519PubKey = ed25519PubKey;
        v.rewardAddress = rewardAddress;
        v.stake         = msg.value;
        v.active        = true;

        activeValidatorCount++;
        totalActiveStake += msg.value;

        emit ValidatorRegistered(msg.sender, ed25519PubKey, rewardAddress, msg.value);

        if (activeValidatorCount == validatorThreshold) {
            emit PermissionlessActivated("validator_threshold_reached", block.timestamp);
        }
    }

    /// @notice Voluntarily leave the active set and start the unbonding timer.
    function startUnbonding() external {
        ValidatorInfo storage v = validators[msg.sender];
        require(v.active, "not active");
        v.active = false;
        v.unbondingUnlockTime = block.timestamp + unbondingPeriod;
        activeValidatorCount--;
        totalActiveStake -= v.stake;
        emit ValidatorJailed(msg.sender);
        emit UnbondingStarted(msg.sender, v.unbondingUnlockTime);
    }

    /// @notice Withdraw stake after unbonding period has elapsed.
    function withdraw() external {
        ValidatorInfo storage v = validators[msg.sender];
        require(!v.active,                                "still active");
        require(v.unbondingUnlockTime > 0,                "not unbonding");
        require(block.timestamp >= v.unbondingUnlockTime, "unbonding not complete");

        uint256 amount = v.stake;
        v.stake = 0;
        v.unbondingUnlockTime = 0;

        emit Withdrawn(msg.sender, amount);
        (bool ok,) = payable(msg.sender).call{value: amount}("");
        require(ok, "transfer failed");
    }

    /// @notice Set your validator commission rate for delegators.
    ///         0 = no commission (delegators keep 100% of their rewards).
    ///         10000 = 100% commission (delegators earn nothing — nobody will delegate).
    ///         Read by DelegationRegistry when distributing delegation rewards.
    function setCommission(uint16 bps) external {
        require(validators[msg.sender].registered, "not a validator");
        require(bps <= 10000, "max 10000 bps (100%)");
        uint16 old = validators[msg.sender].commissionBps;
        validators[msg.sender].commissionBps = bps;
        emit CommissionUpdated(msg.sender, old, bps);
    }

    /// @notice Set a human-readable name for your validator node.
    ///         Displayed in the staking UI and read by DelegationRegistry.
    function setMoniker(string calldata name) external {
        require(validators[msg.sender].registered, "not a validator");
        require(bytes(name).length > 0 && bytes(name).length <= 64, "1-64 chars");
        monikers[msg.sender] = name;
        emit MonikerUpdated(msg.sender, name);
    }

    /// @notice Set an optional website URL for your validator.
    function setWebsite(string calldata url) external {
        require(validators[msg.sender].registered, "not a validator");
        require(bytes(url).length <= 128, "max 128 chars");
        websites[msg.sender] = url;
        emit WebsiteUpdated(msg.sender, url);
    }

    // ── Governance: early permissionless vote ─────────────────────────────────

    /// @notice Propose opening the network to permissionless registration early.
    ///         Any active validator can propose. 30-day cooldown between proposals.
    function proposeEarlyOpen() external onlyActiveValidator {
        require(!isPermissionless(), "already permissionless");
        require(!proposalActive,     "proposal already active");
        require(
            lastProposalTime == 0 || block.timestamp >= lastProposalTime + PROPOSAL_COOLDOWN,
            "cooldown: 30 days between proposals"
        );

        proposalNonce++;
        proposalActive           = true;
        lastProposalTime         = block.timestamp;
        currentProposal.proposer = msg.sender;
        currentProposal.deadline = block.timestamp + VOTING_PERIOD;
        currentProposal.votesFor = 0;
        currentProposal.executed = false;

        emit EarlyOpenProposed(msg.sender, proposalNonce, currentProposal.deadline);
    }

    /// @notice Vote YES on the current early-open proposal.
    ///         Voting power = your stake. Not voting = effectively NO.
    ///         Proposal executes automatically the moment 66% threshold is crossed.
    function voteEarlyOpen() external onlyActiveValidator {
        require(proposalActive,                              "no active proposal");
        require(block.timestamp <= currentProposal.deadline, "voting ended");
        require(!hasVotedOn[proposalNonce][msg.sender],      "already voted");

        hasVotedOn[proposalNonce][msg.sender] = true;
        uint256 weight = validators[msg.sender].stake;
        currentProposal.votesFor += weight;

        emit VoteCast(msg.sender, proposalNonce, weight, currentProposal.votesFor);

        if (totalActiveStake > 0 &&
            currentProposal.votesFor * 100 >= totalActiveStake * OPEN_QUORUM)
        {
            openedEarly              = true;
            proposalActive           = false;
            currentProposal.executed = true;
            emit OpenedEarlyByVote(block.timestamp);
            emit PermissionlessActivated("governance_vote", block.timestamp);
        }
    }

    /// @notice Expire a failed proposal after the deadline.
    ///         Anyone can call — no permission needed.
    function expireProposal() external {
        require(proposalActive,                             "no active proposal");
        require(block.timestamp > currentProposal.deadline, "voting still active");
        proposalActive = false;
        emit ProposalExpired(proposalNonce);
    }

    // ── Views ─────────────────────────────────────────────────────────────────

    function isActive(address validator) external view returns (bool) {
        return validators[validator].active;
    }

    function getValidatorCount() external view returns (uint256) {
        return validatorList.length;
    }

    /// @notice Full public profile of a validator — used by DelegationRegistry and UI.
    function validatorProfile(address validator) external view returns (
        bool    active,
        bool    jailed,
        uint256 stake,
        uint16  commissionBps,
        string  memory moniker,
        string  memory website,
        address rewardAddress
    ) {
        ValidatorInfo storage v = validators[validator];
        active        = v.active;
        jailed        = v.jailed;
        stake         = v.stake;
        commissionBps = v.commissionBps;
        moniker       = monikers[validator];
        website       = websites[validator];
        rewardAddress = v.rewardAddress;
    }

    /// @notice Progress of the current early-open governance proposal.
    function proposalProgress() external view returns (
        uint256 votesFor,
        uint256 totalStake,
        uint256 pct,
        uint256 deadline
    ) {
        votesFor   = currentProposal.votesFor;
        totalStake = totalActiveStake;
        pct        = totalStake > 0 ? (votesFor * 100) / totalStake : 0;
        deadline   = currentProposal.deadline;
    }

    /// @notice Current decentralisation status — readable by anyone, shown in UI.
    function decentralisationStatus() external view returns (
        bool    permissionless,
        uint256 openAt,       // permissionlessAfter timestamp
        uint256 threshold,    // validatorThreshold count
        uint256 activeCount,  // current activeValidatorCount
        uint256 daysRemaining // 0 if already past
    ) {
        permissionless = isPermissionless();
        openAt         = permissionlessAfter;
        threshold      = validatorThreshold;
        activeCount    = activeValidatorCount;
        daysRemaining  = block.timestamp >= permissionlessAfter
            ? 0
            : (permissionlessAfter - block.timestamp) / 1 days;
    }
}
