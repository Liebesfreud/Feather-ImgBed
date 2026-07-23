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

test.describe('WebGL 点阵背景', () => {
  test.use({ contextOptions: { reducedMotion: 'no-preference' } })

  test('低分辨率渲染仍覆盖整个视口', async ({ page }) => {
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

    const background = page.locator('.global-dither')
    const canvas = background.locator('canvas')
    await expect(canvas).toBeVisible({ timeout: 5_000 })
    const sizes = await background.evaluate((element) => {
      const backgroundRect = element.getBoundingClientRect()
      const canvasElement = element.querySelector('canvas')!
      const canvasRect = canvasElement.getBoundingClientRect()
      return {
        background: { width: backgroundRect.width, height: backgroundRect.height },
        canvas: { width: canvasRect.width, height: canvasRect.height },
        buffer: { width: canvasElement.width, height: canvasElement.height },
      }
    })

    expect(sizes.canvas).toEqual(sizes.background)
    expect(sizes.buffer.width).toBeLessThan(sizes.canvas.width)
    expect(sizes.buffer.height).toBeLessThan(sizes.canvas.height)
  })
})

test('认证状态携带启动数据时不再重复请求上传配置', async ({ page }) => {
  const requested: string[] = []
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    requested.push(path)
    if (path !== '/auth/status') {
      await route.fulfill({ status: 500, json: { success: false, error: { code: 'UNEXPECTED', message: '不应重复请求' }, request_id: 'e2e' } })
      return
    }
    await route.fulfill({
      json: {
        success: true,
        data: {
          initialized: true,
          authenticated: true,
          csrf_token: 'e2e',
          upload: {
            storages: [{ id: 'local', name: '本地存储', type: 'local', enabled: true, config: {} }],
            settings: {
              site_name: '轻羽图床', site_url: 'http://127.0.0.1:4173', default_storage_id: 'local',
              max_file_size: 20971520, max_batch_count: 10, allowed_types: ['image/jpeg'], naming_rule: 'random', allow_duplicates: false,
              processing: { generate_webp: false, webp_quality: 82, strip_metadata: true, watermark_enabled: false, watermark_text: '', watermark_position: 'bottom-right' },
            },
            statistics: { image_count: 7, storage_bytes: 1024, traffic_bytes: 2048 },
          },
        },
        request_id: 'e2e',
      },
    })
  })

  await page.goto('/upload')

  await expect(page.getByRole('combobox', { name: '选择上传存储' })).toContainText('本地存储')
  await expect(page.getByText('7', { exact: true })).toBeVisible()
  await expect.poll(() => requested).toEqual(['/auth/status'])
})
