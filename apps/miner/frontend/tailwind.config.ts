import type { Config } from 'tailwindcss'

const config: Config = {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        ink:     '#0E1116',
        ink2:    '#161B22',
        ink3:    '#21262D',
        silver:  '#C9CED6',
        silver2: '#8B949E',
        gold:    '#C9A227',
        cream:   '#F4EDE0',
        ok:      '#22C55E',
        err:     '#EF4444',
        warn:    '#F97316',
      },
      fontFamily: {
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}

export default config
