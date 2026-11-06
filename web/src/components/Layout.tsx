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
    <form onSubmit={handleSearch} className="flex items-center gap-2 mt-2">
      <div className="relative flex-1">
        <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500 pointer-events-none" />
        <input
          value={q}
          onChange={e => setQ(e.target.value)}
          placeholder="Search block / tx hash / address"
          className="w-full pl-8 pr-3 py-1.5 rounded-lg bg-gray-800 border border-white/5 text-xs text-white placeholder:text-gray-500 focus:outline-none focus:ring-1 focus:ring-lime-400/40 font-mono"
        />
      </div>
    </form>
  );
}

export default function Layout() {
  const { pathname } = useLocation();
  const { section, title } = getPageInfo(pathname);

  return (
    <div className="min-h-screen bg-gray-950">

      {/* ── Floating rounded sidebar ── */}
      <aside className={[
        'fixed z-50 my-4 ml-4 h-[calc(100vh-32px)] w-72 rounded-xl',
        'bg-gray-900 border border-white/5',
        'overflow-hidden flex flex-col',
        '-translate-x-96 xl:translate-x-0 transition-transform duration-300',
      ].join(' ')}>

        {/* Brand */}
        <div className="flex items-center gap-3 px-6 py-5 border-b border-white/10 shrink-0">
          <img
            src="/chk-logo.png"
            alt="Chakram"
            className="size-9 rounded-lg object-contain shrink-0"
          />
          <div>
            <div className="text-sm font-bold tracking-widest text-white">CHAKRAM</div>
            <div className="text-[11px] text-white/50 mt-0.5">Node Dashboard</div>
          </div>
        </div>

        {/* Nav items */}
        <nav className="flex-1 overflow-y-auto py-4 px-3 min-h-0">
          <p className="px-3 mb-2 text-[10px] font-semibold uppercase tracking-widest text-white/30">
            Pages
          </p>
          <ul className="flex flex-col gap-0.5">
            {NAV.map(({ to, label, Icon }) => (
              <li key={to}>
                <NavLink
                  to={to}
                  end={to === '/'}
                  className={({ isActive }) => [
                    'flex items-center gap-4 px-4 py-2.5 rounded-lg text-sm font-medium',
                    'transition-all duration-150 no-underline',
                    isActive
                      ? 'bg-gradient-to-tr from-lime-500 to-lime-400 text-gray-900 shadow-md shadow-lime-500/25'
                      : 'text-white/60 hover:bg-white/8 hover:text-white',
                  ].join(' ')}
                >
                  <Icon size={16} className="shrink-0" strokeWidth={1.75} />
                  {label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>

        {/* Footer — version */}
        <div className="shrink-0 px-6 py-4 border-t border-white/10">
          <p className="text-[11px] text-white/30">Chakram v3 · devnet</p>
        </div>
      </aside>

      {/* ── Content area ── */}
      <div className="p-4 xl:ml-80">

        {/* Sticky top navbar with breadcrumbs + search */}
        <nav className="bg-gray-900 rounded-xl sticky top-4 z-40 shadow-md shadow-black/20 py-3 px-6 mb-8 border border-white/5">
          <div className="flex items-start justify-between gap-4">
            <div>
              <div className="flex items-center gap-1 text-[11px] text-gray-500">
                <span>dashboard</span>
                <span>/</span>
                <span className="text-gray-300 font-medium capitalize">{section}</span>
              </div>
              <h6 className="text-sm font-bold text-white mt-0.5">{title}</h6>
            </div>
            <div className="w-80 shrink-0">
              <SearchBar />
            </div>
          </div>
        </nav>

        {/* Page content */}
        <Outlet />
      </div>
    </div>
  );
}
