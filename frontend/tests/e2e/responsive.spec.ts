import { expect, test } from '@playwright/test'

test.use({ viewport: { width: 390, height: 844 } })

test('移动端上传页保留主要导航和上传入口', async ({ page }) => {
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/status') data = { initialized: true, authenticated: true, csrf_token: 'e2e' }
    else if (path === '/storages') data = [{ id: 'local', name: '本地存储', type: 'local', enabled: true, config: {} }]
    else if (path === '/settings') data = { site_name: '轻羽图床', site_url: '', default_storage_id: 'local', max_file_size: 20 << 20, max_batch_count: 10, allowed_types: ['image/png'], naming_rule: 'random', allow_duplicates: false, processing: {} }
    await route.fulfill({ json: { success: true, data, request_id: 'e2e' } })
  })

  await page.goto('/upload')
  await expect(page.getByRole('navigation', { name: '主导航' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '上传图片' })).toBeVisible()
  await expect(page.getByRole('button', { name: '选择图片' })).toBeVisible()
  await expect(page.locator('body')).not.toHaveCSS('overflow-x', 'scroll')
})
