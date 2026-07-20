import { expect, test } from '@playwright/test'

test('未初始化时显示管理员初始化界面', async ({ page }) => {
  await page.route('**/api/v1/auth/status', async (route) => {
    await route.fulfill({
      json: {
        success: true,
        data: { initialized: false, authenticated: false },
        request_id: 'e2e',
      },
    })
  })

  await page.goto('/')

  await expect(page).toHaveTitle('轻羽图床')
  await expect(page.getByRole('heading', { name: '开始使用轻羽' })).toBeVisible()
})

test('认证检查期间不初始化 WebGL 背景', async ({ page }) => {
  let releaseStatus!: () => void
  const statusGate = new Promise<void>((resolve) => {
    releaseStatus = resolve
  })
  await page.route('**/api/v1/auth/status', async (route) => {
    await statusGate
    await route.fulfill({
      json: {
        success: true,
        data: { initialized: false, authenticated: false },
        request_id: 'e2e',
      },
    })
  })

  await page.goto('/')

  await expect(page.getByLabel('正在加载')).toBeVisible()
  await expect(page.locator('.global-dither')).toHaveCount(0)
  releaseStatus()
  await expect(page.getByRole('heading', { name: '开始使用轻羽' })).toBeVisible()
})
