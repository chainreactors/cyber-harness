import { ClaimType } from '../src/gen/decision/claim_pb'
import { test, expect, type Page } from '@playwright/test'
import { create, toBinary, type MessageInitShape } from '@bufbuild/protobuf'
import { anyPack } from '@bufbuild/protobuf/wkt'
import { EventSchema } from '../cyber-ui/packages/aop/src/gen/aop/event_pb'
import { RuntimeEventSchema } from '../src/gen/types/jev_pb'

function runtime(seq: number, payload: MessageInitShape<typeof RuntimeEventSchema>['payload'], segmentId = 'probe') {
  return create(EventSchema, { id: `deep-${seq}`, seq: BigInt(seq), sessionId: 'session-1', turnId: 'turn-1',
    payload: { case: 'extension', value: anyPack(RuntimeEventSchema, create(RuntimeEventSchema, {
      taskId: 'deep-task', segmentId, payload,
    })) } })
}

async function mount(page: Page) {
  // Command discovery is unrelated to the fixture's in-memory event transport.
  await page.route('**/cyber.rpc.chat.SessionService/ListCommands', route => route.fulfill({
    contentType: 'application/json', body: '{"commands":[]}',
  }))
  await page.addInitScript(() => localStorage.setItem('cyber-locale', 'zh'))
  await page.goto('/e2e/fixtures/jev.html')
  await page.waitForFunction(() => typeof (window as any).renderJEVEvents === 'function')
}

async function render(page: Page, events: ReturnType<typeof runtime>[], append = false) {
  await page.evaluate(({ values, append }) => (window as any).renderJEVEvents(values, append), {
    values: events.map(event => [...toBinary(EventSchema, event)]), append,
  })
}

for (const width of [390, 1440]) {
  test(`long decision text stays inside the chat at ${width}px and remains keyboard accessible`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await mount(page)
    const description = '连续证据与完整参数'.repeat(64)
    const claims = Object.fromEntries(Array.from({ length: 4 }, (_, i) => [`q${i}`, {
      type: ClaimType.choice, context: description.repeat(2),
      options: Array.from({ length: 16 }, (_, j) => `c${j}: ${description}`),
    }]))
    await render(page, [runtime(1, { case: 'boundary', value: { reason: 'checking' } }),
      runtime(2, { case: 'decisionRequest', value: { requestId: 'long', claims } }),
      runtime(3, { case: 'decisionResult', value: { requestId: 'long', evaluations: Object.fromEntries(
        Object.keys(claims).map(id => [id, { value: { case: 'choice', value: `c15: ${description}` }, probabilities: { [`c15: ${description}`]: .9 } }])) } }),
      runtime(4, { case: 'boundary', value: { reason: 'defer' } })])
    const card = page.getByTestId('agent-workflow')
    await expect(card).toHaveCount(1)
    await card.locator(':scope > summary').focus()
    await page.keyboard.press('Space')
    await page.keyboard.press('Space')
    await expect(card).toHaveAttribute('open', '')
    await page.locator('[data-record-id="deep-2"]').click()
    await expect(page.getByTestId('jev-question')).toHaveCount(4)
    await expect(page.locator('[data-control-current=true]').locator('[data-selected=true]')).toHaveCount(4)
    await expect(card.locator('[data-runtime-claim]')).toHaveCount(64)
    await expect(card.locator('[data-runtime-claim][data-selected=true]')).toHaveCount(4)
    expect(await page.locator('[data-control-current=true]').evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    const nodes = card.locator('[data-control-node]')
    await nodes.first().focus()
    await page.keyboard.press('Enter')
    await expect(nodes.first()).toHaveAttribute('data-control-current', 'true')
    await page.getByRole('button', { name: 'Reflex', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Reflex', exact: true })).toBeFocused()
    await page.screenshot({ path: info.outputPath('long-decisions.png') })
  })
}

test('200 checkpoints survive duplicate delivery and later updates without losing old decisions', async ({ page }, info) => {
  await mount(page)
  const events = Array.from({ length: 200 }, (_, i) => {
    const segment = `checkpoint-${i}`, seq = i * 4 + 1, requestId = `request-${i}`
    return [runtime(seq, { case: 'boundary', value: { reason: 'checking' } }, segment),
      runtime(seq + 1, { case: 'decisionRequest', value: { requestId, claims: {
        next: { type: ClaimType.choice, context: "Choose the current operation." + "\nread: Inspect\ndefer: New reasoning", options: ["read","defer"] },
      } } }, segment),
      runtime(seq + 2, { case: 'decisionResult', value: { requestId, evaluations: { next: { value: { case: 'choice', value: 'defer' } } } } }, segment),
      runtime(seq + 3, { case: 'boundary', value: { reason: 'defer' } }, segment)]
  }).flat()
  const started = Date.now()
  await render(page, [...events, ...events])
  await expect(page.locator('[data-control-node][data-kind=decision]')).toHaveCount(200)
  await render(page, events, true)
  await expect(page.locator('[data-control-node][data-kind=decision]')).toHaveCount(200)
  await page.locator('[data-control-node][data-kind=decision]').first().click()
  await expect(page.locator('[data-control-current=true]').locator('[data-option-id=defer]')).toHaveAttribute('data-selected', 'true')
  await info.attach('checkpoint-metrics', { contentType: 'application/json', body: JSON.stringify({
    checkpoints: 200, deliveries: 2400, elapsedMs: Date.now() - started,
  }) })
})

test('motion inspection records pending and selected states under both motion preferences', async ({ page }, info) => {
  await mount(page)
  const metrics = []
  for (const reducedMotion of ['no-preference', 'reduce'] as const) {
    await page.emulateMedia({ reducedMotion })
    await render(page, [runtime(1, { case: 'boundary', value: { reason: 'checking' } }),
      runtime(2, { case: 'decisionRequest', value: { requestId: 'motion', claims: {
        next: { type: ClaimType.choice, context: "Choose the current operation." + "\nread: Inspect\ndefer: New reasoning", options: ["read","defer"] },
      } } })])
    await page.locator('[data-control-node][data-kind=decision]').click()
    await expect(page.getByTestId('jev-decision')).toHaveAttribute('data-state', 'judging')
    await expect(page.locator('[data-selected=true]')).toHaveCount(0)
    const pending = await page.getByTestId('jev-decision').evaluate(el => [...el.querySelectorAll('*')]
      .map(node => ({ tag: node.tagName, animation: getComputedStyle(node).animationName }))
      .filter(value => value.animation !== 'none'))
    await render(page, [runtime(3, { case: 'decisionResult', value: { requestId: 'motion', evaluations: {
      next: { value: { case: 'choice', value: 'read' }, probabilities: { read: .9, defer: .1 } },
    } } })], true)
    await expect(page.getByTestId('jev-decision')).toHaveAttribute('data-state', 'answered')
    await expect(page.locator('[data-option-id=read]')).toHaveAttribute('data-selected', 'true')
    const selected = await page.locator('[data-option-id=read]').evaluate(el => ({
      animation: getComputedStyle(el).animationName,
      transition: getComputedStyle(el).transitionProperty,
      duration: getComputedStyle(el).transitionDuration,
      probabilityTransition: getComputedStyle(el.querySelector('.jev-probability-track span')!).transitionDuration,
    }))
    if (reducedMotion === 'reduce') {
      expect(selected.duration).toBe('0s')
      expect(selected.probabilityTransition).toBe('0s')
    } else {
      expect(selected.duration).not.toBe('0s')
      expect(selected.probabilityTransition.split(',').every(value => parseFloat(value) > 0)).toBe(true)
    }
    metrics.push({ reducedMotion, pending, selected })
  }
  await info.attach('motion-metrics', { contentType: 'application/json', body: JSON.stringify(metrics, null, 2) })
})


test('foreground arguments and tool-free generated results stay in the execution timeline', async ({ page }) => {
  await mount(page)
  const definition = { id: 'semantic-reflex', when: 'Classify and compute current input', decide: 'Execute the selected semantic handler', observe: 'js:function(context,args){return {report:args};}' }
  await render(page, [runtime(1, { case: 'takeover', value: { definition } }),
    runtime(2, { case: 'generation', value: { kind: 'parameters_llm', state: 'started', requestId: 'parameters' } }),
    runtime(3, { case: 'generation', value: { kind: 'parameters_llm', state: 'finished', requestId: 'parameters', output: '{"values":[3,9]}', usage: { inputTokens: 20n, outputTokens: 8n } } }),
    runtime(4, { case: 'observation', value: { stateJson: '{"arguments":{"values":[3,9]},"result":{"report":{"answer":12}}}' } }),
    runtime(5, { case: 'handoff', value: { reason: 'report' } })])
  const segment = page.getByTestId('agent-workflow')
  await page.locator('[data-record-id="deep-2"]').click()
  await expect(segment).toContainText('当前任务参数')
  await expect(segment.getByTestId('token-usage')).toHaveText('↓20↑8')
  await page.locator('[data-record-id="deep-4"]').click()
  await expect(segment).toContainText('answer')
  await expect(segment).toContainText('12')
  await expect(page.getByTestId('jev-compilation')).toHaveCount(0)
})
