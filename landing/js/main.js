'use strict';

const API_URL = 'https://node.chakram.one/api/status';

const CHAKRAM_CHAIN = {
  chainId: '0x2D78',  // 11640
  chainName: 'Chakram',
  nativeCurrency: { name: 'Chakram', symbol: 'CHK', decimals: 18 },
  rpcUrls: ['https://rpc.chakram.one'],
  blockExplorerUrls: ['https://node.chakram.one'],
};

/* ── Topbar — scroll state ── */
(function () {
  const bar = document.getElementById('topbar');
  let ticking = false;
  function update() { bar.classList.toggle('scrolled', window.scrollY > 40); }
  update();
  window.addEventListener('scroll', () => {
    if (!ticking) {
      requestAnimationFrame(() => { update(); ticking = false; });
      ticking = true;
    }
  }, { passive: true });
})();

/* ── Scroll reveal ── */
(function () {
  const els = document.querySelectorAll('.reveal');
  if (!els.length) return;
  const io = new IntersectionObserver((entries) => {
    entries.forEach(e => {
      if (e.isIntersecting) { e.target.classList.add('visible'); io.unobserve(e.target); }
    });
  }, { threshold: 0.1 });
  els.forEach(el => io.observe(el));
})();

/* ── Helpers ── */
function fmt(n) {
  if (n == null) return '—';
  return Number(n).toLocaleString();
}

function setEl(id, val) {
  const el = document.getElementById(id);
  if (el) el.textContent = val;
}

/* ── Live stats ── */
async function fetchStats() {
  try {
    const [statusRes, stakingRes] = await Promise.all([
      fetch(API_URL, { signal: AbortSignal.timeout(6000) }),
      fetch('https://node.chakram.one/api/staking', { signal: AbortSignal.timeout(6000) }),
    ]);
    if (!statusRes.ok) throw new Error('status ' + statusRes.status);
    const data = await statusRes.json();
    const staking = stakingRes.ok ? await stakingRes.json() : null;

    const height     = data.height ?? null;
    const validators = staking?.validators?.filter(v => v.active)?.length ?? null;

    // Nav block pill
    setEl('nav-block', height ? fmt(height) : '—');

    // Hero status bar
    setEl('hero-block-height', height ? '#' + fmt(height) : '—');
    setEl('hero-validators', fmt(validators));

    // Live network section
    setEl('stat-block', height ? fmt(height) : '—');
    setEl('stat-validators', fmt(validators));
    setEl('stat-txs', '—');

    const statusEl = document.getElementById('stat-status');
    if (statusEl) {
      statusEl.textContent = '● Live';
      statusEl.style.color = 'var(--lime)';
    }
  } catch {
    setEl('stat-status', '● Offline');
  }
}

fetchStats();
setInterval(fetchStats, 5000);

/* ── Download links from GCS ── */
(async function () {
  const LATEST_URL = 'https://storage.googleapis.com/chakram-bucket/releases/latest.json';

  const map = {
    'dl-linux-amd64':   r => r.linux_amd64,
    'dl-linux-arm64':   r => r.linux_arm64,
    'dl-darwin-arm64':  r => r.darwin_arm64,
    'dl-darwin-amd64':  r => r.darwin_amd64,
    'dl-windows-amd64': r => r.windows_amd64,
    'dl-checksums':     r => r.checksums,
  };

  try {
    const res = await fetch(LATEST_URL, { signal: AbortSignal.timeout(5000) });
    if (!res.ok) throw new Error('status ' + res.status);
    const rel = await res.json();

    const badge = document.getElementById('dl-version');
    if (badge && rel.version) badge.textContent = rel.version;

    for (const [id, getter] of Object.entries(map)) {
      const el  = document.getElementById(id);
      const url = getter(rel);
      if (el && url) {
        el.href = url;
        el.removeAttribute('target');
        el.setAttribute('download', '');
      }
    }

    const cta = document.getElementById('dl-cta');
    if (cta && rel.linux_amd64) {
      cta.href = rel.linux_amd64;
      cta.removeAttribute('target');
      cta.setAttribute('download', '');
      const ctaText = document.getElementById('dl-cta-text');
      if (ctaText && rel.version) ctaText.textContent = `Download chakram-node ${rel.version}`;
    }
  } catch {
    console.info('chakram: falling back to GitHub releases for download links');
  }
})();

/* ── MetaMask: Add Chakram network ── */
async function addNetwork(msgElId) {
  if (!window.ethereum) {
    alert('MetaMask not found. Install it at metamask.io');
    return;
  }
  const msgEl = msgElId ? document.getElementById(msgElId) : null;
  try {
    await window.ethereum.request({
      method: 'wallet_addEthereumChain',
      params: [CHAKRAM_CHAIN],
    });
    if (msgEl) msgEl.textContent = 'Chakram added to your wallet.';
  } catch (err) {
    if (err.code === 4001) return;
    if (err.code === -32601) {
      alert('Your wallet does not support automatic network adding.\n\nAdd manually:\n  Network: Chakram\n  RPC: https://rpc.chakram.one\n  Chain ID: 11640\n  Symbol: CHK');
      return;
    }
    if (msgEl) msgEl.textContent = "Wallet didn't add the network: " + (err?.message || 'request was declined.');
    console.error('Failed to add network:', err);
  }
}

document.getElementById('add-network-btn')  ?.addEventListener('click', () => addNetwork(null));
document.getElementById('add-network-btn-2') ?.addEventListener('click', () => addNetwork('wallet-msg'));
document.getElementById('add-network-btn-3') ?.addEventListener('click', () => addNetwork(null));

/* ── Copy RPC button ── */
document.querySelectorAll('.js-copy-rpc').forEach(btn => {
  btn.addEventListener('click', () => {
    const url = btn.dataset.copy || 'https://rpc.chakram.one';
    const done = () => {
      btn.classList.add('done');
      btn.setAttribute('aria-label', 'Copied!');
      setTimeout(() => {
        btn.classList.remove('done');
        btn.setAttribute('aria-label', 'Copy RPC URL');
      }, 1800);
    };
    if (navigator.clipboard) {
      navigator.clipboard.writeText(url).then(done, done);
    } else {
      done();
    }
  });
});

/* ── Mobile nav toggle ── */
(function () {
  const toggle = document.getElementById('mobile-toggle');
  const links  = document.getElementById('nav-links');
  if (!toggle || !links) return;

  let open = false;

  toggle.addEventListener('click', () => {
    open = !open;
    links.classList.toggle('open', open);
    toggle.innerHTML = open
      ? '<svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M5 5l10 10M15 5L5 15"/></svg>'
      : '<svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M3 6h14M3 14h14"/></svg>';
  });

  links.addEventListener('click', e => {
    if (e.target.tagName === 'A' && open) {
      open = false;
      links.classList.remove('open');
      toggle.innerHTML = '<svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M3 6h14M3 14h14"/></svg>';
    }
  });
})();
