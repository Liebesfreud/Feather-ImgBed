import { expect, test, type Page, type Route } from '@playwright/test'

const pixel = 'data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs='
const image = {
  id: 'image-1',
  original_name: 'cat.jpg',
  storage_type: 'local',
  storage_id: 'local',
  mime_type: 'image/jpeg',
  size: 2048,
  width: 640,
  height: 480,
  url: 'https://img.example.com/cat.jpg',
  thumbnail_url: pixel,
  favorite: false,
  created_at: '2026-07-17T08:00:00Z',
}

function envelope(data: unknown) {
  return { success: true, data, request_id: 'e2e' }
}

async function fulfill(route: Route, data: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(envelope(data)) })
}

async function mockManagementAPI(page: Page) {
  let galleryItems = [image]
  let trashItems = [{ ...image, deleted_at: '2026-07-17T09:00:00Z' }]
  const calls: { path: string; body?: unknown; query?: URLSearchParams }[] = []

  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace('/api/v1', '')
    const body = request.postData() ? request.postDataJSON() : undefined
    calls.push({ path, body, query: url.searchParams })

    if (path === '/auth/status') return fulfill(route, { initialized: true, authenticated: true, csrf_token: 'e2e-csrf' })
    if (path === '/storages') return fulfill(route, [{ id: 'local', name: '本地存储', type: 'local', enabled: true, config: {} }])
    if (path === '/tags') return fulfill(route, [])
    if (path === '/albums') return fulfill(route, [])
    if (/^\/images\/[^/]+\/tags$/.test(path)) return fulfill(route, [])
    if (path === '/images' && request.method() === 'GET') return fulfill(route, { items: galleryItems, next_cursor: '' })
    if (path === '/images/bulk') {
      galleryItems = []
      return fulfill(route, { requested: 1, affected: 1, not_found: 0 })
    }
    if (path === '/trash' && request.method() === 'GET') return fulfill(route, { items: trashItems, next_cursor: '' })
    if (/^\/trash\/[^/]+\/restore$/.test(path)) {
      trashItems = trashItems.filter((item) => item.id !== path.split('/')[2])
      return fulfill(route, { restored: true })
    }
    if (path === '/trash/purge') {
      trashItems = []
      return fulfill(route, { requested: 1, deleted: 1, failed: 0, results: [] })
    }
    return fulfill(route, {})
  })

  return {
    calls,
    resetTrash: () => { trashItems = [{ ...image, deleted_at: '2026-07-17T09:00:00Z' }] },
  }
}

test('图库恢复 query、复制 Markdown 并批量移入回收站', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const { calls } = await mockManagementAPI(page)

  await page.goto('/gallery?search=cat&from=2026-07-01&to=2026-07-17&order=asc')

  await expect(page.getByRole('heading', { name: '图片管理' })).toBeVisible()
  await expect(page.getByLabel('搜索图片名称')).toHaveValue('cat')
  await page.getByRole('button', { name: '筛选' }).click()
  await expect(page.getByLabel('起始日期')).toHaveValue('2026-07-01')
  await expect(page.getByLabel('结束日期')).toHaveValue('2026-07-17')
  const firstImage = page.getByRole('img', { name: 'cat.jpg' })
  await expect(firstImage).toHaveAttribute('src', pixel)
  await expect(firstImage).toHaveAttribute('loading', 'eager')
  await expect(firstImage).toHaveAttribute('fetchpriority', 'high')
  await expect(firstImage).toHaveAttribute('decoding', 'async')

  const imageRequest = calls.find((call) => call.path === '/images')
  expect(imageRequest?.query?.get('order')).toBe('asc')
  expect(imageRequest?.query?.get('from')).toBeTruthy()
  const expectedEndOfDay = await page.evaluate(() => new Date(2026, 6, 17, 23, 59, 59, 999).toISOString())
  expect(imageRequest?.query?.get('to')).toBe(expectedEndOfDay)

  await page.getByRole('button', { name: '批量管理' }).click()
  await page.getByRole('checkbox', { name: '选择 cat.jpg' }).click()
  await page.getByRole('button', { name: '复制链接' }).click()
  await page.getByRole('menuitem', { name: 'Markdown' }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('![cat.jpg](https://img.example.com/cat.jpg)')

  await page.getByRole('button', { name: '移入回收站', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '移入回收站' }).click()
  await expect(page.getByText('没有找到匹配的图片')).toBeVisible()
  expect(calls.find((call) => call.path === '/images/bulk')?.body).toEqual({ action: 'trash', ids: ['image-1'] })
})

test('回收站支持批量恢复和清空确认', async ({ page }) => {
  const api = await mockManagementAPI(page)
  await page.goto('/trash')

  await expect(page.getByRole('heading', { name: '回收站' })).toBeVisible()
  await page.getByRole('button', { name: '批量管理' }).click()
  await page.getByRole('checkbox', { name: '选择 cat.jpg' }).click()
  await page.getByRole('button', { name: '批量恢复' }).click()
  await expect(page.getByText('回收站是空的')).toBeVisible()
  expect(api.calls.some((call) => call.path === '/trash/image-1/restore')).toBe(true)

  api.resetTrash()
  await page.reload()
  await page.getByRole('button', { name: '清空回收站' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '永久删除' }).click()
  await expect(page.getByText('回收站是空的')).toBeVisible()
  expect(api.calls.find((call) => call.path === '/trash/purge')?.body).toEqual({ all: true })
})

test('图片管理统一收纳图库、相册与回收站', async ({ page }) => {
  await mockManagementAPI(page)
  await page.goto('/gallery')

  const mainNavigation = page.getByRole('navigation', { name: '主导航' })
  await expect(mainNavigation.getByRole('link')).toHaveCount(4)
  await expect(mainNavigation.getByRole('link', { name: 'API' })).toHaveAttribute('href', '/developer')
  await expect(mainNavigation.getByRole('link', { name: '图片管理' })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByText('查找、预览并管理所有已上传的图片。')).toHaveCount(0)

  const managementNavigation = page.getByRole('navigation', { name: '图片管理分类' })
  await managementNavigation.getByRole('link', { name: '相册' }).click()
  await expect(page).toHaveURL(/\/albums$/)
  await expect(page.getByRole('heading', { name: '相册', exact: true })).toBeVisible()
  await expect(mainNavigation.getByRole('link', { name: '图片管理' })).toHaveAttribute('aria-current', 'page')

  await managementNavigation.getByRole('link', { name: '回收站' }).click()
  await expect(page).toHaveURL(/\/trash$/)
  await expect(page.getByRole('heading', { name: '回收站', exact: true })).toBeVisible()
  await expect(mainNavigation.getByRole('link', { name: '图片管理' })).toHaveAttribute('aria-current', 'page')

  await managementNavigation.getByRole('link', { name: '全部图片' }).click()
  await expect(page).toHaveURL(url => url.pathname === '/gallery')
  await expect(page.getByRole('heading', { name: '图片管理' })).toBeVisible()
})
