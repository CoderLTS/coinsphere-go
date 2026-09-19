/** 动态路由核心模块：RoutePermissionValidator。 */
/**
 * 路由权限验证模块
 *
 * 提供路由权限验证和路径检查功能
 *
 * ## 主要功能
 *
 * - 验证路径是否在用户菜单权限中
 * - 支持动态路由参数匹配
 *
 * ## 使用场景
 *
 * - 路由守卫中验证用户权限
 * - 动态路由注册后的权限检查
 * - 防止用户访问无权限的页面
 *
 * @module router/core/RoutePermissionValidator
 * @author Art Design Pro Team
 */

import type { AppRouteRecord } from '@/types/router'

/**
 * 路由权限验证器
 */
export class RoutePermissionValidator {
  /**
   * 验证路径是否在用户菜单权限中
   * @param targetPath 目标路径
   * @param menuList 菜单列表
   * @returns 是否有权限访问
   */
  static hasPermission(targetPath: string, menuList: AppRouteRecord[]): boolean {
    return this.matchRoute(targetPath, menuList)
  }

  /**
   * 递归匹配路由配置，支持隐藏路由和动态参数路由
   */
  static matchRoute(targetPath: string, routes: AppRouteRecord[]): boolean {
    if (!Array.isArray(routes) || routes.length === 0) {
      return false
    }

    for (const route of routes) {
      if (!route.path) {
        continue
      }

      const routePath = route.path.startsWith('/') ? route.path : `/${route.path}`

      if (routePath === targetPath || this.isDynamicRouteMatch(targetPath, routePath)) {
        return true
      }

      if (route.children?.length && this.matchRoute(targetPath, route.children)) {
        return true
      }
    }

    return false
  }

  /**
   * 检查目标路径是否匹配动态参数路由，如 /demo/123 匹配 /demo/:id
   */
  static isDynamicRouteMatch(targetPath: string, routePath: string): boolean {
    if (!routePath.includes(':')) {
      return false
    }

    const pattern = routePath
      .replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
      .replace(/:([^/]+)/g, '[^/]+')
      .replace(/\\\*/g, '.*')

    return new RegExp(`^${pattern}$`).test(targetPath)
  }

  /**
   * 验证并返回有效的路径
   * 如果目标路径无权限，返回首页路径
   * @param targetPath 目标路径
   * @param menuList 菜单列表
   * @param homePath 首页路径
   * @returns 验证后的路径
   */
  static validatePath(
    targetPath: string,
    menuList: AppRouteRecord[],
    homePath: string = '/'
  ): { path: string; hasPermission: boolean } {
    const hasPermission = this.hasPermission(targetPath, menuList)

    if (hasPermission) {
      return { path: targetPath, hasPermission: true }
    }

    return { path: homePath, hasPermission: false }
  }
}
