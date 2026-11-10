import { useState } from 'react'
import { Copy, Check } from 'lucide-react'
import { trunc } from '../api/client'

// ── Hash / Address with copy ──────────────────────────────────────────────────

export function Hash({ value, chars = 8, link, className = '' }: {
  value: string; chars?: number; link?: string; className?: string
}) {
  const [copied, setCopied] = useState(false)
  const copy = (e: React.MouseEvent) => {
    e.preventDefault()
    navigator.clipboard.writeText(value)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  const inner = (
    <span className={`font-mono text-[0.8rem] text-zinc-300 ${className}`} title={value}>
      {trunc(value, chars)}
    </span>
  )
  return (
    <span className="inline-flex items-center gap-1.5 group">
      {link ? <a href={link} className="hover:text-lime transition-colors">{inner}</a> : inner}
      <button onClick={copy} className="opacity-0 group-hover:opacity-100 transition-opacity text-zinc-600 hover:text-zinc-400">
        {copied ? <Check size={11} /> : <Copy size={11} />}
      </button>
    </span>
  )
}

// ── Status badge ──────────────────────────────────────────────────────────────

export function StatusBadge({ status }: { status: number }) {
  if (status === 1) return (
    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-lime/10 border border-lime/25 text-lime uppercase tracking-wide">
      Success
    </span>
  )
  if (status === 0) return (
    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-red-500/10 border border-red-500/25 text-red-400 uppercase tracking-wide">
      Failed
    </span>
  )
  return (
    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-zinc-800 border border-white/5 text-zinc-500 uppercase tracking-wide">
      Pending
    </span>
  )
}

// ── Card ──────────────────────────────────────────────────────────────────────

export function Card({ children, className = '' }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={`bg-surface border border-white/[0.07] rounded-2xl ${className}`}>
      {children}
    </div>
  )
}

export function CardHeader({ title, subtitle, action }: { title: string; subtitle?: string; action?: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between px-6 py-4 border-b border-white/[0.07]">
      <div>
        <h3 className="text-sm font-semibold text-white">{title}</h3>
        {subtitle && <p className="text-[11px] text-zinc-500 mt-0.5">{subtitle}</p>}
      </div>
      {action}
    </div>
  )
}

// ── Stat card ─────────────────────────────────────────────────────────────────

export function StatCard({ label, value, sub }: { label: string; value: React.ReactNode; sub?: string }) {
  return (
    <div className="bg-surface border border-white/[0.07] rounded-2xl px-5 py-4">
      <p className="text-[10px] font-bold uppercase tracking-widest text-zinc-500 mb-2">{label}</p>
      <p className="text-xl font-display font-bold text-white">{value}</p>
      {sub && <p className="text-[11px] text-zinc-500 mt-1">{sub}</p>}
    </div>
  )
}

// ── Table ─────────────────────────────────────────────────────────────────────

export function Table({ headers, children }: {
  headers: string[]; children: React.ReactNode; empty?: string
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[600px]">
        <thead>
          <tr>
            {headers.map(h => (
              <th key={h} className="text-left px-5 py-3 text-[10px] font-bold uppercase tracking-widest text-zinc-500 border-b border-white/[0.05]">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {children}
        </tbody>
      </table>
    </div>
  )
}

export function EmptyRow({ cols, msg = 'No data' }: { cols: number; msg?: string }) {
  return (
    <tr>
      <td colSpan={cols} className="py-12 text-center text-sm text-zinc-600">{msg}</td>
    </tr>
  )
}

// ── Pagination ────────────────────────────────────────────────────────────────

export function Pagination({ page, total, limit, onPage }: {
  page: number; total: number; limit: number; onPage: (p: number) => void
}) {
  const pages = Math.ceil(total / limit)
  if (pages <= 1) return null
  return (
    <div className="flex items-center gap-2 px-5 py-3 border-t border-white/[0.05] text-xs text-zinc-500">
      <span className="mr-auto">{total.toLocaleString()} total</span>
      <button
        onClick={() => onPage(page - 1)}
        disabled={page <= 1}
        className="px-3 py-1.5 rounded-lg border border-white/[0.07] hover:border-white/15 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
      >
        ← Prev
      </button>
      <span className="px-2">{page} / {pages}</span>
      <button
        onClick={() => onPage(page + 1)}
        disabled={page >= pages}
        className="px-3 py-1.5 rounded-lg border border-white/[0.07] hover:border-white/15 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
      >
        Next →
      </button>
    </div>
  )
}

// ── Tabs ──────────────────────────────────────────────────────────────────────

export function Tabs({ tabs, active, onChange }: {
  tabs: string[]; active: string; onChange: (t: string) => void
}) {
  return (
    <div className="flex gap-1 p-1 bg-s2 rounded-xl border border-white/[0.07]">
      {tabs.map(t => (
        <button
          key={t}
          onClick={() => onChange(t)}
          className={`px-4 py-1.5 rounded-lg text-xs font-semibold transition-all ${
            active === t
              ? 'bg-surface text-white shadow'
              : 'text-zinc-500 hover:text-zinc-300'
          }`}
        >
          {t}
        </button>
      ))}
    </div>
  )
}

// ── Spinner ───────────────────────────────────────────────────────────────────

export function Spinner() {
  return (
    <div className="flex items-center justify-center py-20">
      <div className="w-5 h-5 border-2 border-lime/30 border-t-lime rounded-full animate-spin" />
    </div>
  )
}

// ── Error ─────────────────────────────────────────────────────────────────────

export function ErrorMsg({ msg }: { msg: string }) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-red-500/20 bg-red-500/8 px-4 py-3 text-sm text-red-400">
      {msg}
    </div>
  )
}

// ── Chip ─────────────────────────────────────────────────────────────────────

export function Chip({ children, color = 'zinc' }: { children: React.ReactNode; color?: 'lime' | 'zinc' | 'amber' | 'red' }) {
  const cls = {
    lime:  'bg-lime/10 border-lime/25 text-lime',
    zinc:  'bg-zinc-800 border-white/5 text-zinc-400',
    amber: 'bg-amber-500/10 border-amber-500/25 text-amber-400',
    red:   'bg-red-500/10 border-red-500/25 text-red-400',
  }[color]
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase tracking-wide border ${cls}`}>
      {children}
    </span>
  )
}

// ── Kv row (detail pages) ─────────────────────────────────────────────────────

export function KvRow({ label, children, border = true }: {
  label: string; children: React.ReactNode; border?: boolean
}) {
  return (
    <div className={`flex flex-wrap items-center justify-between gap-3 px-6 py-3 ${border ? 'border-b border-white/[0.05]' : ''}`}>
      <dt className="text-[11px] font-bold uppercase tracking-widest text-zinc-500 shrink-0 min-w-[160px]">{label}</dt>
      <dd className="text-sm text-white text-right break-all">{children}</dd>
    </div>
  )
}
