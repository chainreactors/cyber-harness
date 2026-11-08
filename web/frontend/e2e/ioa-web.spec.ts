import { expect, test } from '@playwright/test'
import { readFileSync } from 'node:fs'

const evidenceFile = process.env.CYBER_IOA_WEB_EVIDENCE

test('real Hub serves recorded model scheduling paths to the authenticated IOA console', async ({ page }) => {
  test.skip(!evidenceFile, 'Set CYBER_IOA_WEB_EVIDENCE to a real-model replay evidence file and point UI_TEST_BASE_URL at its Hub proxy')
  test.setTimeout(90_000)
  const evidence = JSON.parse(readFileSync(evidenceFile!, 'utf8'))
  const responses: Record<string, number> = {}
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('response', response => {
    const path = new URL(response.url()).pathname
    if (['/ioa/nodes', '/ioa/spaces', '/ioa/messages'].includes(path)) responses[path] = response.status()
  })
  await page.addInitScript(() => localStorage.setItem('cyber-locale', 'en'))
  await page.goto('/')
  await page.locator('#access-token').fill(process.env.CYBER_IOA_WEB_TOKEN ?? 'harness-protocol-local-test')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await page.getByRole('button', { name: 'Open IOA Console', exact: true }).click()
  const space = page.getByRole('tab').filter({ hasText: evidence.space_name })
  await expect(space).toBeVisible()
  await space.click()
  await expect.poll(() => responses).toEqual({ '/ioa/nodes': 200, '/ioa/spaces': 200, '/ioa/messages': 200 })
  await expect.poll(() => page.locator('.react-flow__node').count()).toBeGreaterThan(evidence.message_count)
  await expect.poll(() => page.locator('.react-flow__edge').count()).toBeGreaterThan(evidence.message_count)
  const routed = evidence.messages.find((message: any) => {
    const record = message.meta
    return record?.event === 'inbox.route' && record.source?.node_id && record.source.node_id !== message.sender && message.refs?.messages?.length === 1
  })
  expect(routed, 'real remote input must preserve its producer through router forwarding').toBeTruthy()
  const source = routed.meta.source
  const sourceEntity = 'harness:' + JSON.stringify([routed.space_id ?? '', source.node_id, 'session', source.session_id])
  const edgeIds = await page.locator('.react-flow__edge').evaluateAll(edges => edges.map(edge => edge.getAttribute('data-id')))
  expect(edgeIds).toContain('source:' + sourceEntity + '->' + routed.id)
  expect(edgeIds).toContain('refs.messages:' + routed.refs.messages[0] + '->' + routed.id)
  const returned = evidence.messages.filter((message: any) => message.meta?.event === 'subagent.return').pop()
  expect(returned).toBeTruthy()
  const messageNode = page.locator('.react-flow__node[data-id="' + returned.id + '"]')
  await messageNode.first().dispatchEvent('click')
  await expect.poll(async () => (await messageNode.first().boundingBox())?.width ?? 0).toBeGreaterThan(120)
  await expect(page.locator('aside').getByText('subagent.return', { exact: true })).toBeVisible()
  await expect(page.locator('aside').getByText(returned.meta.id, { exact: true })).toBeVisible()
  for (const ref of returned.refs.messages) await expect(page.locator('aside').getByRole('button', { name: 'Open referenced message ' + ref })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('real-hub-desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(space).toBeVisible()
  await expect.poll(async () => {
    const selected = await messageNode.first().boundingBox()
    const graph = await page.locator('.react-flow').boundingBox()
    return Boolean(selected && graph && selected.x >= graph.x && selected.x + selected.width <= graph.x + graph.width)
  }).toBeTruthy()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  await page.screenshot({ path: test.info().outputPath('real-hub-mobile.png'), fullPage: true })
  expect(errors).toEqual([])
})
