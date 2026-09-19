/** 组合式函数模块：useAuth。 */
import { storeToRefs } from 'pinia'
import { useRoute } from 'vue-router'
import { useUserStore } from '@/store/modules/user'
import type { AppRouteRecord } from '@/types/router'

type ActionItem = NonNullable<AppRouteRecord['meta']['actionList']>[number]

const userStore = useUserStore()

export const useAuth = () => {
  const route = useRoute()
  const { info } = storeToRefs(userStore)

  const frontendPermissions = info.value?.permissions ?? []
  const backendActionList: ActionItem[] = Array.isArray(route.meta.actionList)
    ? (route.meta.actionList as ActionItem[])
    : []

  const hasAuth = (permissionCode: string): boolean => {
    if (permissionCode === 'R_SUPER') return info.value.roleCodes.includes('R_SUPER')
    return (
      info.value.roleCodes.includes('R_SUPER') ||
      frontendPermissions.includes(permissionCode) ||
      backendActionList.some((item) => item?.permissionCode === permissionCode)
    )
  }

  return {
    hasAuth
  }
}
