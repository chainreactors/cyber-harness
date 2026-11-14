// Full runtime suite, paired with recap-provider.mjs and the normal start-server.
// RECAP_TEST_PROVIDER_URL=http://127.0.0.1:38181
// BASE_URL=http://127.0.0.1:38080 npx playwright test e2e/recap-runtime.spec.ts
import { test, expect, type Page, type APIRequestContext } from '@playwright/test'
import { createNodeTask } from './task-ui'

const providerURL = process.env.RECAP_TEST_PROVIDER_URL
const token = process.env.ACCESS_KEY || 'test-token'
let since = 0
let pageErrors: string[] = []

test.skip(!providerURL, 'requires the local recap-provider.mjs fixture')
test.beforeEach(async ({ page }) => {
  since = Date.now()
  pageErrors = []
  page.on('pageerror', error => pageErrors.push(error.message))
  page.on('console', message => { if (message.type() === 'error') pageErrors.push(message.text()) })
  await page.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en')
    const sockets: WebSocket[] = []
    Object.assign(window, { recapTestSockets: sockets })
    const NativeWebSocket = window.WebSocket
    window.WebSocket = class extends NativeWebSocket {
      constructor(url: string | URL, protocols?: string | string[]) {
        super(url, protocols)
        sockets.push(this)
      }
    }
  })
  expect((await page.request.post('/api/auth/login', { data: { token } })).ok()).toBe(true)
  await page.goto('/?node=e2e-node')
  await createNodeTask(page)
})

test.afterEach(async ({ page }, info) => {
  await page.screenshot({ path: info.outputPath('recap-runtime.png'), fullPage: true })
  await info.attach('browser-errors', { body: JSON.stringify(pageErrors), contentType: 'application/json' })
  expect(pageErrors).toEqual([])
})

async function send(page: Page, scenario: string) {
  await page.getByRole('textbox', { name: 'Your goal' }).fill(`RECAP-E2E ${scenario} 请执行本地读取检查。`)
  await page.getByRole('button', { name: 'Send message', exact: true }).click()
  await expect(page).toHaveURL(/\/sessions\//)
}

async function calls(request: APIRequestContext, scenario: string, kind = 'recap') {
  const response = await request.get(providerURL + '/test/requests')
  expect(response.ok()).toBe(true)
  return (await response.json()).filter((entry: any) => entry.at >= since && entry.scenario === scenario && entry.kind === kind)
}

async function history(page: Page) {
  const sessionId = new URL(page.url()).pathname.split('/').at(-1)
  const response = await page.request.post('/cyber.rpc.chat.SessionService/ListEvents', {
    headers: { Authorization: 'Bearer ' + token, 'Connect-Protocol-Version': '1' }, data: { sessionId, limit: 500 },
  })
  expect(response.ok()).toBe(true)
  return (await response.json()).events.map((delivery: any) => delivery.event)
}

const recaps = (events: any[]) => events.filter(event => event.extension?.['@type'] === 'type.googleapis.com/cyber.agent.Recap')
const recapText = (scenario: string) => `已完成 ${scenario} 的本地检查并验证结果。`

test('real tools produce one small-model recap after completion, preserved by reload on desktop and mobile', async ({ page, request }, info) => {
  await send(page, 'basic')
  await expect(page.getByTestId('task-recap')).toHaveText(recapText('basic'))
  await expect(page.getByRole('button', { name: 'Pause response' })).toHaveCount(0)
  const events = await history(page)
  const summaries = recaps(events)
  expect(summaries).toHaveLength(1)
  const summary = summaries[0]
  const ended = events.find((event: any) => event.turnEnded && event.turnId === summary.turnId)
  expect(ended.turnEnded.stopReason).toBe('completed')
  expect(BigInt(summary.seq)).toBeGreaterThan(BigInt(ended.seq))
  expect(events.filter((event: any) => event.toolCall)).toHaveLength(1)
  expect(events.find((event: any) => event.toolResult)?.toolResult.isError).not.toBe(true)
  expect(events.some((event: any) => JSON.stringify(event).includes('PRIVATE_RECAP_THINKING_basic'))).toBe(true)
  const [call] = await calls(request, 'basic')
  expect(await calls(request, 'basic')).toHaveLength(1)
  expect(call).toMatchObject({ model: 'gpt-4.1-nano', tools: 0, stream: false, maxTokens: 256 })
  expect(call.text).toContain('tool call read')
  expect(call.text).toContain('Tool Playbook')
  expect(call.text).not.toContain('PRIVATE_RECAP_THINKING')
  expect(Buffer.byteLength(call.text) / 4).toBeLessThanOrEqual(10_000)
  await info.attach('recap-input', { body: JSON.stringify(call, null, 2), contentType: 'application/json' })
  await page.reload()
  await expect(page.getByTestId('task-recap')).toHaveText(recapText('basic'))
  const finalResponse = page.getByTestId('assistant-response').filter({ has: page.getByTestId('task-recap') })
  await expect(finalResponse).toHaveCount(1)
  await expect(finalResponse.getByTestId('assistant-response-content')).toHaveText('Task basic complete.')
  await page.setViewportSize({ width: 390, height: 844 })
  const collapse = page.getByRole('button', { name: 'Collapse sidebar' })
  if (await collapse.isVisible()) await collapse.click()
  const footer = finalResponse.getByTestId('assistant-response-footer')
  await expect(footer).toBeVisible()
  expect(await footer.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
  const answer = await finalResponse.getByTestId('assistant-response-content').boundingBox()
  const box = await footer.boundingBox()
  expect(box!.y).toBeGreaterThanOrEqual(answer!.y + answer!.height)
})

test('a delayed recap never blocks the next task or enters its context and survives WebSocket reconnect', async ({ page, request }) => {
  await send(page, 'late')
  try {
    await expect.poll(async () => (await calls(request, 'late')).length).toBe(1)
    await expect(page.getByText('Response complete', { exact: true })).toBeVisible()
    await expect(page.getByTestId('task-recap')).toHaveCount(0)
    await send(page, 'next')
    await expect(page.getByTestId('assistant-response-content').last()).toContainText('Task next complete.')
    await expect(page.getByRole('button', { name: 'Pause response' })).toHaveCount(0)
    await page.getByRole('textbox', { name: 'Your goal' }).fill('Unsaved next task')
  } finally {
    await request.post(providerURL + '/test/release?case=late')
  }
  await expect(page.getByTestId('task-recap')).toHaveText([recapText('late'), recapText('next')])
  const cards = page.getByTestId('assistant-response').filter({ has: page.getByTestId('task-recap') })
  await expect(cards).toHaveCount(2)
  await expect(cards.first().getByTestId('assistant-response-content')).toHaveText('Task late complete.')
  await expect(cards.first().getByTestId('task-recap')).toHaveText(recapText('late'))
  await expect(cards.last().getByTestId('assistant-response-content')).toHaveText('Task next complete.')
  await expect(cards.last().getByTestId('task-recap')).toHaveText(recapText('next'))
  await expect(page.getByRole('textbox', { name: 'Your goal' })).toHaveValue('Unsaved next task')
  const [next] = await calls(request, 'next')
  expect(next.text).not.toContain('RECAP-E2E late')
  expect(next.text).not.toContain('Task late complete.')
  for (const call of await calls(request, 'next', 'task')) expect(call.text).not.toContain(recapText('late'))
  const before = recaps(await history(page))
  let historyRequests = 0
  page.on('request', request => { if (request.url().includes('/ListEvents')) historyRequests++ })
  const socketCount = await page.evaluate(() => (window as any).recapTestSockets.length)
  await page.evaluate(() => (window as any).recapTestSockets.at(-1).close())
  await expect.poll(() => page.evaluate(() => (window as any).recapTestSockets.length)).toBeGreaterThan(socketCount)
  await expect.poll(() => page.evaluate(() => (window as any).recapTestSockets.at(-1).readyState)).toBe(1)
  await expect(page.getByTestId('task-recap')).toHaveText([recapText('late'), recapText('next')])
  await expect(page.getByRole('textbox', { name: 'Your goal' })).toHaveValue('Unsaved next task')
  expect(recaps(await history(page)).map((event: any) => event.id)).toEqual(before.map((event: any) => event.id))
  await send(page, 'reconnected')
  await expect(page.getByTestId('task-recap')).toHaveText([recapText('late'), recapText('next'), recapText('reconnected')])
  expect(historyRequests).toBe(0)
})

test('a task larger than 10k estimated tokens sends a bounded recap with recent results', async ({ page, request }) => {
  await send(page, 'long')
  await expect(page.getByTestId('task-recap')).toHaveText(recapText('long'))
  const events = await history(page)
  const visibleText = events.filter((event: any) => event.message || event.toolCall || event.toolResult)
    .map((event: any) => JSON.stringify(event)).join('')
  expect(Buffer.byteLength(visibleText) / 4).toBeGreaterThan(10_000)
  const [call] = await calls(request, 'long')
  expect(Buffer.byteLength(call.text) / 4).toBeLessThanOrEqual(10_000)
  expect(call.text).toContain('LONG_RECORD_START')
  expect(call.text).toContain('LONG_RECORD_END')
  expect(call.text).toContain('Task long complete.')
  expect(call.text).toContain('[...truncated...]')
  expect(call.text).not.toContain('PRIVATE_RECAP_THINKING')
})

test('switching to another node session keeps a pending recap in its original history', async ({ page, request }) => {
  await send(page, 'late')
  const original = page.url()
  try {
    await expect.poll(async () => (await calls(request, 'late')).length).toBe(1)
    await createNodeTask(page, 'e2e-node-2')
    await send(page, 'isolated')
    await expect(page.getByTestId('task-recap')).toHaveText(recapText('isolated'))
    await expect(page.getByTestId('assistant-response-content').filter({ hasText: 'Task late complete.' })).toHaveCount(0)
    expect((await history(page)).some((event: any) => event.emitter === 'e2e-node-2' && event.turnEnded)).toBe(true)
  } finally {
    await request.post(providerURL + '/test/release?case=late')
  }
  await page.goto(original)
  await expect(page.getByTestId('task-recap')).toHaveText(recapText('late'))
  await expect(page.getByTestId('assistant-response-content').filter({ hasText: 'Task isolated complete.' })).toHaveCount(0)
})

test('multiple evaluator rounds trigger one recap at the outer task boundary', async ({ page, request }) => {
  await page.getByRole('button', { name: 'Goal', exact: true }).click()
  await send(page, 'goal')
  await expect(page.getByTestId('task-recap')).toHaveText(recapText('goal'))
  const events = await history(page)
  expect(events.filter((event: any) => event.status?.state === 'eval_end')).toHaveLength(2)
  expect(events.filter((event: any) => event.turnEnded)).toHaveLength(1)
  expect(recaps(events)).toHaveLength(1)
  const collected = await calls(request, 'goal')
  expect(collected).toHaveLength(1)
  expect(collected[0].text).toContain('Task goal complete.')
  expect(collected[0].text).toContain('Task goal-second complete.')
  expect(collected[0].text).not.toContain('PRIVATE_RECAP_THINKING')
})

test('canceling a task produces only its interrupted-work recap', async ({ page, request }) => {
  await send(page, 'cancel')
  await expect(page.getByTestId('assistant-response-content').filter({ hasText: 'Waiting for the cancellation check.' })).toBeVisible()
  await page.getByRole('button', { name: 'Pause response' }).click()
  await expect(page.getByTestId('task-recap')).toHaveText('已读取本地技能，任务随后被取消。')
  const [call] = await calls(request, 'cancel')
  expect(call.text).toContain('Task stopped: canceled')
  expect(call.text).not.toContain('Task cancel complete.')
  expect(recaps(await history(page))).toHaveLength(1)
})

test('recap failure stays silent and the following task still completes', async ({ page, request }) => {
  await send(page, 'failure')
  await expect.poll(async () => (await calls(request, 'failure')).length).toBe(1)
  await expect(page.getByRole('button', { name: 'Pause response' })).toHaveCount(0)
  await send(page, 'after-failure')
  await expect(page.getByTestId('task-recap')).toHaveText(recapText('after-failure'))
  await expect(page.getByTestId('assistant-response').filter({ hasText: 'Task failure complete.' }).getByTestId('task-recap')).toHaveCount(0)
  expect(await calls(request, 'failure')).toHaveLength(1)
  expect(recaps(await history(page))).toHaveLength(1)
})

test('deleting a session while its recap is pending does not recreate the session', async ({ page, request }) => {
  await send(page, 'late')
  const sessionId = new URL(page.url()).pathname.split('/').at(-1)
  try {
    await expect.poll(async () => (await calls(request, 'late')).length).toBe(1)
    const deleted = await request.post('/cyber.rpc.chat.SessionService/DeleteSession', {
      headers: { Authorization: 'Bearer ' + token, 'Connect-Protocol-Version': '1' },
      data: { sessionId, requestId: 'recap-delete-' + Date.now() },
    })
    expect(deleted.ok()).toBe(true)
    expect((await deleted.json()).accepted).toBeTruthy()
  } finally {
    await request.post(providerURL + '/test/release?case=late')
  }
  await page.goto('/?node=e2e-node')
  await createNodeTask(page)
  await send(page, 'after-delete')
  await expect(page.getByTestId('task-recap')).toHaveText(recapText('after-delete'))
  const missing = await request.post('/cyber.rpc.chat.SessionService/GetSession', {
    headers: { Authorization: 'Bearer ' + token, 'Connect-Protocol-Version': '1' }, data: { sessionId },
  })
  expect(missing.status()).toBe(404)
})

test('unavailable small model falls back once and reuses the default for subsequent recaps', async ({ page, request }) => {
  await send(page, 'fallback')
  await expect(page.getByTestId('task-recap')).toHaveText(recapText('fallback'))
  expect((await calls(request, 'fallback')).map((call: any) => call.model)).toEqual(['gpt-4.1-nano', 'gpt-4.1'])
  await send(page, 'cached')
  await expect(page.getByTestId('task-recap')).toHaveText([recapText('fallback'), recapText('cached')])
  expect((await calls(request, 'cached')).map((call: any) => call.model)).toEqual(['gpt-4.1'])
  expect(recaps(await history(page))).toHaveLength(2)
})
