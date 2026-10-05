import { readFileSync } from 'node:fs'
import { expect, test, type Page, type Route } from '@playwright/test'

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
if (!productionCsp || iconifyApiOrigins.some((origin) => !productionCsp.includes(origin)))
  throw new Error('production CSP must permit Iconify')

const authorPermissions = [
  'workflows.read',
  'workflows.create',
  'workflows.update',
  'workflows.publish',
  'workflows.run',
  'human_tasks.read',
  'human_tasks.decide',
  'result_views.read'
]
const graph = {
  schemaVersion: 3,
  entryPoints: { main: 'start' },
  nodes: [
    {
      nodeInstanceId: 'start',
      nodeType: 'core.manual',
      nodeVersion: '1.0.0',
      config: {},
      position: { x: 100, y: 160 }
    },
    {
      nodeInstanceId: 'end',
      nodeType: 'core.end',
      nodeVersion: '1.0.0',
      config: {},
      position: { x: 420, y: 160 }
    }
  ],
  edges: [
    {
      edgeId: 'start-end',
      sourceNodeInstanceId: 'start',
      sourcePort: 'out',
      targetNodeInstanceId: 'end',
      targetPort: 'in'
    }
  ]
}
const nodeDefinitions = ['manual', 'end'].map((type) => ({
  type: `core.${type}`,
  version: '1.0.0',
  title: type === 'manual' ? '手工入口' : '结束',
  kind: type === 'manual' ? 'trigger' : 'action',
  category: 'Core',
  description: '',
  capabilities: { deterministic: true, stateless: true },
  configSchema: {},
  inputSchema: {},
  outputSchema: {},
  uiSchema: {},
  inputPorts: type === 'manual' ? [] : ['in'],
  outputPorts: type === 'end' ? [] : ['out'],
  secretFields: [],
  available: true
}))
const menu = (id: number, path: string, component: string, title: string) => ({
  id,
  parentId: null,
  path,
  component,
  name: `TestMenu${id}`,
  updatedAt: createdAt,
  meta: {
    title,
    keepAlive: false,
    isHide: false,
    isHideTab: false,
    isFullPage: false,
    isIframe: false,
    fixedTab: false,
    isEnable: true,
    sort: id,
    roles: ['R_USER']
  }
})

async function fulfillApi(route: Route, data: unknown, status = 200, msg = '') {
  await route.fulfill({
    status,
    contentType: status >= 400 ? 'application/problem+json' : 'application/json',
    body: JSON.stringify(
      status >= 400
        ? {
            type: 'about:blank',
            title: 'Request rejected',
            status,
            detail: msg,
            requestId: 'synthetic'
          }
        : { code: status, msg, data }
    )
  })
}

// Only transport is replaced. All navigation, forms, component loading and state updates use the production UI.
async function backendFixture(
  page: Page,
  options: {
    permissions?: string[]
    anonymous?: boolean
    count?: number
    denyWorkflow?: boolean
    missingComponent?: boolean
  } = {}
) {
  const permissions = options.permissions ?? authorPermissions
  const calls: string[] = [],
    unexpected: string[] = [],
    payloads: unknown[] = []
  const workflow = {
    id: 7,
    name: '业务审批示例',
    description: '合成定义',
    groupId: null,
    mode: 'batch',
    status: 'inactive',
    draftRevisionId: 11,
    publishedRevisionId: 0,
    ownerUserId: 1,
    mainTriggerNodeId: 'start',
    retentionDays: 30,
    createdBy: 1,
    createdAt,
    updatedAt: createdAt,
    permissions,
    stateNodeInstanceIds: [],
    runtime: { maxConcurrentRuns: 1, backlogLimit: 20, updatedAt: createdAt }
  }
  let revision = {
    id: 11,
    workflowId: 7,
    revisionNumber: 1,
    graph,
    nodeVersions: {},
    mainTriggerNodeId: 'start',
    createdBy: 1,
    createdAt,
    secretFields: {}
  }
  const revisions = [revision]
  const run = {
    id: 21,
    workflowId: 7,
    revisionId: 11,
    entryPoint: 'main',
    input: {},
    triggerType: 'manual',
    status: 'waiting',
    triggeredAt: createdAt,
    diagnostic: false,
    resultSummary: {},
    runNodes: [],
    logs: [],
    artifacts: []
  }
  let tasks = [
    {
      id: 31,
      workflowId: 7,
      runId: 21,
      nodeInstanceId: 'review',
      taskType: 'approval',
      businessKey: 'case-1',
      prompt: '审核合成业务事项',
      status: 'pending',
      expiresAt: '2099-01-01T00:00:00Z',
      createdAt
    }
  ]
  const view = {
    id: 41,
    name: '固定授权结果',
    pluginId: options.missingComponent ? 'test.uncompiled' : 'official.binance',
    pageKey: 'paper',
    status: 'active',
    canManage: false,
    allowedActions: [],
    createdAt
  }
  const catalog = [
    {
      id: view.pluginId,
      version: '1.0.0',
      resultPages: [
        {
          pageKey: 'paper',
          title: '模拟结果',
          componentEntry: 'PaperResultPage',
          scopeSchema: {},
          filterSchema: {},
          actions: [],
          actionPermissions: {},
          permissionCode: 'plugin.official.binance.paper.read'
        }
      ],
      runPanels: []
    }
  ]
  if (!options.anonymous)
    await page.addInitScript(
      (token) => sessionStorage.setItem('coinsphere-access-token', token),
      accessToken
    )
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (iconifyApiOrigins.includes(url.origin)) {
      const prefix =
        url.pathname
          .split('/')
          .pop()
          ?.replace(/\.json$/, '') || 'ri'
      const names = (url.searchParams.get('icons') || '').split(',').filter(Boolean)
      await route.fulfill({
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
    if (!url.pathname.startsWith('/api/')) {
      if (url.origin !== 'http://127.0.0.1:4173') {
        await route.abort()
        return
      }
      await route.continue()
      return
    }
    const path = url.pathname,
      method = route.request().method(),
      key = `${method} ${path}`
    calls.push(key)
    const reply = (data: unknown) => fulfillApi(route, data)
    if (path === '/api/v1/me') {
      if (options.anonymous) {
        await fulfillApi(route, null, 401, '请先登录')
        return
      }
      await reply({
        permissions,
        roleCodes: ['R_USER'],
        userId: 1,
        username: 'e2e-user',
        email: '',
        avatar: '',
        accessMode: 'authenticated'
      })
      return
    }
    if (path === '/api/v1/system/i18n-dictionaries') {
      await reply({ zh: {}, en: {} })
      return
    }
    if (path === '/api/v1/system/menus') {
      await reply([
        menu(1, '/workbench', '/workbench/index', '工作台'),
        ...(permissions.includes('workflows.read')
          ? [menu(2, '/scheduler/definition', '/scheduler/workflow/index', '工作流')]
          : []),
        ...(permissions.includes('result_views.read')
          ? [menu(3, '/results', '/results/index', '共享结果')]
          : [])
      ])
      return
    }
    if (path === '/api/v1/notification-deliveries') {
      await reply({ records: [], total: 0, hasMore: false, nextCursor: '', unreadCount: 0 })
      return
    }
    if (path === '/api/v1/workbench') {
      await reply({
        tasks,
        workflows: { records: [workflow], total: 1, hasMore: false },
        resultViews: [view]
      })
      return
    }
    if (path === '/api/v1/workflow-groups') {
      await reply({ items: [] })
      return
    }
    if (path === '/api/v1/plugins/catalog') {
      await reply({ items: catalog })
      return
    }
    if (path === '/api/v1/result-views') {
      await reply({ items: [view] })
      return
    }
    if (path === '/api/v1/result-views/41/plugins/official.binance/paper') {
      await reply({
        orders: [],
        accounts: [
          { id: 'synthetic-paper', cashBalance: '100.00', equity: '100.00', positions: [] }
        ]
      })
      return
    }
    if (path === '/api/v1/workflows/node-definitions') {
      await reply({ items: nodeDefinitions })
      return
    }
    if (path === '/api/v1/workflows/templates') {
      await reply({
        items: [{ key: 'blank', name: '空白工作流', mode: 'batch', description: '手工入口与结束' }]
      })
      return
    }
    if (path === '/api/v1/workflows/validate') {
      await reply({ valid: true, issues: [] })
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows') {
      const records = Array.from({ length: options.count ?? 1 }, (_, index) => ({
        ...workflow,
        id: 7 + index,
        name: `业务审批示例 ${index + 1}`,
        latestRunId: 21,
        latestRunStatus: index % 2 ? 'retrying' : 'waiting',
        maxConcurrentRuns: 1,
        backlogLimit: 20
      }))
      await reply({ records, total: records.length, hasMore: false, nextCursor: '' })
      return
    }
    if (method === 'POST' && path === '/api/v1/workflows') {
      const body = route.request().postDataJSON()
      payloads.push(body)
      Object.assign(workflow, { name: body.name, description: body.description })
      revision.graph = body.graph
      await reply(workflow)
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows/7') {
      if (options.denyWorkflow) {
        await fulfillApi(route, null, 403, '没有该工作流的访问权限')
        return
      }
      await reply(workflow)
      return
    }
    if (method === 'GET' && path === '/api/v1/workflows/7/revisions') {
      await reply({ items: revisions })
      return
    }
    if (method === 'GET' && /^\/api\/v1\/workflows\/7\/revisions\/\d+$/.test(path)) {
      await reply(revisions.find((item) => item.id === Number(path.split('/').pop())))
      return
    }
    if (method === 'POST' && path === '/api/v1/workflows/7/revisions') {
      const body = route.request().postDataJSON()
      payloads.push(body)
      revision = { ...revision, id: 12, revisionNumber: 2, graph: body.graph }
      revisions.unshift(revision)
      Object.assign(workflow, body.metadata, { draftRevisionId: revision.id })
      await reply(revision)
      return
    }
    if (method === 'POST' && path === '/api/v1/workflows/7/publish') {
      const body = route.request().postDataJSON()
      payloads.push(body)
      workflow.publishedRevisionId = body.revisionId
      await reply(workflow)
      return
    }
    if (method === 'POST' && path === '/api/v1/workflows/7/runs') {
      const body = route.request().postDataJSON()
      payloads.push(body)
      Object.assign(run, body)
      await reply(run)
      return
    }
    if (method === 'GET' && path === '/api/v1/workflow-runs/21') {
      await reply(run)
      return
    }
    if (method === 'POST' && path === '/api/v1/human-tasks/31') {
      const body = route.request().postDataJSON()
      payloads.push(body)
      tasks = []
      await reply({ id: 31, status: 'approved' })
      return
    }
    unexpected.push(key)
    await fulfillApi(route, null, 500, `unhandled fixture: ${key}`)
  })
  return { calls, unexpected, payloads, workflow }
}

test('匿名直接访问编辑器被登录边界拦截', async ({ page }) => {
  const backend = await backendFixture(page, { anonymous: true })
  await page.goto('/scheduler/workflow/create')
  await expect(page).toHaveURL(/\/auth\/login\?redirect=/)
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
  expect(backend.calls.filter((call) => call.includes('/workflows'))).toEqual([])
})

test('作者完成创建、保存、发布和手工运行', async ({ page }) => {
  const backend = await backendFixture(page)
  await page.goto('/scheduler/workflow/create')
  await page.locator('.workflow-meta input').first().fill('合成业务流程')
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await expect(page).toHaveURL(/\/scheduler\/workflow\/7\/edit$/)
  await expect(page.getByText('草稿 #11 · 已发布 暂无')).toBeVisible()
  await page.locator('.workflow-meta input').first().fill('修改后的业务流程')
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await expect(page.getByText('草稿 #12 · 已发布 暂无')).toBeVisible()
  expect(backend.workflow.publishedRevisionId).toBe(0)
  await page.getByRole('button', { name: '发布草稿', exact: true }).click()
  await expect(page.getByText('草稿 #12 · 已发布 #12')).toBeVisible()
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await page.getByRole('button', { name: '加入运行队列' }).click()
  await expect(page).toHaveURL(/\/scheduler\/execution\/21\/detail$/)
  await expect(page.getByRole('heading', { name: '运行 #21' })).toBeVisible()
  await expect(page.locator('.run-header .el-tag')).toHaveText('等待处理')
  expect(backend.payloads).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ expectedDraftRevisionId: 11, graph }),
      { revisionId: 12, expectedPublishedRevisionId: 0 },
      { revisionId: 12, entryPoint: 'main', input: {} }
    ])
  )
  expect(backend.unexpected).toEqual([])
})

test('待办决定成功后从工作台移除', async ({ page }) => {
  const backend = await backendFixture(page, {
    permissions: ['human_tasks.read', 'human_tasks.decide']
  })
  await page.goto('/workbench')
  await expect(page.getByText('审核合成业务事项')).toBeVisible()
  await page.getByRole('button', { name: '通过', exact: true }).click()
  await page.getByRole('button', { name: '确定', exact: true }).click()
  await expect(page.getByText('暂无待处理事项')).toBeVisible()
  expect(backend.calls).toContain('POST /api/v1/human-tasks/31')
  expect(backend.payloads).toContainEqual({ action: 'approve', data: {} })
  expect(backend.unexpected).toEqual([])
})

test('结果用户只通过固定视图读取结果', async ({ page }) => {
  const backend = await backendFixture(page, { permissions: ['result_views.read'] })
  await page.goto('/results')
  await expect(page.getByRole('region', { name: '固定授权结果' })).toBeVisible()
  await expect(page.getByLabel('Paper 账户摘要')).toContainText('100.00')
  await expect(page.getByRole('button', { name: '创建结果视图', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '管理授权' })).toHaveCount(0)
  expect(backend.calls).toContain('GET /api/v1/result-views/41/plugins/official.binance/paper')
  expect(backend.calls.some((call) => call.includes('/workflows'))).toBe(false)
  await page.goto('/scheduler/workflow/7/edit')
  await expect(page).toHaveURL(/\/403$/)
  expect(backend.calls).not.toContain('GET /api/v1/workflows/7')
  expect(backend.unexpected).toEqual([])
})

test('具备读取能力仍不能读取未授权工作流', async ({ page }) => {
  const backend = await backendFixture(page, { denyWorkflow: true })
  await page.goto('/scheduler/workflow/7/edit')
  await expect(page.locator('.workflow-page .el-alert')).toContainText('没有该工作流的访问权限')
  await expect(page.getByRole('button', { name: '发布草稿' })).toHaveCount(0)
  expect(backend.unexpected).toEqual([])
})

test('列表读取不随工作流数量增加，并展示真实等待和重试状态', async ({ page }) => {
  const backend = await backendFixture(page, { count: 25 })
  await page.goto('/scheduler/definition')
  await expect(page.getByText('共 25 个工作流')).toBeVisible()
  await expect(
    page.locator('.workflow-library .el-table').getByText('等待处理').first()
  ).toBeVisible()
  await expect(
    page.locator('.workflow-library .el-table').getByText('等待重试').first()
  ).toBeVisible()
  expect(backend.calls.filter((call) => call === 'GET /api/v1/workflows')).toHaveLength(1)
  expect(backend.calls.filter((call) => /^GET \/api\/v1\/workflows\/\d+/.test(call))).toEqual([])
  expect(backend.unexpected).toEqual([])
})

test('插件缺少前端贡献时给出可见反馈', async ({ page }) => {
  const backend = await backendFixture(page, {
    permissions: ['result_views.read'],
    missingComponent: true
  })
  await page.goto('/results')
  await expect(page.locator('.results-page .el-alert')).toContainText(
    '插件 test.uncompiled 的前端组件未编译'
  )
  expect(backend.unexpected).toEqual([])
})

test('窄屏通过键盘操作主要按钮', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const backend = await backendFixture(page)
  await page.goto('/scheduler/workflow/create')
  await page.locator('.workflow-meta input').first().fill('键盘流程')
  const save = page.getByRole('button', { name: '保存草稿', exact: true })
  await save.focus()
  await expect(save).toBeFocused()
  const bounds = await save.boundingBox()
  expect(bounds && bounds.x >= 0 && bounds.x + bounds.width <= 390).toBeTruthy()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/scheduler\/workflow\/7\/edit$/)
  expect(backend.unexpected).toEqual([])
})

test('生产 CSP 允许登录页渲染', async ({ page }) => {
  const iconifyRequests: string[] = []
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (iconifyApiOrigins.includes(url.origin)) {
      iconifyRequests.push(url.href)
      const prefix =
        url.pathname
          .split('/')
          .pop()
          ?.replace(/\.json$/, '') || 'ri'
      const names = (url.searchParams.get('icons') || '').split(',').filter(Boolean)
      await route.fulfill({
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
    if (url.origin !== 'http://127.0.0.1:4173') {
      await route.abort()
      return
    }
    const response = await route.fetch()
    await route.fulfill({
      response,
      headers: { ...response.headers(), 'content-security-policy': productionCsp }
    })
  })
  await page.goto('/auth/login')
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
  await expect(page.locator('.palette-btn svg.art-svg-icon')).toBeVisible()
  expect(iconifyRequests.length).toBeGreaterThan(0)
})
