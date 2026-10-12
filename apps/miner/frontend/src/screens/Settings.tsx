import { useState, useEffect } from 'react'
import { GetConfig, SaveConfig, ValidateAddress, OpenDataFolder, OpenLogsFolder, ResetData, GetMaxThreads } from '../../wailsjs/go/main/App'

const isMac = typeof navigator !== 'undefined' && navigator.userAgent.includes('Macintosh')

interface Props {
  onBack: () => void
  onReset: () => void
}

export default function Settings({ onBack, onReset }: Props) {
  const [address, setAddress] = useState('')
  const [addressErr, setAddressErr] = useState('')
  const [threads, setThreads] = useState(1)
  const [maxThreads, setMaxThreads] = useState(4)
  const [lightMode, setLightMode] = useState(false)
  const [dataDir, setDataDir] = useState('')
  const [saved, setSaved] = useState(false)
  const [resetConfirm, setResetConfirm] = useState('')
  const [resetErr, setResetErr] = useState('')

  useEffect(() => {
    GetConfig().then((cfg: unknown) => {
      const c = cfg as { minerAddress: string; threads: number; lightMode: boolean; dataDir: string }
      setAddress(c.minerAddress || '')
      setThreads(c.threads || 1)
      setLightMode(c.lightMode || false)
      setDataDir(c.dataDir || '')
    })
    GetMaxThreads().then(setMaxThreads)
  }, [])

  const handleSave = async () => {
    if (address && !(await ValidateAddress(address))) {
      setAddressErr('Invalid address'); return
    }
    setAddressErr('')
    await SaveConfig({ network: 'testnet', minerAddress: address, threads, lightMode, dataDir })
    setSaved(true)
    setTimeout(() => setSaved(false), 2000)
  }

  const handleReset = async () => {
    if (resetConfirm !== 'RESET') { setResetErr('Type RESET to confirm'); return }
    try {
      await ResetData('RESET')
      onReset()
    } catch (e) {
      setResetErr(String(e))
    }
  }

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', background: '#0E1116' }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 12,
        padding: isMac ? '28px 20px 10px' : '10px 20px',
        background: '#161B22',
        borderBottom: '1px solid #21262D',
        flexShrink: 0,
      }}>
        <button onClick={onBack} style={{
          background: 'none', border: 'none', color: '#8B949E',
          cursor: 'pointer', fontSize: 18, padding: 0, lineHeight: 1,
        }}>←</button>
        <span style={{ fontWeight: 600, fontSize: 15 }}>Settings</span>
      </div>

      <div style={{ flex: 1, overflow: 'auto', padding: 24 }}>
        <div style={{ maxWidth: 520 }}>
          {/* Address */}
          <div style={{ marginBottom: 20 }}>
            <label style={{ color: '#8B949E', fontSize: 12, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
              Miner Address
            </label>
            <input
              value={address}
              onChange={e => { setAddress(e.target.value); setAddressErr('') }}
              onBlur={async () => {
                if (address) {
                  const ok = await ValidateAddress(address)
                  setAddressErr(ok ? '' : 'Invalid address')
                }
              }}
              placeholder="0x…"
              style={{
                display: 'block', width: '100%', marginTop: 6,
                padding: '9px 12px', borderRadius: 6,
                border: addressErr ? '1px solid #EF4444' : '1px solid #21262D',
                background: '#161B22', color: '#C9CED6', fontSize: 13,
                fontFamily: 'monospace', outline: 'none',
              }}
            />
            {addressErr && <div style={{ color: '#EF4444', fontSize: 12, marginTop: 4 }}>{addressErr}</div>}
          </div>

          {/* Threads */}
          <div style={{ marginBottom: 20 }}>
            <label style={{ color: '#8B949E', fontSize: 12, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
              Mining Threads: {threads}
            </label>
            <input
              type="range" min={1} max={maxThreads} value={threads}
              onChange={e => setThreads(Number(e.target.value))}
              style={{ display: 'block', width: '100%', marginTop: 8, accentColor: '#C9A227' }}
            />
          </div>

          {/* Light mode */}
          <div style={{ marginBottom: 24 }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer' }}>
              <input
                type="checkbox" checked={lightMode}
                onChange={e => setLightMode(e.target.checked)}
                style={{ accentColor: '#C9A227', width: 16, height: 16 }}
              />
              <div>
                <div style={{ color: '#C9CED6', fontSize: 13 }}>Light mode</div>
                <div style={{ color: '#8B949E', fontSize: 11 }}>256 MB cache instead of 2 GB dataset</div>
              </div>
            </label>
          </div>

          {/* Data folder */}
          <div style={{ marginBottom: 20 }}>
            <label style={{ color: '#8B949E', fontSize: 12, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
              Data Directory
            </label>
            <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
              <div style={{
                flex: 1, padding: '9px 12px', borderRadius: 6,
                border: '1px solid #21262D', background: '#161B22',
                color: '#8B949E', fontSize: 12, fontFamily: 'monospace',
                overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              }}>
                {dataDir || '(default)'}
              </div>
              <button onClick={OpenDataFolder} style={{
                padding: '9px 14px', borderRadius: 6,
                border: '1px solid #21262D', background: '#161B22',
                color: '#C9CED6', fontSize: 13, cursor: 'pointer',
              }}>Open</button>
            </div>
          </div>

          {/* Logs */}
          <div style={{ marginBottom: 28 }}>
            <label style={{ color: '#8B949E', fontSize: 12, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
              Logs
            </label>
            <div style={{ marginTop: 6 }}>
              <button onClick={OpenLogsFolder} style={{
                padding: '8px 14px', borderRadius: 6,
                border: '1px solid #21262D', background: '#161B22',
                color: '#C9CED6', fontSize: 13, cursor: 'pointer',
              }}>Open Logs Folder</button>
            </div>
          </div>

          {/* Save */}
          <button
            onClick={handleSave}
            style={{
              width: '100%', padding: '10px 0', borderRadius: 6, border: 'none',
              background: saved ? '#22C55E20' : '#C9A227',
              color: saved ? '#22C55E' : '#0E1116',
              fontWeight: 600, fontSize: 14, cursor: 'pointer', marginBottom: 32,
              outline: saved ? '1px solid #22C55E60' : 'none',
            }}
          >
            {saved ? '✓ Saved' : 'Save'}
          </button>

          {/* Reset section */}
          <div style={{
            border: '1px solid #EF444440',
            borderRadius: 8, padding: 16,
            background: '#EF444408',
          }}>
            <div style={{ color: '#EF4444', fontWeight: 600, fontSize: 13, marginBottom: 8 }}>
              Reset Chain Data
            </div>
            <div style={{ color: '#8B949E', fontSize: 12, marginBottom: 12 }}>
              Removes the geth and consensus databases. Your config and private keys are NOT affected.
              You will need to re-sync from genesis.
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                value={resetConfirm}
                onChange={e => { setResetConfirm(e.target.value); setResetErr('') }}
                placeholder='Type RESET to confirm'
                style={{
                  flex: 1, padding: '8px 10px', borderRadius: 6,
                  border: '1px solid #21262D', background: '#0E1116',
                  color: '#C9CED6', fontSize: 13, outline: 'none',
                }}
              />
              <button
                onClick={handleReset}
                style={{
                  padding: '8px 16px', borderRadius: 6, border: '1px solid #EF4444',
                  background: 'none', color: '#EF4444', fontSize: 13, cursor: 'pointer',
                }}
              >Reset</button>
            </div>
            {resetErr && <div style={{ color: '#EF4444', fontSize: 12, marginTop: 6 }}>{resetErr}</div>}
          </div>
        </div>
      </div>
    </div>
  )
}
