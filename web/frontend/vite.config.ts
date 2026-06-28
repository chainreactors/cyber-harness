import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

const cyberUI = path.resolve(__dirname, './cyber-ui/packages')

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
      '@aspect/ui': path.resolve(cyberUI, 'ui/src'),
      '@aspect/theme': path.resolve(cyberUI, 'theme/src'),
      '@aspect/markdown': path.resolve(cyberUI, 'markdown/src'),
      '@aspect/viewer': path.resolve(cyberUI, 'viewer/src'),
      '@aspect/terminal': path.resolve(cyberUI, 'terminal/src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        ws: true,
      },
    },
  },
  build: {
    outDir: '../static',
    emptyOutDir: true,
  },
})
