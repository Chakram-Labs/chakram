interface StatCardProps {
  label: string
  value: string
  sub?: string
  accent?: boolean
}

export default function StatCard({ label, value, sub, accent }: StatCardProps) {
  return (
    <div style={{
      background: '#161B22',
      border: '1px solid #21262D',
      borderRadius: 8,
      padding: '14px 18px',
      display: 'flex',
      flexDirection: 'column',
      gap: 4,
      minWidth: 0,
    }}>
      <div style={{ color: '#8B949E', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        {label}
      </div>
      <div style={{
        color: accent ? '#C9A227' : '#C9CED6',
        fontSize: 22,
        fontWeight: 600,
        fontVariantNumeric: 'tabular-nums',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
      }}>
        {value}
      </div>
      {sub && (
        <div style={{ color: '#8B949E', fontSize: 11 }}>{sub}</div>
      )}
    </div>
  )
}
