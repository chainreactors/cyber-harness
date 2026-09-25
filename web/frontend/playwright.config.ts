import { defineConfig } from '@playwright/test';

const baseURL = process.env.BASE_URL || `http://127.0.0.1:${process.env.CYBER_E2E_PORT || '38080'}`;
const manageServer = !process.env.BASE_URL;
const fixturePort = process.env.CYBER_E2E_FIXTURE_PORT || '38082';

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  webServer: manageServer ? [{
    command: 'node ./e2e/start-server.mjs',
    url: `${baseURL}/health`,
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: 'pipe',
    stderr: 'pipe',
  }, {
    command: `npm run dev -- --host 127.0.0.1 --port ${fixturePort} --strictPort`,
    url: `http://127.0.0.1:${fixturePort}/`,
    env: { CYBER_BACKEND_URL: baseURL },
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: 'pipe',
    stderr: 'pipe',
  }] : undefined,
  use: {
    baseURL,
    headless: true,
    viewport: { width: 1280, height: 720 },
    actionTimeout: 10_000,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
});
