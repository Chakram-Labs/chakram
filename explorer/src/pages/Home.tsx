import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type Block, type Tx, type Stats, formatTime, formatWei, trunc } from '../api/client'
import { StatCard, StatusBadge, Spinner, ErrorMsg } from '../components/ui'
import { Layers, Zap } from 'lucide-react'

export default function Home() {
  const [stats, setStats] = useState<Stats | null>(null)
  const [blocks, setBlocks] = useState<Block[]>([])
  const [txs, setTxs] = useState<Tx[]>([])
  const [err, setErr] = useState('')

  async function load() {
    try {
      const [s, b, t] = await Promise.all([
        api.stats(),
        api.blocks(8, 1),
        api.txs(8, 1),
      ])
      setStats(s)
      setBlocks(b.blocks ?? [])
      setTxs(t.txs ?? [])
      setErr('')
    } catch (e: unknown) {
      setErr(String(e))
    }
  }

  useEffect(() => { load(); const id = setInterval(load, 6000); return () => clearInterval(id) }, [])

  if (err && !stats) return (
    <div className="mt-8">
      <ErrorMsg msg={`Explorer API unreachable — ${err}`} />
    </div>
  )

  if (!stats) return <Spinner />

  return (
    <div className="fade-up">
      {/* Hero */}
      <div className="relative mb-10 rounded-3xl overflow-hidden border border-white/[0.07]"
           style={{ background: 'linear-gradient(135deg, #0e0e0e 0%, #111 60%, #0d1a00 100%)' }}>
        <img src="/chk-bg.png" alt=""
             className="absolute inset-0 w-full h-full object-cover opacity-10 mix-blend-luminosity pointer-events-none select-none" />
        <div className="relative z-10 px-8 py-10">
          <div className="flex items-center gap-2 mb-3">
            <span className="live-dot" />
            <span className="text-[11px] font-bold uppercase tracking-widest text-lime">Live</span>
          </div>
          <h1 className="font-display text-3xl font-extrabold text-white mb-1">Chakram Explorer</h1>
          <p className="text-sm text-zinc-400">EVM blockchain · Chain ID 1164 · BFT finality</p>
        </div>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-3 mb-8">
        <StatCard label="Latest Block" value={stats.latest_block.toLocaleString()} />
        <StatCard label="Total Txs" value={stats.total_txs.toLocaleString()} />
        <StatCard label="Avg Block Time" value={`${stats.avg_block_time_s.toFixed(1)}s`} />
        <StatCard label="Tokens" value={stats.total_tokens.toLocaleString()} />
        <StatCard label="Contracts" value={stats.total_contracts.toLocaleString()} />
        <StatCard label="Verified" value={stats.total_verified_contracts.toLocaleString()} />
      </div>

      {/* Indexer sync bar */}
      {stats.indexer_status !== 'synced' && (
        <div className="mb-6 flex items-center gap-3 rounded-xl border border-amber-500/20 bg-amber-500/5 px-4 py-2.5 text-xs text-amber-400">
          <span className="live-dot bg-amber-400" style={{ background: '#fbbf24' }} />
          Indexer syncing — indexed through block {stats.indexed_through.toLocaleString()} of {stats.latest_block.toLocaleString()}
        </div>
      )}

      {/* Latest blocks + txs */}
      <div className="grid xl:grid-cols-2 gap-5">
        {/* Blocks */}
        <div className="bg-surface border border-white/[0.07] rounded-2xl overflow-hidden">
          <div className="flex items-center justify-between px-6 py-4 border-b border-white/[0.07]">
            <div className="flex items-center gap-2">
              <Layers size={14} className="text-lime" />
              <span className="text-sm font-semibold">Latest Blocks</span>
            </div>
            <Link to="/blocks" className="text-[11px] text-zinc-500 hover:text-white transition-colors">View all →</Link>
          </div>
          <div>
            {blocks.length === 0
              ? <p className="py-10 text-center text-sm text-zinc-600">No blocks indexed yet</p>
              : blocks.map((b, i) => (
                <Link key={b.height} to={`/block/${b.height}`}
                      className={`flex items-center gap-4 px-6 py-3 hover:bg-white/[0.02] transition-colors ${i < blocks.length - 1 ? 'border-b border-white/[0.04]' : ''}`}>
                  <div className="w-9 h-9 rounded-xl bg-lime/10 border border-lime/20 flex items-center justify-center shrink-0">
                    <Layers size={13} className="text-lime" />
                  </div>
                  <div className="flex-1 min-w-0">
                    <span className="text-sm font-semibold text-lime">#{b.height.toLocaleString()}</span>
                    <p className="text-[11px] text-zinc-500 mt-0.5">
                      Proposer <span className="font-mono">{trunc(b.proposer || '—', 6)}</span>
                    </p>
                  </div>
                  <div className="text-right shrink-0">
                    <span className="text-[11px] text-zinc-500">{formatTime(b.time, blocks[0]?.time)}</span>
                    <p className="text-[11px] text-zinc-600 mt-0.5">{b.tx_count} txs</p>
                  </div>
                </Link>
              ))
            }
          </div>
        </div>

        {/* Txs */}
        <div className="bg-surface border border-white/[0.07] rounded-2xl overflow-hidden">
          <div className="flex items-center justify-between px-6 py-4 border-b border-white/[0.07]">
            <div className="flex items-center gap-2">
              <Zap size={14} className="text-lime" />
              <span className="text-sm font-semibold">Latest Transactions</span>
            </div>
            <Link to="/txs" className="text-[11px] text-zinc-500 hover:text-white transition-colors">View all →</Link>
          </div>
          <div>
            {txs.length === 0
              ? <p className="py-10 text-center text-sm text-zinc-600">No transactions yet</p>
              : txs.map((tx, i) => (
                <Link key={tx.hash} to={`/tx/${tx.hash}`}
                      className={`flex items-center gap-4 px-6 py-3 hover:bg-white/[0.02] transition-colors ${i < txs.length - 1 ? 'border-b border-white/[0.04]' : ''}`}>
                  <div className="w-9 h-9 rounded-xl bg-s2 border border-white/[0.06] flex items-center justify-center shrink-0">
                    <Zap size={13} className="text-zinc-400" />
                  </div>
                  <div className="flex-1 min-w-0">
                    <span className="font-mono text-xs text-zinc-200">{trunc(tx.hash, 10)}</span>
                    <p className="text-[11px] text-zinc-500 mt-0.5">
                      <span className="font-mono">{trunc(tx.from, 6)}</span>
                      {' → '}
                      <span className="font-mono">{tx.to ? trunc(tx.to, 6) : 'contract'}</span>
                    </p>
                  </div>
                  <div className="text-right shrink-0">
                    <StatusBadge status={tx.status} />
                    <p className="text-[11px] text-zinc-500 mt-1">{formatWei(tx.value)} CHK</p>
                  </div>
                </Link>
              ))
            }
          </div>
        </div>
      </div>
    </div>
  )
}
