import { useState, useEffect } from 'react'
import { GetMaxThreads, DetectLightMode, SaveConfig, StartMining, ValidateAddress } from '../../wailsjs/go/main/App'

interface Props {
  onDone: () => void
}

export default function FirstRun({ onDone }: Props) {
  const [address, setAddress] = useState('')
  const [addressError, setAddressError] = useState('')
  const [threads, setThreads] = useState(1)
  const [maxThreads, setMaxThreads] = useState(4)
  const [lightMode, setLightMode] = useState(false)
  const [lightReason, setLightReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    GetMaxThreads().then(n => {
      setMaxThreads(n)
      setThreads(Math.max(1, n - 1))
    })
    DetectLightMode().then((result: unknown) => {
      const [recommended, reason] = result as [boolean, string]
      if (recommended) {
        setLightMode(true)
        setLightReason(reason)
      }
    })
  }, [])

  const validateOnBlur = async () => {
    if (!address) return
    const ok = await ValidateAddress(address)
    setAddressError(ok ? '' : 'Invalid address — paste a valid 0x… Ethereum address')
  }

  const handleStart = async () => {
    if (!address) { setAddressError('Address is required'); return }
    const ok = await ValidateAddress(address)
    if (!ok) { setAddressError('Invalid address'); return }
    setSaving(true)
    try {
      await SaveConfig({ network: 'testnet', minerAddress: address, threads, lightMode, dataDir: '' })
      await StartMining()
      onDone()
    } catch (e) {
      setError(String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{
      minHeight: '100vh',
      background: '#0E1116',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      padding: 24,
    }}>
      <div style={{
        background: '#161B22',
        border: '1px solid #C9A22740',
        borderRadius: 12,
        padding: 36,
        width: '100%',
        maxWidth: 480,
      }}>
        {/* Logo + title */}
        <div style={{ textAlign: 'center', marginBottom: 28 }}>
          <div style={{ fontSize: 32, fontWeight: 700, color: '#C9A227', letterSpacing: '-0.5px' }}>
            ⚡ Chakram Miner
          </div>
          <div style={{ color: '#8B949E', fontSize: 13, marginTop: 4 }}>
            v2.0 · Testnet
          </div>
        </div>

        {/* Network (display only) */}
        <div style={{ marginBottom: 20 }}>
          <label style={{ color: '#8B949E', fontSize: 12, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Network
          </label>
          <div style={{
            marginTop: 6,
            display: 'flex',
            gap: 8,
          }}>
            <div style={{
              flex: 1,
              padding: '8px 14px',
              borderRadius: 6,
              border: '1px solid #C9A227',
              background: '#C9A22715',
              color: '#C9A227',
              fontSize: 13,
              fontWeight: 600,
            }}>Testnet</div>
            <div style={{
              flex: 1,
              padding: '8px 14px',
              borderRadius: 6,
              border: '1px solid #21262D',
              color: '#8B949E',
              fontSize: 13,
              cursor: 'not-allowed',
            }}>Mainnet · Coming soon</div>
          </div>
        </div>

        {/* Address */}
        <div style={{ marginBottom: 20 }}>
          <label style={{ color: '#8B949E', fontSize: 12, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Miner Address
          </label>
          <input
            value={address}
            onChange={e => { setAddress(e.target.value); setAddressError('') }}
            onBlur={validateOnBlur}
            placeholder="0x…"
            style={{
              display: 'block',
              width: '100%',
              marginTop: 6,
              padding: '9px 12px',
              borderRadius: 6,
              border: addressError ? '1px solid #EF4444' : '1px solid #21262D',
              background: '#0E1116',
              color: '#C9CED6',
              fontSize: 13,
              fontFamily: 'monospace',
              outline: 'none',
            }}
          />
          {addressError && (
            <div style={{ color: '#EF4444', fontSize: 12, marginTop: 4 }}>{addressError}</div>
          )}
        </div>

        {/* Threads */}
        <div style={{ marginBottom: 20 }}>
          <label style={{ color: '#8B949E', fontSize: 12, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Mining Threads: {threads}
          </label>
          <input
            type="range"
            min={1}
            max={maxThreads}
            value={threads}
            onChange={e => setThreads(Number(e.target.value))}
            style={{ display: 'block', width: '100%', marginTop: 8, accentColor: '#C9A227' }}
          />
          <div style={{ display: 'flex', justifyContent: 'space-between', color: '#8B949E', fontSize: 11, marginTop: 2 }}>
            <span>1</span><span>{maxThreads}</span>
          </div>
        </div>

        {/* Light mode */}
        <div style={{ marginBottom: 24 }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={lightMode}
              onChange={e => setLightMode(e.target.checked)}
              style={{ accentColor: '#C9A227', width: 16, height: 16 }}
            />
            <div>
              <div style={{ color: '#C9CED6', fontSize: 13, fontWeight: 500 }}>Light mode</div>
              <div style={{ color: '#8B949E', fontSize: 11 }}>
                {lightReason || 'Uses 256 MB cache instead of 2 GB dataset'}
              </div>
            </div>
          </label>
        </div>

        {error && (
          <div style={{
            background: '#EF444420',
            border: '1px solid #EF4444',
            borderRadius: 6,
            padding: '8px 12px',
            color: '#EF4444',
            fontSize: 13,
            marginBottom: 16,
          }}>{error}</div>
        )}

        <button
          onClick={handleStart}
          disabled={saving}
          style={{
            width: '100%',
            padding: '11px 0',
            borderRadius: 8,
            border: 'none',
            background: saving ? '#21262D' : '#C9A227',
            color: saving ? '#8B949E' : '#0E1116',
            fontSize: 14,
            fontWeight: 700,
            cursor: saving ? 'not-allowed' : 'pointer',
            transition: 'background 0.15s',
          }}
        >
          {saving ? 'Starting…' : 'Start Mining'}
        </button>
      </div>
    </div>
  )
}
