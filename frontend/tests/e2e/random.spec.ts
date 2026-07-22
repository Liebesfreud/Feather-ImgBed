import { expect, test } from '@playwright/test'

const settings = {
  site_name: '轻羽图床',
  site_url: 'https://img.example.com',
  default_storage_id: 'local',
  max_file_size: 20 << 20,
  max_batch_count: 10,
  allowed_types: ['image/jpeg', 'image/png'],
  naming_rule: 'random',
  allow_duplicates: false,
  random: { enabled: false, album_id: '', tag_id: '' },
  processing: { generate_webp: false, webp_quality: 82, strip_metadata: false, watermark_enabled: false, watermark_text: '', watermark_position: 'bottom-right' },
}
const album = { id: 'album-1', name: '公开相册', description: '', image_count: 2, created_at: '2026-07-22T00:00:00Z', updated_at: '2026-07-22T00:00:00Z' }
const tag = { id: 'tag-1', name: '可公开', color: '#22c55e', image_count: 2, created_at: '2026-07-22T00:00:00Z', updated_at: '2026-07-22T00:00:00Z' }
const envelope = (data: unknown) => ({ success: true, data, request_id: 'e2e' })

test('随机图作为图片管理独立页面配置公开范围', async ({ page }) => {
  let saved: typeof settings | undefined
  let settingsReads = 0
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/status') data = { initialized: true, authenticated: true, csrf_token: 'e2e' }
    else if (path === '/settings' && request.method() === 'GET') {
      settingsReads += 1
      data = settingsReads === 1 ? settings : { ...settings, site_name: '其他页面刚保存的名称' }
    }
    else if (path === '/settings' && request.method() === 'PUT') data = saved = request.postDataJSON()
    else if (path === '/albums') data = [album]
    else if (path === '/tags') data = [tag]
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(envelope(data)) })
  })

  await page.goto('/random')

  await expect(page.getByRole('navigation', { name: '图片管理分类' }).getByRole('link', { name: '随机图' })).toHaveAttribute('aria-current', 'page')
  await page.getByRole('switch', { name: '启用公开随机图 API' }).click()
  await page.getByLabel('随机图限定相册').selectOption('album-1')
  await page.getByLabel('随机图限定标签').selectOption('tag-1')
  await page.getByRole('button', { name: '保存设置' }).click()

  await expect.poll(() => saved?.random).toEqual({ enabled: true, album_id: 'album-1', tag_id: 'tag-1' })
  expect(saved?.site_name).toBe('其他页面刚保存的名称')
  await expect(page.getByText('仅从“公开相册”中带有“可公开”标签的图片抽取。', { exact: true })).toBeVisible()
})
