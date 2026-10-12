import { useState, useEffect, useRef } from 'react'
import { GetStatus } from '../wailsjs/go/main/App'
import FirstRun from './screens/FirstRun'
import Dashboard from './screens/Dashboard'
import Settings from './screens/Settings'
import Logs from './screens/Logs'

export interface AppStatus {
  processState: string; plainStatus: string; consensusHeight: number; gethHeight: number
  gethPeers: number; consensusPeers: number; hashrate: number; minerAddress: string
  balanceCHK: string; blocksFound: number; syncPct: number; isSynced: boolean
  isMining: boolean; lightMode: boolean; threads: number; networkHashrate: number
  updateAvailable: string; version: string; isConfigured: boolean
}

const defaultStatus: AppStatus = {
  processState: 'Idle', plainStatus: 'Ready', consensusHeight: 0, gethHeight: 0,
  gethPeers: 0, consensusPeers: 0, hashrate: 0, minerAddress: '',
  balanceCHK: '0.000000', blocksFound: 0, syncPct: 0, isSynced: false,
  isMining: false, lightMode: false, threads: 1, networkHashrate: 0,
  updateAvailable: '', version: 'v2.0.0', isConfigured: false,
}

type View = 'firstrun' | 'dashboard' | 'settings' | 'logs'

export default function App() {
  const [status, setStatus] = useState<AppStatus>(defaultStatus)
  const [view, setView] = useState<View>('firstrun')
  const [loaded, setLoaded] = useState(false)
  const cancelledRef = useRef(false)

  // Wait for window.go, do initial load, then poll every 1 s.
  useEffect(() => {
    cancelledRef.current = false
    let pollTimer: ReturnType<typeof setInterval> | null = null

    const fetchStatus = async () => {
      try {
        const s = await GetStatus() as AppStatus
        if (cancelledRef.current) return
        setStatus(s)
        return s
      } catch (_) {
        return null
      }
    }

    const init = async () => {
      if (cancelledRef.current) return
      if (!(window as unknown as Record<string, unknown>)['go']) {
        setTimeout(init, 50)
        return
      }
      const s = await fetchStatus()
      if (cancelledRef.current) return
      if (s) {
        setView(s.isConfigured ? 'dashboard' : 'firstrun')
        setLoaded(true)
        // Poll every 1 s so state machine changes surface quickly
        pollTimer = setInterval(fetchStatus, 1000)
      } else {
        setTimeout(init, 200)
      }
    }

    init()

    return () => {
      cancelledRef.current = true
      if (pollTimer) clearInterval(pollTimer)
    }
  }, [])

  if (!loaded) {
    return (
      <div style={{
        height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: '#0E1116', color: '#8B949E', fontSize: 14,
      }}>
        Loading…
      </div>
    )
  }

  if (view === 'firstrun') return <FirstRun onDone={() => setView('dashboard')} />
  if (view === 'settings') return <Settings onBack={() => setView('dashboard')} onReset={() => setView('firstrun')} />
  if (view === 'logs') return <Logs onBack={() => setView('dashboard')} />

  return <Dashboard status={status} onNavigate={(tab) => setView(tab)} />
}
