/** 组合式函数模块：useAuth。 */
import { storeToRefs } from 'pinia'
import { useRoute } from 'vue-router'
import { useAppMode } from '@/hooks/core/useAppMode'
import { useUserStore } from '@/store/modules/user'
import type { AppRouteRecord } from '@/types/router'

type ActionItem = NonNullable<AppRouteRecord['meta']['actionList']>[number]

const userStore = useUserStore()

export const useAuth = () => {
  const route = useRoute()
  const { isFrontendMode } = useAppMode()
  const { info } = storeToRefs(userStore)

  const frontendPermissions = info.value?.permissions ?? []
  const backendActionList: ActionItem[] = Array.isArray(route.meta.actionList)
    ? (route.meta.actionList as ActionItem[])
    : []

  const hasAuth = (permissionCode: string): boolean => {
    if (isFrontendMode.value) {
      return frontendPermissions.includes(permissionCode)
    }

    return backendActionList.some((item) => item?.permissionCode === permissionCode)
  }

  return {
    hasAuth
  }
}
