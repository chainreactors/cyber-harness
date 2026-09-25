import { expect, test } from '@playwright/test'
import { createNodeTask } from './task-ui'

test('chat session switching keeps an active run isolated from the new composer', async ({ page }) => {
  test.skip(Boolean(process.env.CYBER_E2E_LLM_BASE_URL), 'requires the scripted interaction provider fixture')
  test.setTimeout(60_000)
  await page.request.post('/api/auth/login', { data: { token: process.env.ACCESS_KEY || 'test-token' } })
  await page.goto('/')
  const nodeGroup = await createNodeTask(page)
  await expect(page).toHaveURL(/\/sessions\//)
  const firstURL = page.url()
  const input = page.getByRole('textbox', { name: 'Your goal' })
  await input.fill(`CHAT-INTERACTION ${Date.now()}`)
  await page.getByRole('button', { name: 'Send message' }).click()
  await expect(page.getByRole('button', { name: 'Pause response' })).toBeVisible()

  await nodeGroup.getByRole('button', { name: 'New task on e2e-node' }).click()
  await expect(page).not.toHaveURL(firstURL)
  await expect(page.getByRole('button', { name: 'Pause response' })).toHaveCount(0)
  await expect(page.getByRole('textbox', { name: 'Your goal' })).toBeEnabled()
  await input.fill('PONG')
  await page.getByRole('button', { name: 'Send message' }).click()
  await expect(page.getByTestId('assistant-response-content').getByText('PONG', { exact: true })).toBeVisible({ timeout: 20_000 })
  await expect(page.getByText('Delayed interaction response.', { exact: true })).toHaveCount(0)

  // The first run may finish after the switch; returning to its route must
  // restore its own response without leaking it into session two.
  await page.goto(firstURL)
  await expect(page.getByText('Delayed interaction response.', { exact: false })).toBeVisible({ timeout: 20_000 })
})

for (const scenario of ['retry', 'reconnect']) {
  test(`issue 145 related: ${scenario} preserves stream content and lifecycle`, async ({ page }, testInfo) => {
    test.skip(Boolean(process.env.CYBER_E2E_LLM_BASE_URL), 'requires the scripted stream fixture')
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.addInitScript(() => {
      const sockets: WebSocket[] = []
      Object.assign(window, { testSockets: sockets })
      const NativeWebSocket = window.WebSocket
      window.WebSocket = class extends NativeWebSocket {
        constructor(url: string | URL, protocols?: string | string[]) {
          super(url, protocols)
          sockets.push(this)
        }
      }
    })
    await page.request.post('/api/auth/login', { data: { token: process.env.ACCESS_KEY || 'test-token' } })
    await page.goto('/')
    await createNodeTask(page)
    await page.getByRole('textbox', { name: 'Your goal' }).fill(`ISSUE-145-STREAM ${scenario} ${Date.now()}`)
    await page.getByRole('button', { name: 'Send message' }).click()
    const pause = page.getByRole('button', { name: 'Pause response' })
    const content = page.getByTestId('assistant-response-content')

    if (scenario === 'retry') {
      await expect(content).toContainText('Stale attempt answer')
      await expect(content).toContainText('Replacement answer')
      await expect(pause).toBeVisible()
      await expect(content).not.toContainText('Stale attempt answer')
      const thinking = page.getByRole('button', { name: 'Thinking', exact: true })
      if (await thinking.getAttribute('aria-expanded') !== 'true') await thinking.click()
      await expect(page.getByRole('region', { name: 'Thinking', exact: true })).toContainText('Mixed frame thought')
      await expect(page.getByRole('region', { name: 'Thinking', exact: true })).not.toContainText('Stale attempt thought')
    } else {
      await expect(content).toContainText('Intermediate step before reconnect.')
      await expect(pause).toBeVisible()
      // Ensure the reconnect projection sees the intermediate assistant tail.
      const reconciled = page.waitForResponse(response => response.url().includes('/ListEvents') && response.ok())
      await page.evaluate(() => (window as any).testSockets.at(-1).close())
      await (await reconciled).finished()
      const sessionID = new URL(page.url()).pathname.split('/').filter(Boolean).at(-1)
      const historyResponse = await page.request.post('/cyber.rpc.chat.SessionService/ListEvents', {
        headers: { Authorization: `Bearer ${process.env.ACCESS_KEY || 'test-token'}`, 'Connect-Protocol-Version': '1' },
        data: { sessionId: sessionID },
      })
      expect(historyResponse.ok()).toBeTruthy()
      const history = await historyResponse.json()
      expect(history.events.map((delivery: any) => delivery.event?.message).filter(Boolean).at(-1)?.role).toBe('assistant')
      await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
      await expect(pause).toBeVisible()
      await expect(page.getByText('Response complete', { exact: true })).toHaveCount(0)
    }
    await page.screenshot({ path: testInfo.outputPath(`${scenario}-active.png`) })
    await expect(page.getByText('Response complete', { exact: true })).toBeVisible()
    await expect(pause).toHaveCount(0)
    await expect(content).toContainText(scenario === 'retry' ? 'Replacement answer' : 'Reconnect finished.')
    await page.reload()
    await expect(content).toContainText(scenario === 'retry' ? 'Replacement answer' : 'Reconnect finished.')
    await expect(content).not.toContainText('Stale attempt answer')
    await expect(pause).toHaveCount(0)
    expect(errors).toEqual([])
  })
}

for (const width of [1280, 520]) {
  test(`issues 143 and 145: Goal preserves streamed steps, reasoning, tools and round order (${width}px)`, async ({ page }, testInfo) => {
    test.skip(Boolean(process.env.CYBER_E2E_LLM_BASE_URL), 'requires the scripted issue-goal provider fixture')
    test.setTimeout(120_000)
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.request.post('/api/auth/login', { data: { token: process.env.ACCESS_KEY || 'test-token' } })
    await page.goto('/')
    await createNodeTask(page)
    await page.setViewportSize({ width, height: 900 })
    if (width < 768) await page.getByRole('button', { name: 'Collapse sidebar' }).click()
    await page.getByRole('button', { name: 'Goal', exact: true }).click()
    // A context attachment makes the first round large enough for the evaluator's
    // inherit_context=false verdict to actually compact, instead of resetting an
    // already small history. The UI folds inline file content as usual.
    await page.locator('input[type="file"]').setInputFiles({
      name: 'regression-context.txt', mimeType: 'text/plain', buffer: Buffer.from('local regression context\n'.repeat(3600)),
    })
    await page.getByRole('button', { name: 'UP', exact: true }).click()
    await expect(page.getByRole('button', { name: 'CTX', exact: true })).toBeVisible()
    await page.getByRole('textbox', { name: 'Your goal' }).fill('ISSUE-143-145: complete both regression rounds using local checks only')
    await page.getByRole('button', { name: 'Send message' }).click()

    const reasoning = page.getByRole('button', { name: 'Thinking', exact: true }).first()
    await expect(reasoning).toBeVisible()
    await expect(reasoning).toHaveAttribute('aria-expanded', 'true')
    // An extended reasoning stream must remain inside its own scroll area.
    const thinkingBody = page.getByRole('region', { name: 'Thinking', exact: true }).first()
    await expect(thinkingBody).toBeVisible()
    expect((await thinkingBody.boundingBox())!.height).toBeLessThanOrEqual(260)
    await page.screenshot({ path: testInfo.outputPath('reasoning-stream.png') })

    await expect(page.getByText('Round two complete.', { exact: true })).toBeVisible({ timeout: 60_000 })
    await expect(page.getByText('Response complete', { exact: true })).toBeVisible()
    const transcript = page.locator('[data-testid="assistant-response-content"]')
    await expect(transcript.filter({ hasText: 'First step: read the embedded skill.' })).toHaveCount(1)
    await expect(transcript.filter({ hasText: 'Second step: check command composition.' })).toHaveCount(1)
    await expect(transcript.filter({ hasText: 'Round one complete.' })).toHaveCount(1)
    await expect(transcript.filter({ hasText: 'Round two complete.' })).toHaveCount(1)
    // Round two must follow its preceding verdict, instead of being appended to
    // the first card above both verdicts.
    const order = await page.locator('[data-testid="assistant-response-content"], [role="status"]').allTextContents()
    expect(order.findIndex(text => text.includes('Run a second regression round')))
      .toBeLessThan(order.findIndex(text => text.includes('Round two complete.')))
    expect(order.filter(text => text.includes('Run a second regression round'))).toHaveLength(1)
    const compact = page.getByRole('status').filter({ hasText: 'Context compacted' })
    await expect(compact).toBeVisible()
    await expect(compact).not.toContainText('?')
    const compactBox = (await compact.boundingBox())!
    const nextRoundBox = (await page.getByTestId('assistant-response').nth(1).boundingBox())!
    expect(nextRoundBox.y - compactBox.y - compactBox.height).toBeGreaterThanOrEqual(0)
    expect(nextRoundBox.y - compactBox.y - compactBox.height).toBeLessThan(50)

    const firstCard = page.getByTestId('assistant-response').first()
    await firstCard.getByRole('button', { name: 'Thinking', exact: true }).click()
    await expect(firstCard).toContainText('ISSUE-143-145 first reasoning')
    await expect(firstCard).toContainText('ISSUE-143-145 second reasoning')
    await firstCard.getByRole('button', { name: /2 tools/i }).click()
    await expect(firstCard).not.toContainText('virtual filesystem is not mounted')
    await expect(firstCard).not.toContainText('unknown flag')
    const sessionID = new URL(page.url()).pathname.split('/').filter(Boolean).at(-1)
    const events = await page.request.post('/cyber.rpc.chat.SessionService/ListEvents', {
      headers: { Authorization: `Bearer ${process.env.ACCESS_KEY || 'test-token'}`, 'Connect-Protocol-Version': '1' },
      data: { sessionId: sessionID },
    })
    expect(events.ok()).toBeTruthy()
    const body = await events.json()
    const results = body.events.map((delivery: any) => delivery.event?.toolResult).filter(Boolean)
    expect(results).toHaveLength(2)
    expect(results.every((result: any) => !result.isError)).toBeTruthy()
    expect(JSON.stringify(results)).toContain('ISSUE-143-145-shell-ok')
    expect(JSON.stringify(results)).not.toContain('unknown flag')
    expect(body.events.some((delivery: any) => delivery.event?.status?.state === 'compact_end')).toBeTruthy()
    await firstCard.getByRole('button', { name: 'Thinking', exact: true }).click()
    await firstCard.getByRole('button', { name: /2 tools/i }).click()
    await page.screenshot({ path: testInfo.outputPath('goal-completed.png'), fullPage: true })
    await page.reload()
    await expect(page.getByText('Round two complete.', { exact: false }).first()).toBeVisible()
    await expect(page.getByTestId('assistant-response-content').filter({ hasText: 'First step: read the embedded skill.' })).toHaveCount(1)
    expect(errors).toEqual([])
  })
}
