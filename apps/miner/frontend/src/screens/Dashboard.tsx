import { useEffect, useState } from 'react'
import { StopMining, StartMining, SetThreads, SetLightMode, GetRecentBlocks } from '../../wailsjs/go/main/App'
import StatCard from '../components/StatCard'
import StatusDot from '../components/StatusDot'
import PlainStatus from '../components/PlainStatus'
import { AppStatus } from '../App'

const isMac = typeof navigator !== 'undefined' && navigator.userAgent.includes('Macintosh')

interface Block { Number: number; Hash: string; Miner: string; Timestamp: number; Difficulty: string; TxCount: number }

interface Props {
  status: AppStatus
  onNavigate: (tab: 'settings' | 'logs') => void
}

function fmtHashrate(hps: number): string {
  if (hps >= 1000) return (hps / 1000).toFixed(2) + ' KH/s'
  return hps.toFixed(1) + ' H/s'
}

function fmtAge(ts: number): string {
  const secs = Math.floor(Date.now() / 1000) - ts
  if (secs < 60) return secs + 's ago'
  if (secs < 3600) return Math.floor(secs / 60) + 'm ago'
  return Math.floor(secs / 3600) + 'h ago'
}

export default function Dashboard({ status, onNavigate }: Props) {
  const [blocks, setBlocks] = useState<Block[]>([])
  const [maxThreads] = useState(navigator.hardwareConcurrency || 4)
  const [toggling, setToggling] = useState(false)
  const [toggleErr, setToggleErr] = useState('')

  // Refresh blocks whenever the block height changes
  useEffect(() => {
    GetRecentBlocks().then((b: unknown) => setBlocks((b as Block[]) || []))
  }, [status.gethHeight])

  const handleToggle = async () => {
    setToggleErr('')
    setToggling(true)
    try {
      if (status.isMining || status.processState !== 'Idle') {
        await StopMining()
      } else {
        await StartMining()
      }
    } catch (e) {
      setToggleErr(String(e))
    } finally {
      setToggling(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: '#0E1116' }}>
      {/* Top bar */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 12,
        padding: isMac ? '28px 20px 10px' : '10px 20px',
        background: '#161B22',
        borderBottom: '1px solid #21262D',
        flexShrink: 0,
      }}>
        <span style={{ color: '#C9A227', fontWeight: 700, fontSize: 15 }}>⚡ Chakram Miner</span>
        <div style={{ marginLeft: 4, display: 'flex', alignItems: 'center', gap: 6 }}>
          <StatusDot state={status.processState} size="sm" />
          <PlainStatus text={status.plainStatus} />
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 16, alignItems: 'center' }}>
          <span style={{ color: '#8B949E', fontSize: 12 }}>
            #{status.consensusHeight > 0 ? status.consensusHeight : status.gethHeight}
          </span>
          <span style={{
            background: '#21262D',
            borderRadius: 4,
            padding: '2px 8px',
            fontSize: 11,
            color: '#8B949E',
          }}>Testnet</span>
          {status.updateAvailable && (
            <span style={{
              background: '#C9A22720', border: '1px solid #C9A227',
              borderRadius: 4, padding: '2px 8px', fontSize: 11, color: '#C9A227',
            }}>Update {status.updateAvailable}</span>
          )}
        </div>
      </div>

      {/* Sync bar */}
      {!status.isSynced && status.processState === 'Running' && (
        <div style={{ background: '#161B22', padding: '6px 20px', borderBottom: '1px solid #21262D', flexShrink: 0 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, color: '#8B949E', marginBottom: 4 }}>
            <span>Syncing blocks…</span>
            <span>{status.syncPct.toFixed(1)}%</span>
          </div>
          <div style={{ height: 4, background: '#21262D', borderRadius: 2, overflow: 'hidden' }}>
            <div style={{ width: `${status.syncPct}%`, height: '100%', background: '#F97316', transition: 'width 0.5s' }} />
          </div>
        </div>
      )}

      {/* Main content */}
      <div style={{ flex: 1, overflow: 'auto', padding: 20 }}>
        {/* Stat cards */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12, marginBottom: 16 }}>
          <StatCard
            label="Hashrate"
            value={status.hashrate > 0 ? fmtHashrate(status.hashrate)
              : (status.isMining && status.processState === 'Running' ? 'Warming up…' : '—')}
            sub="this session" accent />
          <StatCard label="My Blocks" value={String(status.blocksFound)} sub="all time" />
          <StatCard label="Balance" value={status.balanceCHK || '0.000000'} sub="CHK" />
          <StatCard label="Network H/s" value={status.networkHashrate > 0 ? fmtHashrate(status.networkHashrate) : '—'} />
        </div>

        {/* Two-column row */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 16 }}>
          {/* Mining controls */}
          <div style={{
            background: '#161B22', border: '1px solid #21262D', borderRadius: 8, padding: 18,
          }}>
            <div style={{ color: '#8B949E', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 14 }}>
              Mining Controls
            </div>
            <button
              onClick={handleToggle}
              disabled={toggling}
              style={{
                width: '100%', padding: '9px 0', borderRadius: 6, border: 'none',
                background: toggling ? '#21262D' :
                  (status.processState !== 'Idle' ? '#EF444430' : '#22C55E30'),
                color: toggling ? '#8B949E' :
                  (status.processState !== 'Idle' ? '#EF4444' : '#22C55E'),
                fontWeight: 600, fontSize: 13,
                cursor: toggling ? 'not-allowed' : 'pointer',
                marginBottom: 8,
                outline: status.processState !== 'Idle' ? '1px solid #EF444460' : '1px solid #22C55E60',
              }}
            >
              {toggling ? '…' :
                status.processState !== 'Idle' ? '⏹ Stop' : '▶ Start Mining'}
            </button>
            {toggleErr && (
              <div style={{ color: '#EF4444', fontSize: 11, marginBottom: 8, wordBreak: 'break-word' }}>
                {toggleErr}
              </div>
            )}
            <div style={{ color: '#8B949E', fontSize: 11, marginBottom: 12 }}>
              State: <span style={{ color: '#C9CED6' }}>{status.processState}</span>
              {' · '}{status.plainStatus}
            </div>

            <div style={{ marginBottom: 12 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, color: '#8B949E', marginBottom: 6 }}>
                <span>Threads</span><span>{status.threads}</span>
              </div>
              <input
                type="range" min={1} max={maxThreads} value={status.threads}
                onChange={e => SetThreads(Number(e.target.value))}
                style={{ width: '100%', accentColor: '#C9A227' }}
              />
            </div>

            <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
              <input
                type="checkbox" checked={status.lightMode}
                onChange={e => SetLightMode(e.target.checked)}
                style={{ accentColor: '#C9A227' }}
              />
              <span style={{ fontSize: 13, color: '#C9CED6' }}>Light mode (256 MB)</span>
            </label>
          </div>

          {/* Peers */}
          <div style={{
            background: '#161B22', border: '1px solid #21262D', borderRadius: 8, padding: 18,
          }}>
            <div style={{ color: '#8B949E', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 14 }}>
              Network
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ color: '#8B949E', fontSize: 13 }}>Consensus peers</span>
                <span style={{ color: status.consensusPeers > 0 ? '#22C55E' : '#EF4444', fontSize: 13, fontWeight: 600 }}>
                  {status.consensusPeers}/3
                </span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ color: '#8B949E', fontSize: 13 }}>Geth peers</span>
                <span style={{ color: status.gethPeers > 0 ? '#C9CED6' : '#EF4444', fontSize: 13, fontWeight: 600 }}>
                  {status.gethPeers}
                </span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ color: '#8B949E', fontSize: 13 }}>Miner</span>
                <span style={{ color: '#C9CED6', fontSize: 11, fontFamily: 'monospace' }}>
                  {status.minerAddress ? status.minerAddress.slice(0, 6) + '…' + status.minerAddress.slice(-4) : '—'}
                </span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ color: '#8B949E', fontSize: 13 }}>Chain height</span>
                <span style={{ color: '#C9CED6', fontSize: 13 }}>{status.gethHeight}</span>
              </div>
            </div>
          </div>
        </div>

        {/* Recent blocks table */}
        <div style={{
          background: '#161B22', border: '1px solid #21262D', borderRadius: 8, overflow: 'hidden',
        }}>
          <div style={{
            padding: '12px 16px',
            borderBottom: '1px solid #21262D',
            color: '#8B949E', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.06em',
          }}>
            Recent Blocks
          </div>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr style={{ borderBottom: '1px solid #21262D' }}>
                  {['Height', 'Hash', 'Miner', 'Age', 'Txns'].map(h => (
                    <th key={h} style={{ padding: '8px 16px', textAlign: 'left', color: '#8B949E', fontWeight: 500 }}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {blocks.map(b => {
                  const isMe = status.minerAddress &&
                    b.Miner.toLowerCase() === status.minerAddress.toLowerCase()
                  return (
                    <tr key={b.Hash} style={{
                      borderBottom: '1px solid #21262D',
                      background: isMe ? '#C9A22710' : 'transparent',
                    }}>
                      <td style={{ padding: '7px 16px', color: isMe ? '#C9A227' : '#C9CED6', fontWeight: isMe ? 600 : 400 }}>
                        {b.Number}
                      </td>
                      <td style={{ padding: '7px 16px', fontFamily: 'monospace', color: '#8B949E' }}>
                        {b.Hash.slice(0, 10)}…
                      </td>
                      <td style={{ padding: '7px 16px', fontFamily: 'monospace', fontSize: 12,
                        color: isMe ? '#C9A227' : '#8B949E' }}>
                        {b.Miner.slice(0, 8)}…
                      </td>
                      <td style={{ padding: '7px 16px', color: '#8B949E' }}>{fmtAge(b.Timestamp)}</td>
                      <td style={{ padding: '7px 16px', color: '#8B949E' }}>{b.TxCount}</td>
                    </tr>
                  )
                })}
                {blocks.length === 0 && (
                  <tr><td colSpan={5} style={{ padding: '20px 16px', color: '#8B949E', textAlign: 'center' }}>
                    No blocks yet
                  </td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {/* Bottom nav */}
      <div style={{
        display: 'flex',
        borderTop: '1px solid #21262D',
        background: '#161B22',
        flexShrink: 0,
      }}>
        {(['settings', 'logs'] as const).map(tab => (
          <button key={tab} onClick={() => onNavigate(tab)} style={{
            flex: 1,
            padding: '10px 0',
            background: 'none',
            border: 'none',
            color: '#8B949E',
            fontSize: 13,
            cursor: 'pointer',
            textTransform: 'capitalize',
            borderRight: tab === 'settings' ? '1px solid #21262D' : 'none',
          }}>
            {tab === 'settings' ? '⚙ Settings' : '📋 Logs'}
          </button>
        ))}
      </div>
    </div>
  )
}
