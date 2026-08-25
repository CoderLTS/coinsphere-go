import { readFileSync } from 'node:fs'
import { expect, test, type Page, type Route } from '@playwright/test'

type AccessMode = 'guest' | 'authenticated'

const createdAt = '2026-08-01T00:00:00Z'
const accessToken = `header.${Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString('base64url')}.signature`

const productionCsp = readFileSync(new URL('../nginx.conf', import.meta.url), 'utf8').match(
  /add_header Content-Security-Policy "([^"]+)" always;/
)?.[1]

const iconifyApiOrigins = [
  'https://api.iconify.design',
  'https://api.unisvg.com',
  'https://api.simplesvg.com'
]

if (!productionCsp) {
  throw new Error('frontend/nginx.conf must define Content-Security-Policy')
}

for (const origin of iconifyApiOrigins) {
  if (!productionCsp.includes(origin)) {
    throw new Error(`frontend/nginx.conf must allow Iconify API origin: ${origin}`)
  }
}

const homeMenu = {
  id: 1,
  parentId: null,
  path: '/home',
  name: 'Home',
  component: '/home/index',
  updatedAt: createdAt,
  meta: {
    title: '首页',
    i18nKey: '',
    i18nTexts: { zh: '首页', en: 'Home' },
    keepAlive: true,
    isHide: false,
    isHideTab: false,
    isFullPage: false,
    isIframe: false,
    fixedTab: true,
    isEnable: true,
    sort: 10,
    roles: ['R_GUEST', 'R_SUPER']
  }
}

function userInfo(accessMode: AccessMode) {
  const authenticated = accessMode === 'authenticated'
  return {
    permissions: [],
    roleCodes: [authenticated ? 'R_SUPER' : 'R_GUEST'],
    userId: authenticated ? 1 : 0,
    username: authenticated ? 'e2e-user' : 'guest',
    email: '',
    avatar: '',
    accessMode
  }
}

async function fulfillApi(route: Route, data: unknown) {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ code: 200, msg: '', data })
  })
}

async function installBackendMocks(page: Page, accessMode: AccessMode) {
  const unexpectedApiCalls: string[] = []
  const authApiCalls: string[] = []
  const schedulerApiCalls: string[] = []

  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    const isLocalWebServer =
      (url.protocol === 'http:' || url.protocol === 'https:') &&
      url.hostname === '127.0.0.1' &&
      url.port === '4173'

    if (isLocalWebServer) {
      await route.fallback()
      return
    }

    await route.abort('blockedbyclient')
  })

  await page.routeWebSocket('**/api/v1/ws/**', async (webSocket) => {
    await webSocket.close({ code: 1000, reason: 'Playwright API isolation' })
  })

  await page.route('http://127.0.0.1:4173/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()

    if (path.startsWith('/api/v1/workflows')) {
      schedulerApiCalls.push(`${method} ${path}`)
    }
    if (path.startsWith('/api/v1/auth/')) {
      authApiCalls.push(`${method} ${path}`)
    }

    if (method === 'POST' && path === '/api/v1/auth/login') {
      if (accessMode === 'authenticated') {
        await fulfillApi(route, { accessToken })
        return
      }
      await route.fulfill({
        status: 401,
        contentType: 'application/problem+json',
        body: JSON.stringify({
          type: 'about:blank',
          title: 'Unauthorized',
          status: 401,
          detail: 'invalid credentials',
          requestId: 'e2e-request'
        })
      })
      return
    }
    if (method === 'GET' && path === '/api/v1/me') {
      const hasTestSession = request.headers().authorization === `Bearer ${accessToken}`
      if (accessMode === 'authenticated' && hasTestSession) {
        await fulfillApi(route, userInfo('authenticated'))
        return
      }
      await route.fulfill({
        status: 401,
        contentType: 'application/problem+json',
        body: JSON.stringify({
          type: 'about:blank',
          title: 'Unauthorized',
          status: 401,
          detail: 'missing authorization',
          requestId: 'e2e-request'
        })
      })
      return
    }
    if (method === 'GET' && path === '/api/v1/system/i18n-dictionaries') {
      await fulfillApi(route, { zh: {}, en: {} })
      return
    }
    if (method === 'GET' && path === '/api/v1/system/menus') {
      await fulfillApi(route, [homeMenu])
      return
    }
    if (method === 'GET' && path === '/api/v1/home/overview') {
      await fulfillApi(route, {
        process: {
          uptimeSeconds: 60,
          goMemoryAllocBytes: 0,
          goMemorySysBytes: 0,
          goroutines: 1
        },
        http: {
          requestsTotal: 0,
          requestsFailed: 0,
          requestsInFlight: 0,
          trend: []
        },
        database: {
          status: 'healthy',
          schemaVersion: 1,
          maxOpenConnections: 1,
          openConnections: 1,
          inUse: 0,
          idle: 1,
          waitCount: 0
        }
      })
      return
    }
    unexpectedApiCalls.push(`${method} ${path}`)
    await route.fulfill({
      status: 501,
      contentType: 'application/json',
      body: JSON.stringify({ code: 500, msg: '未声明的 E2E API 请求', data: null })
    })
  })

  return {
    unexpectedApiCalls,
    authApiCalls,
    schedulerApiCalls
  }
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

test('生产 CSP 不会阻断登录页首屏渲染', async ({ page }) => {
  const iconifyRequests: string[] = []

  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (iconifyApiOrigins.includes(url.origin)) {
      const prefix =
        url.pathname
          .split('/')
          .pop()
          ?.replace(/\.json$/, '') || 'ri'
      const names = (url.searchParams.get('icons') || '').split(',').filter(Boolean)
      iconifyRequests.push(url.href)
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        headers: { 'access-control-allow-origin': '*' },
        body: JSON.stringify({
          prefix,
          width: 24,
          height: 24,
          icons: Object.fromEntries(
            names.map((name) => [name, { body: '<path fill="currentColor" d="M2 2h20v20H2z" />' }])
          )
        })
      })
      return
    }

    const response = await route.fetch()
    await route.fulfill({
      response,
      headers: { ...response.headers(), 'content-security-policy': productionCsp }
    })
  })

  await page.goto('/auth/login?redirect=/auth')

  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
  await expect(page.locator('.palette-btn svg.art-svg-icon')).toBeVisible()
  expect(iconifyRequests.length).toBeGreaterThan(0)
})

test('匿名访问工作流编辑器时被登录边界拦截', async ({ page }) => {
  const backend = await installBackendMocks(page, 'guest')

  await page.goto('/scheduler/workflow/create')

  await expect(page).toHaveURL(/\/auth\/login\?redirect=/)
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
  expect(backend.schedulerApiCalls).toEqual([])
  expect(backend.authApiCalls).toEqual([])
  expect(backend.unexpectedApiCalls).toEqual([])
})

test('授权用户访问已撤下工作流路径时回退首页', async ({ page }) => {
  const backend = await installBackendMocks(page, 'authenticated')

  await loginAsTestUser(page, '/scheduler/workflow/create')

  await expect(page.getByRole('heading', { name: '系统总览', exact: true })).toBeVisible()
  await expect(page.getByText('新建工作流定义', { exact: true })).toHaveCount(0)
  expect(backend.schedulerApiCalls).toEqual([])
  expect(backend.authApiCalls).toEqual(['POST /api/v1/auth/login'])
  expect(backend.unexpectedApiCalls).toEqual([])
})
