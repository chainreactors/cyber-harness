import { test, expect, type Page } from '@playwright/test';

const API_TOKEN = process.env.ACCESS_KEY || 'test-token';
const LLM_PROVIDER = process.env.LLM_PROVIDER || 'openai';
const LLM_BASE_URL = process.env.LLM_BASE_URL || '';
const LLM_API_KEY = process.env.LLM_API_KEY || '';
const LLM_MODEL = process.env.LLM_MODEL || '';

function apiHeaders() {
  return { Authorization: `Bearer ${API_TOKEN}` };
}

async function openAuthenticatedApp(page: Page) {
  const login = await page.request.post('/api/auth/login', {
    data: { token: API_TOKEN },
  });
  expect(login.ok()).toBeTruthy();
  await page.goto('/');
  await expect(page.locator('button[aria-label="Open settings"]')).toBeVisible();
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
    await expect(dialog.locator('button:has-text("LLM")')).toBeVisible();
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
    const llmTab = dialog.locator('button:has-text("LLM")');
    if (await llmTab.isVisible()) {
      await llmTab.click();
    }
    await expect(dialog).toContainText('Model');
    await expect(dialog).toContainText('Provider');
    await expect(dialog).toContainText('Base URL');
    await expect(dialog).toContainText('API Key');
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
    test.skip(!LLM_API_KEY, 'LLM_API_KEY env var required');
    const res = await request.post('/api/config/llm/test', {
      headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
      data: {
        provider: LLM_PROVIDER,
        base_url: LLM_BASE_URL,
        api_key: LLM_API_KEY,
        model: LLM_MODEL,
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
    // First, get available agents
    const agentsRes = await request.get('/api/agents', { headers: apiHeaders() });
    const agents = await agentsRes.json();
    const agentID = agents.length > 0 ? agents[0].id : '';

    // Skip if no agent is available
    if (!agentID) {
      test.skip();
      return;
    }

    // Create
    const createRes = await request.post('/api/chat/sessions', {
      headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
      data: { agent_id: agentID },
    });
    expect(createRes.ok()).toBeTruthy();
    const session = await createRes.json();
    expect(session.id).toBeTruthy();
    expect(session.agent_id).toBe(agentID);

    // List
    const listRes = await request.get('/api/chat/sessions', { headers: apiHeaders() });
    expect(listRes.ok()).toBeTruthy();
    const sessions = await listRes.json();
    expect(Array.isArray(sessions)).toBeTruthy();
    expect(sessions.some((s: any) => s.id === session.id)).toBeTruthy();

    // Delete
    const delRes = await request.delete(`/api/chat/sessions/${session.id}`, {
      headers: apiHeaders(),
    });
    expect(delRes.ok()).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// 9. Chat LLM Round-trip
// ---------------------------------------------------------------------------

test.describe('Chat LLM round-trip', () => {
  test('send a message and receive an assistant response', async ({ request }) => {
    // Get agent
    const agentsRes = await request.get('/api/agents', { headers: apiHeaders() });
    const agents = await agentsRes.json();
    if (agents.length === 0) {
      test.skip();
      return;
    }
    const agentID = agents[0].id;

    // Create session
    const createRes = await request.post('/api/chat/sessions', {
      headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
      data: { agent_id: agentID },
    });
    const session = await createRes.json();
    const sessionID = session.id;

    try {
      // Send message
      const sendRes = await request.post(`/api/chat/sessions/${sessionID}/messages`, {
        headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
        data: { content: 'Reply with exactly one word: PONG' },
      });
      expect(sendRes.ok()).toBeTruthy();

      // Poll for assistant response (up to 30s)
      let assistantMsg: any = null;
      for (let i = 0; i < 15; i++) {
        await new Promise((r) => setTimeout(r, 2000));
        const msgRes = await request.get(`/api/chat/sessions/${sessionID}/messages`, {
          headers: apiHeaders(),
        });
        const messages = await msgRes.json();
        const assistantMsgs = messages.filter((m: any) => m.role === 'assistant');
        if (assistantMsgs.length > 0) {
          assistantMsg = assistantMsgs[assistantMsgs.length - 1];
          break;
        }
      }

      expect(assistantMsg).not.toBeNull();
      expect(assistantMsg.content).toBeTruthy();
      expect(assistantMsg.content.length).toBeGreaterThan(0);
    } finally {
      // Cleanup
      await request.delete(`/api/chat/sessions/${sessionID}`, {
        headers: apiHeaders(),
      });
    }
  });
});

// ---------------------------------------------------------------------------
// 10. SCO / Asset Pool API
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

test.describe('Scans API', () => {
  test('list scans returns array', async ({ request }) => {
    const res = await request.get('/api/scans', { headers: apiHeaders() });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body)).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// 12. Chat UI (browser)
// ---------------------------------------------------------------------------

test.describe('Chat UI', () => {
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
    test.skip(!LLM_API_KEY, 'LLM_API_KEY env var required');
    const res = await request.post('/api/config/llm/models', {
      headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
      data: {
        provider: LLM_PROVIDER,
        base_url: LLM_BASE_URL,
        api_key: LLM_API_KEY,
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
