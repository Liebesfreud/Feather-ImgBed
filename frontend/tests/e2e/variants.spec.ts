import { expect, test } from '@playwright/test'

const pixel = 'data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs='
const image = { id: 'image-1', original_name: 'cat.jpg', storage_type: 'local', storage_id: 'local', mime_type: 'image/jpeg', size: 2048, width: 640, height: 480, url: 'https://img.example.com/original.jpg', thumbnail_url: pixel, favorite: false, created_at: '2026-07-17T00:00:00Z' }
const envelope = (data: unknown) => ({ success: true, data, request_id: 'e2e' })

test('图片详情可切换派生版本并复制对应链接', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.route('https://img.example.com/**', (route) => route.fulfill({ contentType: 'image/gif', body: Buffer.from('R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs=', 'base64') }))
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/status') data = { initialized: true, authenticated: true, csrf_token: 'e2e' }
    else if (path === '/storages') data = [{ id: 'local', name: '本地存储', type: 'local', enabled: true, config: {} }]
    else if (path === '/tags' || path === '/albums' || path === '/images/image-1/tags') data = []
    else if (path === '/images/image-1') data = { ...image, variants: [{ id: 'variant-webp', kind: 'webp', url: 'https://img.example.com/image.webp', mime_type: 'image/webp', size: 1024, width: 640, height: 480, created_at: '2026-07-17T00:00:01Z' }] }
    else if (path === '/images') data = { items: [image], next_cursor: '' }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(envelope(data)) })
  })

  await page.goto('/gallery')
  await page.getByRole('img', { name: 'cat.jpg' }).click()
  await page.getByLabel('选择图片版本').click()
  await page.getByRole('option', { name: 'WebP' }).click()
  await page.getByRole('button', { name: '复制链接' }).click()

  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('https://img.example.com/image.webp')
})
