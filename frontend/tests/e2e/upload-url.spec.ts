import { expect, test } from '@playwright/test'

const pixel = 'data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs='
const envelope = (data: unknown) => ({ success: true, data, request_id: 'e2e' })

test('从直接图片 URL 导入并显示结果', async ({ page }) => {
  let importBody: unknown
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/status') data = { initialized: true, authenticated: true, csrf_token: 'e2e' }
    else if (path === '/storages') data = [{ id: 'local', name: '本地存储', type: 'local', enabled: true, config: {} }]
    else if (path === '/settings') data = { site_name: '轻羽图床', site_url: '', default_storage_id: 'local', max_file_size: 20 << 20, max_batch_count: 10, allowed_types: ['image/jpeg'], naming_rule: 'random', allow_duplicates: false }
    else if (path === '/images/import-url') {
      importBody = request.postDataJSON()
      data = { id: 'image-url', original_name: 'remote.jpg', storage_type: 'local', storage_id: 'local', mime_type: 'image/jpeg', size: 2048, width: 640, height: 480, url: pixel, thumbnail_url: pixel, favorite: false, created_at: '2026-07-17T00:00:00Z' }
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(envelope(data)) })
  })

  await page.goto('/upload')
  await page.getByRole('tab', { name: '图片 URL' }).click()
  await page.getByLabel('图片地址').fill('https://example.com/image.jpg')
  await page.getByLabel('保存文件名（可选）').fill('remote.jpg')
  await page.getByRole('button', { name: '导入图片' }).click()

  await expect(page.getByText('最近 URL 导入')).toBeVisible()
  await expect(page.getByText('remote.jpg', { exact: true })).toBeVisible()
  expect(importBody).toEqual({ url: 'https://example.com/image.jpg', storage_id: 'local', filename: 'remote.jpg' })
})
