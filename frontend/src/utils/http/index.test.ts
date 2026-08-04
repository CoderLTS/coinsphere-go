import { AxiosError, type AxiosAdapter } from 'axios'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => {
  const createStorage = () => {
    const values = new Map<string, string>()
    return {
      get length() {
        return values.size
      },
      clear: () => values.clear(),
      getItem: (key: string) => values.get(key) ?? null,
      key: (index: number) => [...values.keys()][index] ?? null,
      removeItem: (key: string) => values.delete(key),
      setItem: (key: string, value: string) => values.set(key, value)
    }
  }

  vi.stubGlobal('localStorage', createStorage())
  vi.stubGlobal('sessionStorage', createStorage())

  return {
    fetchRefreshSession: vi.fn(),
    logout: vi.fn(),
    routerReplace: vi.fn(),
    resetRouterState: vi.fn(),
    clearWorktabs: vi.fn(),
    setHomePath: vi.fn()
  }
})

vi.mock('@/api/auth', () => ({
  fetchRefreshSession: mocks.fetchRefreshSession,
  logout: mocks.logout
}))
vi.mock('@/router', () => ({
  router: { currentRoute: { value: {} }, replace: mocks.routerReplace }
}))
vi.mock('@/router/guards/beforeEach', () => ({ resetRouterState: mocks.resetRouterState }))
vi.mock('@/store/modules/menu', () => ({
  useMenuStore: () => ({ setHomePath: mocks.setHomePath })
}))
vi.mock('@/store/modules/setting', () => ({ useSettingStore: () => ({ $state: {} }) }))
vi.mock('@/store/modules/worktab', () => ({
  useWorktabStore: () => ({ $state: {}, clearAll: mocks.clearWorktabs })
}))
vi.mock('@/utils/router', () => ({ setPageTitle: vi.fn() }))
vi.mock('@/locales', () => ({ $t: (key: string) => key }))
vi.mock('./error', () => {
  class HttpError extends Error {
    constructor(
      message: string,
      readonly code: number
    ) {
      super(message)
    }
  }

  return {
    HttpError,
    handleError: (error: {
      message: string
      response?: { status?: number; data?: { msg?: string } }
    }) => {
      throw new HttpError(error.response?.data?.msg || error.message, error.response?.status || 400)
    },
    showError: vi.fn(),
    showSuccess: vi.fn()
  }
})

import { useUserStore } from '@/store/modules/user'
import request from './index'

function createProtectedAdapter(unauthorizedAs: 'http' | 'envelope') {
  let attempts = 0
  const adapter: AxiosAdapter = async (config) => {
    attempts += 1
    const authorized = config.headers.get('Authorization') === 'Bearer refreshed-access-token'
    const response = {
      data: authorized
        ? { code: 200, msg: '', data: config.url }
        : { code: 401, msg: 'expired', data: null },
      status: 200,
      statusText: 'OK',
      headers: {},
      config
    }
    if (!authorized && unauthorizedAs === 'http') {
      response.status = 401
      throw new AxiosError('expired', AxiosError.ERR_BAD_REQUEST, config, undefined, response)
    }
    return response
  }

  return { adapter, attempts: () => attempts }
}

describe('HTTP 会话恢复', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mocks.fetchRefreshSession.mockReset()
    mocks.fetchRefreshSession.mockResolvedValue({ token: 'refreshed-access-token' })
  })

  it('页面恢复期间的并发刷新共享同一个请求', async () => {
    const userStore = useUserStore()

    const first = userStore.refreshSession()
    const second = userStore.refreshSession()

    await expect(Promise.all([first, second])).resolves.toEqual([
      'refreshed-access-token',
      'refreshed-access-token'
    ])
    expect(mocks.fetchRefreshSession).toHaveBeenCalledTimes(1)
    expect(userStore.accessToken).toBe('refreshed-access-token')
  })

  it('并发 HTTP 与业务信封 401 只刷新一次并分别重试一次', async () => {
    const userStore = useUserStore()
    userStore.setToken('expired-access-token')
    const first = createProtectedAdapter('http')
    const second = createProtectedAdapter('envelope')

    await expect(
      Promise.all([
        request.get<string>({ url: '/api/protected/first', adapter: first.adapter }),
        request.get<string>({ url: '/api/protected/second', adapter: second.adapter })
      ])
    ).resolves.toEqual(['/api/protected/first', '/api/protected/second'])

    expect(mocks.fetchRefreshSession).toHaveBeenCalledTimes(1)
    expect(first.attempts()).toBe(2)
    expect(second.attempts()).toBe(2)
  })
})
