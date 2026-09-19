import { readFileSync } from 'node:fs'
import { expect, test, type Page, type Route } from '@playwright/test'

const createdAt = '2026-08-01T00:00:00Z'
const accessToken = `header.${Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString('base64url')}.signature`
const productionCsp = readFileSync(new URL('../nginx.conf', import.meta.url), 'utf8').match(
  /add_header Content-Security-Policy "([^"]+)" always;/
)?.[1]

if (!productionCsp) throw new Error('frontend/nginx.conf must define Content-Security-Policy')

const menu = (id: number, path: string, name: string, title: string, roles: string[]) => ({
  id,
  parentId: null,
  path,
  name,
  component: `${path}/index`,
  updatedAt: createdAt,
  meta: {
    title,
    i18nKey: '',
    i18nTexts: { zh: title, en: title },
    keepAlive: true,
    isHide: false,
    isHideTab: false,
    isFullPage: false,
    fixedTab: false,
    isEnable: true,
    sort: id,
    roles
  }
})

const menus = [
  menu(1, '/home', 'Home', '首页', ['R_SUPER']),
  menu(2, '/results', 'Results', '共享结果', ['R_USER', 'R_SUPER'])
]

async function fulfillApi(route: Route, data: unknown) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ code: 200, msg: '', data })
  })
}

async function installBackendMocks(page: Page) {
  const unexpectedApiCalls: string[] = []
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (url.hostname === '127.0.0.1' && url.port === '4173') return route.fallback()
    await route.abort('blockedbyclient')
  })
  await page.routeWebSocket('**/api/v1/ws/**', (socket) => socket.close())
  await page.route('http://127.0.0.1:4173/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    if (method === 'POST' && path === '/api/v1/auth/login')
      return fulfillApi(route, { accessToken })
    if (method === 'GET' && path === '/api/v1/me') {
      if (request.headers().authorization === `Bearer ${accessToken}`) {
        return fulfillApi(route, {
          permissions: [],
          roleCodes: ['R_SUPER'],
          userId: 1,
          username: 'e2e-user',
          email: 'e2e@example.test',
          avatar: ''
        })
      }
      return route.fulfill({ status: 401, contentType: 'application/problem+json', body: '{}' })
    }
    if (method === 'GET' && path === '/api/v1/system/i18n-dictionaries')
      return fulfillApi(route, { zh: {}, en: {} })
    if (method === 'GET' && path === '/api/v1/system/menus') return fulfillApi(route, menus)
    if (method === 'GET' && path === '/api/v1/result-pages') return fulfillApi(route, { items: [] })
    if (method === 'GET' && path === '/api/v1/result-views') return fulfillApi(route, { items: [] })
    unexpectedApiCalls.push(`${method} ${path}`)
    return route.fulfill({
      status: 501,
      contentType: 'application/json',
      body: JSON.stringify({ code: 500, msg: 'unexpected E2E request', data: null })
    })
  })
  return { unexpectedApiCalls }
}

async function loginAsTestUser(page: Page, protectedPath: string) {
  await page.goto(protectedPath)
  await page.locator('input').nth(0).fill('e2e-user')
  await page.locator('input').nth(1).fill('e2e-password')
  const slider = page.locator('.drag_verify')
  const handler = page.locator('.dv_handler')
  const box = await slider.boundingBox()
  if (!box) throw new Error('login slider is not visible')
  await handler.hover()
  await page.mouse.down()
  await page.mouse.move(box.x + box.width - 4, box.y + box.height / 2)
  await page.mouse.up()
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`${protectedPath}$`))
}

test('生产 CSP 和图标加载不依赖外部 Iconify 服务', async ({ page }) => {
  const iconifyRequests: string[] = []
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (url.hostname.includes('iconify') || url.hostname.includes('unisvg')) {
      iconifyRequests.push(url.href)
      return route.abort()
    }
    const response = await route.fetch()
    await route.fulfill({
      response,
      headers: { ...response.headers(), 'content-security-policy': productionCsp! }
    })
  })
  await page.goto('/auth/login')
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
  expect(iconifyRequests).toEqual([])
})

test('未登录访问受保护页面时被登录边界拦截', async ({ page }) => {
  const backend = await installBackendMocks(page)
  await page.goto('/results')
  await expect(page).toHaveURL(/\/auth\/login\?redirect=/)
  expect(backend.unexpectedApiCalls).toEqual([])
})

test('授权用户可以访问正式 ResultView 页面', async ({ page }) => {
  const backend = await installBackendMocks(page)
  await loginAsTestUser(page, '/results')
  await expect(page.getByRole('heading', { name: '共享结果', exact: true })).toBeVisible()
  await expect(page.getByText('暂无获授权结果', { exact: true })).toBeVisible()
  expect(backend.unexpectedApiCalls).toEqual([])
})
