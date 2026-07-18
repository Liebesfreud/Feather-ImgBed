import { expect, test } from '@playwright/test'

const pixel = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')

test('真实后端可完成初始化和图片上传', async ({ page }) => {
  const consoleErrors: string[] = []
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text())
  })

  await page.goto('/')
  await page.getByLabel('管理员用户名').fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('integration-password')
  await page.getByLabel('确认密码').fill('integration-password')
  await page.getByRole('button', { name: '创建并进入' }).click()

  await expect(page.getByRole('heading', { name: '上传图片' })).toBeVisible()
  await page.locator('input[type=file]').setInputFiles({
    name: 'integration.png',
    mimeType: 'image/png',
    buffer: pixel,
  })
  await expect(page.getByText('integration.png', { exact: true })).toBeVisible()
  await expect(page.getByText('上传成功', { exact: true })).toBeVisible()

  await page.getByRole('link', { name: '系统设置' }).click()
  await page.getByRole('tab', { name: '安全与令牌' }).click()
  await page.getByPlaceholder('Token 用途名称，例如 PicGo').fill('集成测试上传')
  await page.getByRole('button', { name: '创建 Token' }).click()
  await expect(page.locator('.new-token code')).not.toBeEmpty()
  await expect(page.getByText('上传图片 · 永不过期', { exact: false })).toBeVisible()
  expect(consoleErrors).toEqual([])
})
