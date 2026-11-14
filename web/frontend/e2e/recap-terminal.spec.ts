// Use recap-provider.mjs with a freshly built start-server.mjs runtime.
// RECAP_TEST_PROVIDER_URL=http://127.0.0.1:38181 BASE_URL=http://127.0.0.1:38080
import { test, expect, type Page, type APIRequestContext } from '@playwright/test'
import { openNodeTerminal } from './task-ui'

const providerURL = process.env.RECAP_TEST_PROVIDER_URL
const node = process.env.RECAP_TEST_NODE || 'e2e-node'
let since = 0
let browserErrors: string[] = []

test.skip(!providerURL, 'requires the local recap-provider.mjs fixture')
test.beforeEach(async ({ page }) => {
  since = Date.now()
  browserErrors = []
  page.on('pageerror', error => browserErrors.push(error.message))
  await page.addInitScript(() => localStorage.setItem('i18nextLng', 'en'))
  expect((await page.request.post('/api/auth/login', { data: { token: process.env.ACCESS_KEY || 'test-token' } })).ok()).toBe(true)
  await page.goto('/')
  await openNodeTerminal(page, node)
  await expect(page.locator('.xterm-rows')).toContainText('cyber-scan')
  await page.getByRole('textbox', { name: 'Terminal input' }).press('Control+u')
  await page.getByRole('textbox', { name: 'Terminal input' }).press('Control+l')
})

test.afterEach(async ({ page }, info) => {
  await page.screenshot({ path: info.outputPath('recap-terminal.png'), fullPage: true })
  if (await page.locator('.xterm-rows').count()) {
    await info.attach('terminal-screen', { body: await screen(page), contentType: 'text/plain' })
  }
  expect(browserErrors).toEqual([])
})

async function screen(page: Page) {
  return page.locator('.xterm-rows').innerText()
}

async function command(page: Page, text: string) {
  const input = page.getByRole('textbox', { name: 'Terminal input' })
  await input.pressSequentially(text)
  await input.press('Enter')
}

async function calls(request: APIRequestContext, scenario: string) {
  const response = await request.get(providerURL + '/test/requests')
  return (await response.json()).filter((call: any) => call.at >= since && call.kind === 'recap' && call.scenario === scenario)
}

test('CLI recap follows the final answer and preserves a draft and its cursor when delayed', async ({ page, request }) => {
  await command(page, 'RECAP-E2E cli-basic run a local check')
  const rows = page.locator('.xterm-rows')
  await expect(rows).toContainText('✻ 已完成 cli-basic 的本地检查并验证结果。')
  let text = await screen(page)
  expect(text).toContain('Task cli-basic complete.')
  expect(text.indexOf('✻ 已完成 cli-basic')).toBeGreaterThan(text.indexOf('Task cli-basic complete.'))
  expect(text.match(/✻ 已完成 cli-basic/g)).toHaveLength(1)
  expect(text).toMatch(/✻ 已完成 cli-basic 的本地检查并验证结果。 · \d+(?:\.\d+)?(?:ms|s)/)
  expect(text.lastIndexOf('cyber-scan')).toBeGreaterThan(text.indexOf('✻ 已完成 cli-basic'))

  await command(page, 'RECAP-E2E late run a local check')
  try {
    await expect.poll(async () => (await calls(request, 'late')).length).toBe(1)
    await expect(rows).toContainText('Task late complete.')
    await expect(rows).not.toContainText('✻ 已完成 late')
    const input = page.getByRole('textbox', { name: 'Terminal input' })
    await input.pressSequentially('!echo DRAFT_END')
    for (let i = 0; i < 3; i++) await input.press('ArrowLeft')
    await request.post(providerURL + '/test/release?case=late')
    await expect(rows).toContainText('✻ 已完成 late 的本地检查并验证结果。')
    text = await screen(page)
    expect(text.indexOf('✻ 已完成 late')).toBeGreaterThan(text.indexOf('Task late complete.'))
    expect(text.lastIndexOf('!echo DRAFT_END')).toBeGreaterThan(text.indexOf('✻ 已完成 late'))
    await input.pressSequentially('KEPT_')
    await input.press('Enter')
    // Executing the edited draft checks cursor position as well as saved text.
    await expect.poll(async () => (await screen(page)).split('\n').map(line => line.trim())).toContain('DRAFT_KEPT_END')
    const [call] = await calls(request, 'late')
    expect(call.text).not.toContain('PRIVATE_RECAP_THINKING')
    expect(Buffer.byteLength(call.text) / 4).toBeLessThanOrEqual(10_000)
  } finally {
    await request.post(providerURL + '/test/release?case=late')
  }
})

test('CLI accepts a new task without waiting and drops the previous delayed recap', async ({ page, request }) => {
  const rows = page.locator('.xterm-rows')
  await command(page, 'RECAP-E2E late run a local check')
  try {
    await expect.poll(async () => (await calls(request, 'late')).length).toBe(1)
    await expect(rows).toContainText('Task late complete.')
    await command(page, 'RECAP-E2E cli-next run the next check')
    await expect(rows).toContainText('Task cli-next complete.')
  } finally {
    await request.post(providerURL + '/test/release?case=late')
  }
  await expect(rows).toContainText('✻ 已完成 cli-next 的本地检查并验证结果。')
  await expect(rows).not.toContainText('✻ 已完成 late')
  const [call] = await calls(request, 'cli-next')
  expect(call.text).not.toContain('RECAP-E2E late')
  expect(call.text).not.toContain('Task late complete.')

  await command(page, 'RECAP-E2E failure run a local check')
  await expect.poll(async () => (await calls(request, 'failure')).length).toBe(1)
  await expect(rows).toContainText('Task failure complete.')
  await command(page, 'RECAP-E2E cli-recovery run a local check')
  await expect(rows).toContainText('✻ 已完成 cli-recovery 的本地检查并验证结果。')
  await expect(rows).not.toContainText('✻ 已完成 failure')
  await expect(rows).not.toContainText('recap service unavailable')
})
