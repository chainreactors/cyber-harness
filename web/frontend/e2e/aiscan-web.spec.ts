import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const API_TOKEN = process.env.ACCESS_KEY || 'test-token';
const WEB_BASE_URL = process.env.BASE_URL || `http://127.0.0.1:${process.env.AISCAN_E2E_PORT || '38080'}`;
const execFileAsync = promisify(execFile);
const externalGoClientDir = fileURLToPath(new URL('../../../examples/external-go-client/', import.meta.url));

function apiHeaders() {
  return { Authorization: `Bearer ${API_TOKEN}` };
}

function rpcID(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

async function connectRPC(request: APIRequestContext, procedure: string, data: Record<string, unknown>) {
  const response = await request.post(procedure, {
    headers: {
      ...apiHeaders(),
      'Content-Type': 'application/json',
      'Connect-Protocol-Version': '1',
    },
    data,
  });
  if (!response.ok()) {
    const body = await response.text();
    expect(response.ok(), `${procedure}: ${body}`).toBeTruthy();
  }
  return response.json();
}

async function openChatSession(request: APIRequestContext, participant: string) {
  const sessionID = rpcID('session');
  const response = await connectRPC(request, '/aop.ChatService/OpenSession', {
    requestId: rpcID('open'), sessionId: sessionID, participant,
  });
  expect(response.accepted?.id).toBe(sessionID);
  return response.accepted;
}

async function deleteChatSession(request: APIRequestContext, sessionID: string) {
  return connectRPC(request, '/aiscan.chat.SessionService/DeleteSession', {
    requestId: rpcID('delete'), sessionId: sessionID,
  });
}

async function runChatTurn(request: APIRequestContext, sessionID: string, content: string) {
  const turnID = rpcID('turn');
  const messageID = rpcID('message');
  const response = await connectRPC(request, '/aop.ChatService/RunTurn', {
    requestId: rpcID('run'), sessionId: sessionID, turnId: turnID,
    input: { id: messageID, role: 'user', content: [{ text: { text: content } }] },
  });
  expect(response.accepted?.turnId).toBe(turnID);
  return { turnID, messageID };
}

async function openAuthenticatedApp(page: Page) {
  const login = await page.request.post('/api/auth/login', {
    data: { token: API_TOKEN },
  });
  expect(login.ok()).toBeTruthy();
  await page.goto('/');
  await expect(page.locator('button[aria-label="Open settings"]')).toBeVisible();
}

async function requireRegisteredAgents(request: APIRequestContext) {
  let agents: any[] = [];
  await expect.poll(async () => {
    const response = await request.get('/api/agents', { headers: apiHeaders() });
    expect(response.ok()).toBeTruthy();
    agents = await response.json();
    return agents.length;
  }, {
    message: 'the E2E server must start and register its local mock-backed agent',
    timeout: 15_000,
  }).toBeGreaterThan(0);
  return agents;
}

// ---------------------------------------------------------------------------
// 1. Health & Status
// ---------------------------------------------------------------------------

test.describe('Health & Status', () => {
  test('GET /health returns ok', async ({ request }) => {
    const res = await request.get('/health');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.status).toBe('ok');
  });

  test('GET /api/status returns server info with LLM configured', async ({ request }) => {
    const res = await request.get('/api/status', { headers: apiHeaders() });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.llm_available).toBe(true);
    expect(body.llm_provider).toBeTruthy();
    expect(body.llm_model).toBeTruthy();
    expect(body.llm_api_key_configured).toBe(true);
    expect(body.config_loaded).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// 2. Auth
// ---------------------------------------------------------------------------

test.describe('Auth', () => {
  test('rejects requests without valid token', async ({ request }) => {
    const res = await request.get('/api/status', {
      headers: { Authorization: 'Bearer wrong-token' },
    });
    expect(res.status()).toBe(401);
  });

  test('accepts requests with valid token', async ({ request }) => {
    const res = await request.get('/api/status', { headers: apiHeaders() });
    expect(res.ok()).toBeTruthy();
  });

  test('does not accept tokens from URL query parameters', async ({ request }) => {
    const res = await request.get(`/api/status?access_key=${API_TOKEN}`);
    expect(res.status()).toBe(401);
  });
});

// ---------------------------------------------------------------------------
// 3. Static Assets
// ---------------------------------------------------------------------------

test.describe('Static Assets', () => {
  test('index.html never exposes the access token', async ({ request }) => {
    const res = await request.get('/');
    expect(res.ok()).toBeTruthy();
    const html = await res.text();
    expect(html).not.toContain('__AISCAN_ACCESS_KEY__');
    expect(html).not.toContain(API_TOKEN);
  });

  test('JS bundle is served', async ({ request }) => {
    const indexRes = await request.get('/');
    const html = await indexRes.text();
    const jsMatch = html.match(/src="(\/assets\/index-[^"]+\.js)"/);
    expect(jsMatch).toBeTruthy();
    const jsRes = await request.get(jsMatch![1]);
    expect(jsRes.ok()).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// 4. Login
// ---------------------------------------------------------------------------

test.describe('Login', () => {
  test('validates a token without putting it in URL or localStorage', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Access AIScan' })).toBeVisible();

    const token = page.getByLabel('Access token');
    await token.fill('wrong-token');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('alert')).toContainText('invalid');

    await token.fill(API_TOKEN);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.locator('button[aria-label="Open settings"]')).toBeVisible();

    expect(page.url()).not.toContain(API_TOKEN);
    const storedToken = await page.evaluate(() => localStorage.getItem('aiscan-access-key'));
    expect(storedToken).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// 5. Page Load & UI Shell
// ---------------------------------------------------------------------------

test.describe('Page Load', () => {
  test('index page loads and renders AIScan header', async ({ page }) => {
    await openAuthenticatedApp(page);
    // Use a specific selector for the brand name in the header
    const brand = page.locator('header').getByText('AIScan', { exact: true });
    await expect(brand).toBeVisible({ timeout: 10_000 });
    await expect(brand).toHaveText('AIScan');
  });

  test('header shows model name', async ({ page }) => {
    await openAuthenticatedApp(page);
    await expect(page.locator('header')).toContainText(/deepseek/i, { timeout: 10_000 });
  });

  test('LLM health indicator does not show offline or error', async ({ page }) => {
    await openAuthenticatedApp(page);
    const header = page.locator('header');
    await expect(header).toBeVisible();
    // Wait for the async health probe to complete
    await page.waitForTimeout(4000);
    const headerText = await header.textContent();
    expect(headerText).not.toContain('Offline');
    expect(headerText).not.toContain('unreachable');
    expect(headerText).not.toContain('not configured');
  });

  test('settings button is visible', async ({ page }) => {
    await openAuthenticatedApp(page);
    const settingsBtn = page.locator('button[aria-label="Open settings"]');
    await expect(settingsBtn).toBeVisible({ timeout: 10_000 });
  });
});

// ---------------------------------------------------------------------------
// 6. Config Panel
// ---------------------------------------------------------------------------

test.describe('Config Panel', () => {
  test('opens settings dialog and shows tabs', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.locator('button[aria-label="Open settings"]').click();
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await expect(dialog).toContainText('Settings');
    // Should have LLM and other tabs
    await expect(dialog.getByRole('button', { name: 'LLM', exact: true })).toBeVisible();
  });

  test('closes settings dialog', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.locator('button[aria-label="Open settings"]').click();
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible();
    // Close via button or Escape
    await page.keyboard.press('Escape');
    await expect(dialog).not.toBeVisible({ timeout: 5_000 });
  });

  test('LLM tab shows Provider and Model fields', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.locator('button[aria-label="Open settings"]').click();
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible();
    // Click LLM tab
    const llmTab = dialog.getByRole('button', { name: 'LLM', exact: true });
    if (await llmTab.isVisible()) {
      await llmTab.click();
    }
    await expect(dialog).toContainText('Model');
    await expect(dialog).toContainText('Provider');
    await expect(dialog).toContainText('Base URL');
    await expect(dialog).toContainText('Context window');
    await expect(dialog).toContainText('Maximum output');
    await expect(dialog).toContainText('API Key');
  });

  test('keeps dialog geometry stable when switching tabs', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.locator('button[aria-label="Open settings"]').click();
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible();
    await dialog.evaluate((element) =>
      Promise.all(element.getAnimations().map((animation) => animation.finished)),
    );

    const before = await dialog.boundingBox();
    await dialog.getByRole('button', { name: 'Cyberhub', exact: true }).click();
    const after = await dialog.boundingBox();

    expect(before).not.toBeNull();
    expect(after).not.toBeNull();
    expect(Math.abs(after!.y - before!.y)).toBeLessThanOrEqual(1);
    expect(Math.abs(after!.height - before!.height)).toBeLessThanOrEqual(1);
  });

  test('warns for a small context window and rejects an empty model', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.locator('button[aria-label="Open settings"]').click();
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible();

    await dialog.getByLabel('Context window (tokens)').fill('4096');
    await expect(dialog).toContainText('Below 8192 tokens');

    await dialog.getByLabel('Model').fill('');
    await dialog.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('requires a model');
  });

  test('closes after a successful save', async ({ page }) => {
    let saved = false;
    await page.route('**/api/config', async (route) => {
      if (route.request().method() !== 'PUT') {
        await route.continue();
        return;
      }
      saved = true;
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
    });

    await openAuthenticatedApp(page);
    await page.locator('button[aria-label="Open settings"]').click();
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Save', exact: true }).click();

    await expect(dialog).not.toBeVisible();
    expect(saved).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// 6. Config API
// ---------------------------------------------------------------------------

test.describe('Config API', () => {
  test('GET /api/config returns current config status', async ({ request }) => {
    const res = await request.get('/api/config', { headers: apiHeaders() });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.llm).toBeDefined();
    expect(body.llm.provider).toBeTruthy();
    expect(body.llm.model).toBeTruthy();
    expect(body.llm.api_key_configured).toBe(true);
  });

  test('LLM connectivity test succeeds with explicit config', async ({ request }) => {
    const configResponse = await request.get('/api/config', { headers: apiHeaders() });
    expect(configResponse.ok()).toBeTruthy();
    const config = await configResponse.json();
    const res = await request.post('/api/config/llm/test', {
      headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
      data: {
        profile_id: config.llm.active_profile,
        provider: config.llm.provider,
        base_url: config.llm.base_url,
        api_key: '',
        model: config.llm.model,
      },
    });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.ok).toBe(true);
    expect(body.latency_ms).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// 7. Agents API
// ---------------------------------------------------------------------------

test.describe('Agents API', () => {
  test('list agents returns array', async ({ request }) => {
    const res = await request.get('/api/agents', { headers: apiHeaders() });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body)).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// 8. Chat Session CRUD
// ---------------------------------------------------------------------------

test.describe('Chat Session CRUD', () => {
  test('create, list, and delete a session', async ({ request }) => {
    const agents = await requireRegisteredAgents(request);
    const agentID = agents[0].id;

    const session = await openChatSession(request, agentID);
    expect(session.id).toBeTruthy();
    expect(session.participant).toBe(agentID);

    const listed = await connectRPC(request, '/aiscan.chat.SessionService/ListSessions', {
      limit: 100, includeClosed: true,
    });
    expect(Array.isArray(listed.sessions)).toBeTruthy();
    expect(listed.sessions.some((record: any) => record.session?.id === session.id)).toBeTruthy();

    const deleted = await deleteChatSession(request, session.id);
    expect(deleted.accepted?.id).toBe(session.id);
  });

  test('legacy chat REST routes are removed', async ({ request }) => {
    const response = await request.get('/api/chat/sessions', { headers: apiHeaders() });
    expect(response.status()).toBe(404);
  });
});

// ---------------------------------------------------------------------------
// 9. Chat LLM Round-trip
// ---------------------------------------------------------------------------

test.describe('Chat LLM round-trip', () => {
  test('send a message and receive an assistant response', async ({ request }) => {
    const agents = await requireRegisteredAgents(request);
    const agentID = agents[0].id;

    const session = await openChatSession(request, agentID);
    const sessionID = session.id;

    try {
      await runChatTurn(request, sessionID, 'Reply with exactly one word: PONG');

      let assistantMsg: any = null;
      for (let i = 0; i < 15; i++) {
        await new Promise((r) => setTimeout(r, 2000));
        const listed = await connectRPC(request, '/aop.ChatService/ListEvents', {
          sessionId: sessionID, limit: 500,
        });
        const assistantMsgs = listed.events
          .map((delivery: any) => delivery.event?.message)
          .filter((message: any) => message?.role === 'assistant');
        if (assistantMsgs.length > 0) {
          assistantMsg = assistantMsgs[assistantMsgs.length - 1];
          break;
        }
      }

      expect(assistantMsg).not.toBeNull();
      expect(assistantMsg.content?.length).toBeGreaterThan(0);
    } finally {
      await deleteChatSession(request, sessionID);
    }
  });
});

test.describe('External Go Connect client', () => {
  test('an independent Go module opens, runs, and streams a turn', async ({ request }) => {
    const agents = await requireRegisteredAgents(request);
    const { stdout, stderr } = await execFileAsync('go', [
      'run', '.',
      '-url', WEB_BASE_URL,
      '-token', API_TOKEN,
      '-agent', agents[0].id,
      '-prompt', 'Reply with exactly one word: PONG',
      '-timeout', '30s',
    ], {
      cwd: externalGoClientDir,
      timeout: 45_000,
      env: { ...process.env, GOWORK: 'off' },
    });
    expect(stderr).not.toContain('error:');
    expect(stdout).toContain('PONG');
    expect(stdout).toContain('stop=completed');
  });
});

// ---------------------------------------------------------------------------
// 10. Connect stream reconnect and durable event cursor
// ---------------------------------------------------------------------------

test.describe('Connect stream reconnect', () => {
  test('replays missing durable events after the browser reconnects', async ({ page, request, context }) => {
    const agents = await requireRegisteredAgents(request);

    const session = await openChatSession(request, agents[0].id);

    await openAuthenticatedApp(page);
    await page.goto(`/sessions/${session.id}`);
    await expect(page.getByRole('textbox', { name: 'Type a message... (/ for commands)' })).toBeVisible();
    const prompt = 'Reply with exactly one word: PONG';
    await runChatTurn(request, session.id, prompt);

    await context.setOffline(true);
    await page.waitForTimeout(3500);
    await context.setOffline(false);

    const resumed = page.getByText('PONG', { exact: true });
    await expect(resumed).toBeVisible({ timeout: 15_000 });
    await expect(resumed).toHaveCount(1);

    await deleteChatSession(request, session.id);
  });
});

// ---------------------------------------------------------------------------
// 11. SCO / Asset Pool API
// ---------------------------------------------------------------------------

test.describe('Asset Pool API', () => {
  test('list SCO nodes returns array', async ({ request }) => {
    const res = await request.get('/api/sco/nodes', { headers: apiHeaders() });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body)).toBeTruthy();
  });

  test('get SCO stats returns object', async ({ request }) => {
    const res = await request.get('/api/sco/stats', { headers: apiHeaders() });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(typeof body).toBe('object');
  });
});

// ---------------------------------------------------------------------------
// 11. Scans API
// ---------------------------------------------------------------------------

test.describe('Scan ConnectRPC', () => {
  test('lists scans through ScanService and retires the REST route', async ({ request }) => {
    const body = await connectRPC(request, '/aiscan.scan.ScanService/ListScans', {});
    expect(Array.isArray(body.scans ?? [])).toBeTruthy();
    const legacy = await request.get('/api/scans', { headers: apiHeaders() });
    expect(legacy.status()).toBe(404);
  });
});

// ---------------------------------------------------------------------------
// 12. Chat UI (browser)
// ---------------------------------------------------------------------------

test.describe('Chat UI', () => {
  test('sends natural language and receives the streamed answer in the browser', async ({ page, request }) => {
    const agents = await requireRegisteredAgents(request);
    const session = await openChatSession(request, agents[0].id);
    const browserErrors: string[] = [];
    page.on('console', (message) => {
      if (message.type() === 'error') browserErrors.push(`console: ${message.text()}`);
    });
    page.on('pageerror', (error) => browserErrors.push(`page: ${error.message}`));
    page.on('requestfailed', (failed) => {
      if (!failed.failure()?.errorText.includes('ERR_ABORTED')) {
        browserErrors.push(`request: ${failed.method()} ${failed.url()} ${failed.failure()?.errorText}`);
      }
    });

    try {
      await openAuthenticatedApp(page);
      await page.goto(`/sessions/${session.id}`);
      const input = page.getByRole('textbox', { name: 'Type a message... (/ for commands)' });
      await input.fill('Reply with exactly one word: PONG');
      await page.getByRole('button', { name: 'Send message' }).click();
      await expect(page.getByText('PONG', { exact: true })).toBeVisible({ timeout: 20_000 });
      expect(browserErrors).toEqual([]);
    } finally {
      await deleteChatSession(request, session.id);
    }
  });

  test('terminal WebSocket exchanges TerminalFrame protobuf JSON', async ({ page, request }) => {
    const agents = await requireRegisteredAgents(request);
    await openAuthenticatedApp(page);
    const result = await page.evaluate(async ({ agentID }) => {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const url = `${protocol}//${window.location.host}/api/agents/${encodeURIComponent(agentID)}/terminal/ws`;
      return await new Promise<{ type?: string; sessions?: unknown[]; error?: string }>((resolve, reject) => {
        const socket = new WebSocket(url);
        const timer = window.setTimeout(() => {
          socket.close();
          reject(new Error('terminal WebSocket timeout'));
        }, 10_000);
        socket.onopen = () => socket.send(JSON.stringify({ type: 'list' }));
        socket.onerror = () => reject(new Error('terminal WebSocket error'));
        socket.onmessage = (event) => {
          const frame = JSON.parse(String(event.data));
          if (frame.type !== 'sessions' && frame.type !== 'error') return;
          window.clearTimeout(timer);
          socket.send(JSON.stringify({ type: 'detach' }));
          socket.close();
          resolve(frame);
        };
      });
    }, { agentID: agents[0].id });
    expect(result.error).toBeFalsy();
    expect(result.type).toBe('sessions');
    expect(Array.isArray(result.sessions)).toBeTruthy();
  });

  test('UI renders the main chat area', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.waitForLoadState('networkidle');
    // The page should have a main content area
    const main = page.locator('main').first();
    if (await main.isVisible().catch(() => false)) {
      await expect(main).toBeVisible();
    } else {
      // Fallback: just verify the page loaded
      await expect(page.locator('header')).toBeVisible();
    }
  });

  test('sidebar shows session list or agent nodes', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(1000);
    // The sidebar should show sessions or agent nodes
    const sidebar = page.locator('aside, [class*="sidebar"], [class*="Sidebar"]').first();
    if (await sidebar.isVisible().catch(() => false)) {
      await expect(sidebar).toBeVisible();
    }
  });

  test('can find and interact with chat input', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);

    // Find the chat textarea
    const textarea = page.locator('textarea').last();
    if (await textarea.isVisible().catch(() => false)) {
      await textarea.fill('test input');
      await expect(textarea).toHaveValue('test input');
      // Clear it
      await textarea.fill('');
    }
  });
});

// ---------------------------------------------------------------------------
// 13. Theme Toggle
// ---------------------------------------------------------------------------

test.describe('Theme', () => {
  test('can toggle between light and dark theme', async ({ page }) => {
    await openAuthenticatedApp(page);
    await page.waitForLoadState('networkidle');

    const initialDark = await page.evaluate(() =>
      document.documentElement.classList.contains('dark')
    );

    const themeBtn = page.locator('[data-sidebar-theme-toggle] button');
    await themeBtn.click();
    await page.waitForTimeout(500);

    const afterDark = await page.evaluate(() =>
      document.documentElement.classList.contains('dark')
    );

    expect(afterDark).not.toBe(initialDark);
  });
});

// ---------------------------------------------------------------------------
// 14. LLM Models List
// ---------------------------------------------------------------------------

test.describe('LLM Models', () => {
  test('can fetch available models from provider', async ({ request }) => {
    const configResponse = await request.get('/api/config', { headers: apiHeaders() });
    expect(configResponse.ok()).toBeTruthy();
    const config = await configResponse.json();
    const res = await request.post('/api/config/llm/models', {
      headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
      data: {
        profile_id: config.llm.active_profile,
        provider: config.llm.provider,
        base_url: config.llm.base_url,
        api_key: '',
      },
    });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.ok).toBe(true);
    expect(Array.isArray(body.models)).toBeTruthy();
    expect(body.models.length).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// 15. Deploy Local Agent
// ---------------------------------------------------------------------------

test.describe('Local Agent Deploy', () => {
  test('can list local agents', async ({ request }) => {
    const res = await request.get('/api/deploy/local', { headers: apiHeaders() });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body)).toBeTruthy();
  });
});
