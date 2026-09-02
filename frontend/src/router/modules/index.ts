/** 静态路由模块：index。 */
import { AppRouteRecord } from '@/types/router'
import { systemRoutes } from './system'
import { configRoutes } from './config'

/**
 * 导出所有模块化路由
 */
export const routeModules: AppRouteRecord[] = [systemRoutes, configRoutes]
