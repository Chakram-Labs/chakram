import { useEffect, useState } from 'react';
import { api, ValidatorResponse } from '../api/client';
import { ShieldCheck, ShieldOff, Zap, Coins } from 'lucide-react';
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

export default function Validator() {
  const [data, setData] = useState<ValidatorResponse | null>(null);
  const [err, setErr] = useState('');

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

  return (
    <div className="mt-12">

      {/* Stat cards */}
      <div className="mb-12 grid gap-y-10 gap-x-6 md:grid-cols-2 xl:grid-cols-3">

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
          footer={<span className="text-[11px] text-slate-500 dark:text-gray-400">consensus weight</span>}
        />

        {data.reward_balance !== undefined && (
          <StatCard
            color="teal"
            icon={Coins}
            title="Reward Balance"
            value={reward}
            footer={
              data.reward_address
                ? <span className="font-mono text-[11px] text-slate-500 dark:text-gray-400 truncate block">
                    {data.reward_address.slice(0, 16)}…
                  </span>
                : undefined
            }
          />
        )}
      </div>

      {/* Signing rate */}
      <div className="mb-6">
        <Card title="Signing Rate — Last 100 Blocks">
          <div className="px-6 py-5">
            <SignBar signed={data.signed_last_100} missed={data.missed_last_100} />
          </div>
        </Card>
      </div>

      {/* Validator details table card */}
      <div className="rounded-xl overflow-hidden border border-slate-100 dark:border-gray-700 shadow-sm">
        <div className="bg-gradient-to-tr from-gray-900 to-gray-800 px-6 py-5">
          <h6 className="text-sm font-semibold text-white">Validator Details</h6>
        </div>
        <div className="bg-white dark:bg-gray-800 overflow-x-auto px-0 pt-0 pb-2">
          <table className="w-full table-auto">
            <tbody>
              {([
                ['Address',      data.address,              true],
                ['Public Key',   data.pub_key,              true],
                ['Voting Power', String(data.voting_power), false],
                ...(data.reward_address ? [['Reward Address', data.reward_address, true] as [string, string, boolean]] : []),
              ] as [string, string, boolean][]).map(([label, value, mono], i, arr) => (
                <tr key={label}>
                  <td className={`py-3 px-5 w-40 shrink-0 ${i < arr.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                    <span className="text-[11px] font-bold uppercase text-slate-400 dark:text-gray-500">{label}</span>
                  </td>
                  <td className={`py-3 px-5 ${i < arr.length - 1 ? 'border-b border-slate-50 dark:border-gray-700' : ''}`}>
                    <span className={`text-sm text-slate-700 dark:text-white break-all ${mono ? 'font-mono text-xs' : 'font-medium'}`}>
                      {value}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
