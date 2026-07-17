/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  theme: {
    extend: {
      colors: {
        // Runtime variables make the same semantic utilities work in both
        // warm daylight and the darker night workspace.
        ink: {
          950: 'rgb(var(--dd-ink-950) / <alpha-value>)',
          900: 'rgb(var(--dd-ink-900) / <alpha-value>)',
          850: 'rgb(var(--dd-ink-850) / <alpha-value>)',
          800: 'rgb(var(--dd-ink-800) / <alpha-value>)',
          700: 'rgb(var(--dd-ink-700) / <alpha-value>)',
          600: 'rgb(var(--dd-ink-600) / <alpha-value>)',
          500: 'rgb(var(--dd-ink-500) / <alpha-value>)',
        },
        amber: {
          DEFAULT: 'rgb(var(--dd-amber) / <alpha-value>)',
          bright: 'rgb(var(--dd-amber-bright) / <alpha-value>)',
          dim: 'rgb(var(--dd-amber-dim) / <alpha-value>)',
        },
        signal: {
          green: 'rgb(var(--dd-signal-green) / <alpha-value>)',
          red: 'rgb(var(--dd-signal-red) / <alpha-value>)',
          cyan: 'rgb(var(--dd-signal-cyan) / <alpha-value>)',
        },
        // Existing pages use the slate scale semantically. Reversing it to a
        // warm ink scale lets the complete console move to light mode without
        // a brittle page-by-page color migration.
        slate: {
          100: 'rgb(var(--dd-slate-100) / <alpha-value>)',
          200: 'rgb(var(--dd-slate-200) / <alpha-value>)',
          300: 'rgb(var(--dd-slate-300) / <alpha-value>)',
          400: 'rgb(var(--dd-slate-400) / <alpha-value>)',
          500: 'rgb(var(--dd-slate-500) / <alpha-value>)',
          600: 'rgb(var(--dd-slate-600) / <alpha-value>)',
          700: 'rgb(var(--dd-slate-700) / <alpha-value>)',
          800: 'rgb(var(--dd-slate-800) / <alpha-value>)',
          900: 'rgb(var(--dd-slate-900) / <alpha-value>)',
        },
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', 'SF Mono', 'JetBrains Mono', 'Menlo', 'monospace'],
      },
    },
  },
  plugins: [],
}
