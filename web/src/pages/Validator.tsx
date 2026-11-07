import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, ValidatorResponse } from '../api/client';
import { ShieldCheck, ShieldOff, Zap, Coins, Key, Wallet } from 'lucide-react';
import { StatCard, Card, Chip } from '../components/ui';

function SignBar({ signed, missed }: { signed: number; missed: number }) {
  const total = signed + missed;
  if (total === 0) return <span className="text-sm text-slate-400 dark:text-gray-500">No data</span>;
  const pct = Math.round((signed / total) * 100);
  const barColor = pct >= 90 ? 'bg-emerald-500' : pct >= 70 ? 'bg-amber-400' : 'bg-red-500';
  const chipColor: 'green' | 'amber' | 'red' = pct >= 90 ? 'green' : pct >= 70 ? 'amber' : 'red';

  return (
    <div>
      <div className="flex items-center gap-4 mb-3">
        <div className="h-2 flex-1 overflow-hidden rounded-full bg-slate-100 dark:bg-gray-700">
          <div
            className={`h-full rounded-full transition-all duration-700 ease-out ${barColor}`}
            style={{ width: `${pct}%` }}
          />
        </div>
        <Chip color={chipColor}>{pct}%</Chip>
      </div>
      <p className="text-xs text-slate-500 dark:text-gray-400">
        <span className="font-semibold text-emerald-600 dark:text-emerald-400">{signed}</span> signed ·{' '}
        <span className="font-semibold text-red-600 dark:text-red-400">{missed}</span> missed
        {' '}in last {total} blocks
      </p>
    </div>
  );
}

function InfoRow({ label, value, mono = false, sublabel }: {
  label: string; value: string; mono?: boolean; sublabel?: string;
}) {
  return (
    <tr>
      <td className="py-3 px-5 w-48 shrink-0 align-top border-b border-slate-50 dark:border-gray-700 last:border-0">
        <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">{label}</span>
        {sublabel && <p className="text-[10px] text-slate-300 dark:text-gray-600 mt-0.5 normal-case font-normal">{sublabel}</p>}
      </td>
      <td className="py-3 px-5 border-b border-slate-50 dark:border-gray-700 last:border-0">
        <span className={`text-sm text-slate-700 dark:text-white break-all ${mono ? 'font-mono text-xs' : 'font-medium'}`}>
          {value || '—'}
        </span>
      </td>
    </tr>
  );
}

function SectionHeader({ icon: Icon, title, subtitle }: { icon: React.ElementType; title: string; subtitle: string }) {
  return (
    <div className="bg-gradient-to-tr from-gray-900 to-gray-800 px-6 py-4 flex items-center gap-3">
      <Icon size={16} className="text-gray-400 shrink-0" />
      <div>
        <h6 className="text-sm font-semibold text-white">{title}</h6>
        <p className="text-[11px] text-gray-500">{subtitle}</p>
      </div>
    </div>
  );
}

export default function Validator() {
  const [data, setData] = useState<ValidatorResponse | null>(null);
  const [err, setErr]   = useState('');

  async function load() {
    try { setData(await api.validator()); setErr(''); }
    catch (e: unknown) { setErr(String(e)); }
  }

  useEffect(() => { load(); const id = setInterval(load, 5000); return () => clearInterval(id); }, []);

  if (err) return <p className="mt-12 text-sm text-red-700 dark:text-red-400">{err}</p>;
  if (!data) return (
    <div className="mt-12 flex items-center gap-2 text-sm text-slate-500 dark:text-gray-400">
      <span className="size-1.5 rounded-full bg-indigo-500 animate-pulse" />
      Loading…
    </div>
  );

  const reward = data.reward_balance
    ? (Number(BigInt(data.reward_balance)) / 1e18).toFixed(4) + ' CHK'
    : '0 CHK';

  const hasEVMWallet = data.reward_address &&
    data.reward_address !== '0x0000000000000000000000000000000000000000';

  return (
    <div className="mt-12 space-y-6">

      {/* Stat cards */}
      <div className="mb-6 grid gap-y-10 gap-x-6 md:grid-cols-2 xl:grid-cols-3">
        <StatCard
          color={data.is_active ? 'green' : 'red'}
          icon={data.is_active ? ShieldCheck : ShieldOff}
          title="Validator Status"
          value={data.is_active ? 'Active' : 'Inactive'}
          footer={
            <Chip color={data.is_active ? 'green' : 'red'}>
              {data.is_active ? 'In validator set' : 'Not in set'}
            </Chip>
          }
        />
        <StatCard
          color="purple"
          icon={Zap}
          title="Voting Power"
          value={data.voting_power.toLocaleString()}
          footer={<span className="text-[11px] text-slate-500 dark:text-gray-400">0 = not registered yet</span>}
        />
        {data.reward_balance !== undefined && (
          <StatCard
            color="teal"
            icon={Coins}
            title="EVM Wallet Balance"
            value={reward}
            footer={
              hasEVMWallet
                ? <span className="font-mono text-[11px] text-slate-500 dark:text-gray-400 truncate block">
                    {data.reward_address!.slice(0, 20)}…
                  </span>
                : <span className="text-[11px] text-amber-400">No wallet generated yet</span>
            }
          />
        )}
      </div>

      {/* Signing rate */}
      <Card title="Signing Rate — Last 100 Blocks">
        <div className="px-6 py-5">
          <SignBar signed={data.signed_last_100} missed={data.missed_last_100} />
        </div>
      </Card>

      {/* CometBFT Identity */}
      <div className="rounded-xl overflow-hidden border border-slate-100 dark:border-gray-700 shadow-sm">
        <SectionHeader
          icon={Key}
          title="CometBFT Consensus Identity"
          subtitle="Used for block signing — not an EVM address"
        />
        <div className="bg-white dark:bg-gray-800 overflow-x-auto">
          <table className="w-full table-auto">
            <tbody>
              <InfoRow
                label="CometBFT Address"
                value={data.address}
                mono
                sublabel="For block signing only"
              />
              <InfoRow
                label="Ed25519 Public Key"
                value={data.pub_key}
                mono
                sublabel="Submitted to staking contract on register()"
              />
              <InfoRow
                label="Voting Power"
                value={String(data.voting_power)}
                sublabel="Becomes non-zero after registration"
              />
            </tbody>
          </table>
        </div>
      </div>

      {/* EVM Wallet */}
      <div className="rounded-xl overflow-hidden border border-slate-100 dark:border-gray-700 shadow-sm">
        <SectionHeader
          icon={Wallet}
          title="Node EVM Wallet"
          subtitle="Ethereum-compatible address — used for staking and rewards"
        />
        <div className="bg-white dark:bg-gray-800 overflow-x-auto">
          <table className="w-full table-auto">
            <tbody>
              {hasEVMWallet ? (
                <>
                  <InfoRow
                    label="EVM Address"
                    value={data.reward_address!}
                    mono
                    sublabel="Must be whitelisted by chain owner before registration"
                  />
                  <InfoRow
                    label="CHK Balance"
                    value={reward}
                    sublabel="Must have ≥ minStake to register"
                  />
                </>
              ) : (
                <tr>
                  <td colSpan={2} className="px-5 py-4">
                    <p className="text-sm text-amber-400">
                      No EVM wallet found. Start chakram-node — it generates one automatically on first run.
                    </p>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        {hasEVMWallet && (
          <div className="bg-white dark:bg-gray-800 px-5 py-3 border-t border-slate-50 dark:border-gray-700">
            <Link
              to="/wallet"
              className="text-xs text-indigo-500 dark:text-indigo-400 hover:underline"
            >
              Export private key → Wallet page
            </Link>
          </div>
        )}
      </div>

    </div>
  );
}
