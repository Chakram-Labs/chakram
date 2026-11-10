import { useEffect, useState, FormEvent } from 'react';
import { api, StakingResponse, StakingValidator, NodeWalletResponse, trunc } from '../api/client';
import { GradCard, Card, Chip, StatCard } from '../components/ui';
import { ShieldCheck, ShieldOff, Clock, Coins, Users, Copy, Check } from 'lucide-react';
import { base64ToPubKeyHex, weiToChkDisplay } from '../lib/staking';

// ─── helpers ────────────────────────────────────────────────────────────────

function weiToEth(wei: string): string {
  try { return (Number(BigInt(wei)) / 1e18).toString(); } catch { return '0'; }
}

function blocksToHuman(blocks: number): string {
  if (!blocks) return '—';
  const secs = Math.round(blocks * 2.5);
  const human = secs < 120 ? `${secs}s` : secs < 7200 ? `${Math.round(secs / 60)}m` : `${Math.round(secs / 3600)}h`;
  return `${blocks} blocks (~${human})`;
}

function extractError(err: unknown): string {
  if (err instanceof Error) {
    const e = err as unknown as Record<string, unknown>;
    if (typeof e.reason === 'string') return e.reason;
    if (typeof e.shortMessage === 'string') return e.shortMessage;
    return err.message.slice(0, 300);
  }
  return String(err);
}

// ─── UI atoms ────────────────────────────────────────────────────────────────

function StatusBadge({ v, currentHeight = 0 }: { v: StakingValidator; currentHeight?: number }) {
  if (v.jailed) return <Chip color="red"><ShieldOff size={9} strokeWidth={2.5} /> Jailed</Chip>;
  if (v.unbonding_unlock_block > 0) {
    if (currentHeight > 0 && currentHeight >= v.unbonding_unlock_block)
      return <Chip color="lime"><Check size={9} strokeWidth={2.5} /> Ready to Withdraw</Chip>;
    return <Chip color="amber"><Clock size={9} strokeWidth={2.5} /> Unbonding</Chip>;
  }
  if (v.active) return <Chip color="green"><ShieldCheck size={9} strokeWidth={2.5} /> Active</Chip>;
  return <Chip color="gray">Inactive</Chip>;
}

function CopyBtn({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={() => navigator.clipboard.writeText(text).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); })}
      className="ml-1 inline-flex items-center rounded p-0.5 text-gray-500 hover:text-gray-200 transition-colors"
    >
      {copied ? <Check size={11} strokeWidth={2.5} className="text-lime-400" /> : <Copy size={11} strokeWidth={2} />}
    </button>
  );
}

function InfoRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between px-6 py-3 border-b border-white/5 last:border-0">
      <dt className="text-[11px] font-semibold uppercase tracking-wide text-gray-500 shrink-0 mr-4">{label}</dt>
      <dd className={`text-xs text-right text-white break-all flex items-center gap-0.5 ${mono ? 'font-mono' : 'font-medium'}`}>
        {value}{mono && <CopyBtn text={value} />}
      </dd>
    </div>
  );
}

type TxState = 'idle' | 'pending' | 'confirmed' | 'error';

function TxResult({ state, hash, msg }: { state: TxState; hash?: string; msg?: string }) {
  if (state === 'idle')      return null;
  if (state === 'pending')   return <p className="mt-2 text-xs text-lime-400 animate-pulse">Sending…</p>;
  if (state === 'confirmed') return <p className="mt-2 text-xs text-lime-400 font-mono break-all">⟳ Sent: {hash}</p>;
  return <p className="mt-2 text-xs text-red-400 break-all">{msg}</p>;
}

const iCls = 'w-full appearance-none rounded-lg border border-white/10 bg-gray-900 px-3 py-2 text-sm text-white placeholder:text-gray-600 font-mono focus:outline-none focus:ring-2 focus:ring-lime-500/40';
const btnLime  = 'inline-flex items-center gap-2 rounded-lg bg-lime-400 px-4 py-2 text-sm font-semibold text-gray-900 shadow-md shadow-lime-500/20 hover:bg-lime-300 transition-colors disabled:opacity-40 disabled:cursor-not-allowed';
const btnAmber = 'inline-flex items-center gap-2 rounded-lg bg-gradient-to-tr from-amber-500 to-amber-400 px-4 py-2 text-sm font-semibold text-white shadow-md shadow-amber-500/20 hover:shadow-amber-500/40 transition-shadow disabled:opacity-40 disabled:cursor-not-allowed';
const btnRed   = 'inline-flex items-center gap-2 rounded-lg bg-gradient-to-tr from-red-600 to-red-400 px-4 py-2 text-sm font-semibold text-white shadow-md shadow-red-500/20 hover:shadow-red-500/40 transition-shadow disabled:opacity-40 disabled:cursor-not-allowed';

function CliCmd({ cmd }: { cmd: string }) {
  return (
    <div className="flex items-center gap-2 rounded-lg border border-white/10 bg-gray-900 px-3 py-2">
      <code className="text-xs font-mono text-lime-400 flex-1 select-all">{cmd}</code>
      <CopyBtn text={cmd} />
    </div>
  );
}

// ─── Validators table with pagination ────────────────────────────────────────

const PAGE_SIZE = 5;

function ValidatorsTable({ validators, totalPower, currentHeight }: {
  validators: StakingValidator[];
  totalPower: number;
  currentHeight: number;
}) {
  const [page, setPage] = useState(0);
  const totalPages = Math.ceil(validators.length / PAGE_SIZE);
  const slice = validators.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE);

  return (
    <GradCard title="Registered Validators" subtitle={`${validators.length} registered total`}>
      {validators.length === 0 ? (
        <div className="py-16 text-center">
          <p className="text-sm text-gray-500">No validators registered yet</p>
          <p className="mt-1 text-xs text-gray-600">Owner must whitelist an address, then it can call register()</p>
        </div>
      ) : (
        <>
          <table className="w-full min-w-[640px] table-auto">
            <thead>
              <tr>
                {['Address', 'Stake', 'Status', 'Power', 'Reward'].map(h => (
                  <th key={h} className="border-b border-white/5 py-3 px-4 text-left">
                    <span className="text-[11px] font-bold uppercase text-gray-500">{h}</span>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {slice.map((v, i) => (
                <tr key={v.evm_addr} className="hover:bg-white/3 transition-colors">
                  <td className={`py-3 px-4 ${i < slice.length - 1 ? 'border-b border-white/5' : ''}`}>
                    <span className="font-mono text-xs text-lime-400">{trunc(v.evm_addr, 8)}</span>
                    {v.moniker && (() => {
                      const m = v.moniker.match(/^\[(.+?)\]\s*(.+)/);
                      return m
                        ? <div className="flex items-center gap-1 mt-0.5">
                            <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/8 text-gray-400 font-semibold">{m[1]}</span>
                            <span className="text-[11px] text-gray-300">{m[2]}</span>
                          </div>
                        : <div className="text-[11px] text-gray-300 mt-0.5">{v.moniker}</div>;
                    })()}
                    {v.website && <a href={v.website} target="_blank" rel="noopener noreferrer" className="text-[10px] text-lime-400/60 hover:text-lime-400 mt-0.5 block truncate max-w-[140px]">{v.website.replace(/^https?:\/\//, '')}</a>}
                  </td>
                  <td className={`py-3 px-4 ${i < slice.length - 1 ? 'border-b border-white/5' : ''}`}>
                    <span className="text-xs font-semibold text-white tabular-nums">{weiToChkDisplay(v.stake)}</span>
                  </td>
                  <td className={`py-3 px-4 ${i < slice.length - 1 ? 'border-b border-white/5' : ''}`}>
                    <StatusBadge v={v} currentHeight={currentHeight} />
                  </td>
                  <td className={`py-3 px-4 ${i < slice.length - 1 ? 'border-b border-white/5' : ''}`}>
                    <span className="text-sm font-bold tabular-nums text-lime-400">{v.voting_power}</span>
                    {v.active && totalPower > 0 && (
                      <span className="ml-1.5 text-[10px] text-gray-500">
                        {((v.voting_power ?? 0) / totalPower * 100).toFixed(1)}%
                      </span>
                    )}
                  </td>
                  <td className={`py-3 px-4 ${i < slice.length - 1 ? 'border-b border-white/5' : ''}`}>
                    <span className="font-mono text-xs text-gray-400">{trunc(v.reward_addr, 8)}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {totalPages > 1 && (
            <div className="flex items-center justify-between px-4 py-3 border-t border-white/5">
              <span className="text-[11px] text-gray-500">
                {page * PAGE_SIZE + 1}–{Math.min((page + 1) * PAGE_SIZE, validators.length)} of {validators.length}
              </span>
              <div className="flex items-center gap-1">
                <button
                  onClick={() => setPage(p => p - 1)}
                  disabled={page === 0}
                  className="px-2.5 py-1 rounded text-xs text-gray-400 hover:text-white hover:bg-white/8 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                >← Prev</button>
                <span className="text-[11px] text-gray-600 px-1">{page + 1} / {totalPages}</span>
                <button
                  onClick={() => setPage(p => p + 1)}
                  disabled={page >= totalPages - 1}
                  className="px-2.5 py-1 rounded text-xs text-gray-400 hover:text-white hover:bg-white/8 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                >Next →</button>
              </div>
            </div>
          )}
        </>
      )}
    </GradCard>
  );
}

// ─── Node wallet bar ──────────────────────────────────────────────────────────

function NodeWalletBar({ wallet }: { wallet: NodeWalletResponse | null }) {
  if (!wallet) return null;
  if (!wallet.has_key) {
    return (
      <div className="flex items-center gap-3 px-4 py-3 rounded-xl border border-amber-500/20 bg-amber-500/5">
        <span className="text-xs text-amber-400">Node EVM wallet not generated yet. Start chakram-node to create it automatically.</span>
      </div>
    );
  }
  return (
    <div className="flex items-center gap-3 px-4 py-3 rounded-xl border border-lime-500/20 bg-lime-500/5">
      <span className="size-2 rounded-full bg-lime-400" />
      <div className="flex flex-col">
        <span className="text-[10px] text-gray-500 uppercase tracking-wide font-semibold">Node EVM Wallet</span>
        <span className="text-xs font-mono text-lime-400">{wallet.address}</span>
      </div>
      <span className="text-xs text-gray-400 ml-auto">{weiToChkDisplay(wallet.balance_wei)}</span>
    </div>
  );
}

// ─── Validator panel ──────────────────────────────────────────────────────────

function ValidatorPanel({ contractAddr, minStakeWei, validators, totalPower, nodeWallet, currentHeight, onRefresh }: {
  contractAddr?: string;
  minStakeWei?: string;
  validators: StakingValidator[];
  totalPower: number;
  nodeWallet: NodeWalletResponse | null;
  currentHeight: number;
  onRefresh: () => void;
}) {
  const [rewardAddr,   setReward]      = useState('');
  const [stakeAmt,     setStakeAmt]    = useState('');
  const [pubKey,       setPubKey]      = useState('');
  const [nodeName,     setNodeName]    = useState('');
  const [isInstitution,setIsInst]      = useState(false);
  const [instType,     setInstType]    = useState('University');
  const [website,      setWebsite]     = useState('');

  const [regState, setRegState] = useState<TxState>('idle');
  const [regHash,  setRegHash]  = useState('');
  const [regErr,   setRegErr]   = useState('');

  const [ubState,  setUbState]  = useState<TxState>('idle');
  const [ubHash,   setUbHash]   = useState('');
  const [ubErr,    setUbErr]    = useState('');

  const [wdState,  setWdState]  = useState<TxState>('idle');
  const [wdHash,   setWdHash]   = useState('');
  const [wdErr,    setWdErr]    = useState('');

  const [whitelisted, setWhitelisted] = useState<boolean | null>(null);

  useEffect(() => {
    api.validator().then(v => { if (v.pub_key) setPubKey(v.pub_key); }).catch(() => {});
  }, []);

  useEffect(() => {
    if (!stakeAmt && minStakeWei) {
      setStakeAmt(weiToEth(minStakeWei));
    }
  }, [minStakeWei]);

  useEffect(() => {
    if (nodeWallet?.has_key && !rewardAddr) setReward(nodeWallet.address);
  }, [nodeWallet]);

  const nodeAddress = nodeWallet?.has_key ? nodeWallet.address : '';
  const isLocal = window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1';

  useEffect(() => {
    if (!nodeAddress) return;
    const fetch = () => api.stakingWhitelistStatus().then(s => setWhitelisted(s.whitelisted)).catch(() => {});
    fetch();
    const id = setInterval(fetch, 10_000);
    return () => clearInterval(id);
  }, [nodeAddress]);
  const myV = nodeAddress
    ? validators.find(v => v.evm_addr.toLowerCase() === nodeAddress.toLowerCase())
    : undefined;

  const pubKeyHex  = base64ToPubKeyHex(pubKey);
  const canUnbond   = myV?.active && (myV.unbonding_unlock_block ?? 0) === 0;
  const canWithdraw = (myV?.unbonding_unlock_block ?? 0) > 0
    && currentHeight > 0
    && currentHeight >= (myV?.unbonding_unlock_block ?? 0);

  const busy = [regState, ubState, wdState].some(s => s === 'pending');

  async function doRegister(e: FormEvent) {
    e.preventDefault();
    if (!contractAddr || !nodeAddress) return;
    setRegState('pending'); setRegHash(''); setRegErr('');
    try {
      // convert ETH → wei: split on decimal to avoid float precision loss
      let stakeWei: string | undefined;
      if (stakeAmt.trim()) {
        const [whole, frac = ''] = stakeAmt.trim().split('.');
        const fracPadded = frac.slice(0, 18).padEnd(18, '0');
        stakeWei = (BigInt(whole) * BigInt('1000000000000000000') + BigInt(fracPadded)).toString();
      }
      const name = nodeName.trim();
      const moniker = name ? (isInstitution ? `[${instType}] ${name}` : name) : undefined;
      const site = website.trim() || undefined;
      const r = await api.stakingRegister(rewardAddr.trim() || undefined, stakeWei, moniker, site);
      if ('error' in r) throw new Error(r.error);
      setRegState('confirmed'); setRegHash(r.tx_hash);
      setTimeout(onRefresh, 3000);
    } catch (err) { setRegState('error'); setRegErr(extractError(err)); }
  }

  async function doUnbond(e: FormEvent) {
    e.preventDefault();
    setUbState('pending'); setUbHash(''); setUbErr('');
    try {
      const r = await api.stakingUnbond();
      if ('error' in r) throw new Error(r.error);
      setUbState('confirmed'); setUbHash(r.tx_hash);
      setTimeout(onRefresh, 3000);
    } catch (err) { setUbState('error'); setUbErr(extractError(err)); }
  }

  async function doWithdraw(e: FormEvent) {
    e.preventDefault();
    setWdState('pending'); setWdHash(''); setWdErr('');
    try {
      const r = await api.stakingWithdraw();
      if ('error' in r) throw new Error(r.error);
      setWdState('confirmed'); setWdHash(r.tx_hash);
      setTimeout(onRefresh, 3000);
    } catch (err) { setWdState('error'); setWdErr(extractError(err)); }
  }

  return (
    <Card title="Validator Panel" subtitle="Register your node as a validator — signed by the node's own EVM wallet">
      <div className="px-6 py-5 space-y-5">
        {!nodeAddress && (
          <p className="text-xs text-gray-500">
            Node EVM wallet not found. Start chakram-node to generate it automatically.
          </p>
        )}

        {myV && (
          <div className="rounded-lg bg-gray-900 border border-white/8 px-4 py-3">
            <p className="text-[11px] font-bold uppercase text-gray-500 mb-2">Your Registration</p>
            <div className="flex flex-wrap items-center gap-3">
              <StatusBadge v={myV} currentHeight={currentHeight} />
              <span className="text-xs font-semibold text-white tabular-nums">{weiToChkDisplay(myV.stake)} staked</span>
              <span className="text-xs text-gray-400">Power: {myV.voting_power}{totalPower > 0 ? ` (${((myV.voting_power ?? 0) / totalPower * 100).toFixed(1)}% of network)` : ''}</span>
              {myV.unbonding_unlock_block > 0 && (
                <span className="text-xs text-amber-400">
                  Unlocks at block {myV.unbonding_unlock_block}
                  {currentHeight > 0 && myV.unbonding_unlock_block > currentHeight &&
                    ` (~${Math.ceil((myV.unbonding_unlock_block - currentHeight) * 2.5)}s remaining)`}
                </span>
              )}
            </div>
          </div>
        )}

        {nodeAddress && (!myV || (!myV.active && !myV.jailed && (myV.unbonding_unlock_block ?? 0) === 0 && myV.stake === '0')) && (
          <div className="border-t border-white/5 pt-5">
            {!whitelisted ? (
              <div className="rounded-lg border border-white/8 bg-gray-900/60 px-4 py-4 space-y-1">
                <p className="text-xs font-semibold text-white">Not yet whitelisted</p>
                <p className="text-[11px] text-gray-400">
                  Contact the Chakram Foundation to apply as a validator. Your node address is:
                </p>
                <p className="font-mono text-xs text-lime-400 break-all">{nodeAddress}</p>
                <p className="text-[11px] text-gray-600 pt-1">
                  Once whitelisted, this panel will automatically show the {isLocal ? 'registration form' : 'command to register'}.
                </p>
              </div>
            ) : (
            <>
              <p className="text-xs font-semibold text-white mb-1">Register as Validator</p>
              {!isLocal ? (
                <div className="space-y-2">
                  <p className="text-[11px] text-gray-400">Run this command in your node's terminal — it will prompt for your validator name, website, and stake amount:</p>
                  <CliCmd cmd="chakram-node register" />
                </div>
              ) : (
                <form onSubmit={doRegister} className="space-y-3">
                  <div>
                    <label className="block text-[11px] font-bold uppercase text-gray-500 mb-1">Ed25519 Public Key (base64)</label>
                    <input value={pubKey} onChange={e => setPubKey(e.target.value)} placeholder="Auto-filled from running node…" className={iCls} />
                    {pubKey && (
                      <p className={`mt-1 text-[11px] font-mono ${pubKeyHex ? 'text-lime-400' : 'text-red-400'}`}>
                        {pubKeyHex ? `hex: ${pubKeyHex.slice(0, 22)}…` : 'Invalid — must be a 32-byte base64 key'}
                      </p>
                    )}
                  </div>
                  <div>
                    <label className="block text-[11px] font-bold uppercase text-gray-500 mb-1">Reward Address</label>
                    <input value={rewardAddr} onChange={e => setReward(e.target.value)} placeholder={nodeAddress || '0x reward address'} className={iCls} />
                    <p className="mt-1 text-[11px] text-gray-500">Defaults to node EVM wallet. Can be any address (e.g. a cold wallet).</p>
                  </div>
                  <div>
                    <label className="block text-[11px] font-bold uppercase text-gray-500 mb-1">Stake Amount (CHK)</label>
                    <input type="number" step="any" min="0" value={stakeAmt} onChange={e => setStakeAmt(e.target.value)} placeholder="1.0" className={iCls} />
                    <p className="mt-1 text-[11px] text-gray-500">
                      Minimum: {weiToChkDisplay(minStakeWei ?? '0')} &nbsp;·&nbsp; Voting power: <span className="text-lime-400 font-semibold">{Math.floor(parseFloat(stakeAmt) || 0)}</span> <span className="text-gray-600">(1 CHK = 1 power)</span>
                    </p>
                  </div>
                  <div className="border-t border-white/5 pt-4 space-y-3">
                    <p className="text-[11px] font-bold uppercase text-gray-500">Identity <span className="normal-case font-normal text-gray-600">(optional)</span></p>
                    <div>
                      <label className="block text-[11px] font-bold uppercase text-gray-500 mb-1">Node / Validator Name</label>
                      <input value={nodeName} onChange={e => setNodeName(e.target.value)} placeholder="e.g. IIT Delhi Node" className={iCls} maxLength={64} />
                    </div>
                    <div className="flex items-center gap-3">
                      <button
                        type="button"
                        onClick={() => setIsInst(v => !v)}
                        className={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors ${isInstitution ? 'bg-lime-400' : 'bg-white/10'}`}
                      >
                        <span className={`pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow transition-transform ${isInstitution ? 'translate-x-4' : 'translate-x-0'}`} />
                      </button>
                      <span className="text-xs text-gray-400">Part of an institution</span>
                    </div>
                    {isInstitution && (
                      <div>
                        <label className="block text-[11px] font-bold uppercase text-gray-500 mb-1">Institution Type</label>
                        <select value={instType} onChange={e => setInstType(e.target.value)} className={iCls}>
                          <option>University</option>
                          <option>Private Company</option>
                          <option>Government</option>
                          <option>Research Lab</option>
                          <option>NGO</option>
                        </select>
                      </div>
                    )}
                    <div>
                      <label className="block text-[11px] font-bold uppercase text-gray-500 mb-1">Website</label>
                      <input value={website} onChange={e => setWebsite(e.target.value)} placeholder="https://" className={iCls} maxLength={128} />
                    </div>
                  </div>
                  <button type="submit" disabled={!pubKeyHex || !stakeAmt || busy} className={btnLime}>
                    Register
                  </button>
                  <TxResult state={regState} hash={regHash} msg={regErr} />
                </form>
              )}
            </>
            )}
          </div>
        )}

        {myV && (
          <div className="border-t border-white/5 pt-5 grid grid-cols-1 md:grid-cols-2 gap-6">
            <div>
              <p className="text-xs font-semibold text-white mb-2">Start Unbonding</p>
              <p className="text-[11px] text-gray-500 mb-3">Leave the validator set and start the unbonding countdown.</p>
              {isLocal ? (
                <>
                  <form onSubmit={doUnbond}>
                    <button type="submit" disabled={!canUnbond || busy} className={btnAmber}>Start Unbonding</button>
                  </form>
                  {!canUnbond && myV && (
                    <p className="mt-1 text-[11px] text-gray-500">
                      {(myV.unbonding_unlock_block ?? 0) > 0 ? 'Already unbonding' : 'Not currently active'}
                    </p>
                  )}
                  <TxResult state={ubState} hash={ubHash} msg={ubErr} />
                </>
              ) : (
                <>
                  <CliCmd cmd="chakram-node unbond" />
                  {!canUnbond && myV && (
                    <p className="mt-2 text-[11px] text-gray-500">
                      {(myV.unbonding_unlock_block ?? 0) > 0 ? 'Already unbonding' : 'Not currently active'}
                    </p>
                  )}
                </>
              )}
            </div>
            <div>
              <p className="text-xs font-semibold text-white mb-2">Withdraw Stake</p>
              <p className="text-[11px] text-gray-500 mb-3">Reclaim staked CHK after the unbonding period ends.</p>
              {isLocal ? (
                <>
                  <form onSubmit={doWithdraw}>
                    <button type="submit" disabled={!canWithdraw || busy} className={btnRed}>Withdraw</button>
                  </form>
                  {!canWithdraw && myV && (
                    <p className="mt-1 text-[11px] text-gray-500">
                      {(myV.unbonding_unlock_block ?? 0) > 0
                        ? `Unlocks at block ${myV.unbonding_unlock_block}`
                        : 'Start unbonding first'}
                    </p>
                  )}
                  <TxResult state={wdState} hash={wdHash} msg={wdErr} />
                </>
              ) : (
                <>
                  <CliCmd cmd="chakram-node withdraw" />
                  {!canWithdraw && myV && (
                    <p className="mt-2 text-[11px] text-gray-500">
                      {(myV.unbonding_unlock_block ?? 0) > 0
                        ? `Unlocks at block ${myV.unbonding_unlock_block}`
                        : 'Start unbonding first'}
                    </p>
                  )}
                </>
              )}
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function Staking() {
  const [data,          setData]          = useState<StakingResponse | null>(null);
  const [nodeWallet,    setNodeWallet]    = useState<NodeWalletResponse | null>(null);
  const [err,           setErr]           = useState('');
  const [currentHeight, setCurrentHeight] = useState(0);

  async function load() {
    try { setData(await api.staking()); setErr(''); }
    catch (e: unknown) { setErr(String(e)); }
  }

  useEffect(() => {
    load();
    api.nodeWallet().then(setNodeWallet).catch(() => {});
    const id = setInterval(load, 10_000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    const fetch = () => api.status().then(s => setCurrentHeight(s.height)).catch(() => {});
    fetch();
    const id = setInterval(fetch, 5000);
    return () => clearInterval(id);
  }, []);

  if (err && !data) return <p className="text-sm text-red-400">{err}</p>;
  if (!data) return (
    <div className="flex items-center gap-2 text-sm text-gray-400">
      <span className="size-1.5 rounded-full bg-lime-400 animate-pulse" />Loading…
    </div>
  );

  if (!data.enabled) {
    return (
      <div className="rounded-xl border border-amber-500/20 bg-amber-500/10 px-6 py-5">
        <p className="text-sm font-semibold text-amber-400">PoA Mode</p>
        <p className="mt-1 text-sm text-amber-400/80">
          No staking contract configured. Set <code className="font-mono text-xs">CHAKRAM_STAKING_CONTRACT</code> to activate on-chain staking.
        </p>
      </div>
    );
  }

  const activeCount   = data.validators.filter(v => v.active).length;
  const totalStakeWei = data.validators.reduce((acc, v) => acc + BigInt(v.stake || '0'), 0n);
  const totalPower    = data.validators.filter(v => v.active).reduce((acc, v) => acc + (v.voting_power ?? 0), 0);

  return (
    <div className="space-y-6 mt-10">

      <div className="grid gap-y-10 gap-x-6 md:grid-cols-2 xl:grid-cols-3 mb-6">
        <StatCard color="lime" icon={Users} title="Active Validators" value={activeCount}
          footer={<span className="text-[11px] text-gray-400">{data.validators.length} registered total</span>} />
        <StatCard color="teal" icon={Coins} title="Total Staked" value={weiToChkDisplay(totalStakeWei.toString())}
          footer={<span className="text-[11px] text-gray-400">min stake: {weiToChkDisplay(data.min_stake ?? '0')}</span>} />
        <StatCard color="lime" icon={Clock} title="Unbonding Period" value={blocksToHuman(data.unbonding_period ?? 0)}
          footer={<span className="text-[11px] text-gray-400">after startUnbonding()</span>} />
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
        <div className="rounded-xl border border-white/5 bg-gray-900 overflow-hidden">
          <div className="bg-gray-800 px-6 py-5 border-b border-white/5">
            <h6 className="text-sm font-semibold text-white">Contract</h6>
            <p className="mt-0.5 text-[11px] text-gray-500">StakingRegistry.sol on-chain state</p>
          </div>
          <dl>
            <InfoRow label="Address"    value={data.contract_addr ?? '—'} mono />
            <InfoRow label="Owner"      value={data.owner ? trunc(data.owner, 10) : '—'} mono />
            <InfoRow label="Min Stake"  value={weiToChkDisplay(data.min_stake ?? '0')} />
            <InfoRow label="Unbonding"  value={blocksToHuman(data.unbonding_period ?? 0)} />
            <InfoRow label="Validators" value={String(data.validators.length)} />
          </dl>
        </div>

        <div className="xl:col-span-2">
          <ValidatorsTable validators={data.validators} totalPower={totalPower} currentHeight={currentHeight} />
        </div>
      </div>

      <NodeWalletBar wallet={nodeWallet} />
      <ValidatorPanel
        contractAddr={data.contract_addr}
        minStakeWei={data.min_stake}
        validators={data.validators}
        totalPower={totalPower}
        nodeWallet={nodeWallet}
        currentHeight={currentHeight}
        onRefresh={load}
      />
    </div>
  );
}
