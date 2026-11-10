import { useEffect, useState } from 'react'
import { api, type Validator } from '../api/client'
import { Card, CardHeader, Spinner, ErrorMsg, Chip } from '../components/ui'
import { Shield, Globe, Coins, Key, Award } from 'lucide-react'

function formatStake(wei?: string): string {
  if (!wei || wei === '0') return '—'
  try {
    const chk = Number(BigInt(wei)) / 1e18
    return chk.toLocaleString(undefined, { maximumFractionDigits: 2 }) + ' CHK'
  } catch { return wei }
}

function trunc(s: string, n = 8) {
  if (!s) return '—'
  return s.length <= n * 2 + 2 ? s : `${s.slice(0, n)}…${s.slice(-n)}`
}

function ValidatorCard({ v, rank }: { v: Validator; rank: number }) {
  const name = v.moniker || trunc(v.address, 8)
  const hasProfile = !!(v.moniker || v.website || v.evm_addr)

  return (
    <div className="rounded-xl border border-white/[0.08] bg-[#0e0e0e] p-5 space-y-4">
      {/* Header */}
      <div className="flex items-start gap-3">
        <div className="w-9 h-9 rounded-lg bg-lime/10 border border-lime/20 flex items-center justify-center shrink-0">
          <span className="text-lime text-sm font-bold">{rank}</span>
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-semibold text-white text-sm truncate">{name}</span>
            {v.jailed
              ? <Chip color="red">Jailed</Chip>
              : v.active
                ? <Chip color="lime">Active</Chip>
                : <Chip color="zinc">Inactive</Chip>
            }
          </div>
          {v.website && (
            <a href={v.website.startsWith('http') ? v.website : `https://${v.website}`}
               target="_blank" rel="noopener noreferrer"
               className="inline-flex items-center gap-1 text-[11px] text-zinc-500 hover:text-lime transition-colors mt-0.5">
              <Globe size={10} />
              {v.website.replace(/^https?:\/\//, '')}
            </a>
          )}
        </div>
        <div className="text-right shrink-0">
          <div className="text-lg font-bold text-white">{v.voting_power}</div>
          <div className="text-[10px] text-zinc-500 uppercase tracking-wide">power</div>
        </div>
      </div>

      {/* Details grid */}
      <div className="space-y-2 text-[11px]">
        <div className="flex items-center gap-2">
          <Shield size={11} className="text-zinc-500 shrink-0" />
          <span className="text-zinc-500 w-20 shrink-0">Comet addr</span>
          <span className="font-mono text-zinc-300 break-all">{v.address}</span>
        </div>
        {v.evm_addr && (
          <div className="flex items-center gap-2">
            <Key size={11} className="text-zinc-500 shrink-0" />
            <span className="text-zinc-500 w-20 shrink-0">EVM addr</span>
            <span className="font-mono text-zinc-300 break-all">{v.evm_addr}</span>
          </div>
        )}
        {v.reward_addr && v.reward_addr !== v.evm_addr && (
          <div className="flex items-center gap-2">
            <Award size={11} className="text-zinc-500 shrink-0" />
            <span className="text-zinc-500 w-20 shrink-0">Reward addr</span>
            <span className="font-mono text-zinc-300 break-all">{v.reward_addr}</span>
          </div>
        )}
        {v.stake && v.stake !== '0' && (
          <div className="flex items-center gap-2">
            <Coins size={11} className="text-zinc-500 shrink-0" />
            <span className="text-zinc-500 w-20 shrink-0">Staked</span>
            <span className="text-zinc-300">{formatStake(v.stake)}</span>
          </div>
        )}
        {!hasProfile && (
          <div className="flex items-center gap-2">
            <span className="font-mono text-[10px] text-zinc-500 break-all">{v.pub_key?.value?.slice(0, 32)}…</span>
          </div>
        )}
      </div>
    </div>
  )
}

export default function Validators() {
  const [vals, setVals] = useState<Validator[]>([])
  const [height, setHeight] = useState(0)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    api.validators()
      .then(r => { setVals(r.validators ?? []); setHeight(r.height) })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  return (
    <div className="fade-up">
      <div className="flex items-center gap-3 mb-6">
        <Shield size={18} className="text-lime" />
        <h2 className="font-display text-xl font-bold">Validators</h2>
        {height > 0 && <span className="text-xs text-zinc-500">at block #{height.toLocaleString()}</span>}
      </div>
      {err && <ErrorMsg msg={err} />}
      <Card>
        <CardHeader
          title={`Active Validator Set (${vals.length})`}
          subtitle="CometBFT BFT consensus — requires >⅔ of total voting power to commit a block"
        />
        {loading ? <Spinner /> : (
          <div className="p-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {vals.map((v, i) => (
              <ValidatorCard key={v.address} v={v} rank={i + 1} />
            ))}
          </div>
        )}
      </Card>
    </div>
  )
}
