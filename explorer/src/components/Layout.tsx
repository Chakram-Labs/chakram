import { useState, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Search } from 'lucide-react'
import { api } from '../api/client'

export default function Layout({ children }: { children: React.ReactNode }) {
  const [q, setQ] = useState('')
  const [searching, setSearching] = useState(false)
  const navigate = useNavigate()
  const inputRef = useRef<HTMLInputElement>(null)

  async function doSearch(e: React.FormEvent) {
    e.preventDefault()
    const v = q.trim()
    if (!v) return
    setSearching(true)
    try {
      const r = await api.search(v)
      if (r.type === 'block') navigate(`/block/${r.value}`)
      else if (r.type === 'tx') navigate(`/tx/${r.value}`)
      else if (r.type === 'token') navigate(`/token/${r.value}`)
      else if (r.type === 'contract' || r.type === 'address') navigate(`/address/${r.value}`)
      else navigate(`/search?q=${encodeURIComponent(v)}`)
    } catch {
      navigate(`/search?q=${encodeURIComponent(v)}`)
    } finally {
      setSearching(false)
      setQ('')
    }
  }

  return (
    <div className="min-h-screen flex flex-col">
      {/* Nav */}
      <header className="sticky top-0 z-50 border-b border-white/[0.06] bg-bg/90 backdrop-blur-md">
        <div className="max-w-[1280px] mx-auto px-6 h-14 flex items-center gap-6">
          {/* Logo */}
          <Link to="/" className="shrink-0">
            <img src="/chk-logo-text-icon.png" alt="Chakram" className="h-6 object-contain" />
          </Link>

          {/* Search */}
          <form onSubmit={doSearch} className="flex-1 max-w-xl">
            <div className="relative">
              <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-zinc-500 pointer-events-none" />
              <input
                ref={inputRef}
                value={q}
                onChange={e => setQ(e.target.value)}
                placeholder="Search by block / tx hash / address"
                className="w-full h-9 pl-8 pr-4 rounded-xl bg-s2 border border-white/[0.07] text-sm text-zinc-200 placeholder:text-zinc-600 focus:outline-none focus:border-lime/40 transition-colors"
              />
              {searching && (
                <div className="absolute right-3 top-1/2 -translate-y-1/2 w-3 h-3 border border-lime/40 border-t-lime rounded-full animate-spin" />
              )}
            </div>
          </form>

          {/* Nav links */}
          <nav className="hidden md:flex items-center gap-5 text-[13px] text-zinc-400">
            <Link to="/blocks" className="hover:text-white transition-colors">Blocks</Link>
            <Link to="/txs" className="hover:text-white transition-colors">Txs</Link>
            <Link to="/tokens" className="hover:text-white transition-colors">Tokens</Link>
            <Link to="/validators" className="hover:text-white transition-colors">Validators</Link>
          </nav>
        </div>
      </header>

      {/* Main */}
      <main className="flex-1 max-w-[1280px] mx-auto w-full px-6 py-8">
        {children}
      </main>

      {/* Footer */}
      <footer className="border-t border-white/[0.06] py-6 text-center text-xs text-zinc-600">
        Chakram Explorer · Chain ID 11640
      </footer>
    </div>
  )
}
