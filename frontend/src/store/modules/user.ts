/** 用户会话状态；普通会话保存在当前标签页，30 天会话才跨刷新持久化。 */
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
    const storedAccessToken =
      sessionStorage.getItem(StorageConfig.ACCESS_TOKEN_KEY) ||
      localStorage.getItem(StorageConfig.REMEMBERED_ACCESS_TOKEN_KEY) ||
      ''
    // 登录状态
    const isLogin = ref(Boolean(storedAccessToken))
    const accessMode = ref<Api.Auth.UserInfo['accessMode']>(
      storedAccessToken ? 'authenticated' : 'guest'
    )
    // 锁屏状态
    const isLock = ref(false)
    // 锁屏密码
    const lockPassword = ref('')
    // 用户信息
    const info = ref<Api.Auth.UserInfo>(createGuestUserInfo())
    // 搜索历史记录
    const searchHistory = ref<AppRouteRecord[]>([])
    // 访问令牌
    const accessToken = ref(storedAccessToken)

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

    const setToken = (newAccessToken: string, keepLoggedIn = false) => {
      accessToken.value = newAccessToken
      sessionStorage.removeItem(StorageConfig.ACCESS_TOKEN_KEY)
      localStorage.removeItem(StorageConfig.REMEMBERED_ACCESS_TOKEN_KEY)
      const storage = keepLoggedIn ? localStorage : sessionStorage
      const key = keepLoggedIn
        ? StorageConfig.REMEMBERED_ACCESS_TOKEN_KEY
        : StorageConfig.ACCESS_TOKEN_KEY
      storage.setItem(key, newAccessToken)
    }

    /** 清理本地会话并回到登录页。 */
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
      sessionStorage.removeItem(StorageConfig.ACCESS_TOKEN_KEY)
      localStorage.removeItem(StorageConfig.REMEMBERED_ACCESS_TOKEN_KEY)
      // 移除iframe路由缓存
      useWorktabStore().clearAll()
      sessionStorage.removeItem('iframeRoutes')
      // 清空主页路径
      useMenuStore().setHomePath('')
      // 重置路由状态
      resetRouterState(0)
      router.replace({ name: 'Login' })
    }

    /** 后端登出为 best-effort，本地会话立即清理。 */
    const logOut = () => {
      const token = accessToken.value
      if (token) {
        void import('@/api/auth').then(({ logout }) => logout(token)).catch(() => {})
      }
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
      clearSession,
      logOut,
      checkAndClearWorktabs
    }
  },
  {
    persist: {
      key: 'user-preferences-v2',
      storage: localStorage,
      pick: ['language', 'searchHistory'],
      // 清理旧版本可能持久化的会话 token，避免恢复已废弃的登录状态。
      beforeHydrate: () => {
        localStorage.removeItem('user')
        localStorage.removeItem('user-preferences')
      }
    }
  }
)
