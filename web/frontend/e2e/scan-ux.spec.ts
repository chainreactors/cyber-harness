import { expect, test, type Page } from '@playwright/test'
import { create, fromBinary, toBinary } from '@bufbuild/protobuf'
import { anyPack, timestampNow } from '@bufbuild/protobuf/wkt'
import { EventSchema, type Event as AOPEvent } from '../cyber-ui/packages/aop/src/gen/aop/event_pb'
import { ArtifactSchema, LootSchema } from '../cyber-ui/packages/aop/src/gen/aop/tool/protocol_pb'
import { RefSchema, Correlation } from '../cyber-ui/packages/aop/src/gen/aop/operation/protocol_pb'
import { SyncArtifactsRequestSchema, SyncArtifactsResponseSchema } from '../src/gen/types/artifact_pb'
import { ListAgentsResponseSchema } from '../src/gen/types/agent_pb'

const preamble = `<script type="module">import RefreshRuntime from '/@react-refresh'; RefreshRuntime.injectIntoGlobalHook(window); window.$RefreshReg$ = () => {}; window.$RefreshSig$ = () => (type) => type; window.__vite_plugin_react_preamble_installed__ = true;</script>`

function artifact(id: string, operationId: string, data: unknown, tool = 'gogo', resultId = id): AOPEvent {
  return create(EventSchema, { id, emittedAt: timestampNow(), sessionId: 'session',
    payload: { case: 'extension', value: anyPack(ArtifactSchema, create(ArtifactSchema, { tool, target: '127.0.0.1:80', resultId, data: new TextEncoder().encode(typeof data === 'string' ? data : JSON.stringify(data)) })) },
    extensions: [anyPack(RefSchema, create(RefSchema, { operationId, callId: 'outer-call', correlation: Correlation.EXPLICIT }))],
  })
}
function loot(id: string, operationId: string, resultId: string): AOPEvent {
  const event = artifact(id, operationId, {})
  event.payload = { case: 'extension', value: anyPack(LootSchema, create(LootSchema, { resultId, tool: 'gogo', kind: 'vuln', verificationStatus: 'confirmed' })) }
  return event
}
async function archive(page: Page, entries: AOPEvent[]) {
  await page.route('**/cyber.rpc.artifact.ArtifactService/SyncArtifacts', async (route) => {
    const request = fromBinary(SyncArtifactsRequestSchema, route.request().postDataBuffer()!)
    for (const event of request.artifacts) if (!entries.some((existing) => existing.id === event.id)) entries.push(event)
    const offset = Number(request.afterCursor || 0)
    const response = create(SyncArtifactsResponseSchema, { artifacts: entries.slice(offset, offset + 100).map((event, index) => ({ cursor: String(offset + index + 1), event })) })
    await route.fulfill({ contentType: 'application/proto', body: Buffer.from(toBinary(SyncArtifactsResponseSchema, response)) })
  })
  await page.route('**/cstx-test', (route) => route.fulfill({ contentType: 'text/html', body: `<html><head>${preamble}</head><body>Asset test</body></html>` }))
  await page.goto('/cstx-test')
  await page.evaluate(async () => { (window as any).runtime = await import('/src/lib/cstx-runtime.ts') })
}

test('history is isolated and Loot joins in either order without crossing operations', async ({ page }) => {
  const entries = [loot('l1', 'op1', 'shared'), artifact('a1', 'op1', { ip: '127.0.0.1', port: '80', protocol: 'http' }, 'gogo', 'shared')]
  await archive(page, entries)
  const before = await page.evaluate(async () => { const r = (window as any).runtime; await r.syncCSTXArtifacts(); return r.listSCONodes({ scanId: 'op1' }) })
  expect(before.total).toBeGreaterThan(0)
  expect(before.items.some((node: any) => node._evidence.some((e: any) => e.status === 'confirmed'))).toBeTruthy()
  entries.push(artifact('a2', 'op2', { ip: '127.0.0.1', port: '80', protocol: 'ssh' }, 'gogo', 'shared'))
  await page.evaluate(() => (window as any).runtime.syncCSTXArtifacts())
  const after = await page.evaluate(async () => { const r = (window as any).runtime; return { old: await r.listSCONodes({ scanId: 'op1' }), next: await r.listSCONodes({ scanId: 'op2' }) } })
  expect(after.old).toEqual(before)
  expect(after.next.items.every((node: any) => node._evidence.every((e: any) => !e.status))).toBeTruthy()
  entries.push(loot('l2', 'op2', 'shared'))
  await page.evaluate(() => (window as any).runtime.syncCSTXArtifacts())
  const next = await page.evaluate(() => (window as any).runtime.listSCONodes({ scanId: 'op2' }))
  expect(next.items.some((node: any) => node._evidence.some((e: any) => e.status === 'confirmed'))).toBeTruthy()
  await page.reload()
  await page.evaluate(async () => { (window as any).runtime = await import('/src/lib/cstx-runtime.ts') })
  expect(await page.evaluate(() => (window as any).runtime.listSCONodes({ scanId: 'op1' }))).toEqual(before)
})

test('parse errors are durable and storage errors do not advance the cursor', async ({ page }) => {
  const pageErrors: string[] = []
  page.on('pageerror', (error) => pageErrors.push(error.message))
  const entries = [artifact('bad', 'bad-op', {}, 'unsupported-parser')]
  await archive(page, entries)
  expect(await page.evaluate(async () => { const r = (window as any).runtime; await r.syncCSTXArtifacts(); return (await r.cstxFailures()).length })).toBe(1)
  await page.evaluate(() => (window as any).runtime.retryCSTXFailures())
  expect(await page.evaluate(async () => (await (window as any).runtime.cstxFailures()).length)).toBe(1)
  entries.push(artifact('good', 'op-good', { ip: '127.0.0.1', port: '80' }))
  await page.evaluate(() => {
    const original = IDBObjectStore.prototype.put
    ;(window as any).restorePut = () => { IDBObjectStore.prototype.put = original }
    IDBObjectStore.prototype.put = function (...args: Parameters<IDBObjectStore['put']>) {
      if (this.name === 'observations') { this.transaction.abort(); throw new Error('simulated storage failure') }
      return original.apply(this, args)
    }
  })
  const failure = await page.evaluate(async () => { try { await (window as any).runtime.syncCSTXArtifacts(); return '' } catch (error) { return String(error) } })
  expect(failure).toContain('simulated storage failure')
  await page.evaluate(() => (window as any).restorePut())
  await page.evaluate(() => (window as any).runtime.syncCSTXArtifacts())
  expect((await page.evaluate(() => (window as any).runtime.listSCONodes({ scanId: 'op-good' }))).total).toBeGreaterThan(0)
  expect(pageErrors).toEqual([])
})

test('pagination remains complete beyond 5000 assets', async ({ page }) => {
  const records = Array.from({ length: 2600 }, (_, i) => ({ ip: `10.${Math.floor(i / 256)}.${i % 256}.1`, port: '80', protocol: 'http' }))
  await archive(page, [artifact('many', 'bulk', records.map((record) => JSON.stringify(record)).join('\n'))])
  const result = await page.evaluate(async () => {
    const r = (window as any).runtime
    await r.syncCSTXArtifacts()
    const full = await r.listSCONodes({ scanId: 'bulk' })
    const ids: string[] = []
    let afterCursor = ''
    do { const page = await r.listSCONodes({ scanId: 'bulk', limit: 1000, afterCursor }); ids.push(...page.items.map((node: any) => node.cstx_id)); afterCursor = page.nextCursor } while (afterCursor)
    return { total: full.total, count: ids.length, unique: new Set(ids).size, failures: await r.cstxFailures() }
  })
  expect(result.failures).toEqual([])
  expect(result.total).toBeGreaterThan(5000)
  expect(result.count).toBe(result.total)
  expect(result.unique).toBe(result.total)
})

test('composer preserves failed drafts, attachments and edits during submission', async ({ page }) => {
  await page.route('**/composer-test', (route) => route.fulfill({ contentType: 'text/html', body: `<html><head>${preamble}</head><body><div id="root"></div><script type="module" src="/e2e/fixtures/composer.tsx"></script></body></html>` }))
  await page.goto('/composer-test')
  const input = page.locator('textarea').first()
  await input.fill('first draft')
  await page.locator('input[type=file]').setInputFiles({ name: 'context.txt', mimeType: 'text/plain', buffer: Buffer.from('evidence') })
  await page.getByRole('button', { name: 'Send message', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Send message', exact: true })).toBeDisabled()
  await page.evaluate(() => (window as any).settle(false))
  await expect(input).toHaveValue('first draft')
  await page.getByRole('button', { name: 'Send message', exact: true }).click()
  await input.fill('next draft')
  await page.evaluate(() => (window as any).settle(true))
  await expect(input).toHaveValue('next draft')
  expect(await page.evaluate(() => (window as any).submissions)).toEqual([{ content: 'first draft', files: 1 }, { content: 'first draft', files: 1 }])
})

test('failed scanner card retains findings and shows verification with parse retry', async ({ page }) => {
  const entries = [
    artifact('service', 'scan-op', { ip: '127.0.0.1', port: '80', protocol: 'http' }),
    artifact('vulnerability', 'scan-op', { target: 'http://127.0.0.1:80', template_id: 'local-finding', template_name: 'Local finding', severity: 'low', matched: true, request: 'GET / HTTP/1.1', response: 'HTTP/1.1 200 OK' }, 'neutron', 'v1'),
    loot('verdict', 'scan-op', 'v1'),
    artifact('malformed', 'scan-op', {}, 'unsupported-parser'),
  ]
  await archive(page, entries)
  await page.route('**/scan-result-test', (route) => route.fulfill({ contentType: 'text/html', body: `<html><head>${preamble}</head><body><div id="root"></div><script type="module" src="/e2e/fixtures/scan-result.tsx"></script></body></html>` }))
  await page.goto('/scan-result-test')
  await page.locator('button[aria-expanded]').first().click()
  await expect(page.getByRole('alert')).toContainText('unsupported CSTX artifact')
  await page.getByRole('tab').nth(1).click()
  await expect(page.getByText('Local finding', { exact: true })).toBeVisible()
  await expect(page.getByText(/^AI (已验证|Verified)$/)).toBeVisible()
  await page.getByRole('alert').getByRole('button').click()
  await expect(page.getByRole('alert')).toContainText('unsupported CSTX artifact')
  await expect(page.getByText('Local finding', { exact: true })).toBeVisible()
})

test('uncertain session creation retries the same session and request identity', async ({ page }) => {
  await page.route('**/cyber.rpc.*/**', (route) => route.fulfill({ contentType: 'application/proto', body: '' }))
  await page.route('**/cyber.rpc.agent.AgentService/ListAgents', (route) => route.fulfill({
    contentType: 'application/proto',
    body: Buffer.from(toBinary(ListAgentsResponseSchema, create(ListAgentsResponseSchema, { agents: [{ hello: { nodeId: 'execution-node' } }] }))),
  }))
  await page.route('**/session-retry-test', (route) => route.fulfill({ contentType: 'text/html', body: `<html><head>${preamble}</head><body><div id="root"></div><script type="module" src="/e2e/fixtures/session-retry.tsx"></script></body></html>` }))
  await page.goto('/session-retry-test')
  await expect(page.getByText('1 nodes', { exact: true })).toBeVisible()
  expect(await page.evaluate(() => (window as any).session.ensureSession())).toBeNull()
  expect(await page.evaluate(() => (window as any).session.ensureSession())).toBeNull()
  const requests = await page.evaluate(() => (window as any).openRequests)
  expect(requests).toHaveLength(2)
  expect(requests[0].id).toBeTruthy()
  expect(requests[0].sessionID).toBeTruthy()
  expect(requests[1]).toEqual(requests[0])
})
