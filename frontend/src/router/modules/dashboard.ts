/** 静态路由模块：dashboard。 */
import { AppRouteRecord } from '@/types/router'

export const dashboardRoutes: AppRouteRecord = {
  name: 'Dashboard',
  path: '/dashboard',
  component: '/index/index',
  meta: {
    title: 'menus.dashboard.title',
    icon: 'ri:pie-chart-line',
    roles: ['R_SUPER']
  },
  children: [
    {
      path: 'overview',
      name: 'DashboardOverview',
      component: '/dashboard/overview',
      meta: {
        title: 'menus.dashboard.overview',
        keepAlive: false,
        fixedTab: true
      }
    }
  ]
}

