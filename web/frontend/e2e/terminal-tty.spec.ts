import { expect, test, type Page } from '@playwright/test'

const token = process.env.ACCESS_KEY || 'test-token'

async function screen(page: Page) {
  return (await page.locator('.xterm-rows').innerText()).split('\n').map(line => line.trimEnd())
}

async function clearEditor(page: Page) {
  await page.getByRole('button', { name: /Main REPL/ }).click()
  await expect(page.getByRole('button', { name: /Main REPL/ })).toHaveAttribute('aria-current', 'true')
  await expect(page.locator('.xterm-rows')).toContainText('cyber-scan')
  const input = page.getByRole('textbox', { name: 'Terminal input' })
  await input.press('Control+e')
  await input.press('Control+u')
  await input.press('Control+l')
  await expect.poll(async () => (await screen(page)).filter(line => line.trim())).toEqual(['cyber-scan ❯'])
  return input
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('cyber-locale', 'en')
    localStorage.setItem('cyber-interface-guide-seen', 'true')
  })
  expect((await page.request.post('/api/auth/login', { data: { token } })).ok()).toBe(true)
  await page.goto('/')
  await page.getByRole('button', { name: /agent\(s\) connected/ }).click()
  await page.getByRole('dialog', { name: 'Agent Console', exact: true })
    .getByRole('button').filter({ has: page.getByText('e2e-node', { exact: true }) }).click()
  await expect(page.getByRole('button', { name: /Main REPL/ })).toHaveAttribute('aria-current', 'true')
})

test('remote system shell retains state across REPL switches and browser reconnects', async ({ page }) => {
  const shell = page.getByRole('button', { name: /^Remote Shell/ })
  const input = page.getByRole('textbox', { name: 'Terminal input' })
  await shell.click()
  await expect(shell).toHaveAttribute('aria-current', 'true')
  await input.pressSequentially(process.platform === 'win32' ? 'set TTY_SHELL_STATE=REMOTE_SESSION_KEPT' : 'export TTY_SHELL_STATE=REMOTE_SESSION_KEPT')
  await input.press('Enter')
  const echoState = process.platform === 'win32' ? 'echo %TTY_SHELL_STATE%' : 'echo "$TTY_SHELL_STATE"'
  await input.pressSequentially(echoState)
  await input.press('Enter')
  await expect.poll(() => screen(page)).toContain('REMOTE_SESSION_KEPT')

  await clearEditor(page)
  await shell.click()
  await expect(shell).toHaveAttribute('aria-current', 'true')
  await page.getByRole('button', { name: 'Close panel', exact: true }).click()
  await page.reload()
  await page.getByRole('button', { name: /agent\(s\) connected/ }).click()
  await expect(page.getByRole('button', { name: /Main REPL/ })).toHaveAttribute('aria-current', 'true')
  await shell.click()
  await expect(shell).toHaveAttribute('aria-current', 'true')
  await input.pressSequentially(echoState)
  await input.press('Enter')
  await expect.poll(async () => (await screen(page)).filter(line => line === 'REMOTE_SESSION_KEPT').length).toBe(2)

  // exit ends the real shell; it must not silently start another process.
  await input.pressSequentially('exit')
  await input.press('Enter')
  await expect(shell).not.toHaveAttribute('aria-current', 'true')
  await expect(page.getByRole('dialog', { name: 'Agent Console', exact: true })).toContainText('closed')
  await shell.click()
  await expect(shell).toHaveAttribute('aria-current', 'true')
  await expect(page.locator('.xterm-rows')).not.toContainText('REMOTE_SESSION_KEPT')
})

test('short REPL input stays on the prompt row and retains the editing cursor', async ({ page }) => {
  const input = await clearEditor(page)
  await input.pressSequentially('!echo TTY_END')
  await expect.poll(async () => (await screen(page)).filter(line => line.trim())).toEqual(['cyber-scan ❯ !echo TTY_END'])
  for (let i = 0; i < 3; i++) await input.press('ArrowLeft')
  await input.pressSequentially('KEPT_')
  await expect.poll(async () => (await screen(page)).filter(line => line.trim())).toEqual(['cyber-scan ❯ !echo TTY_KEPT_END'])
  await input.press('Enter')
  await expect.poll(() => screen(page)).toContain('TTY_KEPT_END')
  await expect.poll(() => screen(page)).toContain('cyber-scan ❯ !echo TTY_KEPT_END')
})

test('resizing a wrapped REPL draft preserves its text and cursor', async ({ page }) => {
  const input = await clearEditor(page)
  const word = `RESIZE_${'x'.repeat(60)}_END`
  await input.pressSequentially(`!echo ${word}`)
  const compact = async () => (await screen(page)).join('').replace(/\s/g, '')
  await expect.poll(compact).toBe(`cyber-scan❯!echo${word}`)
  for (const width of [390, 820, 1280]) {
    await page.setViewportSize({ width, height: 844 })
    // Client reflow can already show the expected text before the remote
    // redraw arrives. Finish an editor round trip before resizing again.
    await input.pressSequentially('~')
    await expect.poll(compact).toBe(`cyber-scan❯!echo${word}~`)
    await input.press('Backspace')
    await expect.poll(compact).toBe(`cyber-scan❯!echo${word}`)
  }
  for (let i = 0; i < 3; i++) await input.press('ArrowLeft')
  await input.pressSequentially('KEPT_')
  const edited = word.replace('_END', '_KEPT_END')
  await expect.poll(compact).toBe(`cyber-scan❯!echo${edited}`)
  await input.press('Enter')
  await expect.poll(compact).toBe(`cyber-scan❯!echo${edited}${edited}cyber-scan❯`)
})

test('async output preserves the next REPL draft while a task finishes', async ({ page }) => {
  const input = await clearEditor(page)
  await input.pressSequentially('Reply with exactly one word: PONG')
  await input.press('Enter')
  await expect(page.locator('.xterm-rows')).toContainText('talking')
  await input.pressSequentially('!echo ASYNC_END')
  for (let i = 0; i < 3; i++) await input.press('ArrowLeft')
  await expect.poll(() => screen(page)).toContain('PONG')
  await expect(page.locator('.xterm-rows')).toContainText('!echo ASYNC_END')
  await input.pressSequentially('KEPT_')
  await input.press('Enter')
  await expect.poll(() => screen(page)).toContain('ASYNC_KEPT_END')
})

test('shell PTY supports command editing and Ctrl+C after resizing', async ({ page }) => {
  await page.getByRole('button', { name: 'New shell PTY', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'Agent Console', exact: true })).toContainText('shell-e2e-node')
  const input = page.getByRole('textbox', { name: 'Terminal input' })
  await input.pressSequentially('echo SHELL_END')
  for (let i = 0; i < 3; i++) await input.press('ArrowLeft')
  await input.pressSequentially('KEPT_')
  await input.press('Enter')
  await expect.poll(() => screen(page)).toContain('SHELL_KEPT_END')
  await page.setViewportSize({ width: 820, height: 844 })
  await input.pressSequentially('echo SHOULD_BE_CANCELED')
  await input.press('Control+c')
  await input.pressSequentially('echo SHELL_RECOVERED')
  await input.press('Enter')
  await expect.poll(() => screen(page)).toContain('SHELL_RECOVERED')
  await input.pressSequentially('exit')
  await input.press('Enter')
  await page.getByRole('button', { name: /Main REPL/ }).click()
  await expect(page.locator('.xterm-rows')).toContainText('cyber-scan')
})
