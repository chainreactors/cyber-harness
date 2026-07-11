import { aspectPreset } from './cyber-ui/packages/theme/src/tailwind-preset.ts'

const preset = aspectPreset()

/** @type {import('tailwindcss').Config} */
export default {
  presets: [preset],
  darkMode: 'class',
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
    // The cyber-ui submodule is consumed as source (vite aliases @cyber/* to
    // its packages), so Tailwind must scan it too. Without this, utilities used
    // ONLY inside cyber-ui — e.g. the DisclosureCard `grid-rows-[0fr]/[1fr]`
    // collapse animation — are never generated and silently no-op (the
    // expand/collapse body never actually collapses).
    './cyber-ui/packages/**/src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {
      ...preset.theme?.extend,
      colors: {
        ...preset.theme?.extend?.colors,
        // Cortex — the AI/agent role (used only where the AI is the actor)
        ai: {
          DEFAULT: 'hsl(var(--ai))',
          foreground: 'hsl(var(--ai-foreground))',
        },
      },
      fontFamily: {
        sans: ['system-ui', '-apple-system', 'PingFang SC', 'Microsoft YaHei', 'Noto Sans SC', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Consolas', 'monospace'],
        display: ['system-ui', '-apple-system', 'PingFang SC', 'Microsoft YaHei', 'sans-serif'],
      },
      boxShadow: {
        ...preset.theme?.extend?.boxShadow,
        // Theme-aware elevation — values live in index.css (:root / .dark) so light
        // gets a cool layered umbra and dark keeps its glassy top-lit black drop.
        soft: 'var(--shadow-soft)',
        lifted: 'var(--shadow-lifted)',
        elevated: 'var(--shadow-elevated)',
        glow: '0 8px 26px -10px hsl(var(--primary) / 0.55)',
        'glow-sm': '0 4px 14px -8px hsl(var(--primary) / 0.55)',
      },
      keyframes: {
        ...preset.theme?.extend?.keyframes,
        'fade-in': {
          from: { opacity: '0', transform: 'translateY(4px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
        'slide-in-right': {
          from: { transform: 'translateX(-100%)' },
          to: { transform: 'translateX(0)' },
        },
        'pulse-glow': {
          '0%, 100%': { opacity: '1' },
          '50%': { opacity: '0.45' },
        },
      },
      animation: {
        ...preset.theme?.extend?.animation,
        'fade-in': 'fade-in 0.3s ease-out',
        'slide-in-right': 'slide-in-right 0.2s ease-out',
        'pulse-glow': 'pulse-glow 2s ease-in-out infinite',
      },
    },
  },
  plugins: [
    require('tailwindcss-animate'),
    require('@tailwindcss/typography'),
  ],
}
