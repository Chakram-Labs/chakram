interface StatusDotProps {
  state: string
  size?: 'sm' | 'md'
}

export default function StatusDot({ state, size = 'md' }: StatusDotProps) {
  let color = '#8B949E'
  if (state === 'Running') color = '#22C55E'
  else if (state === 'Errored') color = '#EF4444'
  else if (state.startsWith('Starting') || state.startsWith('Waiting') || state === 'ShuttingDown') color = '#F97316'

  const sz = size === 'sm' ? 8 : 10
  return (
    <span
      style={{
        display: 'inline-block',
        width: sz,
        height: sz,
        borderRadius: '50%',
        backgroundColor: color,
        flexShrink: 0,
      }}
    />
  )
}
