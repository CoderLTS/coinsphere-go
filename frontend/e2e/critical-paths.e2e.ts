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

const schedulerMenu = {
  id: 2,
  parentId: null,
  path: '/scheduler',
  name: 'SchedulerCenter',
  component: '/index/index',
  updatedAt: createdAt,
  meta: {
    title: '工作流调度',
    i18nKey: '',
    i18nTexts: { zh: '工作流调度', en: 'Workflow Scheduler' },
    keepAlive: false,
    isHide: false,
    isHideTab: false,
    isFullPage: false,
    isIframe: false,
    fixedTab: false,
    isEnable: true,
    sort: 20,
    roles: ['R_SUPER']
  },
  children: [
    {
      id: 3,
      parentId: 2,
      path: '/scheduler/node-definition',
      name: 'NodeDefinitions',
      component: '/scheduler/node-definition/index',
      updatedAt: createdAt,
      meta: {
        title: '节点定义',
        i18nKey: '',
        i18nTexts: { zh: '节点定义', en: 'Node Definitions' },
        keepAlive: true,
        isHide: false,
        isHideTab: false,
        isFullPage: false,
        isIframe: false,
        fixedTab: false,
        isEnable: true,
        sort: 10,
        roles: ['R_SUPER']
      }
    },
    {
      id: 4,
      parentId: 2,
      path: '/scheduler/definition',
      name: 'WorkflowDefinitions',
      component: '/scheduler/workflow/index',
      updatedAt: createdAt,
      meta: {
        title: '工作流定义',
        i18nKey: '',
        i18nTexts: { zh: '工作流定义', en: 'Workflow Definitions' },
        keepAlive: true,
        isHide: false,
        isHideTab: false,
        isFullPage: false,
        isIframe: false,
        fixedTab: false,
        isEnable: true,
        sort: 20,
        roles: ['R_SUPER']
      }
    }
  ]
}

const dataMenu = {
  id: 5,
  parentId: null,
  path: '/data',
  name: 'DataCenter',
  component: '/index/index',
  updatedAt: createdAt,
  meta: {
    title: '数据管理',
    i18nKey: '',
    i18nTexts: { zh: '数据管理', en: 'Data Management' },
    keepAlive: false,
    isHide: false,
    isHideTab: false,
    isFullPage: false,
    isIframe: false,
    fixedTab: false,
    isEnable: true,
    sort: 30,
    roles: ['R_SUPER']
  },
  children: [
    {
      id: 6,
      parentId: 5,
      path: '/data/market-metadata',
      name: 'MarketMetadata',
      component: '/data/market-metadata/index',
      updatedAt: createdAt,
      meta: {
        title: '币种数据',
        i18nKey: '',
        i18nTexts: { zh: '币种数据', en: 'Instruments' },
        keepAlive: true,
        isHide: false,
        isHideTab: false,
        isFullPage: false,
        isIframe: false,
        fixedTab: false,
        isEnable: true,
        sort: 10,
        roles: ['R_SUPER']
      }
    },
    {
      id: 7,
      parentId: 5,
      path: '/data/market-chart',
      name: 'MarketChart',
      component: '/data/market-chart/index',
      updatedAt: createdAt,
      meta: {
        title: 'K 线详情',
        i18nKey: '',
        i18nTexts: { zh: 'K 线详情', en: 'Candles' },
        keepAlive: false,
        isHide: true,
        isHideTab: false,
        isFullPage: false,
        isIframe: false,
        fixedTab: false,
        isEnable: true,
        sort: 20,
        roles: ['R_SUPER']
      }
    }
  ]
}

const workflow = {
  id: 7,
  name: '批处理示例',
  description: 'E2E workflow',
  mode: 'batch',
  status: 'running',
  activeRevisionId: 11,
  mainTriggerNodeId: 'manual-trigger',
  retentionDays: 30,
  createdBy: 1,
  createdAt,
  updatedAt: createdAt,
  runtime: { activityCursor: 0, healthSummary: 'idle', updatedAt: createdAt }
}

const workflowRevision = {
  id: 11,
  workflowId: 7,
  revisionNumber: 1,
  graph: {
    schemaVersion: 1,
    nodes: [
      {
        nodeInstanceId: 'manual-trigger',
        nodeType: 'core.manual',
        nodeVersion: '1.0.0',
        config: {},
        position: { x: 160, y: 220 }
      },
      {
        nodeInstanceId: 'end',
        nodeType: 'core.end',
        nodeVersion: '1.0.0',
        config: {},
        position: { x: 520, y: 220 }
      }
    ],
    edges: [
      {
        edgeId: 'manual-to-end',
        sourceNodeInstanceId: 'manual-trigger',
        sourcePort: 'out',
        targetNodeInstanceId: 'end',
        targetPort: 'in'
      }
    ]
  },
  nodeVersions: {
    'manual-trigger': { nodeType: 'core.manual', nodeVersion: '1.0.0' },
    end: { nodeType: 'core.end', nodeVersion: '1.0.0' }
  },
  mainTriggerNodeId: 'manual-trigger',
  createdBy: 1,
  createdAt,
  secretFields: {}
}

const workflowActivity = {
  cursor: 9,
  workflowId: 7,
  batchId: 21,
  eventType: 'batch.succeeded',
  status: 'succeeded',
  summary: '批次执行成功',
  occurredAt: createdAt
}

const workflowBatchDetail = {
  id: 21,
  workflowId: 7,
  revisionId: 11,
  triggerType: 'manual',
  status: 'succeeded',
  triggeredAt: createdAt,
  startedAt: createdAt,
  completedAt: createdAt,
  nodeRuns: [
    {
      id: 31,
      nodeInstanceId: 'manual-trigger',
      nodeType: 'core.manual',
      nodeVersion: '1.0.0',
      executionPool: 'stream',
      attempt: 1,
      loopIteration: 0,
      operationKey: 'a'.repeat(64),
      status: 'succeeded',
      startedAt: createdAt,
      completedAt: createdAt,
      durationMs: 4
    },
    {
      id: 32,
      nodeInstanceId: 'end',
      nodeType: 'core.end',
      nodeVersion: '1.0.0',
      executionPool: 'stream',
      attempt: 1,
      loopIteration: 0,
      operationKey: 'b'.repeat(64),
      status: 'succeeded',
      startedAt: createdAt,
      completedAt: createdAt,
      durationMs: 2
    }
  ],
  activities: [workflowActivity],
  artifacts: []
}

const nodeDefinitions = ['core.manual', 'core.constant', 'core.end'].map((type) => ({
  type,
  version: '1.0.0',
  title: { 'core.manual': 'Manual trigger', 'core.constant': 'Constant', 'core.end': 'End' }[type],
  description: 'E2E node',
  kind: type === 'core.manual' ? 'trigger' : 'action',
  configSchema:
    type === 'core.constant'
      ? {
          $schema: 'https://json-schema.org/draft/2020-12/schema',
          type: 'object',
          properties: { value: { type: 'string', title: 'Value' } },
          required: ['value'],
          additionalProperties: false
        }
      : {
          $schema: 'https://json-schema.org/draft/2020-12/schema',
          type: 'object',
          additionalProperties: false
        },
  uiSchema: {},
  inputSchema: {
    $schema: 'https://json-schema.org/draft/2020-12/schema',
    type: 'object',
    additionalProperties: false
  },
  outputSchema: {
    $schema: 'https://json-schema.org/draft/2020-12/schema',
    type: 'object',
    additionalProperties: false
  },
  inputPorts: type === 'core.manual' ? [] : ['in'],
  outputPorts: type === 'core.end' ? [] : ['out'],
  secretFields: [],
  available: true
}))

function userInfo(accessMode: AccessMode) {
  const authenticated = accessMode === 'authenticated'
  return {
    permissions: authenticated ? ['scheduler.workflow_definitions.update'] : [],
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

    if (path.startsWith('/api/v1/workflows') || path.startsWith('/api/v1/batches')) {
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
      await fulfillApi(
        route,
        accessMode === 'authenticated' ? [homeMenu, schedulerMenu, dataMenu] : [homeMenu]
      )
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows') {
      await fulfillApi(route, { items: [workflow] })
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows/node-definitions') {
      await fulfillApi(route, { items: nodeDefinitions })
      return
    }
    if (method === 'GET' && path === '/api/v1/human-tasks') {
      await fulfillApi(route, { items: [] })
      return
    }
    if (method === 'GET' && path === '/api/v1/strategies') {
      await fulfillApi(route, { records: [] })
      return
    }
    if (method === 'GET' && path === '/api/v1/trading/accounts') {
      await fulfillApi(route, [])
      return
    }
    if (method === 'GET' && path === '/api/v1/notification-channels') {
      await fulfillApi(route, [])
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows/7') {
      await fulfillApi(route, workflow)
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows/7/revisions') {
      await fulfillApi(route, { items: [workflowRevision] })
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows/7/revisions/11') {
      await fulfillApi(route, workflowRevision)
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows/7/batches') {
      await fulfillApi(route, { items: [] })
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows/7/activity') {
      await fulfillApi(route, { items: [workflowActivity], nextCursor: 9 })
      return
    }
    if (method === 'GET' && path === '/api/v1/batches/21') {
      await fulfillApi(route, workflowBatchDetail)
      return
    }
    if (method === 'POST' && path === '/api/v1/workflows/7/batches') {
      await fulfillApi(route, {
        id: 21,
        workflowId: 7,
        revisionId: 11,
        triggerType: 'manual',
        status: 'queued',
        triggeredAt: createdAt
      })
      return
    }
    if (method === 'GET' && path === '/api/v1/plugins/official.quant/instruments') {
      await fulfillApi(route, {
        items: [
          {
            market: 'spot',
            symbol: 'BTCUSDT',
            baseAsset: 'BTC',
            quoteAsset: 'USDT',
            status: 'TRADING',
            priceTick: '0.01',
            quantityStep: '0.00001',
            minQuantity: '0.00001',
            updatedAt: createdAt
          }
        ]
      })
      return
    }
    if (method === 'GET' && path === '/api/v1/plugins/official.quant/candles') {
      await fulfillApi(route, {
        items: [
          {
            market: 'spot',
            instrument: 'BTCUSDT',
            interval: '1h',
            openTime: '2026-08-01T00:00:00Z',
            closeTime: '2026-08-01T00:59:59Z',
            open: '100',
            high: '110',
            low: '90',
            close: '105',
            volume: '12.5'
          }
        ]
      })
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

test('授权用户访问已撤下共享结果路径时回退首页', async ({ page }) => {
  const backend = await installBackendMocks(page, 'authenticated')

  await loginAsTestUser(page, '/results')

  await expect(page.getByRole('heading', { name: '系统总览', exact: true })).toBeVisible()
  await expect(page.getByText('共享结果', { exact: true })).toHaveCount(0)
  expect(backend.schedulerApiCalls).toEqual([])
  expect(backend.authApiCalls).toEqual(['POST /api/v1/auth/login'])
  expect(backend.unexpectedApiCalls).toEqual([])
})

test('超级管理员可以使用原节点列表页面', async ({ page }) => {
  const backend = await installBackendMocks(page, 'authenticated')

  await loginAsTestUser(page, '/scheduler/node-definition')

  await expect(page.getByRole('heading', { name: '节点定义', exact: true })).toBeVisible()
  await expect(page.getByRole('tab', { name: '内置节点', exact: true })).toBeVisible()
  await expect(page.getByText('手动开始', { exact: true })).toBeVisible()

  expect(backend.schedulerApiCalls).toEqual(
    expect.arrayContaining(['GET /api/v1/workflows/node-definitions'])
  )
  expect(backend.unexpectedApiCalls).toEqual([])
})

test('超级管理员可以使用原工作流列表页面', async ({ page }) => {
  const backend = await installBackendMocks(page, 'authenticated')

  await loginAsTestUser(page, '/scheduler/definition')
  await expect(page.getByText('批处理示例', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '查看执行记录', exact: true })).toBeVisible()
  expect(backend.unexpectedApiCalls).toEqual([])
})

test('超级管理员可以使用原工作流编辑器', async ({ page }) => {
  const backend = await installBackendMocks(page, 'authenticated')

  await loginAsTestUser(page, '/scheduler/workflow/7/edit')

  await expect(page.getByRole('button', { name: '保存定义', exact: true })).toBeVisible()
  await expect(page.locator('.workflow-canvas__graph .x6-graph-svg')).toBeVisible()
  expect(backend.schedulerApiCalls).toEqual(
    expect.arrayContaining([
      'GET /api/v1/workflows/node-definitions',
      'GET /api/v1/workflows/7',
      'GET /api/v1/workflows/7/revisions'
    ])
  )
  expect(backend.unexpectedApiCalls).toEqual([])
})

test('超级管理员可以使用原币种元数据页面', async ({ page }) => {
  const calls = await installBackendMocks(page, 'authenticated')
  await loginAsTestUser(page, '/data/market-metadata')

  await expect(page.getByRole('heading', { name: '币种元数据', exact: true })).toBeVisible()
  await expect(page.getByText('BTCUSDT', { exact: true }).first()).toBeVisible()
  expect(calls.unexpectedApiCalls).toEqual([])
})

test('超级管理员可以使用原 K 线详情页面', async ({ page }) => {
  const calls = await installBackendMocks(page, 'authenticated')
  await loginAsTestUser(page, '/data/market-chart')

  await expect(page.getByRole('heading', { name: 'K 线与策略信号', exact: true })).toBeVisible()
  await expect(page.getByText('BTCUSDT', { exact: true }).first()).toBeVisible()
  expect(calls.unexpectedApiCalls).toEqual([])
})
