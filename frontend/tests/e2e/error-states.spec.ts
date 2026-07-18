import { expect, test } from '@playwright/test'

const envelope = (message: string) => ({
  success: false,
  error: { code: 'TEMPORARY_ERROR', message },
  request_id: 'e2e-error',
})

test('设置读取失败时显示重试状态而不是默认配置', async ({ page }) => {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/auth/status')) {
      await route.fulfill({ json: { success: true, data: { initialized: true, authenticated: true, csrf_token: 'e2e' }, request_id: 'e2e' } })
      return
    }
    await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify(envelope('暂时不可用')) })
  })

  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: '系统设置暂时无法读取' })).toBeVisible()
  await expect(page.getByRole('button', { name: '重新加载' })).toBeVisible()
  await expect(page.getByRole('button', { name: '保存设置' })).toHaveCount(0)
})
