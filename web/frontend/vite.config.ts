import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

const backendURL = process.env.AISCAN_BACKEND_URL || 'http://127.0.0.1:8080'

// Design-system primitives + the terminal view are consumed from the cyber-ui
// submodule (single source of truth for what aiscan contributes upstream). The
// remaining composite views (markdown/viewer) stay vendored under @/ because
// aiscan still diverges them.
const cyberUI = path.resolve(__dirname, './cyber-ui/packages')

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
      '@cyber/ui': path.resolve(cyberUI, 'ui/src'),
      '@cyber/theme': path.resolve(cyberUI, 'theme/src'),
      '@cyber/markdown': path.resolve(cyberUI, 'markdown/src'),
      '@cyber/terminal': path.resolve(cyberUI, 'terminal/src'),
      '@cyber/cstx': path.resolve(cyberUI, 'cstx/src'),
      '@cyber/cstx-easm': path.resolve(cyberUI, 'cstx-easm/src'),
      '@cyber/agent-protocol': path.resolve(__dirname, './src/compat/agent-protocol.ts'),
      '@cyber/viewer': path.resolve(cyberUI, 'viewer/src'),
      '@cyber/ioa': path.resolve(__dirname, './src/compat/ioa.tsx'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: backendURL,
        ws: true,
      },
    },
  },
  build: {
    outDir: '../static',
    emptyOutDir: true,
    // Split stable vendor code into its own chunk so an app-code change doesn't
    // bust the cache for react/radix/i18next too (hashed assets get immutable
    // headers). Does not shrink first-load payload — that's what the lazy
    // terminal split and the viewer tree-shake do.
    chunkSizeWarningLimit: 900,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return
          if (/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/.test(id)) return 'react'
          if (id.includes('@radix-ui')) return 'radix'
          if (/i18next|react-i18next/.test(id)) return 'i18n'
          if (id.includes('lucide-react')) return 'icons'
        },
      },
    },
  },
})
