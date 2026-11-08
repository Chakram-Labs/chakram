import { useEffect, useState, FormEvent } from 'react';
import { api, StakingResponse, StakingValidator, NodeWalletResponse, trunc } from '../api/client';
import { GradCard, Card, Chip, StatCard } from '../components/ui';
import { ShieldCheck, ShieldOff, Clock, Coins, Users, Copy, Check } from 'lucide-react';
import { base64ToPubKeyHex, weiToChkDisplay } from '../lib/staking';

function weiToEth(wei: string): string {
  try { return (Number(BigInt(wei)) / 1e18).toString(); } catch { return '0'; }
}

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

function StatusBadge({ v }: { v: StakingValidator }) {
  if (v.jailed)                    return <Chip color="red"><ShieldOff size={9} strokeWidth={2.5} /> Jailed</Chip>;
  if (v.unbonding_unlock_time > 0) return <Chip color="amber"><Clock size={9} strokeWidth={2.5} /> Unbonding</Chip>;
  if (v.active)                    return <Chip color="green"><ShieldCheck size={9} strokeWidth={2.5} /> Active</Chip>;
  return <Chip color="gray">Inactive</Chip>;
}

function CopyBtn({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={() => navigator.clipboard.writeText(text).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); })}
      className="ml-1 inline-flex items-center rounded p-0.5 text-gray-600 hover:text-gray-200 transition-colors"
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

const iCls = 'w-full appearance-none rounded-lg border border-white/8 bg-[#161616] px-3 py-2 text-sm text-white placeholder:text-gray-600 font-mono focus:outline-none focus:ring-1 focus:ring-lime-400/30';
const btnLime  = 'inline-flex items-center gap-2 rounded-lg bg-lime-400 px-4 py-2 text-sm font-semibold text-gray-900 hover:bg-lime-300 transition-colors disabled:opacity-40 disabled:cursor-not-allowed';
const btnAmber = 'inline-flex items-center gap-2 rounded-lg border border-amber-400/30 bg-amber-400/10 px-4 py-2 text-sm font-semibold text-amber-400 hover:bg-amber-400/20 transition-colors disabled:opacity-40 disabled:cursor-not-allowed';
const btnRed   = 'inline-flex items-center gap-2 rounded-lg border border-red-500/30 bg-red-500/10 px-4 py-2 text-sm font-semibold text-red-400 hover:bg-red-500/20 transition-colors disabled:opacity-40 disabled:cursor-not-allowed';

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
    <div className="flex items-center gap-3 px-4 py-3 rounded-xl border border-lime-400/20 bg-lime-400/5">
      <span className="size-2 rounded-full bg-lime-400" />
      <div className="flex flex-col">
        <span className="text-[10px] text-gray-500 uppercase tracking-wide font-semibold">Node EVM Wallet</span>
        <span className="text-xs font-mono text-lime-400">{wallet.address}</span>
      </div>
      <span className="text-xs text-gray-400 ml-auto">{weiToChkDisplay(wallet.balance_wei)}</span>
    </div>
  );
}

function ValidatorPanel({ contractAddr, minStakeWei, validators, nodeWallet, onRefresh }: {
  contractAddr?: string;
  minStakeWei?: string;
  validators: StakingValidator[];
  nodeWallet: NodeWalletResponse | null;
  onRefresh: () => void;
}) {
  const [rewardAddr, setReward]  = useState('');
  const [stakeAmt,  setStakeAmt] = useState('');
  const [pubKey,    setPubKey]   = useState('');

  const [regState, setRegState] = useState<TxState>('idle');
  const [regHash,  setRegHash]  = useState('');
  const [regErr,   setRegErr]   = useState('');

  const [ubState,  setUbState]  = useState<TxState>('idle');
  const [ubHash,   setUbHash]   = useState('');
  const [ubErr,    setUbErr]    = useState('');

  const [wdState,  setWdState]  = useState<TxState>('idle');
  const [wdHash,   setWdHash]   = useState('');
  const [wdErr,    setWdErr]    = useState('');

  useEffect(() => {
    api.validator().then(v => { if (v.pub_key) setPubKey(v.pub_key); }).catch(() => {});
  }, []);

  useEffect(() => {
    if (!stakeAmt && minStakeWei) setStakeAmt(weiToEth(minStakeWei));
  }, [minStakeWei]);

  useEffect(() => {
    if (nodeWallet?.has_key && !rewardAddr) setReward(nodeWallet.address);
  }, [nodeWallet]);

  const nodeAddress = nodeWallet?.has_key ? nodeWallet.address : '';
  const myV = nodeAddress
    ? validators.find(v => v.evm_addr.toLowerCase() === nodeAddress.toLowerCase())
    : undefined;

  const pubKeyHex  = base64ToPubKeyHex(pubKey);
  const canUnbond  = myV?.active && (myV.unbonding_unlock_time ?? 0) === 0;
  const canWithdraw = (myV?.unbonding_unlock_time ?? 0) > 0
    && (myV?.unbonding_unlock_time ?? 0) < Date.now() / 1000;

  const busy = [regState, ubState, wdState].some(s => s === 'pending');

  async function doRegister(e: FormEvent) {
    e.preventDefault();
    if (!contractAddr || !nodeAddress) return;
    setRegState('pending'); setRegHash(''); setRegErr('');
    try {
      let stakeWei: string | undefined;
      if (stakeAmt.trim()) {
        const [whole, frac = ''] = stakeAmt.trim().split('.');
        const fracPadded = frac.slice(0, 18).padEnd(18, '0');
        stakeWei = (BigInt(whole) * BigInt('1000000000000000000') + BigInt(fracPadded)).toString();
      }
      const r = await api.stakingRegister(rewardAddr.trim() || undefined, stakeWei);
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
          <div className="rounded-lg bg-[#161616] border border-white/8 px-4 py-3">
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

        {!myV && nodeAddress && (
          <div className="border-t border-white/5 pt-5">
            <p className="text-xs font-semibold text-white mb-1">Register as Validator</p>
            <p className="text-[11px] text-gray-500 mb-4">
              The chain owner must whitelist your EVM address first.
              Run <code className="font-mono bg-[#161616] px-1 rounded">contracts/whitelist.py --address {nodeAddress}</code> on the owner's machine.
            </p>
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
                <p className="mt-1 text-[11px] text-gray-500">Minimum: {weiToChkDisplay(minStakeWei ?? '0')}</p>
              </div>
              <button type="submit" disabled={!pubKeyHex || !stakeAmt || busy} className={btnLime}>
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

export default function Staking() {
  const [data,       setData]       = useState<StakingResponse | null>(null);
  const [nodeWallet, setNodeWallet] = useState<NodeWalletResponse | null>(null);
  const [err,        setErr]        = useState('');

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

  if (err && !data) return <p className="text-sm text-red-400">{err}</p>;
  if (!data) return (
    <div className="flex items-center gap-2 text-sm text-gray-500">
      <span className="size-1.5 rounded-full bg-lime-400 animate-pulse" />Loading…
    </div>
  );

  if (!data.enabled) {
    return (
      <div className="rounded-xl border border-amber-500/20 bg-amber-500/8 px-6 py-5">
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
    <div className="space-y-6 mt-6">

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <StatCard color="lime" icon={Users} title="Active Validators" value={activeCount}
          footer={<span className="text-[11px] text-gray-500">{data.validators.length} registered total</span>} />
        <StatCard color="lime" icon={Coins} title="Total Staked" value={weiToChkDisplay(totalStakeWei.toString())}
          footer={<span className="text-[11px] text-gray-500">min stake: {weiToChkDisplay(data.min_stake ?? '0')}</span>} />
        <StatCard color="amber" icon={Clock} title="Unbonding Period" value={secondsToHuman(data.unbonding_period ?? 0)}
          footer={<span className="text-[11px] text-gray-500">after startUnbonding()</span>} />
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
        <div className="rounded-xl border border-white/8 bg-[#111] overflow-hidden">
          <div className="bg-[#161616] px-6 py-4 border-b border-white/8">
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
                        <span className="text-[11px] font-bold uppercase text-gray-600">{h}</span>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {data.validators.map((v, i) => (
                    <tr key={v.evm_addr} className="hover:bg-white/4 transition-colors">
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
                        <span className="font-mono text-xs text-gray-500">{trunc(v.reward_addr, 8)}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </GradCard>
        </div>
      </div>

      <NodeWalletBar wallet={nodeWallet} />
      <ValidatorPanel
        contractAddr={data.contract_addr}
        minStakeWei={data.min_stake}
        validators={data.validators}
        nodeWallet={nodeWallet}
        onRefresh={load}
      />
    </div>
  );
}
