import { expect, type Page } from '@playwright/test'

export async function nodeActions(page: Page, nodeName = 'e2e-node') {
  const node = page.locator('aside [data-node-id]').filter({ has: page.getByRole('button', { name: `New task on ${nodeName}`, exact: true }) })
  await expect(node).toBeVisible()
  return node
}

export async function createNodeTask(page: Page, nodeName = 'e2e-node') {
  const actions = await nodeActions(page, nodeName)
  await actions.getByRole('button', { name: `New task on ${nodeName}`, exact: true }).click()
  await expect(page).toHaveURL(/\/sessions\//)
  await expect(page.getByRole('textbox', { name: 'Your goal' })).toBeVisible()
  return actions
}

export async function openNodeTerminal(page: Page, nodeName = 'e2e-node') {
  await page.getByRole('button', { name: /agent.*connected/ }).click()
  const drawer = page.getByRole('dialog').filter({ hasText: 'Agent Console' })
  await expect(drawer).toBeVisible()
  const node = drawer.getByRole('button').filter({ has: page.getByText(nodeName, { exact: true }) })
  if (await node.count()) await node.click()
  await expect(page.locator('.xterm')).toBeVisible({ timeout: 20_000 })
  return drawer
}
