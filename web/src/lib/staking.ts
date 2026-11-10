import { ethers } from 'ethers';
import { GETH_RPC } from '../api/client';

export const STAKING_ABI = [
  'function whitelist(address validator)',
  'function removeWhitelist(address validator)',
  'function jail(address validator)',
  'function unjail(address validator)',
  'function register(bytes32 ed25519PubKey, address rewardAddress) payable',
  'function startUnbonding()',
  'function withdraw()',
  'function owner() view returns (address)',
  'function minStake() view returns (uint256)',
  'function isActive(address) view returns (bool)',
  'function validators(address) view returns (bytes32, address, uint256, bool, bool, bool, bool, uint16, uint256)',
];

export function getReadProvider(): ethers.JsonRpcProvider {
  return new ethers.JsonRpcProvider(GETH_RPC);
}

export function getReadContract(contractAddr: string): ethers.Contract {
  return new ethers.Contract(contractAddr, STAKING_ABI, getReadProvider());
}

export function base64ToPubKeyHex(b64: string): string {
  try {
    const bytes = Uint8Array.from(atob(b64), c => c.charCodeAt(0));
    if (bytes.length !== 32) return '';
    return '0x' + Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
  } catch {
    return '';
  }
}

export function weiToChkDisplay(wei: string): string {
  try {
    const chk = Number(BigInt(wei)) / 1e18;
    return chk.toLocaleString(undefined, { maximumFractionDigits: 4 }) + ' CHK';
  } catch { return wei + ' wei'; }
}
