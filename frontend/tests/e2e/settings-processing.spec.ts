import { expect, test } from '@playwright/test'

const baseSettings = {
  site_name: '轻羽图床',
  site_url: 'https://img.example.com',
  default_storage_id: 'local',
  max_file_size: 20 << 20,
  max_batch_count: 10,
  allowed_types: ['image/jpeg', 'image/png'],
  naming_rule: 'random',
  allow_duplicates: false,
  random: { enabled: false, album_id: '', tag_id: '' },
  processing: {
    generate_webp: false,
    webp_quality: 82,
    strip_metadata: false,
    watermark_enabled: false,
    watermark_text: '',
    watermark_position: 'bottom-right',
  },
}
const envelope = (data: unknown) => ({ success: true, data, request_id: 'e2e' })

test('设置页保存 WebP 与水印派生配置', async ({ page }) => {
  let saved: typeof baseSettings | undefined
  let settingsReads = 0
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/status') data = { initialized: true, authenticated: true, csrf_token: 'e2e' }
    else if (path === '/settings' && request.method() === 'GET') {
      settingsReads += 1
      data = settingsReads === 1 ? baseSettings : { ...baseSettings, random: { enabled: true, album_id: 'album-1', tag_id: 'tag-1' } }
    }
    else if (path === '/settings' && request.method() === 'PUT') data = saved = request.postDataJSON()
    else if (path === '/storages') data = [{ id: 'local', name: '本地存储', type: 'local', enabled: true, config: {} }]
    else if (path === '/tokens') data = []
    else if (path === '/system') data = { version: 'test', database: 'ok', enabled_storages: 1 }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(envelope(data)) })
  })

  await page.goto('/settings')
  await page.getByRole('switch', { name: '生成 WebP 版本' }).click()
  await page.getByRole('switch', { name: '清理图片元数据' }).click()
  await page.getByLabel('WebP 质量').fill('88')
  await page.getByRole('switch', { name: '生成水印版本' }).click()
  await page.getByLabel('水印文字').fill('Feather')
  await page.getByLabel('水印位置').selectOption({ label: '左上角' })
  await page.getByRole('button', { name: '保存设置' }).click()

  await expect.poll(() => saved?.processing).toEqual({
    generate_webp: true,
    webp_quality: 88,
    strip_metadata: true,
    watermark_enabled: true,
    watermark_text: 'Feather',
    watermark_position: 'top-left',
  })
  expect(saved?.random).toEqual({ enabled: true, album_id: 'album-1', tag_id: 'tag-1' })
  expect(saved?.processing).not.toHaveProperty('default_link_variant')
})
