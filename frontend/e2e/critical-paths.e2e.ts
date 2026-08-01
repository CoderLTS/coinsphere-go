import { expect, test, type Page, type Route } from '@playwright/test'

type AccessMode = 'guest' | 'authenticated'

interface WorkflowPayload {
  displayName: string
  description: string
  graph: {
    nodes: Array<{ id: string; type: string }>
    edges: Array<{ id: string; source: string; target: string }>
  }
}

const createdAt = '2026-08-01T00:00:00Z'

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

const nodeDefinitions = [
  {
    typeCode: 'start.manual',
    label: '手动开始',
    configSchema: { type: 'object', properties: {} },
    kind: 'start'
  },
  {
    typeCode: 'end',
    label: '结束',
    configSchema: { type: 'object', properties: {} },
    kind: 'terminal'
  }
]

function userInfo(accessMode: AccessMode) {
  const authenticated = accessMode === 'authenticated'
  return {
    permissions: authenticated
      ? ['scheduler.workflow_definitions.create', 'scheduler.workflow_definitions.update']
      : [],
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
  const schedulerApiCalls: string[] = []
  let createdPayload: WorkflowPayload | null = null
  let createdDefinition: Record<string, unknown> | null = null

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

  await page.routeWebSocket('**/ws/**', async (webSocket) => {
    await webSocket.close({ code: 1000, reason: 'Playwright API isolation' })
  })

  await page.route('http://127.0.0.1:4173/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()

    if (path.startsWith('/api/scheduler/')) {
      schedulerApiCalls.push(`${method} ${path}`)
    }

    if (method === 'GET' && path === '/api/auth/me') {
      const hasTestSession = request.headers().authorization === 'Bearer playwright-access-token'
      const resolvedAccessMode =
        accessMode === 'authenticated' && hasTestSession ? 'authenticated' : 'guest'
      await fulfillApi(route, userInfo(resolvedAccessMode))
      return
    }
    if (method === 'GET' && path === '/api/system/i18n-dictionaries') {
      await fulfillApi(route, { zh: {}, en: {} })
      return
    }
    if (method === 'GET' && path === '/api/system/menus') {
      await fulfillApi(route, [homeMenu])
      return
    }
    if (method === 'GET' && path === '/api/scheduler/task-definitions') {
      await fulfillApi(route, [])
      return
    }
    if (method === 'GET' && path === '/api/scheduler/node-definitions') {
      await fulfillApi(route, nodeDefinitions)
      return
    }
    if (method === 'GET' && path === '/api/scheduler/agent-options') {
      await fulfillApi(route, [])
      return
    }
    if (method === 'POST' && path === '/api/scheduler/workflow-definitions/validate') {
      await fulfillApi(route, { valid: true, issues: [] })
      return
    }
    if (method === 'POST' && path === '/api/scheduler/workflow-definitions') {
      createdPayload = request.postDataJSON() as WorkflowPayload
      createdDefinition = {
        id: 42,
        code: 'e2e-workflow',
        version: 1,
        ...createdPayload,
        isLatest: true,
        isBuiltin: false,
        isActive: false,
        isWorkflowActive: false,
        activeDefinitionId: null,
        activeVersion: null,
        executionCount: 0,
        createdBy: 1,
        createdAt
      }
      await fulfillApi(route, createdDefinition)
      return
    }
    if (method === 'GET' && path === '/api/scheduler/workflow-definitions/42') {
      if (createdDefinition) {
        await fulfillApi(route, createdDefinition)
        return
      }
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
    schedulerApiCalls,
    get createdPayload() {
      return createdPayload
    }
  }
}

test('游客访问工作流编辑器时被权限边界拦截', async ({ page }) => {
  const backend = await installBackendMocks(page, 'guest')

  await page.goto('/scheduler/workflow/create')

  await expect(page).toHaveURL(/\/403$/)
  await expect(page.getByText('抱歉，您无权访问该页面')).toBeVisible()
  await expect(page.getByRole('button', { name: '返回首页' })).toBeVisible()
  expect(backend.schedulerApiCalls).toEqual([])
  expect(backend.unexpectedApiCalls).toEqual([])
})

test('授权用户可以填写基础信息并保存默认工作流', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('user', JSON.stringify({ accessToken: 'playwright-access-token' }))
  })
  const backend = await installBackendMocks(page, 'authenticated')

  await page.goto('/scheduler/workflow/create')

  await expect(page.getByText('新建工作流定义', { exact: true })).toBeVisible()
  await page.getByLabel('工作流名称').fill('浏览器门禁工作流')
  await page.getByLabel('工作流说明').fill('验证工作流编辑器关键保存路径')
  await page.getByRole('button', { name: '应用', exact: true }).click()
  await expect(page.getByText('未保存', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '保存定义', exact: true }).click()

  const detailResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'GET' &&
      new URL(response.url()).pathname === '/api/scheduler/workflow-definitions/42'
  )
  await page.getByRole('button', { name: '离开', exact: true }).click()
  await detailResponse

  await expect(page).toHaveURL(/\/scheduler\/workflow\/42\/edit$/)
  await expect(
    page.getByRole('navigation', { name: 'breadcrumb' }).getByText('编辑工作流定义', {
      exact: true
    })
  ).toBeVisible()
  await expect(page.getByText('已同步', { exact: true })).toBeVisible()

  const payload = backend.createdPayload
  expect(payload).not.toBeNull()
  expect(payload).toMatchObject({
    displayName: '浏览器门禁工作流',
    description: '验证工作流编辑器关键保存路径'
  })
  expect(payload?.graph.nodes.map((node) => node.type)).toEqual(['start.manual', 'end'])
  expect(payload?.graph.edges).toHaveLength(1)
  expect(backend.schedulerApiCalls).toEqual(
    expect.arrayContaining([
      'GET /api/scheduler/task-definitions',
      'GET /api/scheduler/node-definitions',
      'GET /api/scheduler/agent-options',
      'POST /api/scheduler/workflow-definitions/validate',
      'POST /api/scheduler/workflow-definitions',
      'GET /api/scheduler/workflow-definitions/42'
    ])
  )
  expect(backend.unexpectedApiCalls).toEqual([])
})
