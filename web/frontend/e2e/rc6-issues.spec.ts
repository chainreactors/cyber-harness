import { expect, test, type Page } from '@playwright/test'

const API_TOKEN = process.env.ACCESS_KEY || 'test-token'

async function openApp(page: Page) {
  const login = await page.request.post('/api/auth/login', { data: { token: API_TOKEN } })
  expect(login.ok()).toBeTruthy()
  await page.goto('/')
  await expect(page.getByRole('button', { name: 'Open settings', exact: true })).toBeVisible()
}

test('quick connect copies on HTTP origins without the Clipboard API', async ({ page }) => {
  await openApp(page)
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.getByRole('button', { name: 'Quick connect an agent' }).click()
  const panel = page.getByRole('dialog', { name: 'Download & connect an agent' })
  await expect(panel.getByText('Token configured', { exact: true })).toBeVisible()
  const row = panel.locator('pre').last().locator('..')
  const command = await row.locator('pre').innerText()
  await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined }))

  const copy = row.getByRole('button')
  await copy.click()
  await expect(copy).toHaveClass(/text-emerald-500/)
  await page.evaluate(() => { delete (navigator as any).clipboard })
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(command)
  await expect(panel.getByRole('alert')).toHaveCount(0)
})

test('quick connect reports clipboard failures', async ({ page }) => {
  await openApp(page)
  await page.getByRole('button', { name: 'Quick connect an agent' }).click()
  const panel = page.getByRole('dialog', { name: 'Download & connect an agent' })
  await expect(panel.getByText('Token configured', { exact: true })).toBeVisible()
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    Object.defineProperty(document, 'execCommand', { configurable: true, value: () => false })
  })

  const copy = panel.locator('pre').first().locator('..').getByRole('button')
  await copy.click()
  await expect(panel.getByRole('alert')).toHaveText('Could not copy the command. Select and copy it manually.')
  await expect(copy).not.toHaveClass(/text-emerald-500/)
})

test('spray help renders as compact output in the web transcript', async ({ page }) => {
  await openApp(page)
  const newTask = page.locator('aside [data-node-id="e2e-node"]').getByRole('button', { name: 'New task on e2e-node' })
  const globalNewTask = page.getByRole('button', { name: 'New task', exact: true })
  await expect(newTask.or(globalNewTask)).toBeVisible({ timeout: 20_000 })
  if (await newTask.count()) {
    await newTask.click()
  } else {
    await globalNewTask.click()
  }
  await page.getByRole('textbox', { name: /Your goal|Type a message/ }).fill('!spray -h')
  await page.getByRole('button', { name: 'Send message' }).click()

  const help = page.locator('pre').filter({ hasText: '--client-fingerprint' }).last()
  await expect(help).toBeVisible({ timeout: 20_000 })
  const output = await help.innerText()
  expect(output).toContain('Usage:')
  expect(output).toContain('Request Options:')
  expect(Math.max(...output.split('\n').map((line) => line.length))).toBeLessThanOrEqual(96)
})
