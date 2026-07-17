import { expect, test } from '@playwright/test'

const pixel = 'data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs='
const album = { id: 'album-1', name: '旅行', description: '夏日照片', image_count: 1, created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-17T00:00:00Z' }
const image = { id: 'image-1', original_name: 'sea.jpg', storage_type: 'local', storage_id: 'local', mime_type: 'image/jpeg', size: 1024, width: 640, height: 480, url: pixel, thumbnail_url: pixel, favorite: false, created_at: '2026-07-17T00:00:00Z' }
const envelope = (data: unknown) => ({ success: true, data, request_id: 'e2e' })

test('相册详情可设置封面并移除图片', async ({ page }) => {
  let images = [image]
  const calls: { path: string; method: string; body?: unknown }[] = []
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace('/api/v1', '')
    const body = request.postData() ? request.postDataJSON() : undefined
    calls.push({ path, method: request.method(), body })
    let data: unknown = {}
    if (path === '/auth/status') data = { initialized: true, authenticated: true, csrf_token: 'e2e' }
    else if (path === '/albums' && request.method() === 'GET') data = [album]
    else if (path === '/albums/album-1' && request.method() === 'GET') data = { album, images }
    else if (path === '/albums/album-1' && request.method() === 'PUT') data = { ...album, cover_image_id: (body as { cover_image_id: string }).cover_image_id, cover_url: pixel }
    else if (path === '/albums/album-1/images/image-1') {
      images = []
      data = { removed: true }
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(envelope(data)) })
  })

  await page.goto('/albums')
  await page.getByRole('heading', { name: '相册' }).waitFor()
  await page.getByText('旅行', { exact: true }).click()
  await expect(page).toHaveURL(/\/albums\/album-1$/)
  await expect(page.getByRole('img', { name: 'sea.jpg' })).toBeVisible()

  await page.getByRole('button', { name: '设为封面' }).first().click()
  expect(calls.find((call) => call.method === 'PUT')?.body).toMatchObject({ cover_image_id: 'image-1' })

  await page.getByRole('button', { name: '移出' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '移出相册' }).click()
  await expect(page.getByText('相册还是空的')).toBeVisible()
})
