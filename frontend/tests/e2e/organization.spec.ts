import { expect, test } from '@playwright/test'

const pixel = 'data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs='
const baseImage = { id: 'image-1', original_name: 'cat.jpg', storage_type: 'local', storage_id: 'local', mime_type: 'image/jpeg', size: 2048, width: 640, height: 480, url: pixel, thumbnail_url: pixel, favorite: false, created_at: '2026-07-17T00:00:00Z' }
const tag = { id: 'tag-1', name: '猫咪', color: '#1db954', image_count: 0, created_at: '2026-07-17T00:00:00Z', updated_at: '2026-07-17T00:00:00Z' }
const envelope = (data: unknown) => ({ success: true, data, request_id: 'e2e' })

test('图库可收藏、筛选收藏并批量添加标签', async ({ page }) => {
  let image = { ...baseImage }
  const calls: { path: string; method: string; body?: unknown; query: URLSearchParams }[] = []
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace('/api/v1', '')
    const body = request.postData() ? request.postDataJSON() : undefined
    calls.push({ path, method: request.method(), body, query: url.searchParams })
    let data: unknown = {}
    if (path === '/auth/status') data = { initialized: true, authenticated: true, csrf_token: 'e2e' }
    else if (path === '/storages') data = [{ id: 'local', name: '本地存储', type: 'local', enabled: true, config: {} }]
    else if (path === '/tags') data = [tag]
    else if (path === '/albums' || path === '/images/image-1/tags') data = []
    else if (path === '/images' && request.method() === 'GET') data = { items: [image], next_cursor: '' }
    else if (path === '/images/image-1' && request.method() === 'PATCH') {
      image = { ...image, favorite: (body as { favorite: boolean }).favorite }
      data = image
    } else if (path === '/images/bulk/tags') data = { images: 1, tags: 1, affected: 1 }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(envelope(data)) })
  })

  await page.goto('/gallery')
  await page.getByRole('button', { name: '收藏 cat.jpg' }).click()
  await expect(page.getByRole('button', { name: '取消收藏 cat.jpg' })).toBeVisible()

  await page.getByRole('button', { name: '仅看收藏' }).click()
  await expect(page).toHaveURL(/favorite=true/)
  await expect.poll(() => calls.some((call) => call.path === '/images' && call.query.get('favorite') === 'true')).toBe(true)

  await page.getByRole('button', { name: '批量管理' }).click()
  await page.getByRole('checkbox', { name: '选择 cat.jpg' }).click()
  await page.getByRole('button', { name: '标签', exact: true }).click()
  await page.getByRole('checkbox', { name: '猫咪' }).click()
  await page.getByRole('button', { name: '保存标签' }).click()

  expect(calls.find((call) => call.path === '/images/bulk/tags')?.body).toEqual({ action: 'add', ids: ['image-1'], tag_ids: ['tag-1'] })
})
