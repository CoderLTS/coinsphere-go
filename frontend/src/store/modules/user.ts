/** 用户会话状态；访问令牌和锁屏密码只保存在当前页面内存中。 */
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { LanguageEnum } from '@/enums/appEnum'
import { router } from '@/router'
import { useSettingStore } from './setting'
import { useWorktabStore } from './worktab'
import { AppRouteRecord } from '@/types/router'
import { setPageTitle } from '@/utils/router'
import { resetRouterState } from '@/router/guards/beforeEach'
import { useMenuStore } from './menu'
import { StorageConfig } from '@/utils/storage/storage-config'

export const useUserStore = defineStore(
  'userStore',
  () => {
    const createGuestUserInfo = (): Api.Auth.UserInfo => ({
      permissions: [],
      roleCodes: ['R_GUEST'],
      userId: 0,
      username: '游客',
      email: '',
      avatar: '',
      accessMode: 'guest'
    })

    // 语言设置
    const language = ref(LanguageEnum.ZH)
    // 登录状态
    const isLogin = ref(false)
    const accessMode = ref<Api.Auth.UserInfo['accessMode']>('guest')
    // 锁屏状态
    const isLock = ref(false)
    // 锁屏密码
    const lockPassword = ref('')
    // 用户信息
    const info = ref<Api.Auth.UserInfo>(createGuestUserInfo())
    // 搜索历史记录
    const searchHistory = ref<AppRouteRecord[]>([])
    // 访问令牌
    const accessToken = ref('')
    let refreshPromise: Promise<string> | null = null

    // 计算属性：获取用户信息
    const getUserInfo = computed(() => info.value)
    const isGuest = computed(() => accessMode.value === 'guest')
    // 计算属性：获取设置状态
    const getSettingState = computed(() => useSettingStore().$state)
    // 计算属性：获取工作台状态
    const getWorktabState = computed(() => useWorktabStore().$state)

    /**
     * 设置用户信息
     * @param newInfo 新的用户信息
     */
    const setUserInfo = (newInfo: Api.Auth.UserInfo) => {
      info.value = newInfo
      accessMode.value = newInfo.accessMode
      isLogin.value = newInfo.accessMode === 'authenticated'
    }

    /**
     * 设置登录状态
     * @param status 登录状态
     */
    const setLoginStatus = (status: boolean) => {
      isLogin.value = status
      accessMode.value = status ? 'authenticated' : 'guest'
    }

    /**
     * 设置语言
     * @param lang 语言枚举值
     */
    const setLanguage = (lang: LanguageEnum) => {
      language.value = lang
      setPageTitle(router.currentRoute.value)
    }

    /**
     * 设置搜索历史
     * @param list 搜索历史列表
     */
    const setSearchHistory = (list: AppRouteRecord[]) => {
      searchHistory.value = list
    }

    /**
     * 设置锁屏状态
     * @param status 锁屏状态
     */
    const setLockStatus = (status: boolean) => {
      isLock.value = status
    }

    /**
     * 设置锁屏密码
     * @param password 锁屏密码
     */
    const setLockPassword = (password: string) => {
      lockPassword.value = password
    }

    /** access token 只写入 Pinia 内存，refresh token 由 HttpOnly Cookie 承载。 */
    const setToken = (newAccessToken: string) => {
      accessToken.value = newAccessToken
    }

    /** 清理本地会话并回到游客首页。 */
    const clearSession = () => {
      // 保存当前用户 ID，用于下次登录时判断是否为同一用户
      const currentUserId = info.value.userId
      if (currentUserId && accessMode.value === 'authenticated') {
        localStorage.setItem(StorageConfig.LAST_USER_ID_KEY, String(currentUserId))
      }

      // 清空用户信息
      info.value = createGuestUserInfo()
      // 重置登录状态
      isLogin.value = false
      accessMode.value = 'guest'
      // 重置锁屏状态
      isLock.value = false
      // 清空锁屏密码
      lockPassword.value = ''
      // 清空访问令牌
      accessToken.value = ''
      // 移除iframe路由缓存
      useWorktabStore().clearAll()
      sessionStorage.removeItem('iframeRoutes')
      // 清空主页路径
      useMenuStore().setHomePath('')
      // 重置路由状态
      resetRouterState(0)
      // 游客模式下直接回到首页，由路由守卫重新初始化游客菜单与首页路径
      router.replace({ path: '/home' })
    }

    /** 并发恢复和 401 请求共享一次 Cookie 刷新，失败后才清理既有登录态。 */
    const refreshSession = () => {
      if (refreshPromise) return refreshPromise

      const hadAuthenticatedSession =
        accessMode.value === 'authenticated' || Boolean(accessToken.value)
      refreshPromise = import('@/api/auth')
        .then(({ fetchRefreshSession }) => fetchRefreshSession())
        .then(({ token }) => {
          if (!token) throw new Error('Refresh failed - no token received')
          accessToken.value = token
          return token
        })
        .catch((error) => {
          if (hadAuthenticatedSession) clearSession()
          throw error
        })
        .finally(() => {
          refreshPromise = null
        })

      return refreshPromise
    }

    /** 后端登出为 best-effort，本地会话立即清理。 */
    const logOut = () => {
      void import('@/api/auth').then(({ logout }) => logout()).catch(() => {})
      clearSession()
    }

    /**
     * 检查并清理工作台标签页
     * 如果不是同一用户登录，清空工作台标签页
     * 应在登录成功后调用
     */
    const checkAndClearWorktabs = () => {
      if (accessMode.value === 'guest') {
        useWorktabStore().clearAll()
        localStorage.removeItem(StorageConfig.LAST_USER_ID_KEY)
        return
      }

      const lastUserId = localStorage.getItem(StorageConfig.LAST_USER_ID_KEY)
      const currentUserId = info.value.userId

      // 无法获取当前用户 ID，跳过检查
      if (!currentUserId) return

      // 首次登录或缓存已清除，保留现有标签页
      if (!lastUserId) {
        return
      }

      // 不同用户登录，清空工作台标签页
      if (String(currentUserId) !== lastUserId) {
        const worktabStore = useWorktabStore()
        worktabStore.opened = []
        worktabStore.keepAliveExclude = []
      }

      // 清除临时存储
      localStorage.removeItem(StorageConfig.LAST_USER_ID_KEY)
    }

    return {
      language,
      isLogin,
      accessMode,
      isLock,
      lockPassword,
      info,
      searchHistory,
      accessToken,
      getUserInfo,
      isGuest,
      getSettingState,
      getWorktabState,
      setUserInfo,
      setLoginStatus,
      setLanguage,
      setSearchHistory,
      setLockStatus,
      setLockPassword,
      setToken,
      refreshSession,
      clearSession,
      logOut,
      checkAndClearWorktabs
    }
  },
  {
    persist: {
      key: 'user-preferences',
      storage: localStorage,
      pick: ['language', 'searchHistory'],
      // 旧键可能包含 accessToken、refreshToken、lockPassword，初始化前直接删除。
      beforeHydrate: () => localStorage.removeItem('user')
    }
  }
)
