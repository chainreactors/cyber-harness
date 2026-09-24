import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

const API_TOKEN = process.env.ACCESS_KEY || 'test-token'
// Goal mode needs a model that can answer an evaluator call with a verdict tool
// call — a real one, or a stand-in that scripts it. The default e2e stand-in
// only replies PONG, so the round-driving test stays opt-in.
const GOAL_READY = process.env.CYBER_E2E_GOAL === '1'

async function login(page: Page) {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Access Cyber' })).toBeVisible()
  await page.getByLabel('Access token').fill(API_TOKEN)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('button', { name: 'Open settings', exact: true })).toBeVisible()
}

async function openGoalSession(page: Page) {
  const newTask = page.locator('aside [data-node-id="e2e-node"]').getByRole('button', { name: 'New task on e2e-node' })
  const globalNewTask = page.getByRole('button', { name: 'New task', exact: true })
  await expect(newTask.or(globalNewTask)).toBeVisible({ timeout: 20_000 })
  if (await newTask.count()) {
    await newTask.click()
  } else {
    await globalNewTask.click()
  }
  await expect(page.getByRole('textbox', { name: 'Your goal' })).toBeVisible()
  await page.getByRole('button', { name: 'Goal' }).click()
}

test('goal mode uses the existing message box', async ({ page }) => {
  test.setTimeout(90_000)
  await login(page)
  await openGoalSession(page)
  const goal = '统计当前目录下的文件数量，并说明用什么命令得到的'
  const composer = page.getByRole('textbox', { name: 'Your goal' })
  await expect(composer).toBeFocused()
  await composer.fill(goal)
  await expect(composer).toHaveValue(goal)
  await expect(page.locator('textarea')).toHaveCount(1)
  await expect(page.getByPlaceholder(/Describe in plain language what "done" looks like/)).toHaveCount(0)
})

type EvalDetail = { state?: string; round?: number; maxRounds?: number; pass?: boolean; reason?: string }

// The judged rounds are read back from the session's own event log, because
// that is where the verdict lands whatever the timeline chooses to draw.
async function evalDetails(request: APIRequestContext, sessionID: string): Promise<EvalDetail[]> {
  const response = await request.post('/cyber.rpc.chat.SessionService/ListEvents', {
    headers: {
      Authorization: `Bearer ${API_TOKEN}`,
      'Content-Type': 'application/json',
      'Connect-Protocol-Version': '1',
    },
    data: { sessionId: sessionID },
  })
  expect(response.ok(), await response.text()).toBeTruthy()
  const body = await response.json()
  return (body.events || []).flatMap((entry: any) =>
    (entry.event?.extensions || [])
      .filter((extension: any) => String(extension['@type']).endsWith('cyber.agent.EvalDetail'))
      .map((extension: any) => ({ ...extension, state: entry.event?.status?.state })))
}

test('goal mode drives rounds off the evaluator verdict', async ({ page, request }) => {
  test.skip(!GOAL_READY, 'needs an LLM that answers evaluator calls with a verdict (CYBER_E2E_GOAL=1)')
  test.setTimeout(300_000)
  await login(page)
  await openGoalSession(page)

  const sessionID = new URL(page.url()).pathname.split('/').filter(Boolean).at(-1) || ''
  expect(sessionID).not.toBe('')

  await page.getByRole('textbox', { name: 'Your goal' })
    .fill('必须给出当前目录下文件的数量，并说明用什么命令得到的')
  await page.getByRole('button', { name: 'Send message' }).click()

  // A round was judged, so what the browser sent really entered the eval loop.
  await expect
    .poll(async () => (await evalDetails(request, sessionID)).filter((d) => d.state === 'eval_end').length,
      { timeout: 240_000, intervals: [2_000] })
    .toBeGreaterThan(0)

  const details = await evalDetails(request, sessionID)
  expect(details.some((d) => d.state === 'eval_start')).toBeTruthy()
  const ended = details.filter((d) => d.state === 'eval_end')
  // With no pacing override, the default backstop remains in force.
  expect(details[0].maxRounds).toBe(20)
  // The loop ended on a verdict, not by exhausting the backstop.
  expect(ended.at(-1)?.round).toBeLessThan(20)
  expect(typeof ended.at(-1)?.reason).toBe('string')
  await expect(page.getByText('Response complete')).toBeVisible({ timeout: 60_000 })
})
