import { expect, type Page } from '@playwright/test'

export async function nodeActions(page: Page, nodeName = 'e2e-node') {
  await page.getByRole('button', { name: 'Nodes', exact: true }).click()
  const node = page.getByRole('button', { name: new RegExp(`${nodeName}.*idle`) })
  await expect(node).toBeVisible()
  return node.locator('xpath=..')
}

export async function createNodeTask(page: Page, nodeName = 'e2e-node') {
  const actions = await nodeActions(page, nodeName)
  await actions.getByRole('button', { name: 'New', exact: true }).click()
  await expect(page).toHaveURL(/\/sessions\//)
  await expect(page.getByRole('textbox', { name: 'Your goal' })).toBeVisible()
  return actions
}
