import { randomBytes } from 'node:crypto'
import { expect, test } from '@playwright/test'

for (const imageOnly of [false, true]) {
  test(`vision attachment reaches the model (${imageOnly ? 'image only' : 'text and image'})`, async ({ page }) => {
    test.setTimeout(120_000)
    await page.request.post('/api/auth/login', { data: { token: process.env.ACCESS_KEY || 'test-token' } })
    await page.goto('/')
    const node = page.getByRole('button', { name: /e2e-node.*idle/ })
    await expect(node).toBeVisible()
    await node.locator('xpath=..').getByRole('button', { name: 'New', exact: true }).click()
    await expect(page).toHaveURL(/\/sessions\//)
    const code = randomBytes(4).toString('hex').toUpperCase()
    const png = await page.evaluate(value => {
      const canvas = document.createElement('canvas')
      canvas.width = 720
      canvas.height = 200
      const context = canvas.getContext('2d')!
      context.fillStyle = 'white'
      context.fillRect(0, 0, 720, 200)
      context.fillStyle = 'black'
      context.font = 'bold 80px monospace'
      context.fillText(value, 35, 130)
      return canvas.toDataURL('image/png').split(',')[1]
    }, code)
    await page.locator('input[type="file"]').setInputFiles({ name: 'input.png', mimeType: 'image/png', buffer: Buffer.from(png, 'base64') })
    if (!imageOnly) await page.getByRole('textbox', { name: 'Type a message... (/ for commands)' }).fill('Read the code printed in the attached image. Reply with only the code.')
    await page.getByRole('button', { name: 'Send message' }).click()
    const answer = process.env.CYBER_E2E_LLM_BASE_URL ? code : 'IMAGE_RECEIVED'
    await expect(page.getByTestId('assistant-response-content')).toContainText(answer, { timeout: 90_000 })
    await expect(page.getByRole('button', { name: 'Pause response' })).toHaveCount(0)
    await page.reload()
    await expect(page.getByTestId('assistant-response-content')).toContainText(answer)
  })
}
