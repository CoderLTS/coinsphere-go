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
    logout: vi.fn(),
    routerReplace: vi.fn(),
    resetRouterState: vi.fn(),
    clearWorktabs: vi.fn(),
    setHomePath: vi.fn()
  }
})

vi.mock('@/api/auth', () => ({
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
      response?: { status?: number; data?: { detail?: string } }
    }) => {
      throw new HttpError(
        error.response?.data?.detail || error.message,
        error.response?.status || 400
      )
    },
    showError: vi.fn(),
    showSuccess: vi.fn()
  }
})

import { useUserStore } from '@/store/modules/user'
import request from './index'

function createProtectedAdapter() {
  let attempts = 0
  const adapter: AxiosAdapter = async (config) => {
    attempts += 1
    const response = {
      data: {
        type: 'about:blank',
        title: 'Unauthorized',
        status: 401,
        detail: 'expired',
        requestId: 'test-request'
      },
      status: 401,
      statusText: 'Unauthorized',
      headers: { 'content-type': 'application/problem+json' },
      config
    }
    throw new AxiosError('expired', AxiosError.ERR_BAD_REQUEST, config, undefined, response)
  }

  return { adapter, attempts: () => attempts }
}

describe('HTTP 会话边界', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mocks.logout.mockReset()
  })

  it('401 清理内存会话且不刷新或重试', async () => {
    const userStore = useUserStore()
    userStore.setToken('expired-access-token')
    userStore.setLoginStatus(true)
    const protectedRequest = createProtectedAdapter()

    await expect(
      request.get<string>({ url: '/api/v1/protected', adapter: protectedRequest.adapter })
    ).rejects.toMatchObject({ code: 401, message: 'expired' })

    expect(protectedRequest.attempts()).toBe(1)
    expect(userStore.accessToken).toBe('')
    expect(userStore.isLogin).toBe(false)
  })

  it('并发 401 不会调用登出网络接口', async () => {
    const userStore = useUserStore()
    userStore.setToken('expired-access-token')
    userStore.setLoginStatus(true)
    const first = createProtectedAdapter()
    const second = createProtectedAdapter()

    await expect(
      Promise.all([
        request.get<string>({ url: '/api/v1/protected/first', adapter: first.adapter }),
        request.get<string>({ url: '/api/v1/protected/second', adapter: second.adapter })
      ])
    ).rejects.toBeTruthy()

    expect(first.attempts()).toBe(1)
    expect(second.attempts()).toBe(1)
    expect(mocks.logout).not.toHaveBeenCalled()
  })

  it('登出在清理会话前捕获访问令牌', async () => {
    const userStore = useUserStore()
    userStore.setToken('active-access-token')
    userStore.setLoginStatus(true)

    userStore.logOut()

    expect(userStore.accessToken).toBe('')
    await vi.waitFor(() => {
      expect(mocks.logout).toHaveBeenCalledWith('active-access-token')
    })
  })
})
