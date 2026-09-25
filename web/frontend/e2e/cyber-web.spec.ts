import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
import { createNodeTask, openNodeTerminal } from './task-ui'

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
    const response = await connectRPC(request, '/cyber.rpc.agent.AgentService/ListAgents', {})
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
  return connectRPC(request, '/cyber.rpc.chat.SessionService/DeleteSession', {
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
    await expect(page.getByRole('heading', { name: 'Access Cyber' })).toBeVisible()
    const token = page.getByLabel('Access token')
    await token.fill(API_TOKEN)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.locator('button[aria-label="Open settings"]')).toBeVisible()
    expect(page.url()).not.toContain(API_TOKEN)
    expect(await page.evaluate(() => localStorage.getItem('cyber-access-key'))).toBeNull()

    const ioa = await page.evaluate(async () => {
      const response = await fetch('/ioa/nodes')
      return { status: response.status, nodes: await response.json() }
    })
    expect(ioa.status).toBe(200)
    expect(Array.isArray(ioa.nodes)).toBeTruthy()
    expect(ioa.nodes.some((node: { name?: string }) => node.name === 'cyber.web')).toBeTruthy()
  })

  test('mobile header fits and the session drawer can close', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 })
    await openAuthenticatedApp(page)

    const brand = await page.getByText('Cyber', { exact: true }).first().boundingBox()
    const assets = await page.getByRole('button', { name: 'Asset pool' }).boundingBox()
    const logout = await page.getByRole('button', { name: 'Sign out' }).boundingBox()
    expect(brand).not.toBeNull()
    expect(assets).not.toBeNull()
    expect(logout).not.toBeNull()
    expect(brand!.x + brand!.width).toBeLessThanOrEqual(assets!.x)
    expect(logout!.x + logout!.width).toBeLessThanOrEqual(320)

    await page.getByRole('button', { name: 'Chat history' }).click()
    const close = page.getByRole('button', { name: 'Collapse sidebar' })
    await expect(close).toBeVisible()
    await close.click()
    await expect(close).toBeHidden()
  })

  test('sidebar groups tasks by node while assets stay in the header', async ({ page, request }) => {
    let agents = await requireRegisteredAgents(request)
    await expect.poll(async () => {
      const response = await connectRPC(request, '/cyber.rpc.agent.AgentService/ListAgents', {})
      agents = response.agents ?? []
      return agents.length
    }).toBeGreaterThanOrEqual(2)
    const [nodeID, otherNodeID] = agents.map((agent) => agent.hello.nodeId as string)
    const login = await page.request.post('/api/auth/login', { data: { token: API_TOKEN } })
    expect(login.ok()).toBeTruthy()
    await page.goto('/?view=nodes&target=legacy')

    const sidebar = page.locator('aside')
    await expect(sidebar.getByText('Tasks', { exact: true })).toBeVisible()
    await expect(sidebar.getByRole('button', { name: 'Nodes', exact: true })).toHaveCount(0)
    await expect(sidebar.getByRole('button', { name: 'Targets', exact: true })).toHaveCount(0)
    await expect(sidebar.getByRole('combobox')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Asset pool' })).toBeVisible()
    await expect.poll(() => new URL(page.url()).searchParams.has('view')).toBe(false)
    expect(new URL(page.url()).searchParams.has('target')).toBe(false)

    const firstNode = sidebar.locator(`[data-node-id="${nodeID}"]`)
    const secondNode = sidebar.locator(`[data-node-id="${otherNodeID}"]`)
    await firstNode.locator('button[aria-pressed][aria-expanded]').click()
    await expect(firstNode.locator('button[aria-pressed][aria-expanded]')).toHaveAttribute('aria-expanded', 'true')
    await expect(secondNode.locator('button[aria-pressed][aria-expanded]')).toHaveAttribute('aria-expanded', 'false')
    expect(new URL(page.url()).searchParams.get('node')).toBe(nodeID)

    const created: string[] = []
    try {
      for (const [id, title, group] of [[nodeID, 'Sidebar task A', firstNode], [otherNodeID, 'Sidebar task B', secondNode]] as const) {
        const previousPath = new URL(page.url()).pathname
        await group.getByRole('button', { name: /New task on/ }).click()
        await expect.poll(() => new URL(page.url()).pathname).not.toBe(previousPath)
        expect(new URL(page.url()).pathname).toMatch(/^\/sessions\//)
        const sessionID = new URL(page.url()).pathname.split('/').filter(Boolean).at(-1)!
        created.push(sessionID)
        const record = await connectRPC(request, '/cyber.rpc.chat.SessionService/GetSession', { sessionId: sessionID })
        expect(record.session?.session?.nodeId).toBe(id)
        await connectRPC(request, '/cyber.rpc.chat.SessionService/UpdateSession', {
          requestId: rpcID('rename'), sessionId: sessionID, title,
        })
      }

      await sidebar.getByRole('textbox', { name: 'Search tasks' }).fill('Sidebar task')
      await firstNode.locator('button[aria-pressed][aria-expanded]').click()
      await expect(firstNode.getByText('Sidebar task A')).toBeVisible()
      await expect(sidebar.getByText('Sidebar task B')).toHaveCount(0)
      await secondNode.locator('button[aria-pressed][aria-expanded]').click()
      await expect(secondNode.getByText('Sidebar task B')).toBeVisible()
      await expect(sidebar.getByText('Sidebar task A')).toHaveCount(0)
      await sidebar.getByRole('textbox', { name: 'Search tasks' }).fill('')

      const actions = secondNode.getByRole('button', { name: 'Actions for Sidebar task B' })
      await actions.hover()
      await actions.click()
      await expect(page.getByRole('menuitem', { name: 'Rename' })).toBeVisible()
      await page.getByRole('menuitem', { name: 'Archive', exact: true }).click()
      await expect(secondNode.getByText('Sidebar task B')).toHaveCount(0)
      await sidebar.getByRole('button', { name: 'Archived' }).click()
      await expect(secondNode.getByText('Sidebar task B')).toBeVisible()
      await secondNode.getByRole('button', { name: 'Actions for Sidebar task B' }).hover()
      await secondNode.getByRole('button', { name: 'Actions for Sidebar task B' }).click()
      await page.getByRole('menuitem', { name: 'Restore' }).click()
      await expect(secondNode.getByText('Sidebar task B')).toHaveCount(0)
      await page.reload()
      await expect(sidebar.getByRole('button', { name: 'Archived' })).toHaveAttribute('aria-pressed', 'true')
      await expect(sidebar.getByText('No archived tasks')).toBeVisible()
      await sidebar.getByRole('button', { name: 'Show active tasks' }).click()
      await expect(secondNode.getByText('Sidebar task B')).toBeVisible()

      const search = sidebar.getByRole('textbox', { name: 'Search tasks' })
      await search.fill('sidebar-no-match')
      await expect(sidebar.getByText('No matching tasks')).toBeVisible()
      await sidebar.getByRole('button', { name: 'Clear search' }).click()
      await expect(search).toHaveValue('')

      await page.reload()
      await expect(secondNode.locator('button[aria-pressed][aria-expanded]')).toHaveAttribute('aria-expanded', 'true')
      await expect(firstNode.locator('button[aria-pressed][aria-expanded]')).toHaveAttribute('aria-expanded', 'false')
      await sidebar.getByRole('button', { name: /All tasks/ }).click()
      await expect(firstNode.getByText('Sidebar task A')).toBeVisible()
      await expect(secondNode.getByText('Sidebar task B')).toBeVisible()
      await firstNode.locator('[data-session-id]').getByRole('button', { name: /Sidebar task A/ }).first().click()
      await expect(firstNode.locator('button[aria-pressed][aria-expanded]')).toHaveAttribute('aria-expanded', 'true')
      await expect(secondNode.locator('button[aria-pressed][aria-expanded]')).toHaveAttribute('aria-expanded', 'false')
    } finally {
      for (const sessionID of created) await deleteSession(request, sessionID)
    }
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
    const response = await request.post('/cyber.rpc.system.SystemService/GetStatus', {
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
    const system = await connectRPC(request, '/cyber.rpc.system.SystemService/GetStatus', {})
    expect(system.status?.configLoaded).toBe(true)
    expect(typeof system.status?.agents).toBe('number')

    const config = await connectRPC(request, '/cyber.rpc.config.ConfigService/GetConfig', {})
    expect(config.config?.loaded).toBe(true)

    const agents = await requireRegisteredAgents(request)
    expect(agents.some((agent) => agent.hello?.nodeId === 'local')).toBeTruthy()
  })

  test('session, scan and artifact archive queries use ConnectRPC', async ({ request }) => {
    const sessions = await connectRPC(request, '/cyber.rpc.chat.SessionService/ListSessions', { includeClosed: true })
    expect(Array.isArray(sessions.sessions ?? [])).toBeTruthy()

    const scans = await connectRPC(request, '/cyber.rpc.scan.ScanService/ListScans', {})
    expect(Array.isArray(scans.scans ?? [])).toBeTruthy()

    const artifacts = await connectRPC(request, '/cyber.rpc.artifact.ArtifactService/SyncArtifacts', {})
    expect(Array.isArray(artifacts.artifacts ?? [])).toBeTruthy()

    const agents = await connectRPC(request, '/cyber.rpc.agent.AgentService/ListAgents', {})
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
    await expect(installCommand).toContainText('https://github.com/chainreactors/cyber-harness/releases/download/v1.0.0-rc1/aiscan-full_windows_amd64.zip')
    await expect(installCommand).not.toContainText('ghfast.top')

    await quickConnect.getByRole('button', { name: 'China' }).click()
    await expect(installCommand).toContainText('https://ghfast.top/https://github.com/chainreactors/cyber-harness/releases/download/v1.0.0-rc1/aiscan-full_windows_amd64.zip')
    const chinaCommand = await installCommand.innerText()
    await installCommand.locator('..').getByRole('button').click()
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(chinaCommand)

    await quickConnect.getByRole('button', { name: 'Global' }).click()
    await expect(installCommand).toContainText('https://github.com/chainreactors/cyber-harness/releases/download/v1.0.0-rc1/aiscan-full_windows_amd64.zip')
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

  test('quick connect copies on an HTTP origin without the Clipboard API', async ({ page }) => {
    await openAuthenticatedApp(page)
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
    await page.getByRole('button', { name: 'Quick connect an agent' }).click()
    const quickConnect = page.getByRole('dialog', { name: 'Download & connect an agent' })
    await expect(quickConnect.getByText('Token configured', { exact: true })).toBeVisible()
    const row = quickConnect.locator('pre').last().locator('..')
    const command = await row.locator('pre').innerText()
    await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined }))

    const copy = row.getByRole('button')
    await copy.click()
    await expect(copy).toHaveClass(/text-emerald-500/)
    await page.evaluate(() => { delete (navigator as any).clipboard })
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(command)
    await expect(quickConnect.getByRole('alert')).toHaveCount(0)
  })

  test('quick connect reports a failed clipboard fallback', async ({ page }) => {
    await openAuthenticatedApp(page)
    await page.getByRole('button', { name: 'Quick connect an agent' }).click()
    const quickConnect = page.getByRole('dialog', { name: 'Download & connect an agent' })
    await expect(quickConnect.getByText('Token configured', { exact: true })).toBeVisible()
    await page.evaluate(() => {
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
      Object.defineProperty(document, 'execCommand', { configurable: true, value: () => false })
    })

    const copy = quickConnect.locator('pre').first().locator('..').getByRole('button')
    await copy.click()
    await expect(quickConnect.getByRole('alert')).toHaveText('Could not copy the command. Select and copy it manually.')
    await expect(copy).not.toHaveClass(/text-emerald-500/)
  })

  test('spray help command renders compact output in the web transcript', async ({ page, request }) => {
    await requireRegisteredAgents(request)
    await openAuthenticatedApp(page)
    await page.locator('aside [data-node-id="local"]').getByRole('button', { name: /New task on/ }).click()
    await expect.poll(() => new URL(page.url()).pathname).toMatch(/^\/sessions\//)
    const sessionID = new URL(page.url()).pathname.split('/').filter(Boolean).at(-1)!
    try {
      await page.getByRole('textbox', { name: 'Your goal' }).fill('!spray -h')
      await page.getByRole('button', { name: 'Send message' }).click()
      const help = page.locator('pre').filter({ hasText: '--client-fingerprint' }).last()
      await expect(help).toBeVisible({ timeout: 20_000 })
      const text = await help.innerText()
      expect(text).toContain('Usage:')
      expect(text).toContain('--poc-config')
      expect(text).toContain('Request Options:')
      expect(Math.max(...text.split('\n').map(line => line.length))).toBeLessThanOrEqual(96)
    } finally {
      await deleteSession(request, sessionID)
    }
  })

  test('creates a session and streams a turn through the application AOP client', async ({ page, request }) => {
    await requireRegisteredAgents(request)
    await openAuthenticatedApp(page)

    await createNodeTask(page)
    const input = page.getByRole('textbox', { name: 'Your goal' })
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
    await openNodeTerminal(page)
    await expect(page.locator('.xterm')).toBeVisible({ timeout: 15_000 })
  })
})
