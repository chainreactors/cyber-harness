import { defineConfig } from '@playwright/test';

const baseURL = process.env.BASE_URL || `http://127.0.0.1:${process.env.AISCAN_E2E_PORT || '38080'}`;
const manageServer = !process.env.BASE_URL;

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  webServer: manageServer ? {
    command: 'node ./e2e/start-server.mjs',
    url: `${baseURL}/health`,
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: 'pipe',
    stderr: 'pipe',
  } : undefined,
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
