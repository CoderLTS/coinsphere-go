/** 静态路由模块：system。 */
import { AppRouteRecord } from '@/types/router'

export const systemRoutes: AppRouteRecord = {
  path: '/profile',
  name: 'UserCenter',
  component: '/system/user-center',
  meta: {
    title: 'menus.system.userCenter',
    isHide: true,
    keepAlive: true,
    isHideTab: true
  }
}
