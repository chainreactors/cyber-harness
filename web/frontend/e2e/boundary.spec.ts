import { test, expect, type Page, type APIRequestContext, type WebSocketRoute } from '@playwright/test'
import { create, fromBinary, toBinary } from '@bufbuild/protobuf'
import { anyPack, anyUnpack } from '@bufbuild/protobuf/wkt'
import { EnvelopeSchema } from '../cyber-ui/packages/aop/src/gen/aop/envelope_pb'
import { ProtocolMessageSchema } from '../cyber-ui/packages/aop/src/gen/aop/protocol_pb'
import { createNodeTask } from './task-ui'

const providerURL = `http://127.0.0.1:${process.env.CYBER_BOUNDARY_PROVIDER_PORT || '38481'}`
const unique = (prefix: string) => `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
const composer = (page: Page) => page.getByRole('textbox', { name: /Type a message|Your goal/ })
const pause = (page: Page) => page.getByRole('button', { name: 'Pause response', exact: true })

async function state(request: APIRequestContext) {
  return (await request.get(`${providerURL}/control/state`)).json()
}
async function release(request: APIRequestContext, id: string) {
  await request.post(`${providerURL}/control/release?id=${id}`)
}
async function rpc(request: APIRequestContext, procedure: string, data: Record<string, unknown>) {
  const response = await request.post(`/cyber.rpc.chat.SessionService/${procedure}`, {
    headers: { Authorization: 'Bearer test-token', 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' }, data,
  })
  expect(response.ok(), await response.text()).toBeTruthy()
  return response.json()
}
async function events(request: APIRequestContext, sessionID: string) {
  return (await rpc(request, 'ListEvents', { sessionId: sessionID, limit: 500 })).events || []
}
async function newSession(page: Page, node = 'e2e-node') {
  await createNodeTask(page, node)
  const sessionID = new URL(page.url()).pathname.split('/').at(-1)!
  await expect(page.locator(`[data-session-id="${sessionID}"] textarea`)).toBeVisible()
  return sessionID
}
async function send(page: Page, id: string, mode = 'ok') {
  await composer(page).fill(`BOUNDARY-E2E ${id} ${mode}`)
  await page.getByRole('button', { name: 'Send message', exact: true }).click()
}
async function held(page: Page, request: APIRequestContext, id: string, mode = 'hold') {
  await send(page, id, mode)
  await expect.poll(async () => (await state(request)).pending).toContain(id)
  await expect(pause(page)).toBeVisible()
}
async function completed(page: Page, id: string) {
  await expect(page.getByTestId('assistant-response-content').filter({ hasText: `BOUNDARY-OK ${id}` })).toBeVisible()
  await expect(pause(page)).toBeHidden()
}

function decode(frame: string | Buffer) {
  if (typeof frame === 'string') return undefined
  const envelope = fromBinary(EnvelopeSchema, frame)
  const core = envelope.payload && anyUnpack(envelope.payload, ProtocolMessageSchema)
  return { envelope, core }
}
async function intercept(page: Page) {
  const sent: ReturnType<typeof decode>[] = []
  const received: { frame: string | Buffer; decoded: ReturnType<typeof decode> }[] = []
  let client: WebSocketRoute
  let server: WebSocketRoute
  let holdCase = ''
  let rejectCancel = false
  const withheld: (string | Buffer)[] = []
  await page.routeWebSocket('**/api/aop/application/ws', ws => {
    client = ws
    server = ws.connectToServer()
    ws.onMessage(frame => {
      const decoded = decode(frame)
      sent.push(decoded)
      if (rejectCancel && decoded?.core?.message.case === 'cancelTurnRequest') {
        const payload = create(ProtocolMessageSchema, { message: { case: 'cancelTurnResponse', value: { outcome: { case: 'rejected', value: { code: 'NOT_FOUND', message: 'fixture unknown turn' } } } } })
        ws.send(Buffer.from(toBinary(EnvelopeSchema, create(EnvelopeSchema, { id: unique('rejected'), replyTo: decoded.envelope.id, payload: anyPack(ProtocolMessageSchema, payload) }))))
      } else server.send(frame)
    })
    server.onMessage(frame => {
      const decoded = decode(frame)
      received.push({ frame, decoded })
      if (holdCase && decoded?.core?.message.case === holdCase) withheld.push(frame)
      else ws.send(frame)
    })
  })
  return {
    sent, received, withheld,
    hold: (value: string) => { holdCase = value },
    flush: () => { holdCase = ''; for (const frame of withheld.splice(0)) client.send(frame) },
    replay: (frame: string | Buffer) => client.send(frame),
    disconnect: () => server.close(),
    rejectCancel: () => { rejectCancel = true },
  }
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    const sockets: WebSocket[] = []
    const Native = window.WebSocket
    Object.assign(window, { boundarySockets: sockets })
    window.WebSocket = class extends Native {
      constructor(url: string | URL, protocols?: string | string[]) { super(url, protocols); sockets.push(this) }
    }
  })
  const response = await page.request.post('/api/auth/login', { data: { token: 'test-token' } })
  expect(response.ok()).toBeTruthy()
})

test('loaded history subscribes once from its last durable cursor', async ({ page, request }) => {
  const wire = await intercept(page)
  await page.goto('/')
  const session = await newSession(page)
  const id = unique('history-cursor')
  await send(page, id)
  await completed(page, id)
  const stored = await events(request, session)
  const lastCursor = stored.at(-1)?.cursor
  expect(lastCursor).toBeTruthy()
  wire.sent.length = 0
  await page.reload()
  await completed(page, id)
  const watches = () => wire.sent.filter(frame => frame?.core?.message.case === 'watchEventsRequest'
    && frame.core.message.value.sessionId === session)
  await expect.poll(() => watches().length).toBe(1)
  const watch = watches()[0]?.core?.message
  expect(watch?.case === 'watchEventsRequest' && watch.value.afterCursor).toBe(lastCursor)
})

test('reconnect during an intermediate assistant/tool step keeps the turn active', async ({ page, request }) => {
  await page.goto('/')
  const session = await newSession(page)
  const id = unique('midtool')
  try {
    await held(page, request, id, 'tool-hold')
    await expect(page.getByTestId('assistant-response-content').filter({ hasText: `INTERMEDIATE ${id}` })).toBeVisible()
    await page.evaluate(() => (window as any).boundarySockets.at(-1).close())
    await expect.poll(() => page.evaluate(() => (window as any).boundarySockets.filter((socket: WebSocket) => socket.readyState === 1).length)).toBeGreaterThan(0)
    // Wait for the durable subscription to resume, then give stale reconciliation
    // time to run. An intermediate assistant message is not a terminal event.
    await page.waitForTimeout(800)
    expect((await events(request, session)).some((delivery: any) => delivery.event?.turnEnded)).toBe(false)
    await expect(pause(page)).toBeVisible()
    await release(request, id)
    await completed(page, id)
  } finally { await release(request, id) }
})

test('a delayed cancel receipt from session A cannot stop session B', async ({ page, request }) => {
  const wire = await intercept(page)
  await page.goto('/')
  await newSession(page)
  const a = unique('cancel-a'), b = unique('cancel-b')
  try {
    await held(page, request, a)
    wire.hold('cancelTurnResponse')
    await pause(page).click()
    await expect.poll(() => wire.withheld.length).toBe(1)
    await newSession(page)
    await held(page, request, b)
    wire.flush()
    await page.waitForTimeout(200)
    await expect(pause(page)).toBeVisible()
    await release(request, b)
    await completed(page, b)
  } finally { wire.flush(); await release(request, a); await release(request, b) }
})

test('a delayed run receipt from session A does not block sending in session B', async ({ page, request }) => {
  const wire = await intercept(page)
  await page.goto('/')
  await newSession(page)
  const a = unique('send-a'), b = unique('send-b')
  try {
    wire.hold('runTurnResponse')
    await held(page, request, a)
    await expect.poll(() => wire.withheld.length).toBe(1)
    await newSession(page)
    await send(page, b)
    await expect.poll(async () => (await state(request)).calls.some((call: any) => call.id === b && call.stream), { timeout: 5_000 }).toBe(true)
    wire.flush()
    await completed(page, b)
  } finally { wire.flush(); await release(request, a) }
})

test('duplicate turnStarted and delta frames cannot reactivate an ended turn', async ({ page }) => {
  const wire = await intercept(page)
  await page.goto('/')
  await newSession(page)
  const id = unique('duplicate')
  await send(page, id)
  await completed(page, id)
  const started = wire.received.find(item => item.decoded?.core?.message.case === 'event' && item.decoded.core.message.value.payload.case === 'turnStarted')!
  expect(started).toBeTruthy()
  wire.replay(started.frame)
  await page.waitForTimeout(100)
  await expect(pause(page)).toBeHidden()
  const delta = wire.received.find(item => item.decoded?.core?.message.case === 'event' && item.decoded.core.message.value.payload.case === 'messageDelta')!
  wire.replay(delta.frame)
  await page.waitForTimeout(100)
  await expect(pause(page)).toBeHidden()
  await expect(page.getByTestId('assistant-response-content').filter({ hasText: `BOUNDARY-OK ${id}` })).toHaveCount(1)
})

for (const mode of ['quota', 'unauthorized', 'bad-request']) {
  test(`${mode} error terminates once and the same session accepts a recovery turn`, async ({ page, request }) => {
    await page.goto('/')
    const session = await newSession(page)
    const failed = unique(mode), recovered = unique('recover')
    await send(page, failed, mode)
    await expect.poll(async () => (await events(request, session)).filter((delivery: any) => delivery.event?.turnEnded).length).toBe(1)
    await expect(pause(page)).toBeHidden()
    await send(page, recovered)
    await completed(page, recovered)
    expect((await state(request)).calls.find((call: any) => call.id === recovered && call.stream).messages.some((message: any) => JSON.stringify(message.content).includes(failed))).toBe(true)
    expect((await events(request, session)).filter((delivery: any) => delivery.event?.turnEnded).length).toBe(2)
  })
}

for (const mode of ['retry', 'rate-limit', 'stream-break', 'split-unicode']) {
  test(`${mode} preserves one user input and one terminal`, async ({ page, request }) => {
    await page.goto('/')
    const session = await newSession(page)
    const id = unique(mode)
    await send(page, id, mode)
    await completed(page, id)
    const history = await events(request, session)
    expect(history.filter((delivery: any) => delivery.event?.turnEnded)).toHaveLength(1)
    expect(history.filter((delivery: any) => delivery.event?.emitter === 'cyber.web' && delivery.event?.message?.role === 'user' && JSON.stringify(delivery.event.message).includes(id))).toHaveLength(1)
    if (mode === 'split-unicode') await expect(page.getByTestId('assistant-response-content')).toContainText('中文 🧪 café')
    else {
      const calls = (await state(request)).calls.filter((call: any) => call.id === id && call.stream)
      expect(calls).toHaveLength(2)
      expect(calls[1].messages.some((message: any) => JSON.stringify(message).includes('STALE'))).toBe(false)
    }
  })
}

test('queued instructions survive pausing the currently running stream', async ({ page, request }) => {
  await page.goto('/')
  const session = await newSession(page)
  const a = unique('queue-a'), b = unique('queue-b')
  try {
    await held(page, request, a)
    await composer(page).fill(`BOUNDARY-E2E ${b} ok`)
    await composer(page).press('Enter')
    await expect.poll(async () => (await events(request, session)).filter((delivery: any) => delivery.event?.emitter === 'cyber.web' && delivery.event?.message?.role === 'user').length).toBe(2)
    await pause(page).click()
    await completed(page, b)
    expect((await state(request)).pending).not.toContain(a)
    expect((await events(request, session)).filter((delivery: any) => delivery.event?.turnEnded)).toHaveLength(2)
  } finally { await release(request, a) }
})

test('a rejected unknown-turn cancellation keeps the running stream pausable', async ({ page, request }) => {
  const wire = await intercept(page)
  await page.goto('/')
  await newSession(page)
  const id = unique('rejected-cancel')
  try {
    await held(page, request, id)
    wire.rejectCancel()
    await pause(page).click()
    await expect(page.getByText('fixture unknown turn', { exact: false }).first()).toBeVisible()
    await expect(pause(page)).toBeVisible()
    await release(request, id)
    await completed(page, id)
  } finally { await release(request, id) }
})

test('malformed WebSocket data does not crash the live WebUI', async ({ page, request }) => {
  const wire = await intercept(page)
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('/')
  await newSession(page)
  const id = unique('invalid-frame')
  try {
    await held(page, request, id)
    wire.replay(Buffer.from([255, 255, 255]))
    await expect(pause(page)).toBeVisible()
    await release(request, id)
    await completed(page, id)
    expect(errors).toEqual([])
  } finally { await release(request, id) }
})

test('refresh during a held stream restores its active turn and can cancel it', async ({ page, request }) => {
  await page.goto('/')
  const session = await newSession(page)
  const id = unique('refresh'), next = unique('refresh-next')
  try {
    await held(page, request, id)
    await page.reload()
    await expect(pause(page)).toBeVisible()
    await pause(page).click()
    await expect.poll(async () => (await events(request, session)).filter((delivery: any) => delivery.event?.turnEnded).length).toBe(1)
    await expect(pause(page)).toBeHidden()
    await send(page, next)
    await completed(page, next)
  } finally { await release(request, id) }
})

test('double Enter and IME confirmation cannot submit duplicate turns', async ({ page, request }) => {
  await page.goto('/')
  const session = await newSession(page)
  const id = unique('keyboard')
  await composer(page).fill(`BOUNDARY-E2E ${id} ok`)
  await composer(page).dispatchEvent('keydown', { key: 'Enter', code: 'Enter', isComposing: true, keyCode: 229 })
  await page.waitForTimeout(100)
  expect((await events(request, session)).filter((delivery: any) => delivery.event?.message?.role === 'user')).toHaveLength(0)
  await composer(page).evaluate(element => {
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
  })
  await completed(page, id)
  expect((await events(request, session)).filter((delivery: any) => delivery.event?.emitter === 'cyber.web' && delivery.event?.message?.role === 'user')).toHaveLength(1)
})

test('320px mobile layout handles long hostile Markdown without executing HTML', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.setViewportSize({ width: 320, height: 700 })
  await page.goto('/')
  await page.getByRole('button', { name: 'Chat history', exact: true }).click()
  await newSession(page)
  const close = page.getByRole('button', { name: 'Collapse sidebar', exact: true })
  if (await close.isVisible()) await close.click()
  const id = unique('markup')
  await send(page, id, 'markup')
  await expect(page.getByTestId('assistant-response-content')).toContainText(`SAFE ${id}`)
  await expect(pause(page)).toBeHidden()
  expect(await page.evaluate(() => Boolean((window as any).boundaryXSS))).toBe(false)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320)
  expect(errors).toEqual([])
})

async function saveModel(page: Page, model: string) {
  await page.getByRole('button', { name: 'Open settings', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Settings', exact: true })
  await dialog.getByRole('combobox', { name: 'Model', exact: true }).fill(model)
  await dialog.getByRole('button', { name: 'Save', exact: true }).click()
  return dialog
}

for (const node of ['e2e-node', 'e2e-node-2']) {
  test(`settings model changes preserve the active ${node} session and reject quota probes`, async ({ page, request }) => {
    test.setTimeout(60_000)
    await page.goto('/')
    const session = await newSession(page, node)
    const active = unique('settings-active'), next = unique('settings-next'), recovered = unique('settings-recovered')
    try {
      await held(page, request, active)
      await expect(await saveModel(page, 'boundary-model-b')).toBeHidden({ timeout: 15_000 })
      expect((await events(request, session)).filter((delivery: any) => delivery.event?.turnEnded)).toHaveLength(0)
      await expect(pause(page)).toBeVisible()
      await release(request, active)
      await completed(page, active)
      await send(page, next)
      await completed(page, next)
      const call = (await state(request)).calls.find((call: any) => call.id === next && call.stream)
      expect(call.model).toBe('boundary-model-b')
      expect(call.messages.some((message: any) => JSON.stringify(message.content).includes(active))).toBe(true)
      await page.getByRole('button', { name: 'Open settings', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: 'Settings', exact: true })
      await dialog.getByRole('combobox', { name: 'Model', exact: true }).fill('quota-model')
      await dialog.getByRole('button', { name: 'Test connection', exact: true }).click()
      await expect(dialog.getByText(/insufficient quota/).first()).toBeVisible({ timeout: 15_000 })
      await dialog.getByRole('button', { name: 'Close', exact: true }).click()
      await send(page, recovered)
      await completed(page, recovered)
      const recoveredCall = (await state(request)).calls.find((call: any) => call.id === recovered && call.stream)
      expect(recoveredCall.model).toBe('boundary-model-b')
      expect(recoveredCall.messages.some((message: any) => JSON.stringify(message.content).includes(active))).toBe(true)
      expect((await events(request, session)).filter((delivery: any) => delivery.event?.sessionStarted)).toHaveLength(1)
    } finally {
      await release(request, active)
      const dialog = page.getByRole('dialog', { name: 'Settings', exact: true })
      if (await dialog.isVisible()) await dialog.getByRole('button', { name: 'Close', exact: true }).click()
      await expect(await saveModel(page, 'boundary-model-a')).toBeHidden({ timeout: 15_000 })
    }
  })
}

test('offline completion replays its terminal and the same session can recover', async ({ page, request, context }) => {
  await page.goto('/')
  const session = await newSession(page)
  const id = unique('offline'), next = unique('offline-next')
  try {
    await held(page, request, id)
    await context.setOffline(true)
    await page.evaluate(() => (window as any).boundarySockets.forEach((socket: WebSocket) => socket.close()))
    await release(request, id)
    await expect.poll(async () => (await events(request, session)).filter((delivery: any) => delivery.event?.turnEnded).length).toBe(1)
    await context.setOffline(false)
    await completed(page, id)
    await send(page, next)
    await completed(page, next)
  } finally { await context.setOffline(false).catch(() => {}); await release(request, id) }
})

test('a second tab can cancel a held turn and both tabs converge', async ({ page, request, context }) => {
  await page.goto('/')
  await newSession(page)
  const id = unique('two-tabs'), next = unique('two-tabs-next')
  try {
    await held(page, request, id)
    const second = await context.newPage()
    await second.goto(page.url())
    await expect(pause(second)).toBeVisible()
    await pause(second).click()
    await expect(pause(page)).toBeHidden()
    await expect(pause(second)).toBeHidden()
    await send(page, next)
    await completed(page, next)
    await completed(second, next)
    await second.close()
  } finally { await release(request, id) }
})

test('empty and malformed HAR attachments do not strand subsequent turns', async ({ page, request }) => {
  await page.goto('/')
  const session = await newSession(page)
  for (const [name, buffer] of [['empty.har', Buffer.alloc(0)], ['中文-🧪.har', Buffer.from('{malformed JSON')]] as const) {
    const id = unique('har')
    await page.locator('input[type="file"]').setInputFiles({ name, mimeType: 'application/json', buffer })
    await send(page, id)
    await completed(page, id)
  }
  expect((await events(request, session)).filter((delivery: any) => delivery.event?.turnEnded)).toHaveLength(2)
})
