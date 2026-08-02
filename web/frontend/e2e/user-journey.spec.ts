import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

const API_TOKEN = process.env.ACCESS_KEY || 'test-token'
const E2E_MODEL = process.env.AISCAN_E2E_LLM_MODEL || 'deepseek-chat'

function rpcID(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

async function connectRPC(request: APIRequestContext, procedure: string, data: Record<string, unknown>) {
  const response = await request.post(procedure, {
    headers: {
      Authorization: `Bearer ${API_TOKEN}`,
      'Content-Type': 'application/json',
      'Connect-Protocol-Version': '1',
    },
    data,
  })
  if (!response.ok()) {
    const body = await response.text()
    expect(response.ok(), `${procedure}: ${body}`).toBeTruthy()
  }
  return response.json()
}

async function waitForNode(request: APIRequestContext) {
  let nodeID = ''
  await expect.poll(async () => {
    const response = await connectRPC(request, '/aiscan.rpc.agent.AgentService/ListAgents', {})
    nodeID = response.agents?.find((agent: { hello?: { nodeId?: string } }) => agent.hello?.nodeId === 'e2e-node')?.hello?.nodeId || ''
    return nodeID
  }, { timeout: 20_000 }).toBe('e2e-node')
  return nodeID
}

async function login(page: Page) {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Access AIScan' })).toBeVisible()
  await page.getByLabel('Access token').fill(API_TOKEN)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('button', { name: 'Open settings', exact: true })).toBeVisible()
}

async function sendChat(page: Page, text: string) {
  const input = page.getByRole('textbox', { name: 'Type a message... (/ for commands)' })
  await input.fill(text)
  await page.getByRole('button', { name: 'Send message' }).click()
}

test('operator completes a full AIScan Web journey', async ({ page, request }) => {
  test.setTimeout(120_000)
  await waitForNode(request)
  await login(page)

  const firstPrompt = 'Reply with exactly one word: PONG'
  const secondPrompt = 'Return PONG.'
  let sessionID = ''

  try {
    // Settings are exercised through the visible UI, including a real provider
    // probe against the configured mock/DeepSeek endpoint.
    await page.getByRole('button', { name: 'Open settings', exact: true }).click()
    const settings = page.getByRole('dialog', { name: 'Settings' })
    await expect(settings).toBeVisible()
    await expect(settings.getByRole('textbox', { name: 'Profile name' })).toHaveValue('E2E DeepSeek')
    await expect(settings.getByRole('combobox', { name: 'Model' })).toHaveValue(E2E_MODEL)
    await expect(settings.getByRole('textbox', { name: 'Base URL' })).not.toHaveValue('')
    await settings.getByRole('button', { name: 'Test connection' }).click()
    await expect(settings.getByText('Connection OK')).toBeVisible({ timeout: 20_000 })
    await settings.getByRole('button', { name: 'Close', exact: true }).first().click()

    // Theme is user state, so verify both rendered state and persisted state.
    await page.getByRole('button', { name: 'Switch to dark theme' }).click()
    await expect.poll(() => page.locator('html').getAttribute('class')).toContain('dark')
    await expect.poll(() => page.evaluate(() => localStorage.getItem('aiscan-theme'))).toBe('dark')

    // One durable Chat session, two LLM turns, then a real REPL command through
    // the same node channel.
    const remoteNode = page.getByRole('button', { name: /e2e-node.*idle/ })
    await expect(remoteNode).toBeVisible()
    const remoteNodeGroup = remoteNode.locator('xpath=..')
    await remoteNodeGroup.getByRole('button', { name: 'New', exact: true }).click()
    await expect(page.getByRole('textbox', { name: 'Type a message... (/ for commands)' })).toBeVisible()
    sessionID = new URL(page.url()).pathname.split('/').filter(Boolean).at(-1) || ''
    expect(sessionID).not.toBe('')

    await sendChat(page, firstPrompt)
    await expect(page.getByText('PONG', { exact: true })).toBeVisible({ timeout: 25_000 })
    await sendChat(page, secondPrompt)
    await expect.poll(() => page.getByText('PONG', { exact: true }).count()).toBeGreaterThanOrEqual(2)

    await sendChat(page, '/status')
    await expect(page.getByText(/Session:/).last()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText(/Provider:\s*openai/).last()).toBeVisible()
    await expect(page.getByText(`Model: ${E2E_MODEL}`, { exact: false }).last()).toBeVisible()

    // Terminal attaches to the resident tmux-backed Main REPL, accepts keyboard
    // input as a user would, and receives output from the selected node.
    await remoteNodeGroup.getByRole('button', { name: 'Terminal', exact: true }).click()
    await expect(page.locator('.xterm')).toBeVisible({ timeout: 20_000 })
    await page.getByRole('button', { name: /Main REPL/ }).click()
    const terminalInput = page.locator('.xterm-helper-textarea')
    await terminalInput.focus()
    await terminalInput.pressSequentially('/status')
    await terminalInput.press('Enter')
    await expect.poll(async () => page.locator('.xterm-rows').innerText(), { timeout: 20_000 }).toContain('Provider:')
    await expect.poll(async () => (await page.locator('.xterm-rows').innerText()).replace(/\s+/g, '')).toContain(E2E_MODEL.replace(/\s+/g, ''))

    await page.getByRole('button', { name: 'Show details' }).click()
    const agentDrawer = page.getByRole('dialog').filter({ hasText: 'Agent Console' })
    await expect(agentDrawer).toContainText('e2e-node')
    await expect(agentDrawer).toContainText('openai')
    await expect(agentDrawer).toContainText(E2E_MODEL)
    await expect(agentDrawer).toContainText('pty')
    await page.getByRole('button', { name: 'Close' }).last().click()

    // Co-hosted IOA shares Web authentication, while remaining a separate
    // service boundary. Assets and Quick Connect are also checked visibly.
    await page.getByRole('button', { name: 'Open IOA Console' }).click()
    const ioaDrawer = page.getByRole('dialog').filter({ hasText: 'IOA Console' })
    await expect(ioaDrawer).toBeVisible()
    await expect(ioaDrawer).toContainText('live')
    await expect(ioaDrawer).toContainText('No messages in this space')
    await page.getByRole('button', { name: 'Close' }).last().click()

    await page.getByRole('button', { name: 'Asset pool' }).click()
    const assetDrawer = page.getByRole('dialog').filter({ hasText: 'Assets' })
    await expect(assetDrawer).toContainText('No assets discovered yet')
    await page.getByRole('button', { name: 'Close' }).last().click()

    await page.getByRole('button', { name: 'Quick connect an agent' }).click()
    const quickConnect = page.getByRole('dialog', { name: 'Download & connect an agent' })
    await expect(quickConnect).toBeVisible()
    const commands = quickConnect.locator('pre')
    await expect(commands).toHaveCount(2)
    for (let i = 0; i < 2; i++) {
      const command = await commands.nth(i).innerText()
      expect(command.match(/--server-url/g)).toHaveLength(1)
      expect(command).not.toContain('--web-url')
      expect(command).toContain('ACCESS_TOKEN')
      expect(command).toContain('NODE_NAME')
    }
    await page.keyboard.press('Escape')

    // Authentication renewal must restore the complete durable transcript,
    // including accepted operator messages and command input.
    await page.getByRole('button', { name: 'Sign out' }).click()
    await expect(page.getByRole('heading', { name: 'Access AIScan' })).toBeVisible()
    await page.getByLabel('Access token').fill(API_TOKEN)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText(firstPrompt, { exact: true }).last()).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText(secondPrompt, { exact: true }).last()).toBeVisible()
    await expect(page.getByText('/status', { exact: true }).last()).toBeVisible()
    await expect.poll(() => page.getByText('PONG', { exact: true }).count()).toBeGreaterThanOrEqual(2)

    // Destructive cleanup is also a visible user action with confirmation.
    const sessionRow = page.getByText(firstPrompt, { exact: true }).first().locator('xpath=ancestor::div[contains(@class,"group")]')
    await sessionRow.hover()
    await sessionRow.getByRole('button', { name: 'Delete session' }).click()
    const confirm = page.getByRole('dialog', { name: 'Please confirm' })
    await expect(confirm).toContainText('Delete this session?')
    await confirm.getByRole('button', { name: 'Confirm' }).click()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByText(firstPrompt, { exact: true })).toHaveCount(0)
    sessionID = ''
  } finally {
    // A failed assertion must not leak durable state into later test runs.
    if (sessionID) {
      await connectRPC(request, '/aiscan.rpc.chat.SessionService/DeleteSession', {
        requestId: rpcID('cleanup'),
        sessionId: sessionID,
      }).catch(() => undefined)
    }
  }
})
