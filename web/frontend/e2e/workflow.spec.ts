import { ClaimType } from '../src/gen/decision/claim_pb'
import { test, expect, type Page } from '@playwright/test'
import { create, fromBinary, toBinary } from '@bufbuild/protobuf'
import { anyPack, timestampFromDate } from '@bufbuild/protobuf/wkt'
import { EventSchema } from '../cyber-ui/packages/aop/src/gen/aop/event_pb'
import { ArtifactSchema } from '../cyber-ui/packages/aop/src/gen/aop/tool/protocol_pb'
import { RefSchema, Correlation } from '../cyber-ui/packages/aop/src/gen/aop/operation/protocol_pb'
import { SyncArtifactsRequestSchema, SyncArtifactsResponseSchema } from '../src/gen/types/artifact_pb'
import { RuntimeEventSchema } from '../src/gen/types/jev_pb'
import { reduceAOPToTimeline } from '../cyber-ui/packages/viewer/src/lib/aop-reducer'
import { projectJEV, withJEV } from '../src/lib/jev-view'
import { withWorkflows, type WorkflowTurn } from '../src/lib/workflow-view'
import { EvalDetailSchema } from '../src/gen/types/agent_pb'

function event(seq: number, payload: any, sessionId = 'session-1', turnId = 'turn-1') {
  return create(EventSchema, { id: `${sessionId}-${turnId}-${seq}`, seq: BigInt(seq), sessionId, turnId, emitter: 'agent',
    emittedAt: timestampFromDate(new Date(1700000000000 + seq * 1000)), payload })
}
function trace(seq: number, payload: any, background = false, session = 'session-1', turn = 'turn-1') {
  return event(seq, { case: 'extension', value: anyPack(RuntimeEventSchema, create(RuntimeEventSchema, {
    taskId: 'task', segmentId: background ? '' : 'loop', step: 1, background, payload,
  })) }, session, turn)
}
const question = { type: ClaimType.choice, context: "Choose the current operation." + "\nread: Read evidence\ndefer: More reasoning", options: ["read","defer"] }
const call = { id: 'call', name: 'read-evidence', arguments: { data: new TextEncoder().encode('{"target":"current"}') } }
const definition = { id: 'read-loop', when: 'Read current evidence', decide: 'Choose a supplied operation', observe: 'js:function(){return {report:1}}' }
const project = (events: ReturnType<typeof event>[]) => withWorkflows(withJEV(reduceAOPToTimeline(events), projectJEV(events)), events)
const turns = (events: ReturnType<typeof event>[]) => project(events).filter(item => item.kind === 'extension' && item.extensionType === 'workflow').map(item => (item as any).data.workflow as WorkflowTurn)
async function mount(page: Page) {
  await page.route('**/cyber.rpc.chat.SessionService/ListCommands', route => route.fulfill({ contentType: 'application/json', body: '{"commands":[]}' }))
  await page.addInitScript(() => localStorage.setItem('cyber-locale', 'zh'))
  await page.goto('/e2e/fixtures/jev.html')
  await page.waitForFunction(() => typeof (window as any).renderJEVEvents === 'function')
}
async function render(page: Page, events: ReturnType<typeof event>[], append = false) {
  await page.evaluate(({ values, append }) => (window as any).renderJEVEvents(values, append), { values: events.map(e => [...toBinary(EventSchema, e)]), append })
}

for (const width of [390, 1280]) test(`execution review retains each model step, receipts and usage through replay ${width}`, async ({ page }, info) => {
  await page.setViewportSize({ width, height: 900 })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await mount(page)
  const reasoning = (seq: number, text: string) => event(seq, { case: 'message', value: { id: `thought-${seq}`, role: 'assistant',
    content: [{ value: { case: 'reasoning', value: { text } } }] } })
  const first = [event(1, { case: 'turnStarted', value: {} }), event(2, { case: 'status', value: { state: 'llm_request' } }),
    reasoning(3, 'Inspect the service before attempting verification.'),
    event(4, { case: 'toolCall', value: { id: 'inspect', name: 'bash', arguments: { data: new TextEncoder().encode('{"command":"curl http://lab/"}') } } }),
    event(5, { case: 'toolResult', value: { callId: 'inspect', name: 'bash', durationMs: 250n, output: [{ value: { case: 'text', value: { text: 'HTTP 200 service found' } } }] } }),
    event(6, { case: 'usage', value: { inputTokens: 1000n, outputTokens: 200n, totalTokens: 1200n, detail: { cache_read: 800n } } })]
  await render(page, first)
  const review = page.getByTestId('workflow-review')
  await review.locator('summary').click()
  await expect(review).toContainText('1 次工具已返回')
  const rest = [event(7, { case: 'status', value: { state: 'llm_request' } }), reasoning(8, 'Attempt verification and record the actual failure.'),
    event(9, { case: 'toolCall', value: { id: 'verify', name: 'bash', arguments: { data: new TextEncoder().encode('{"command":"curl http://lab/verify"}') } } }),
    event(10, { case: 'toolCall', value: { id: 'unfinished', name: 'read', arguments: { data: new TextEncoder().encode('{"path":"evidence.txt"}') } } }),
    event(11, { case: 'toolResult', value: { callId: 'verify', name: 'bash', isError: true, durationMs: 1250n, output: [{ value: { case: 'text', value: { text: 'HTTP 502 verification unavailable' } } }] } }),
    event(12, { case: 'usage', value: { inputTokens: 2000n, outputTokens: 400n, totalTokens: 2400n, detail: { cache_read: 400n } } }),
    event(13, { case: 'status', value: { state: 'llm_request' } }),
    event(14, { case: 'message', value: { id: 'answer', role: 'assistant', content: [{ value: { case: 'text', value: { text: 'Service found; verification remains incomplete.' } } }] } }),
    event(15, { case: 'usage', value: { inputTokens: 300n, outputTokens: 10n, totalTokens: 310n } }),
    { ...event(16, { case: 'status', value: { state: 'eval_end' } }), extensions: [anyPack(EvalDetailSchema, create(EvalDetailSchema, { pass: false, reason: 'Missing verification evidence' }))] },
    event(17, { case: 'turnEnded', value: { stopReason: 'completed', usage: { inputTokens: 3300n, outputTokens: 610n, totalTokens: 3910n } } })]
  await render(page, rest, true)
  await expect(page.locator('[data-kind=reasoning]')).toHaveCount(2)
  await expect(review.getByTestId('workflow-review-usage')).toHaveCount(3)
  await expect(review.locator('summary')).toContainText('3.9k tok')
  await expect(review.locator('summary')).toContainText('1 次工具失败')
  await expect(review.locator('summary')).toContainText('1 次工具未收到结果')
  await expect(review).toContainText('HTTP 502 verification unavailable')
  await expect(review).toContainText('1.3s')
  await expect(review).toContainText('缓存 80%')
  await expect(review.locator('[data-state=failed]')).toContainText(['bash', '完成评估'])
  await review.getByRole('button', { name: 'Token 用量', exact: true }).click()
  const highest = review.locator('[data-review-node]').first()
  await expect(highest).toContainText('Attempt verification and record the actual failure.')
  await highest.getByRole('button').click()
  await expect(page.locator('[data-control-current=true]')).toContainText('Attempt verification and record the actual failure.')
  await render(page, [...first, ...rest, ...first, ...rest])
  await expect(review.getByTestId('workflow-review-usage')).toHaveCount(3)
  await expect(review.locator('summary')).toContainText('3.9k tok')
  await expect(page.locator('[data-kind=reasoning]')).toHaveCount(2)
  const overflow = await review.evaluate(element => element.scrollWidth > element.clientWidth)
  expect(overflow).toBe(false)
  await page.screenshot({ path: info.outputPath(`execution-review-${width}.png`), fullPage: true })
})

test('one turn owns ordinary tools and JEV calls, even with duplicated native and runtime deliveries', () => {
  const events = [event(1, { case: 'turnStarted', value: {} }), event(2, { case: 'toolCall', value: { ...call, id: 'ordinary' } }),
    event(3, { case: 'toolResult', value: { callId: 'ordinary', name: call.name } }), trace(4, { case: 'takeover', value: { definition } }),
    trace(5, { case: 'dispatch', value: { call } }), event(6, { case: 'toolCall', value: call }),
    trace(7, { case: 'result', value: { result: { callId: call.id, name: call.name } } }), event(8, { case: 'toolResult', value: { callId: call.id, name: call.name } }),
    trace(9, { case: 'handoff', value: { reason: 'report' } }), event(10, { case: 'message', value: { id: 'reply', role: 'assistant', content: [{ value: { case: 'text', value: { text: 'Actual answer' } } }] } }),
    event(11, { case: 'turnEnded', value: { stopReason: 'completed' } })]
  const workflows = turns([...events, ...events])
  expect(workflows).toHaveLength(1)
  expect(workflows[0].nodes.filter(node => node.kind === 'tool')).toHaveLength(2)
  expect(new Set(workflows[0].nodes.map(node => node.id)).size).toBe(workflows[0].nodes.length)
  expect(workflows[0].nodes[workflows[0].nodes.length - 1].kind).toBe('response')
})

test('stream retries keep their message identity, request counts and final usage', async ({ page }) => {
  await mount(page)
  const values = [event(1, { case: 'turnStarted', value: {} }), event(2, { case: 'status', value: { state: 'llm_request' } }),
    event(3, { case: 'messageDelta', value: { messageId: 'retried', value: { case: 'reasoning', value: 'Discarded partial thought' } } }),
    event(4, { case: 'message', value: { id: 'retried', role: 'assistant', content: [] } }),
    event(5, { case: 'status', value: { state: 'llm_request' } }),
    event(6, { case: 'message', value: { id: 'retried', role: 'assistant', content: [{ value: { case: 'reasoning', value: { text: 'Recovered reasoning after retry' } } }] } }),
    event(7, { case: 'toolCall', value: call }), event(8, { case: 'toolResult', value: { callId: call.id, name: call.name } }),
    event(9, { case: 'usage', value: { inputTokens: 600n, outputTokens: 60n, totalTokens: 660n, detail: { requests: 2n } } }),
    event(10, { case: 'status', value: { state: 'llm_request' } }),
    event(11, { case: 'message', value: { id: 'final', role: 'assistant', content: [{ value: { case: 'text', value: { text: 'Final response after tool receipt' } } }] } }),
    event(12, { case: 'usage', value: { inputTokens: 100n, outputTokens: 10n, totalTokens: 110n, detail: { requests: 1n } } }),
    event(13, { case: 'turnEnded', value: { usage: { totalTokens: 770n } } })]
  await render(page, values.slice(0, 4))
  await render(page, values.slice(4), true)
  await expect(page.locator('[data-kind=reasoning]')).toHaveCount(1)
  await expect(page.locator('[data-kind=reasoning]')).toContainText('Recovered reasoning after retry')
  await expect(page.getByText('Discarded partial thought', { exact: true })).toHaveCount(0)
  const review = page.getByTestId('workflow-review')
  await review.locator('summary').click()
  await expect(review.locator('summary')).toContainText('770 tok')
  await expect(review).toContainText('2 次请求（含重试）')
  await expect(review.getByTestId('workflow-review-usage')).toHaveCount(2)
  await render(page, [...values, ...values])
  await expect(review.locator('summary')).toContainText('770 tok')
  await expect(review.getByTestId('workflow-review-usage')).toHaveCount(2)
})

test('review excludes compiler aggregate counters when individual rounds are recorded', async ({ page }) => {
  await mount(page)
  await render(page, [event(1, { case: 'turnStarted', value: {} }),
    trace(2, { case: 'generation', value: { kind: 'compiler_round', state: 'finished', requestId: 'round', parentRequestId: 'compiler', usage: { inputTokens: 100n, outputTokens: 10n } } }, true),
    trace(3, { case: 'generation', value: { kind: 'reflex_llm', state: 'finished', requestId: 'compiler', usage: { inputTokens: 100n, outputTokens: 10n } } }, true),
    trace(4, { case: 'decisionResult', value: { requestId: 'judge', usage: { inputTokens: 20n, outputTokens: 2n } } }, true)])
  const review = page.getByTestId('workflow-review')
  await expect(review.locator('summary')).toContainText('132 tok')
  await review.locator('summary').click()
  await expect(review.getByTestId('workflow-review-usage')).toHaveCount(2)
})

test('reused request and call IDs stay separate across sessions and turns', () => {
  const events = ['a', 'b'].flatMap((session, index) => ['one', 'two'].flatMap((turn, turnIndex) => {
    const n = index * 20 + turnIndex * 10
    return [event(n + 1, { case: 'turnStarted', value: {} }, session, turn), trace(n + 2, { case: 'takeover', value: { definition } }, false, session, turn),
      trace(n + 3, { case: 'decisionRequest', value: { requestId: 'same', claims: { next: question } } }, false, session, turn),
      trace(n + 4, { case: 'decisionResult', value: { requestId: 'same', evaluations: { next: { value: { case: 'choice', value: 'read' } } } } }, false, session, turn),
      trace(n + 5, { case: 'dispatch', value: { call } }, false, session, turn), trace(n + 6, { case: 'result', value: { result: { callId: call.id, name: call.name } } }, false, session, turn)]
  }))
  const workflows = turns(events)
  expect(workflows).toHaveLength(4)
  for (const workflow of workflows) {
    expect(workflow.nodes.filter(node => node.kind === 'decision')).toHaveLength(1)
    expect(workflow.nodes.filter(node => node.kind === 'tool')).toHaveLength(1)
    expect(workflow.nodes.find(node => node.kind === 'decision')?.state).toBe('completed')
  }
})

test('delegated execution branches once and background events stay in their original completed turn', () => {
  const events = [event(1, { case: 'turnStarted', value: {} }), event(2, { case: 'toolCall', value: { id: 'delegate', name: 'subagent' } }),
    event(3, { case: 'sessionStarted', value: { parentSessionId: 'session-1', parentToolCallId: 'delegate', agentName: 'worker' } }, 'child', ''),
    event(4, { case: 'turnStarted', value: {} }, 'child', 'child-turn'), event(5, { case: 'toolCall', value: call }, 'child', 'child-turn'),
    event(6, { case: 'toolResult', value: { callId: call.id, name: call.name } }, 'child', 'child-turn'),
    event(7, { case: 'sessionEnded', value: { reason: 'completed' } }, 'child', 'child-turn'), event(8, { case: 'toolResult', value: { callId: 'delegate', name: 'subagent' } }),
    event(9, { case: 'turnEnded', value: { stopReason: 'completed' } }), trace(10, { case: 'generation', value: { kind: 'claim_llm', state: 'started', requestId: 'background' } }, true)]
  // ChatPanel already folds child content into a subagent_run entry.
  const root = reduceAOPToTimeline(events.filter(e => e.sessionId === 'session-1'))
  const child = reduceAOPToTimeline(events.filter(e => e.sessionId === 'child'))
  const items = withJEV([...root, { id: 'child', kind: 'subagent_run' as const, name: 'worker', prompt: 'Read evidence', timestamp: 1700000003000,
    sessionID: 'child', status: 'completed' as const, items: child }], projectJEV(events))
  const workflow = (withWorkflows(items, events).find(item => item.kind === 'extension' && item.extensionType === 'workflow') as any).data.workflow as WorkflowTurn
  expect(workflow.live).toBe(false)
  expect(workflow.nodes.filter(node => node.sessionId === 'child' && node.kind === 'tool')).toHaveLength(1)
  expect(workflow.edges.some(edge => workflow.nodes.find(node => node.id === edge.target)?.sessionId === 'child')).toBe(true)
  expect(workflow.nodes.find(node => node.background)?.state).toBe('pending')
})

test('card usage accumulates calls and excludes the turn aggregate after a response boundary', () => {
  const usage = (seq: number, input: bigint, output: bigint) => event(seq, { case: 'usage', value: {
    inputTokens: input, outputTokens: output, totalTokens: input + output, detail: { cache_read: input / 2n, cache_write: 0n },
  } })
  const events = [event(1, { case: 'message', value: { id: 'first', role: 'assistant', content: [{ value: { case: 'text', value: { text: 'First step' } } }] } }),
    usage(2, 100n, 20n), usage(3, 50n, 10n), event(4, { case: 'status', value: { state: 'eval_start' } }),
    event(5, { case: 'message', value: { id: 'last', role: 'assistant', content: [{ value: { case: 'text', value: { text: 'Final answer' } } }] } }),
    usage(6, 40n, 5n), event(7, { case: 'turnEnded', value: { usage: { inputTokens: 190n, outputTokens: 35n, totalTokens: 225n } } })]
  const items = reduceAOPToTimeline([...events, ...events], { responseBoundary: e => e.payload.case === 'status' })
  expect(items.filter(item => item.kind === 'assistant_response').map(item => item.response?.metadata?.usage)).toEqual([
    { input_tokens: 150, output_tokens: 30, total_tokens: 180, cache_read: 75, cache_write: 0, model: '' },
    { input_tokens: 40, output_tokens: 5, total_tokens: 45, cache_read: 20, cache_write: 0, model: '' },
  ])
})

for (const width of [390, 1280]) test(`card token usage stays compact and appears once through live updates and replay ${width}`, async ({ page }, info) => {
  await page.setViewportSize({ width, height: 900 })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.route('**/cyber.rpc.artifact.ArtifactService/SyncArtifacts', route => route.fulfill({
    contentType: 'application/proto', body: Buffer.from(toBinary(SyncArtifactsResponseSchema, create(SyncArtifactsResponseSchema))),
  }))
  await mount(page)
  const first = [event(1, { case: 'turnStarted', value: {} }),
    event(2, { case: 'message', value: { id: 'thought', role: 'assistant', content: [{ value: { case: 'reasoning', value: { text: 'Inspect the recorded evidence.' } } }] } }),
    event(3, { case: 'toolCall', value: call }), event(4, { case: 'toolResult', value: { callId: call.id, name: call.name,
      output: [{ value: { case: 'text', value: { text: 'Current evidence retrieved.' } } }] } }),
    event(5, { case: 'usage', value: { inputTokens: 1200n, outputTokens: 240n, totalTokens: 1440n, detail: { cache_read: 960n, cache_write: 0n } } })]
  await render(page, first)
  const badges = page.getByTestId('token-usage')
  await expect(badges).toHaveCount(1)
  await expect(badges.first()).toHaveText('↓1.2k↑24080%')
  const rest = [event(6, { case: 'status', value: { state: 'eval_start' } }),
    event(7, { case: 'message', value: { id: 'reply', role: 'assistant', content: [{ value: { case: 'text', value: { text: 'Recorded evidence verified.' } } }] } }),
    event(8, { case: 'usage', value: { inputTokens: 300n, outputTokens: 30n, totalTokens: 330n, detail: { cache_read: 0n } } }),
    event(9, { case: 'turnEnded', value: { usage: { inputTokens: 1500n, outputTokens: 270n, totalTokens: 1770n } } })]
  await render(page, rest, true)
  await expect(badges).toHaveCount(2)
  await expect(page.locator('[data-kind=tool]').getByTestId('token-usage')).toHaveCount(0)
  await expect(page.locator('[data-kind=response]').getByTestId('token-usage')).toHaveCount(0)
  await expect(page.getByTestId('assistant-response-footer').getByTestId('token-usage')).toHaveText('↓300↑30')
  await badges.first().focus()
  await expect(page.getByRole('tooltip')).toContainText('缓存读取 960 token')
  await expect(page.getByRole('tooltip')).toContainText('缓存写入 0 token')
  await page.keyboard.press('Escape')
  const content = await badges.allTextContents()
  await render(page, [...first, ...rest, ...first, ...rest])
  await expect(badges).toHaveCount(2)
  expect(await badges.allTextContents()).toEqual(content)
  for (const badge of await badges.all()) {
    const bounds = await badge.boundingBox()
    expect(bounds!.height).toBeLessThan(24)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
  }
  await badges.first().evaluate(element => (element as HTMLElement).blur())
  await expect(page.getByRole('tooltip')).toHaveCount(0)
  await page.screenshot({ path: info.outputPath(`card-token-usage-${width}.png`), fullPage: true })
})

test('total-only usage stays visible and unreported usage adds no placeholder', async ({ page }) => {
  await mount(page)
  const answer = event(1, { case: 'message', value: { id: 'answer', role: 'assistant',
    content: [{ value: { case: 'text', value: { text: 'Verified current evidence.' } } }] } })
  await render(page, [answer])
  await expect(page.getByTestId('token-usage')).toHaveCount(0)
  await render(page, [event(2, { case: 'turnEnded', value: { usage: { totalTokens: 11n } } })], true)
  await expect(page.getByTestId('token-usage')).toHaveText('11 tok')
  await expect(page.getByTestId('token-usage')).toHaveAccessibleName('合计 11 token')
  await render(page, [answer, event(2, { case: 'usage', value: { inputTokens: 100n, detail: { usage_missing: 1n } } })])
  await expect(page.getByTestId('token-usage')).toHaveCount(0)
})

test('live decisions and tool receipts update inline without duplicating their content', async ({ page }, info) => {
  await mount(page)
  const initial = [event(1, { case: 'turnStarted', value: {} }), trace(2, { case: 'takeover', value: { definition } }),
    trace(3, { case: 'decisionRequest', value: { requestId: 'next', claims: { next: question } } })]
  await render(page, initial)
  const decision = page.locator('[data-control-node][data-kind=decision]')
  await expect(decision).toHaveAttribute('data-state', 'pending')
  const original = await decision.elementHandle()
  await render(page, [trace(4, { case: 'decisionResult', value: { requestId: 'next', evaluations: { next: { value: { case: 'choice', value: 'read' }, probabilities: { read: .95, defer: .05 } } } } }),
    trace(5, { case: 'dispatch', value: { call } })], true)
  await expect(decision).toHaveAttribute('data-state', 'completed')
  expect(await decision.evaluate((element, original) => element === original, original)).toBe(true)
  const tool = page.locator('[data-control-node][data-kind=tool][data-control-stage=execution]')
  await expect(tool.getByTestId('workflow-tool-arguments')).toContainText('current')
  const toolElement = await tool.elementHandle()
  await render(page, [trace(6, { case: 'result', value: { result: { callId: call.id, name: call.name, output: [{ value: { case: 'text', value: { text: 'Unique result evidence' } } }] } } })], true)
  await expect(page.getByTestId('workflow-tool-result')).toContainText('Unique result evidence')
  await expect(page.getByText('Unique result evidence', { exact: true })).toHaveCount(1)
  await expect(page.getByTestId('workflow-tool-arguments')).toHaveCount(1)
  expect(await tool.evaluate((element, original) => element === original, toolElement)).toBe(true)
  await render(page, initial, true)
  await expect(page.getByTestId('agent-workflow')).toHaveCount(1)
  await expect(page.getByTestId('workflow-detail')).toHaveCount(0)
  await page.screenshot({ path: info.outputPath('live-workflow.png'), fullPage: true })
})

for (const width of [390, 1440]) for (const theme of ['light', 'dark']) test(`unified workflow shows content once and supports keyboard navigation ${width} ${theme}`, async ({ page }, info) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.setViewportSize({ width, height: 900 })
  await mount(page)
  await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
  const decision = page.locator('[data-control-node][data-kind=decision]')
  await decision.focus(); await decision.press('Enter')
  await expect(decision).toHaveAttribute('data-control-current', 'true')
  await expect(decision.locator('[data-runtime-claim][data-selected=true]')).toHaveCount(1)
  await expect(page.getByTestId('workflow-detail')).toHaveCount(0)
  await expect(page.locator('.workflow-inspect, .workflow-navigation, .workflow-render-toggle, .workflow-board')).toHaveCount(0)
  await expect(page.getByTestId('jev-segment')).toHaveCount(0)
  await expect(page.getByTestId('jev-decision')).toHaveCount(1)
  await decision.press('ArrowDown')
  await expect(page.locator('[data-kind=tool][data-control-stage=execution]').first()).toBeFocused()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: info.outputPath(`workflow-${width}-${theme}.png`), fullPage: true })
  expect(errors).toEqual([])
})

for (const layout of [{ width: 390, narrow: false }, { width: 1440, narrow: true }, { width: 1440, narrow: false }]) {
  test(`LLM, JEV and TOOL retain distinct badges and colors at ${layout.width}px, narrow container ${layout.narrow}`, async ({ page }) => {
    await page.setViewportSize({ width: layout.width, height: 1000 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mount(page)
    if (layout.narrow) await page.getByTestId('agent-workflow').evaluate(element => element.style.maxWidth = '480px')
    const graph = page.getByTestId('jev-control-flow')
    const colors: string[] = []
    for (const actor of ['LLM', 'JEV', 'TOOL']) {
      await expect(graph.locator(`.control-role-legend [data-actor=${actor}]`)).toBeVisible()
      const card = graph.locator(`.control-card[data-actor=${actor}]`).first()
      await expect(card.locator('.control-actor')).toContainText(actor)
      colors.push(await card.evaluate(element => getComputedStyle(element).borderTopColor))
    }
    expect(new Set(colors).size).toBe(3)
    const toolCards = graph.locator('.control-card[data-actor=TOOL]')
    expect(new Set(await toolCards.evaluateAll(elements => elements.map(element => getComputedStyle(element.querySelector('.control-card-title')!).color))).size).toBe(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

test('reduced motion preserves pending status without moving particles', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await mount(page)
  await render(page, [event(1, { case: 'turnStarted', value: {} }), trace(2, { case: 'observation', value: { stateJson: '{}' } }),
    trace(3, { case: 'decisionRequest', value: { requestId: 'pending', claims: { next: question } } })])
  await expect(page.locator('[data-control-node][data-kind=decision]')).toHaveAttribute('data-state', 'pending')
  for (const packet of await page.locator('.control-particle').all()) await expect(packet).toBeHidden()
})

for (const width of [390, 1440]) test(`scopes retain one graph and keyboard navigation at ${width}px`, async ({ page }) => {
  await page.setViewportSize({ width, height: 900 })
  await mount(page)
  const nodes = page.locator('[data-control-node]')
  await expect(page.locator('[data-kind=tool][data-control-stage=execution]').first()).toContainText('playwright open')
  await expect(page.locator('[data-kind=decision] [data-runtime-claim=inspect]')).toContainText('inspect')
  await page.getByRole('button', { name: '前台执行 6', exact: true }).click()
  await expect(nodes).toHaveCount(8) // Six semantic events, including two call receipts.
  await expect(page.locator('[data-background=true][data-control-node]')).toHaveCount(0)
  await nodes.first().focus(); await nodes.first().press('ArrowDown')
  await expect(nodes.nth(1)).toBeFocused()
  await expect(nodes.nth(1)).toHaveAttribute('data-control-current', 'true')
  await page.getByRole('button', { name: '后台归纳 1', exact: true }).click()
  await expect(nodes).toHaveCount(1)
  await expect(page.getByTestId('jev-reflex-definition')).toBeVisible()
  // Event links restore all records even while the background scope is active.
  await page.evaluate(() => window.dispatchEvent(new CustomEvent('cyber-workflow-select', { detail: 'event-4' })))
  await expect(page.locator('[data-kind=decision]')).toBeFocused()
  await expect(page.locator('[data-kind=decision]')).toHaveAttribute('data-control-current', 'true')
})

test('Markdown replies stay in the conversation and selecting a card never creates a detail panel', async ({ page }) => {
  await mount(page)
  const reply = page.getByTestId('assistant-response-content')
  await expect(reply).toHaveCount(1)
  await expect(page.getByTestId('agent-workflow').getByTestId('assistant-response-content')).toHaveCount(0)
  await page.locator('[data-control-node][data-kind=decision]').click()
  await expect(page.getByTestId('workflow-detail')).toHaveCount(0)
  await expect(page.getByRole('button', { name: /^(查看详情|收起详情|上一步|下一步)$/ })).toHaveCount(0)
  await expect(reply).toHaveCount(1)
})

test('focused history keeps its cursor during live updates and follow returns to the active node', async ({ page }) => {
  await mount(page)
  await render(page, [event(1, { case: 'turnStarted', value: {} }), trace(2, { case: 'takeover', value: { definition } }),
    trace(3, { case: 'decisionRequest', value: { requestId: 'next', claims: { next: question } } })])
  const takeover = page.locator('[data-record-id="session-1-turn-1-2"]')
  await takeover.click()
  await render(page, [trace(4, { case: 'decisionResult', value: { requestId: 'next', evaluations: { next: { value: { case: 'choice', value: 'read' } } } } }),
    trace(5, { case: 'dispatch', value: { call } })], true)
  await expect(takeover).toHaveAttribute('data-control-current', 'true')
  const follow = page.getByRole('button', { name: '跟随运行', exact: true })
  await expect(follow).toHaveAttribute('aria-pressed', 'false')
  await follow.click()
  await expect(page.locator('[data-kind=tool]')).toHaveAttribute('data-control-current', 'true')
  await expect(follow).toHaveAttribute('aria-pressed', 'true')
  await page.evaluate(() => window.dispatchEvent(new CustomEvent('cyber-workflow-select', { detail: 'session-1-turn-1-4' })))
  await expect(page.locator('[data-kind=decision]')).toHaveAttribute('data-control-current', 'true')
  await expect(page.getByTestId('jev-decision')).toHaveAttribute('data-state', 'answered')
})

test('generated drafts link to their matching publication and retain unrelated drafts inline', async ({ page }) => {
  await mount(page)
  const draft = { type: 'choice', context: 'Unique generated condition', options: ['Read evidence', 'Finish'] }
  await render(page, [event(1, { case: 'turnStarted', value: {} }),
    trace(2, { case: 'generation', value: { kind: 'claim_llm', state: 'finished', output: JSON.stringify({ claims: [draft] }), requestId: 'draft' } }, true),
    trace(3, { case: 'libraryChange', value: { state: 'claim_published', claim: { ...draft, type: ClaimType.choice, id: 'other', context: 'An unrelated condition' } } }, true)])
  const generation = page.locator('[data-kind=generation]')
  await expect(generation.getByTestId('jev-claim-definition')).toContainText(draft.context)
  await expect(page.getByRole('button', { name: '查看发布内容', exact: true })).toHaveCount(0)
  await render(page, [trace(4, { case: 'libraryChange', value: { state: 'claim_published', claim: { ...draft, type: ClaimType.choice, id: 'matching' } } }, true)], true)
  await expect(generation.getByTestId('jev-claim-definition')).toHaveCount(0)
  await expect(page.getByTestId('jev-claim-definition')).toHaveCount(2)
  await page.getByRole('button', { name: '查看发布内容', exact: true }).click()
  const publication = page.locator('[data-record-id="session-1-turn-1-4"]')
  await expect(publication).toHaveAttribute('data-control-current', 'true')
  await expect(publication).toContainText(draft.context)
  await expect(page.getByText(draft.context, { exact: true })).toHaveCount(1)
})

test('native media, terminal output and download links remain available inline', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await mount(page)
  const png = new Uint8Array(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jkAAAAABJRU5ErkJggg==', 'base64'))
  const screenshot = { ...call, id: 'capture', name: 'record' }
  await render(page, [event(1, { case: 'turnStarted', value: {} }), event(2, { case: 'toolCall', value: screenshot }),
    event(3, { case: 'toolResult', value: { callId: screenshot.id, name: 'record', output: [
      { value: { case: 'text', value: { text: '\u001b[32mCaptured current page\u001b[0m' } } },
      { value: { case: 'media', value: { kind: 'image', resource: { mediaType: 'image/png', filename: 'page.png', source: { case: 'data', value: png } } } } },
    ] } }), event(4, { case: 'turnEnded', value: { stopReason: 'completed' } })])
  const result = page.getByTestId('workflow-tool-result')
  await expect(result).toContainText('Captured current page')
  expect(await result.innerText()).not.toContain('\u001b')
  const image = result.getByTestId('record-image')
  await expect(image).toBeVisible()
  await expect(image).toHaveAttribute('src', /^blob:/)
  await expect(result.getByRole('link', { name: '打开截图' })).toHaveAttribute('href', /^blob:/)
  await expect(result.getByRole('link', { name: '下载', exact: true })).toHaveCount(1)
  await expect(page.getByTestId('workflow-tool-arguments')).toHaveCount(1)
  await expect(page.getByTestId('workflow-detail')).toHaveCount(0)
})

for (const runtime of [false, true]) test(`tool document previews reuse timeline formatting ${runtime ? 'Reflex' : 'native'}`, async ({ page }) => {
  await mount(page)
  const calls = [
    { id: 'markdown', name: 'read', arguments: { data: new TextEncoder().encode('{"path":"report.md"}') } },
    { id: 'source', name: 'read', arguments: { data: new TextEncoder().encode('{"path":"main.go"}') } },
  ]
  const results = [
    { callId: 'markdown', name: 'read', output: [{ value: { case: 'text', value: { text: '# Current evidence\n\n**Verified** [source](https://example.com/evidence)' } } }] },
    { callId: 'source', name: 'read', output: [{ value: { case: 'text', value: { text: 'package main\nfunc main() { println("ready") }' } } }] },
  ]
  const events = [event(1, { case: 'turnStarted', value: {} })]
  if (runtime) events.push(trace(2, { case: 'takeover', value: { definition } }))
  calls.forEach((call, i) => events.push(
    runtime ? trace(3 + i * 2, { case: 'dispatch', value: { call } }) : event(3 + i * 2, { case: 'toolCall', value: call }),
    runtime ? trace(4 + i * 2, { case: 'result', value: { result: results[i], elapsedMs: 1200 } }) : event(4 + i * 2, { case: 'toolResult', value: results[i] }),
  ))
  events.push(event(9, { case: 'turnEnded', value: { stopReason: 'completed' } }))
  await render(page, events)
  const output = page.getByTestId('workflow-tool-result')
  await expect(output.first().getByRole('heading', { name: 'Current evidence' })).toBeVisible()
  await expect(output.first().locator('strong')).toHaveText('Verified')
  await expect(output.first().getByRole('link', { name: 'source', exact: true })).toHaveAttribute('href', 'https://example.com/evidence')
  await expect(output.last().locator('code')).toContainText('package main')
  await expect(page.getByTestId('workflow-tool-arguments')).toHaveCount(2)
  await expect(output.locator('[data-testid=tool-arguments]')).toHaveCount(0)
  await expect(output.locator('details')).toHaveCount(0)
  if (runtime) await expect(output.first()).toContainText('1.2 s')
})

test('Reflex record receipts retain the canonical status UI and show mixed output only once', async ({ page }) => {
  await mount(page)
  const record = { id: 'record', name: 'record', arguments: { data: new TextEncoder().encode('{"action":"status"}') } }
  await render(page, [event(1, { case: 'turnStarted', value: {} }), trace(2, { case: 'takeover', value: { definition } }),
    trace(3, { case: 'dispatch', value: { call: record } }),
    trace(4, { case: 'result', value: { result: { callId: 'record', name: 'record', output: [{ value: { case: 'text', value: { text: JSON.stringify({ recording_id: 'capture-1', state: 'completed', target: { kind: 'window', title: 'Recorded browser', width: 1280, height: 720 }, duration_ms: 1500, frames: 45, fps: 30 }) } } }] } } }),
    trace(5, { case: 'dispatch', value: { call } }),
    trace(6, { case: 'result', value: { result: { callId: call.id, name: call.name, output: [
      { value: { case: 'text', value: { text: '\u001b[32mCurrent evidence\u001b[0m' } } },
      { value: { case: 'reasoning', value: { text: 'Reasoned evidence' } } },
      { value: { case: 'refusal', value: 'Refused operation' } },
    ] } } }), event(7, { case: 'turnEnded', value: { stopReason: 'completed' } })])
  const status = page.getByTestId('record-status')
  await expect(status).toContainText('1280 × 720')
  await expect(status).toContainText('1.5 秒')
  await expect(status).toContainText('45 帧')
  await expect(status).toContainText('Recorded browser')
  await expect(page.getByTestId('workflow-tool-result').last().getByTestId('tool-result')).toHaveText(/Current evidence.*Reasoned evidence.*Refused operation/s)
  expect(await page.getByTestId('workflow-tool-result').last().innerText()).not.toContain('\u001b')
  await expect(page.getByTestId('workflow-tool-result').locator('[data-testid=tool-arguments]')).toHaveCount(0)
  await expect(page.getByTestId('workflow-tool-result').locator('details')).toHaveCount(0)
})

test('Reflex tools retain scan assets and findings from the shared timeline renderer', async ({ page }) => {
  const artifact = event(20, { case: 'extension', value: anyPack(ArtifactSchema, create(ArtifactSchema, {
    tool: 'gogo', target: '127.0.0.1:80', resultId: 'scan-output', data: new TextEncoder().encode('{"ip":"127.0.0.1","port":"80","protocol":"http"}'),
  })) })
  artifact.extensions.push(anyPack(RefSchema, create(RefSchema, { operationId: 'scan-operation', callId: 'scan-call', correlation: Correlation.EXPLICIT })))
  await page.route('**/cyber.rpc.artifact.ArtifactService/SyncArtifacts', route => {
    const request = fromBinary(SyncArtifactsRequestSchema, route.request().postDataBuffer()!)
    const response = create(SyncArtifactsResponseSchema, { artifacts: request.afterCursor === '0' || !request.afterCursor ? [{ cursor: '1', event: artifact }] : [] })
    return route.fulfill({ contentType: 'application/proto', body: Buffer.from(toBinary(SyncArtifactsResponseSchema, response)) })
  })
  await mount(page)
  const scanCall = { ...call, id: 'scan-call', name: 'scan' }
  await render(page, [event(1, { case: 'turnStarted', value: {} }), trace(2, { case: 'takeover', value: { definition } }),
    trace(3, { case: 'dispatch', value: { call: scanCall } }),
    trace(4, { case: 'result', value: { result: { callId: scanCall.id, name: scanCall.name, output: [{ value: { case: 'text', value: { text: 'Scan completed' } } }] } } }),
    event(5, { case: 'turnEnded', value: { stopReason: 'completed' } })])
  const output = page.getByTestId('workflow-tool-result')
  await expect(output.getByRole('tab', { name: '资产', exact: true })).toBeVisible()
  await expect(output).toContainText('127.0.0.1')
  await output.getByRole('tab', { name: /发现项/ }).click()
  await expect(output.getByRole('tab', { name: /发现项/ })).toHaveAttribute('data-state', 'active')
  await expect(output.getByTestId('tool-result')).toContainText('Scan completed')
  await expect(page.getByTestId('workflow-tool-arguments')).toHaveCount(1)
  await expect(page.getByTestId('workflow-tool-arguments').getByRole('tab')).toHaveCount(0)
  await expect(output.locator('details')).toHaveCount(0)
})
