/** 静态路由模块：index。 */
import { AppRouteRecord } from '@/types/router'
import { dashboardRoutes } from './dashboard'
import { systemRoutes } from './system'
import { resultRoutes } from './result'
import { exceptionRoutes } from './exception'
import { tradingRoutes } from './trading'
import { configRoutes } from './config'

/**
 * 导出所有模块化路由
 */
export const routeModules: AppRouteRecord[] = [
  dashboardRoutes,
  tradingRoutes,
  systemRoutes,
  configRoutes,
  resultRoutes,
  exceptionRoutes
]
