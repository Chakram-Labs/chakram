/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        lime: {
          DEFAULT: '#a3e635',
          dim:     '#84cc16',
        },
        bg:      '#070707',
        surface: '#111111',
        s2:      '#181818',
      },
      fontFamily: {
        display: ['"Inter Tight"', 'Inter', 'system-ui', 'sans-serif'],
        sans:    ['Inter', 'system-ui', 'sans-serif'],
        mono:    ['"SF Mono"', '"Fira Code"', 'monospace'],
      },
    },
  },
  plugins: [],
}


