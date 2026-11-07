'use strict';

const API_URL = 'https://node.chakram.one/api/status';

const CHAKRAM_CHAIN = {
  chainId: '0x2D78',  // 11640 in hex
  chainName: 'Chakram',
  nativeCurrency: { name: 'Chakram', symbol: 'CHK', decimals: 18 },
  rpcUrls: ['https://rpc.chakram.one'],
  blockExplorerUrls: ['https://node.chakram.one'],
};

/* ── Nav blur on scroll ── */
(function () {
  const nav = document.getElementById('nav');
  let ticking = false;
  window.addEventListener('scroll', () => {
    if (!ticking) {
      requestAnimationFrame(() => {
        nav.classList.toggle('scrolled', window.scrollY > 20);
        ticking = false;
      });
      ticking = true;
    }
  });
})();

/* ── Scroll reveal ── */
(function () {
  const els = document.querySelectorAll('.reveal');
  if (!els.length) return;

  const io = new IntersectionObserver((entries) => {
    entries.forEach(e => {
      if (e.isIntersecting) {
        e.target.classList.add('visible');
        io.unobserve(e.target);
      }
    });
  }, { threshold: 0.12 });

  els.forEach(el => io.observe(el));
})();

/* ── Live stats ── */
function fmt(n) {
  if (n == null) return '—';
  return Number(n).toLocaleString();
}

function setStatSafe(id, val) {
  const el = document.getElementById(id);
  if (el) el.textContent = val;
}

async function fetchStats() {
  try {
    const [statusRes, stakingRes] = await Promise.all([
      fetch(API_URL, { signal: AbortSignal.timeout(6000) }),
      fetch('https://node.chakram.one/api/staking', { signal: AbortSignal.timeout(6000) }),
    ]);
    if (!statusRes.ok) throw new Error('status ' + statusRes.status);
    const data = await statusRes.json();
    const staking = stakingRes.ok ? await stakingRes.json() : null;

    const height = data.height ?? null;
    const validators = staking?.validators?.filter(v => v.active)?.length ?? null;

    setStatSafe('stat-block', fmt(height));
    setStatSafe('stat-validators', fmt(validators));
    setStatSafe('stat-txs', '—');
    setStatSafe('stat-status', '● Live');
    setStatSafe('hero-block-height', height ? '#' + fmt(height) : '—');
    setStatSafe('hero-validators', fmt(validators));

    document.getElementById('stat-status').style.color = 'var(--lime)';
  } catch {
    setStatSafe('stat-status', '● Syncing');
  }
}

fetchStats();
setInterval(fetchStats, 5000);

/* ── MetaMask: Add Chakram network ── */
async function addNetwork() {
  if (!window.ethereum) {
    alert('MetaMask not found. Install it at metamask.io');
    return;
  }
  try {
    await window.ethereum.request({
      method: 'wallet_addEthereumChain',
      params: [CHAKRAM_CHAIN],
    });
  } catch (err) {
    if (err.code === 4001) return; // user rejected
    if (err.code === -32601) {
      alert('Your wallet does not support automatic network adding.\n\nAdd manually:\n  Network: Chakram\n  RPC: https://rpc.chakram.one\n  Chain ID: 11640\n  Symbol: CHK');
      return;
    }
    console.error('Failed to add network:', err);
  }
}

document.getElementById('add-network-btn')?.addEventListener('click', addNetwork);
document.getElementById('add-network-btn-2')?.addEventListener('click', addNetwork);

/* ── Run-a-node tab switcher ── */
(function () {
  const tabs = document.querySelectorAll('.run-tab');
  if (!tabs.length) return;

  tabs.forEach(tab => {
    tab.addEventListener('click', () => {
      const target = tab.dataset.tab;

      tabs.forEach(t => t.classList.toggle('active', t === tab));

      document.querySelectorAll('.run-panel').forEach(panel => {
        panel.classList.toggle('active', panel.id === 'tab-' + target);
      });
    });
  });
})();

/* ── Mobile nav toggle (simple show/hide) ── */
(function () {
  const toggle = document.getElementById('mobile-toggle');
  const links = document.querySelector('.nav-links');
  if (!toggle || !links) return;

  let open = false;
  toggle.addEventListener('click', () => {
    open = !open;
    links.style.cssText = open
      ? 'display:flex;flex-direction:column;position:absolute;top:var(--nav-h);left:0;right:0;background:var(--bg);border-bottom:1px solid var(--border);padding:var(--space-4) var(--space-6);gap:var(--space-2);'
      : '';
    toggle.textContent = open ? '✕' : '☰';
  });

  // close on link click
  links.addEventListener('click', () => {
    if (open) toggle.click();
  });
})();
