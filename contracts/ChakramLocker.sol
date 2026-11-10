// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract ChakramLocker {

    struct Lock {
        uint256 amount;
        uint256 vestStart;
        uint256 claimed;
    }

    address public owner;
    uint256 public immutable vestCliff;
    uint256 public immutable vestPeriod;

    mapping(address => Lock[]) private _locks;

    event Credited(address indexed beneficiary, uint256 amount, uint256 vestStart, uint256 vestEnd);
    event Claimed(address indexed beneficiary, uint256 amount);

    error NotOwner();
    error NothingVested();
    error TransferFailed();

    constructor(address _owner, uint256 _vestCliff, uint256 _vestPeriod) {
        owner      = _owner;
        vestCliff  = _vestCliff;
        vestPeriod = _vestPeriod;
    }

    function credit(address beneficiary, uint256 amount) external {
        if (msg.sender != owner) revert NotOwner();
        uint256 start = block.timestamp + vestCliff;
        _locks[beneficiary].push(Lock({ amount: amount, vestStart: start, claimed: 0 }));
        emit Credited(beneficiary, amount, start, start + vestPeriod);
    }

    function claim() external {
        uint256 total;
        Lock[] storage ls = _locks[msg.sender];
        uint256 len = ls.length;
        for (uint256 i; i < len; ++i) {
            uint256 v = _vestedNow(ls[i]);
            if (v > 0) {
                ls[i].claimed += v;
                total += v;
            }
        }
        if (total == 0) revert NothingVested();
        emit Claimed(msg.sender, total);
        (bool ok,) = payable(msg.sender).call{value: total}("");
        if (!ok) revert TransferFailed();
    }

    function claimable(address beneficiary) external view returns (uint256 total) {
        Lock[] storage ls = _locks[beneficiary];
        for (uint256 i; i < ls.length; ++i) {
            total += _vestedNow(ls[i]);
        }
    }

    function locked(address beneficiary) external view returns (uint256 total) {
        Lock[] storage ls = _locks[beneficiary];
        for (uint256 i; i < ls.length; ++i) {
            Lock storage l = ls[i];
            uint256 vested = _totalVested(l);
            if (l.amount > vested) {
                total += l.amount - vested;
            }
        }
    }

    function lockCount(address beneficiary) external view returns (uint256) {
        return _locks[beneficiary].length;
    }

    function _vestedNow(Lock storage l) private view returns (uint256) {
        uint256 vested = _totalVested(l);
        return vested > l.claimed ? vested - l.claimed : 0;
    }

    function _totalVested(Lock storage l) private view returns (uint256) {
        if (block.timestamp < l.vestStart) return 0;
        uint256 elapsed = block.timestamp - l.vestStart;
        if (elapsed >= vestPeriod) return l.amount;
        return l.amount * elapsed / vestPeriod;
    }

    receive() external payable {}
}
