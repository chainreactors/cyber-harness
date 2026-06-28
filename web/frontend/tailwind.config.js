import { aspectPreset } from './cyber-ui/packages/theme/src/tailwind-preset.ts'

const preset = aspectPreset()

/** @type {import('tailwindcss').Config} */
export default {
  presets: [preset],
  darkMode: 'class',
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
    './cyber-ui/packages/*/src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {
      ...preset.theme?.extend,
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
      },
      animation: {
        ...preset.theme?.extend?.animation,
        'fade-in': 'fade-in 0.3s ease-out',
        'slide-in-right': 'slide-in-right 0.2s ease-out',
      },
    },
  },
  plugins: [
    require('tailwindcss-animate'),
    require('@tailwindcss/typography'),
  ],
}
