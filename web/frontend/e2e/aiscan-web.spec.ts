import { test, expect, type APIRequestContext, type Page } from '@playwright/test'

const API_TOKEN = process.env.ACCESS_KEY || 'test-token'

function apiHeaders() {
  return { Authorization: `Bearer ${API_TOKEN}` }
}

function rpcID(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

async function connectRPC(request: APIRequestContext, procedure: string, data: Record<string, unknown>) {
  const response = await request.post(procedure, {
    headers: {
      ...apiHeaders(),
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

async function openAuthenticatedApp(page: Page) {
  const login = await page.request.post('/api/auth/login', { data: { token: API_TOKEN } })
  expect(login.ok()).toBeTruthy()
  await page.goto('/')
  await expect(page.locator('button[aria-label="Open settings"]')).toBeVisible()
}

async function requireRegisteredAgents(request: APIRequestContext) {
  let agents: any[] = []
  await expect.poll(async () => {
    const response = await connectRPC(request, '/aiscan.rpc.agent.AgentService/ListAgents', {})
    agents = response.agents ?? []
    return agents.length
  }, {
    message: 'the E2E server must register its local mock-backed agent',
    timeout: 15_000,
  }).toBeGreaterThan(0)
  expect(agents[0].hello?.nodeId).toBeTruthy()
  return agents
}

async function deleteSession(request: APIRequestContext, sessionID: string) {
  return connectRPC(request, '/aiscan.rpc.chat.SessionService/DeleteSession', {
    requestId: rpcID('delete'),
    sessionId: sessionID,
  })
}

test.describe('HTTP shell and authentication', () => {
  test('health and static assets are served', async ({ request }) => {
    const health = await request.get('/health')
    expect(health.ok()).toBeTruthy()
    expect(await health.json()).toEqual({ status: 'ok' })

    const index = await request.get('/')
    expect(index.ok()).toBeTruthy()
    const html = await index.text()
    expect(html).not.toContain(API_TOKEN)
    const script = html.match(/src="(\/assets\/index-[^"]+\.js)"/)
    expect(script).toBeTruthy()
    expect((await request.get(script![1])).ok()).toBeTruthy()
  })

  test('login uses the auth endpoint without leaking the token', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Access AIScan' })).toBeVisible()
    const token = page.getByLabel('Access token')
    await token.fill(API_TOKEN)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.locator('button[aria-label="Open settings"]')).toBeVisible()
    expect(page.url()).not.toContain(API_TOKEN)
    expect(await page.evaluate(() => localStorage.getItem('aiscan-access-key'))).toBeNull()

    const ioa = await page.evaluate(async () => {
      const response = await fetch('/ioa/nodes')
      return { status: response.status, nodes: await response.json() }
    })
    expect(ioa.status).toBe(200)
    expect(Array.isArray(ioa.nodes)).toBeTruthy()
    expect(ioa.nodes.some((node: { name?: string }) => node.name === 'aiscan.web')).toBeTruthy()
  })

  test('agent token is available only to authenticated clients and is not cacheable', async ({ request }) => {
    const unauthorized = await request.get('/api/auth/agent-token')
    expect(unauthorized.status()).toBe(401)

    const authorized = await request.get('/api/auth/agent-token', { headers: apiHeaders() })
    expect(authorized.ok()).toBeTruthy()
    expect(authorized.headers()['cache-control']).toBe('no-store')
    expect(authorized.headers()['pragma']).toBe('no-cache')
    expect(await authorized.json()).toEqual({ token: API_TOKEN })
  })

  test('management RPC rejects an invalid bearer token', async ({ request }) => {
    const response = await request.post('/aiscan.rpc.system.SystemService/GetStatus', {
      headers: {
        Authorization: 'Bearer wrong-token',
        'Content-Type': 'application/json',
        'Connect-Protocol-Version': '1',
      },
      data: {},
    })
    expect(response.status()).toBe(401)
  })
})

test.describe('ConnectRPC management plane', () => {
  test('system, config and agent views are protobuf-shaped', async ({ request }) => {
    const system = await connectRPC(request, '/aiscan.rpc.system.SystemService/GetStatus', {})
    expect(system.status?.configLoaded).toBe(true)
    expect(typeof system.status?.agents).toBe('number')

    const config = await connectRPC(request, '/aiscan.rpc.config.ConfigService/GetConfig', {})
    expect(config.config?.loaded).toBe(true)

    const agents = await requireRegisteredAgents(request)
    expect(agents.some((agent) => agent.hello?.nodeId === 'local')).toBeTruthy()
  })

  test('session, scan, SCO and node queries use ConnectRPC', async ({ request }) => {
    const sessions = await connectRPC(request, '/aiscan.rpc.chat.SessionService/ListSessions', { includeClosed: true })
    expect(Array.isArray(sessions.sessions ?? [])).toBeTruthy()

    const scans = await connectRPC(request, '/aiscan.rpc.scan.ScanService/ListScans', {})
    expect(Array.isArray(scans.scans ?? [])).toBeTruthy()

    const nodes = await connectRPC(request, '/aiscan.rpc.sco.SCOService/ListNodes', { limit: 10 })
    expect(Array.isArray(nodes.nodes?.nodes ?? [])).toBeTruthy()

    const agents = await connectRPC(request, '/aiscan.rpc.agent.AgentService/ListAgents', {})
    expect(Array.isArray(agents.agents ?? [])).toBeTruthy()
    expect(agents.agents?.some((agent: { hello?: { nodeId?: string } }) => agent.hello?.nodeId === 'local')).toBeTruthy()
  })

  test('retired REST management routes stay removed', async ({ request }) => {
    for (const path of ['/api/status', '/api/config', '/api/agents', '/api/scans', '/api/sco/nodes', '/api/deploy/local']) {
      expect((await request.get(path, { headers: apiHeaders() })).status(), path).toBe(404)
    }
  })
})

test.describe('single AOP WebSocket browser plane', () => {
  test('quick connect fetches and copies the authenticated agent token on demand', async ({ page }) => {
    await openAuthenticatedApp(page)
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])

    let tokenRequests = 0
    page.on('request', (request) => {
      if (new URL(request.url()).pathname === '/api/auth/agent-token') tokenRequests++
    })

    const trigger = page.getByRole('button', { name: 'Quick connect an agent' })
    await trigger.click()
    const quickConnect = page.getByRole('dialog', { name: 'Download & connect an agent' })
    await expect(quickConnect.getByText('Token configured', { exact: true })).toBeVisible()

    await quickConnect.getByRole('button', { name: 'Windows' }).click()
    const installCommand = quickConnect.locator('pre').first()
    await expect(installCommand).toContainText('https://github.com/chainreactors/aiscan/releases/download/v1.0.0-rc1/aiscan-full_windows_amd64.zip')
    await expect(installCommand).not.toContainText('ghfast.top')

    await quickConnect.getByRole('button', { name: 'China' }).click()
    await expect(installCommand).toContainText('https://ghfast.top/https://github.com/chainreactors/aiscan/releases/download/v1.0.0-rc1/aiscan-full_windows_amd64.zip')
    const chinaCommand = await installCommand.innerText()
    await installCommand.locator('..').getByRole('button').click()
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(chinaCommand)

    await quickConnect.getByRole('button', { name: 'Global' }).click()
    await expect(installCommand).toContainText('https://github.com/chainreactors/aiscan/releases/download/v1.0.0-rc1/aiscan-full_windows_amd64.zip')
    await expect(installCommand).not.toContainText('ghfast.top')

    const commands = quickConnect.locator('pre')
    await expect(commands).toHaveCount(2)
    const connectCommand = await commands.last().innerText()
    expect(connectCommand).toContain(`http://${API_TOKEN}@`)
    expect(connectCommand).not.toContain('ACCESS_TOKEN')
    expect(connectCommand).toContain('NODE_NAME')

    await quickConnect.locator('button').last().click()
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(connectCommand)

    await page.keyboard.press('Escape')
    await expect(quickConnect).toBeHidden()
    await trigger.click()
    await expect(quickConnect.getByText('Token configured', { exact: true })).toBeVisible()
    expect(tokenRequests).toBe(2)
  })

  test('creates a session and streams a turn through the application AOP client', async ({ page, request }) => {
    await requireRegisteredAgents(request)
    await openAuthenticatedApp(page)

    await page.getByRole('button', { name: 'New', exact: true }).first().click()
    const input = page.getByRole('textbox', { name: 'Type a message... (/ for commands)' })
    await expect(input).toBeVisible()
    const sessionID = new URL(page.url()).pathname.split('/').filter(Boolean).at(-1)!

    try {
      await page.getByRole('button', { name: 'Input guide', exact: true }).click()
      const inputGuide = page.getByRole('dialog', { name: 'Input guide' })
      await expect(inputGuide).toBeVisible()
      await expect(inputGuide.getByText('Mention context', { exact: true })).toBeVisible()
      await expect(inputGuide.getByText('Session commands', { exact: true })).toBeVisible()
      await expect(inputGuide.getByText('Agent tools', { exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(inputGuide).toBeHidden()
      await page.getByRole('button', { name: 'Input guide', exact: true }).click()

      await inputGuide.getByRole('button', { name: /@ Mention context/ }).click()
      await expect(input).toHaveValue('@')
      await expect(page.getByRole('button', { name: /Assets/ }).last()).toBeVisible()
      await expect(page.getByRole('button', { name: 'File', exact: true })).toBeVisible()
      await input.press('Escape')

      await input.fill('Reply with exactly one word: PONG')
      await page.getByRole('button', { name: 'Send message' }).click()
      await expect(page.getByText('PONG', { exact: true })).toBeVisible({ timeout: 20_000 })
    } finally {
      if (sessionID) await deleteSession(request, sessionID)
    }
  })

  test('opens the PTY console without a terminal-specific socket', async ({ page, request }) => {
    await requireRegisteredAgents(request)
    await openAuthenticatedApp(page)
    await page.getByRole('button', { name: 'Terminal', exact: true }).first().click()
    await expect(page.locator('.xterm')).toBeVisible({ timeout: 15_000 })
  })
})
