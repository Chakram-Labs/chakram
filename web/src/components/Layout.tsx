import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useState, FormEvent } from 'react';
import {
  LayoutDashboard, ShieldCheck, Layers3, Wallet,
  Network, ScrollText, Settings2, Coins, Search,
} from 'lucide-react';

const NAV = [
  { to: '/',          label: 'Overview',  Icon: LayoutDashboard },
  { to: '/validator', label: 'Validator', Icon: ShieldCheck },
  { to: '/staking',   label: 'Staking',   Icon: Coins },
  { to: '/blocks',    label: 'Blocks',    Icon: Layers3 },
  { to: '/wallet',    label: 'Wallet',    Icon: Wallet },
  { to: '/peers',     label: 'Peers',     Icon: Network },
  { to: '/logs',      label: 'Logs',      Icon: ScrollText },
  { to: '/settings',  label: 'Network',   Icon: Settings2 },
];

function getPageInfo(pathname: string) {
  const seg = pathname.split('/').filter(Boolean);
  if (!seg.length) return { section: 'overview', title: 'Overview' };
  if (seg[0] === 'block' && seg[1]) return { section: 'blocks', title: `Block #${seg[1]}` };
  if (seg[0] === 'tx' && seg[1]) return { section: 'transactions', title: 'Transaction' };
  const name = seg[0].charAt(0).toUpperCase() + seg[0].slice(1);
  return { section: seg[0], title: name };
}

function SearchBar() {
  const [q, setQ] = useState('');
  const navigate = useNavigate();

  function handleSearch(e: FormEvent) {
    e.preventDefault();
    const v = q.trim();
    if (!v) return;
    if (/^\d+$/.test(v) || /^#\d+$/.test(v)) {
      navigate(`/block/${v.replace('#', '')}`);
    } else if (/^0x[0-9a-fA-F]{64}$/.test(v)) {
      navigate(`/tx/${v}`);
    } else if (/^0x[0-9a-fA-F]{40}$/i.test(v)) {
      navigate(`/wallet?addr=${v}`);
    } else if (/^[0-9a-fA-F]{64}$/.test(v)) {
      navigate(`/tx/0x${v}`);
    } else {
      navigate(`/tx/${v}`);
    }
    setQ('');
  }

  return (
    <form onSubmit={handleSearch} className="flex items-center">
      <div className="relative w-full">
        <Search size={12} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-600 pointer-events-none" />
        <input
          value={q}
          onChange={e => setQ(e.target.value)}
          placeholder="Search block / tx / address"
          className="w-full pl-8 pr-3 py-1.5 rounded-lg bg-[#1a1a1a] border border-white/8 text-xs text-white placeholder:text-gray-600 focus:outline-none focus:ring-1 focus:ring-lime-400/30 font-mono"
        />
      </div>
    </form>
  );
}

export default function Layout() {
  const { pathname } = useLocation();
  const { section } = getPageInfo(pathname);

  return (
    <div className="min-h-screen bg-[#0a0a0a]">

      {/* Compact sidebar — 176px */}
      <aside className="fixed z-50 my-4 ml-4 h-[calc(100vh-32px)] w-44 rounded-xl bg-[#111] border border-white/8 overflow-hidden flex flex-col">

        {/* Brand */}
        <div className="flex items-center gap-2.5 px-4 py-4 border-b border-white/8 shrink-0">
          <img
            src="/chk-logo.png"
            alt="Chakram"
            className="size-7 rounded-md object-contain shrink-0"
          />
          <div>
            <div className="text-[11px] font-bold tracking-widest text-white">CHAKRAM</div>
            <div className="text-[10px] text-white/35 mt-0.5">Node Dashboard</div>
          </div>
        </div>

        {/* Nav */}
        <nav className="flex-1 overflow-y-auto py-3 px-2 min-h-0">
          <ul className="flex flex-col gap-0.5">
            {NAV.map(({ to, label, Icon }) => (
              <li key={to}>
                <NavLink
                  to={to}
                  end={to === '/'}
                  className={({ isActive }) => [
                    'flex items-center gap-2.5 px-3 py-2 rounded-lg text-xs font-medium',
                    'transition-all duration-150 no-underline',
                    isActive
                      ? 'bg-lime-400 text-gray-900 font-semibold'
                      : 'text-white/50 hover:bg-white/6 hover:text-white',
                  ].join(' ')}
                >
                  <Icon size={14} className="shrink-0" strokeWidth={1.75} />
                  {label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>

        {/* Footer */}
        <div className="shrink-0 px-4 py-3 border-t border-white/8">
          <p className="text-[10px] text-white/25">Chakram v3 · devnet</p>
        </div>
      </aside>

      {/* Content area — offset by sidebar width + gap */}
      <div className="p-4 ml-48">

        {/* Slim top bar */}
        <nav className="bg-[#111] rounded-xl sticky top-4 z-40 border border-white/8 mb-6 px-5 py-2.5 flex items-center justify-between gap-4">
          <div className="flex items-center gap-1.5 shrink-0">
            <span className="text-[11px] text-gray-600">dashboard /</span>
            <span className="text-[11px] font-semibold text-white capitalize">{section}</span>
          </div>
          <div className="w-72 shrink-0">
            <SearchBar />
          </div>
        </nav>

        <Outlet />
      </div>
    </div>
  );
}
