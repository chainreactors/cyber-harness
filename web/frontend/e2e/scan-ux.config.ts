import { defineConfig } from '@playwright/test'
import { fileURLToPath } from 'node:url'
export default defineConfig({
  testDir: '.', testMatch: 'scan-ux.spec.ts', timeout: 60000, workers: 1,
  reporter: 'list', use: { baseURL: 'http://127.0.0.1:38117', headless: true },
  webServer: { cwd: fileURLToPath(new URL('..', import.meta.url)), command: 'node node_modules/vite/bin/vite.js --host 127.0.0.1 --port 38117 --strictPort', url: 'http://127.0.0.1:38117', reuseExistingServer: false },
})
