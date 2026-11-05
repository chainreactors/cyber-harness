import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => { await page.goto('/e2e/fixtures/ioa-graph.html'); await expect(page.locator('.react-flow__node')).toHaveCount(9) })

test('application graph connects router, task, execution, return and remote requester', async ({ page }) => {
  await expect(page.getByText('5 messages · 12 links')).toBeVisible()
  await expect(page.getByText('refs.messages', { exact: true })).toBeVisible()
  await expect(page.locator('.react-flow__edge')).toHaveCount(12)
  const edgeIds = await page.locator('.react-flow__edge').evaluateAll(edges => edges.map(edge => edge.getAttribute('data-id')))
  expect(edgeIds).toContain('refs.messages:worker-delegate-42->worker-return-42')
  expect(edgeIds).not.toContain('task_id:router-handoff-42->worker-delegate-42')
  await page.locator('.react-flow__node').filter({ hasText: 'Return from auditor' }).click()
  await expect.poll(() => page.evaluate(() => (window as any).selectedGraphMessage)).toBe('worker-return-42')
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('.react-flow__edge')).toHaveCount(12)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  await page.screenshot({ path: test.info().outputPath('harness-path-mobile.png'), fullPage: true })
})

test('restart executions share a durable task without inventing causal ordering', async ({ page }) => {
  const result = await page.evaluate(() => {
    const { messages, project } = (window as any).harnessFixture
    const restarted = structuredClone(messages[2]); restarted.id = 'restart-delegate'
    restarted.meta.id = 'delegate-restarted'; restarted.meta.target.session_id = 'restarted'
    const graph = project([...messages, restarted])
    return { tasks: graph.nodes.filter((n: any) => n.kind === 'task').length, sessions: graph.nodes.filter((n: any) => n.kind === 'session').map((n: any) => n.title), edges: graph.edges }
  })
  expect(result.tasks).toBe(1)
  expect(result.sessions).toContain('Session restarted')
  expect(result.sessions).toContain('Session execution-42')
  expect(result.edges.some((e: any) => e.source === 'router-handoff-42' && e.target === 'restart-delegate')).toBeFalsy()
  expect(result.edges.some((e: any) => e.source === 'worker-return-42' && e.target === 'restart-delegate')).toBeFalsy()
})

test('cross-node identical task and session IDs remain distinct and provenance is explicit', async ({ page }) => {
  const graph = await page.evaluate(() => {
    const { project } = (window as any).harnessFixture
    const record = (sender: string, id: string) => ({ id, sender, content: {}, meta: { version: 1, event: 'inbox.route', id: 'same-event', source: { node_id: 'remote', session_id: 'origin' }, target: { task_id: 'same-task', session_id: 'same-session' } } })
    return project([record('node-a', 'a'), record('node-b', 'b')])
  })
  expect(graph.nodes.filter((n: any) => n.title === 'Task same-task')).toHaveLength(2)
  expect(graph.nodes.filter((n: any) => n.title === 'Session same-session')).toHaveLength(2)
  expect(graph.nodes.filter((n: any) => n.title === 'Session origin')).toHaveLength(1)
  expect(graph.nodes.find((n: any) => n.title === 'Session origin').meta).toBe('remote')
})

test('ACK retries and late arrival preserve event identity and order-independent affiliations', async ({ page }) => {
  const result = await page.evaluate(() => {
    const { messages, project } = (window as any).harnessFixture
    const retry = structuredClone(messages[1]); retry.id = 'transport-retry'
    const data = [...messages, retry]
    const graph = project(data), reversed = project([...data].reverse())
    const sorted = (g: any) => g.edges.map((e: any) => JSON.stringify(e)).sort()
    return { labels: graph.nodes.filter((n: any) => ['router-handoff-42', 'transport-retry'].includes(n.id)).map((n: any) => n.meta), tasks: graph.nodes.filter((n: any) => n.kind === 'task').length, edges: sorted(graph), reversed: sorted(reversed) }
  })
  expect(result.labels).toEqual(['router · handoff-42', 'router · handoff-42'])
  expect(result.tasks).toBe(1)
  expect(result.edges).toEqual(result.reversed)
})

test('foreign and malformed metadata never acquires Harness semantics', async ({ page }) => {
  const result = await page.evaluate(() => {
    const { parse, project } = (window as any).harnessFixture
    const bad = [null, { version: 2, event: 'message' }, { version: 1, event: 'subagent.delegate', id: 'missing-target' }, { version: 1, event: 'message', source: { unknown: 'value' } }, { version: 1, event: 'task.result', id: 'bad', interrupt: true }, { version: 1, event: 'inbox.route', id: 'bad-call', target: { task_id: 'worker' }, call_id: 'not-a-delegation' }, { 'cyber.harness': { version: 1, event: 'message' } }]
    const messages = bad.map((record, i) => ({ id: 'bad-' + i, sender: 'peer', content: {}, meta: record }))
    messages.push({ id: 'foreign', sender: 'peer', content: {}, meta: { 'other.app': { task_id: 'x' }, source_task_id: 'old' } } as any)
    return { parsed: bad.map(value => Boolean(parse(value))), graph: project(messages),
      extra: Boolean(parse({ version: 1, event: 'message', 'other.app': { tag: 'preserved' } })) }
  })
  expect(result.parsed).toEqual([false, false, false, false, false, false, false])
  expect(result.extra).toBeTruthy()
  expect(result.graph).toEqual({ nodes: [], edges: [] })
})
