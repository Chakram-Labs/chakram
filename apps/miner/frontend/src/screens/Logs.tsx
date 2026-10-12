import { useState, useEffect, useRef } from 'react'
import { GetLogs } from '../../wailsjs/go/main/App'

const isMac = typeof navigator !== 'undefined' && navigator.userAgent.includes('Macintosh')

interface Props {
  onBack: () => void
}

export default function Logs({ onBack }: Props) {
  const [source, setSource] = useState<'chakram' | 'geth'>('chakram')
  const [lines, setLines] = useState<string[]>([])
  const [pinned, setPinned] = useState(true)
  const bottomRef = useRef<HTMLDivElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    GetLogs(source, 500).then((l: string[] | null) => setLines(l || []))
  }, [source])

  // Poll for new log lines every 2 s while this screen is open
  useEffect(() => {
    const t = setInterval(() => {
      GetLogs(source, 500).then((l: string[] | null) => setLines(l || []))
    }, 2000)
    return () => clearInterval(t)
  }, [source])

  useEffect(() => {
    if (pinned && bottomRef.current) {
      bottomRef.current.scrollIntoView({ behavior: 'smooth' })
    }
  }, [lines, pinned])

  const handleScroll = () => {
    const el = containerRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    setPinned(atBottom)
  }

  const copyAll = () => navigator.clipboard.writeText(lines.join('\n'))

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', background: '#0E1116' }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 12,
        padding: isMac ? '28px 16px 10px' : '10px 16px',
        background: '#161B22',
        borderBottom: '1px solid #21262D',
        flexShrink: 0,
      }}>
        <button onClick={onBack} style={{
          background: 'none', border: 'none', color: '#8B949E',
          cursor: 'pointer', fontSize: 18, padding: 0, lineHeight: 1,
        }}>←</button>
        <div style={{ display: 'flex', gap: 4 }}>
          {(['chakram', 'geth'] as const).map(s => (
            <button key={s} onClick={() => setSource(s)} style={{
              padding: '4px 12px', borderRadius: 4,
              border: source === s ? '1px solid #C9A227' : '1px solid #21262D',
              background: source === s ? '#C9A22720' : 'none',
              color: source === s ? '#C9A227' : '#8B949E',
              fontSize: 12, cursor: 'pointer', textTransform: 'capitalize',
            }}>{s}</button>
          ))}
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
          <button onClick={() => setPinned(true)} style={{
            padding: '4px 10px', borderRadius: 4,
            border: '1px solid #21262D', background: 'none',
            color: pinned ? '#22C55E' : '#8B949E', fontSize: 12, cursor: 'pointer',
          }}>↓ Follow</button>
          <button onClick={copyAll} style={{
            padding: '4px 10px', borderRadius: 4,
            border: '1px solid #21262D', background: 'none',
            color: '#8B949E', fontSize: 12, cursor: 'pointer',
          }}>Copy</button>
        </div>
      </div>

      {/* Log content */}
      <div
        ref={containerRef}
        onScroll={handleScroll}
        style={{
          flex: 1, overflow: 'auto',
          padding: '12px 16px',
          fontFamily: 'monospace', fontSize: 12,
          color: '#8B949E',
          background: '#0E1116',
        }}
      >
        {lines.length === 0 ? (
          <div style={{ color: '#21262D', marginTop: 20 }}>No log output yet.</div>
        ) : (
          lines.map((line, i) => {
            let color = '#8B949E'
            const l = line.toLowerCase()
            if (l.includes('error') || l.includes('fatal')) color = '#EF4444'
            else if (l.includes('warn')) color = '#F97316'
            else if (l.includes('hashrate') || l.includes('block found')) color = '#22C55E'
            return (
              <div key={i} style={{ color, lineHeight: 1.5, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                {line}
              </div>
            )
          })
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}
