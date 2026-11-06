import { useEffect, useState, FormEvent } from 'react';
import { ethers } from 'ethers';
import { api, StakingResponse, StakingValidator, trunc } from '../api/client';
import { GradCard, Card, Chip, StatCard } from '../components/ui';
import { ShieldCheck, ShieldOff, Clock, Coins, Users, Copy, Check, Wallet } from 'lucide-react';
import { connectWallet, getSignerContract, getEthProvider, base64ToPubKeyHex, weiToChkDisplay } from '../lib/staking';

// ─── helpers ────────────────────────────────────────────────────────────────

function secondsToHuman(s: number): string {
  if (s < 120) return `${s}s`;
  if (s < 7200) return `${Math.round(s / 60)}m`;
  if (s < 172800) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
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

function StatusBadge({ v }: { v: StakingValidator }) {
  if (v.jailed)                  return <Chip color="red"><ShieldOff size={9} strokeWidth={2.5} /> Jailed</Chip>;
  if (v.unbonding_unlock_time > 0) return <Chip color="amber"><Clock size={9} strokeWidth={2.5} /> Unbonding</Chip>;
  if (v.active)                  return <Chip color="green"><ShieldCheck size={9} strokeWidth={2.5} /> Active</Chip>;
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

type TxState = 'idle' | 'pending' | 'submitted' | 'confirmed' | 'error';

function TxResult({ state, hash, msg }: { state: TxState; hash?: string; msg?: string }) {
  if (state === 'idle')      return null;
  if (state === 'pending')   return <p className="mt-2 text-xs text-lime-400 animate-pulse">Waiting for wallet…</p>;
  if (state === 'submitted') return <p className="mt-2 text-xs text-lime-400 animate-pulse font-mono break-all">⟳ Submitted: {hash}</p>;
  if (state === 'confirmed') return <p className="mt-2 text-xs text-lime-400 font-mono break-all">✓ Confirmed: {hash}</p>;
  return <p className="mt-2 text-xs text-red-400 break-all">{msg}</p>;
}

const iCls = 'w-full appearance-none rounded-lg border border-white/10 bg-gray-900 px-3 py-2 text-sm text-white placeholder:text-gray-600 font-mono focus:outline-none focus:ring-2 focus:ring-lime-500/40';
const btnLime   = 'inline-flex items-center gap-2 rounded-lg bg-lime-400 px-4 py-2 text-sm font-semibold text-gray-900 shadow-md shadow-lime-500/20 hover:bg-lime-300 transition-colors disabled:opacity-40 disabled:cursor-not-allowed';
const btnAmber  = 'inline-flex items-center gap-2 rounded-lg bg-gradient-to-tr from-amber-500 to-amber-400 px-4 py-2 text-sm font-semibold text-white shadow-md shadow-amber-500/20 hover:shadow-amber-500/40 transition-shadow disabled:opacity-40 disabled:cursor-not-allowed';
const btnRed    = 'inline-flex items-center gap-2 rounded-lg bg-gradient-to-tr from-red-600 to-red-400 px-4 py-2 text-sm font-semibold text-white shadow-md shadow-red-500/20 hover:shadow-red-500/40 transition-shadow disabled:opacity-40 disabled:cursor-not-allowed';

// ─── Wallet connect bar ───────────────────────────────────────────────────────

function WalletBar({ account, onConnect, loading, error }: {
  account: string;
  onConnect: () => void;
  loading: boolean;
  error: string;
}) {
  if (account) {
    return (
      <div className="flex items-center gap-3 px-4 py-3 rounded-xl border border-lime-500/20 bg-lime-500/5">
        <span className="size-2 rounded-full bg-lime-400" />
        <span className="text-xs font-mono text-lime-400">{account}</span>
        <span className="text-[10px] text-gray-500 ml-auto">Connected</span>
      </div>
    );
  }
  return (
    <div className="flex items-center gap-4 px-4 py-3 rounded-xl border border-white/8 bg-gray-900">
      <span className="text-xs text-gray-400 flex-1">Connect your wallet to interact with the staking contract.</span>
      <button onClick={onConnect} disabled={loading} className={btnLime}>
        <Wallet size={14} />
        {loading ? 'Connecting…' : 'Connect Wallet'}
      </button>
      {error && <p className="text-xs text-red-400">{error}</p>}
    </div>
  );
}

// ─── Validator panel ──────────────────────────────────────────────────────────

function ValidatorPanel({ contractAddr, minStakeWei, validators, account, onRefresh }: {
  contractAddr?: string;
  minStakeWei?: string;
  validators: StakingValidator[];
  account: string;
  onRefresh: () => void;
}) {
  const [pubKey,    setPubKey]    = useState('');
  const [rewardAddr, setReward]   = useState('');
  const [stakeAmt,  setStakeAmt]  = useState('');

  const [regState,  setRegState]  = useState<TxState>('idle');
  const [regHash,   setRegHash]   = useState('');
  const [regErr,    setRegErr]    = useState('');

  const [ubState,   setUbState]   = useState<TxState>('idle');
  const [ubHash,    setUbHash]    = useState('');
  const [ubErr,     setUbErr]     = useState('');

  const [wdState,   setWdState]   = useState<TxState>('idle');
  const [wdHash,    setWdHash]    = useState('');
  const [wdErr,     setWdErr]     = useState('');

  useEffect(() => {
    api.validator().then(v => { if (v.pub_key) setPubKey(v.pub_key); }).catch(() => {});
  }, []);

  useEffect(() => {
    if (!stakeAmt && minStakeWei) {
      try { setStakeAmt(ethers.formatEther(BigInt(minStakeWei))); } catch { /* ignore */ }
    }
  }, [minStakeWei]);

  useEffect(() => {
    if (account && !rewardAddr) setReward(account);
  }, [account]);

  const myV = account
    ? validators.find(v => v.evm_addr.toLowerCase() === account.toLowerCase())
    : undefined;

  const pubKeyHex  = base64ToPubKeyHex(pubKey);
  const canUnbond  = myV?.active && (myV.unbonding_unlock_time ?? 0) === 0;
  const canWithdraw = (myV?.unbonding_unlock_time ?? 0) > 0
    && (myV?.unbonding_unlock_time ?? 0) < Date.now() / 1000;

  const busy = [regState, ubState, wdState].some(s => s === 'pending' || s === 'submitted');

  async function doRegister(e: FormEvent) {
    e.preventDefault();
    if (!contractAddr || !account || !pubKeyHex) return;
    setRegState('pending'); setRegHash(''); setRegErr('');
    try {
      const c = await getSignerContract(contractAddr);
      const value = ethers.parseEther(stakeAmt.trim());
      const tx = await c.register(pubKeyHex, rewardAddr.trim() || account, { value }) as { hash: string; wait: () => Promise<unknown> };
      setRegState('submitted'); setRegHash(tx.hash);
      await tx.wait();
      setRegState('confirmed');
      onRefresh();
    } catch (err) { setRegState('error'); setRegErr(extractError(err)); }
  }

  async function doUnbond(e: FormEvent) {
    e.preventDefault();
    if (!contractAddr || !account) return;
    setUbState('pending'); setUbHash(''); setUbErr('');
    try {
      const c = await getSignerContract(contractAddr);
      const tx = await c.startUnbonding() as { hash: string; wait: () => Promise<unknown> };
      setUbState('submitted'); setUbHash(tx.hash);
      await tx.wait();
      setUbState('confirmed');
      onRefresh();
    } catch (err) { setUbState('error'); setUbErr(extractError(err)); }
  }

  async function doWithdraw(e: FormEvent) {
    e.preventDefault();
    if (!contractAddr || !account) return;
    setWdState('pending'); setWdHash(''); setWdErr('');
    try {
      const c = await getSignerContract(contractAddr);
      const tx = await c.withdraw() as { hash: string; wait: () => Promise<unknown> };
      setWdState('submitted'); setWdHash(tx.hash);
      await tx.wait();
      setWdState('confirmed');
      onRefresh();
    } catch (err) { setWdState('error'); setWdErr(extractError(err)); }
  }

  return (
    <Card title="Validator Panel" subtitle="Register your node as a validator — signed by your wallet">
      <div className="px-6 py-5 space-y-5">
        {!account && <p className="text-xs text-gray-500">Connect your wallet above first.</p>}

        {myV && (
          <div className="rounded-lg bg-gray-900 border border-white/8 px-4 py-3">
            <p className="text-[11px] font-bold uppercase text-gray-500 mb-2">Your Registration</p>
            <div className="flex flex-wrap items-center gap-3">
              <StatusBadge v={myV} />
              <span className="text-xs font-semibold text-white tabular-nums">{weiToChkDisplay(myV.stake)} staked</span>
              <span className="text-xs text-gray-400">Power: {myV.voting_power}</span>
              {myV.unbonding_unlock_time > 0 && (
                <span className="text-xs text-amber-400">
                  Unlocks: {new Date(myV.unbonding_unlock_time * 1000).toLocaleString()}
                </span>
              )}
            </div>
          </div>
        )}

        {!myV && (
          <div className="border-t border-white/5 pt-5">
            <p className="text-xs font-semibold text-white mb-4">Register as Validator</p>
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
                <input value={rewardAddr} onChange={e => setReward(e.target.value)} placeholder={account || '0x reward address'} className={iCls} />
              </div>
              <div>
                <label className="block text-[11px] font-bold uppercase text-gray-500 mb-1">Stake Amount (CHK)</label>
                <input type="number" step="any" min="0" value={stakeAmt} onChange={e => setStakeAmt(e.target.value)} placeholder="1.0" className={iCls} />
                <p className="mt-1 text-[11px] text-gray-500">Minimum: {weiToChkDisplay(minStakeWei ?? '0')}</p>
              </div>
              <button type="submit" disabled={!account || !pubKeyHex || !stakeAmt || busy} className={btnLime}>
                Register
              </button>
              <TxResult state={regState} hash={regHash} msg={regErr} />
            </form>
          </div>
        )}

        {myV && (
          <div className="border-t border-white/5 pt-5 grid grid-cols-1 md:grid-cols-2 gap-6">
            <div>
              <p className="text-xs font-semibold text-white mb-2">Start Unbonding</p>
              <p className="text-[11px] text-gray-500 mb-3">Leave the validator set and start the unbonding countdown.</p>
              <form onSubmit={doUnbond}>
                <button type="submit" disabled={!canUnbond || busy} className={btnAmber}>Start Unbonding</button>
              </form>
              {!canUnbond && myV && (
                <p className="mt-1 text-[11px] text-gray-500">
                  {(myV.unbonding_unlock_time ?? 0) > 0 ? 'Already unbonding' : 'Not currently active'}
                </p>
              )}
              <TxResult state={ubState} hash={ubHash} msg={ubErr} />
            </div>
            <div>
              <p className="text-xs font-semibold text-white mb-2">Withdraw Stake</p>
              <p className="text-[11px] text-gray-500 mb-3">Reclaim staked CHK after the unbonding period ends.</p>
              <form onSubmit={doWithdraw}>
                <button type="submit" disabled={!canWithdraw || busy} className={btnRed}>Withdraw</button>
              </form>
              {!canWithdraw && myV && (
                <p className="mt-1 text-[11px] text-gray-500">
                  {(myV.unbonding_unlock_time ?? 0) > 0
                    ? `Unlocks ${new Date((myV.unbonding_unlock_time ?? 0) * 1000).toLocaleString()}`
                    : 'Start unbonding first'}
                </p>
              )}
              <TxResult state={wdState} hash={wdHash} msg={wdErr} />
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function Staking() {
  const [data,    setData]    = useState<StakingResponse | null>(null);
  const [err,     setErr]     = useState('');
  const [account, setAccount] = useState('');
  const [walletLoading, setWalletLoading] = useState(false);
  const [walletError,   setWalletError]   = useState('');

  async function load() {
    try { setData(await api.staking()); setErr(''); }
    catch (e: unknown) { setErr(String(e)); }
  }

  useEffect(() => {
    load();
    const id = setInterval(load, 10_000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    const eth = getEthProvider();
    if (!eth) return;
    const onAccounts = (accounts: unknown) => setAccount((accounts as string[])[0] ?? '');
    const onChain = () => window.location.reload();
    eth.on('accountsChanged', onAccounts);
    eth.on('chainChanged', onChain);
    return () => {
      eth.removeListener('accountsChanged', onAccounts);
      eth.removeListener('chainChanged', onChain);
    };
  }, []);

  async function handleConnect() {
    setWalletLoading(true); setWalletError('');
    try { setAccount(await connectWallet()); }
    catch (e: unknown) { setWalletError(extractError(e)); }
    finally { setWalletLoading(false); }
  }

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

  return (
    <div className="space-y-6 mt-10">

      <div className="grid gap-y-10 gap-x-6 md:grid-cols-2 xl:grid-cols-3 mb-6">
        <StatCard color="lime" icon={Users} title="Active Validators" value={activeCount}
          footer={<span className="text-[11px] text-gray-400">{data.validators.length} registered total</span>} />
        <StatCard color="teal" icon={Coins} title="Total Staked" value={weiToChkDisplay(totalStakeWei.toString())}
          footer={<span className="text-[11px] text-gray-400">min stake: {weiToChkDisplay(data.min_stake ?? '0')}</span>} />
        <StatCard color="lime" icon={Clock} title="Unbonding Period" value={secondsToHuman(data.unbonding_period ?? 0)}
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
            <InfoRow label="Unbonding"  value={secondsToHuman(data.unbonding_period ?? 0)} />
            <InfoRow label="Validators" value={String(data.validators.length)} />
          </dl>
        </div>

        <div className="xl:col-span-2">
          <GradCard title="Registered Validators" subtitle="All addresses that have called register() on the staking contract">
            {data.validators.length === 0 ? (
              <div className="py-16 text-center">
                <p className="text-sm text-gray-500">No validators registered yet</p>
                <p className="mt-1 text-xs text-gray-600">Owner must whitelist an address, then it can call register()</p>
              </div>
            ) : (
              <table className="w-full min-w-[640px] table-auto">
                <thead>
                  <tr>
                    {['EVM Address', 'Stake', 'Status', 'Power', 'Reward Address'].map(h => (
                      <th key={h} className="border-b border-white/5 py-3 px-4 text-left">
                        <span className="text-[11px] font-bold uppercase text-gray-500">{h}</span>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {data.validators.map((v, i) => (
                    <tr key={v.evm_addr} className="hover:bg-white/3 transition-colors">
                      <td className={`py-3 px-4 ${i < data.validators.length - 1 ? 'border-b border-white/5' : ''}`}>
                        <span className="font-mono text-xs text-lime-400">{trunc(v.evm_addr, 8)}</span>
                      </td>
                      <td className={`py-3 px-4 ${i < data.validators.length - 1 ? 'border-b border-white/5' : ''}`}>
                        <span className="text-xs font-semibold text-white tabular-nums">{weiToChkDisplay(v.stake)}</span>
                      </td>
                      <td className={`py-3 px-4 ${i < data.validators.length - 1 ? 'border-b border-white/5' : ''}`}>
                        <StatusBadge v={v} />
                      </td>
                      <td className={`py-3 px-4 ${i < data.validators.length - 1 ? 'border-b border-white/5' : ''}`}>
                        <span className="text-sm font-bold tabular-nums text-lime-400">{v.voting_power}</span>
                      </td>
                      <td className={`py-3 px-4 ${i < data.validators.length - 1 ? 'border-b border-white/5' : ''}`}>
                        <span className="font-mono text-xs text-gray-400">{trunc(v.reward_addr, 8)}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </GradCard>
        </div>
      </div>

      <WalletBar account={account} onConnect={handleConnect} loading={walletLoading} error={walletError} />
      <ValidatorPanel
        contractAddr={data.contract_addr}
        minStakeWei={data.min_stake}
        validators={data.validators}
        account={account}
        onRefresh={load}
      />
    </div>
  );
}
