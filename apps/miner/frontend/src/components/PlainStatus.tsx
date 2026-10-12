interface PlainStatusProps {
  text: string
}

export default function PlainStatus({ text }: PlainStatusProps) {
  let color = '#C9CED6'
  if (text.startsWith('Synced') || text.startsWith('Running')) color = '#22C55E'
  else if (text.startsWith('Syncing')) color = '#F97316'
  else if (text.startsWith('Error') || text.startsWith('No peers')) color = '#EF4444'

  return <span style={{ color, fontSize: 13 }}>{text}</span>
}
