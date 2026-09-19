/** 动态路由核心模块：MenuProcessor。 */
import type { AppRouteRecord } from '@/types/router'
import { fetchGetMenuList } from '@/api/system'
import { RoutesAlias } from '../routesAlias'
import { formatMenuTitle } from '@/utils'

export class MenuProcessor {
  async getMenuList(): Promise<AppRouteRecord[]> {
    const menuList = await this.processBackendMenu()

    this.validateMenuPaths(menuList)
    return this.normalizeMenuPaths(menuList)
  }

  private async processBackendMenu(): Promise<AppRouteRecord[]> {
    const list = await fetchGetMenuList()
    return this.filterEmptyMenus(this.applyBackendI18nKeys(list))
  }

  private applyBackendI18nKeys(menuList: AppRouteRecord[]): AppRouteRecord[] {
    return menuList.map((item) => {
      const actionList = item.meta?.actionList?.map((action) => ({
        ...action,
        title: action.i18nKey || action.title
      }))

      return {
        ...item,
        meta: {
          ...item.meta,
          title: item.meta?.i18nKey || item.meta?.title || '',
          actionList
        },
        children: item.children?.length ? this.applyBackendI18nKeys(item.children) : item.children
      }
    })
  }

  private filterEmptyMenus(menuList: AppRouteRecord[]): AppRouteRecord[] {
    return menuList
      .map((item) => {
        if (item.children?.length) {
          return {
            ...item,
            children: this.filterEmptyMenus(item.children)
          }
        }
        return item
      })
      .filter((item) => {
        if ('children' in item) {
          return true
        }

        return Boolean(
          item.component && item.component !== '' && item.component !== RoutesAlias.Layout
        )
      })
  }

  validateMenuList(menuList: AppRouteRecord[]): boolean {
    return Array.isArray(menuList) && menuList.length > 0
  }

  private normalizeMenuPaths(menuList: AppRouteRecord[], parentPath = ''): AppRouteRecord[] {
    return menuList.map((item) => {
      const fullPath = this.buildFullPath(item.path || '', parentPath)
      const children = item.children?.length
        ? this.normalizeMenuPaths(item.children, fullPath)
        : item.children
      const redirect = item.redirect || this.resolveDefaultRedirect(children)

      return {
        ...item,
        path: fullPath,
        redirect,
        children
      }
    })
  }

  private resolveDefaultRedirect(children?: AppRouteRecord[]): string | undefined {
    if (!children?.length) {
      return undefined
    }

    for (const child of children) {
      if (this.isNavigableRoute(child)) {
        return child.path
      }

      const nestedRedirect = this.resolveDefaultRedirect(child.children)
      if (nestedRedirect) {
        return nestedRedirect
      }
    }

    return undefined
  }

  private isNavigableRoute(route: AppRouteRecord): boolean {
    return Boolean(route.path && route.path !== '/' && route.component && route.component !== '')
  }

  private validateMenuPaths(menuList: AppRouteRecord[], level = 1): void {
    menuList.forEach((route) => {
      if (!route.children?.length) return

      const parentName = String(route.name || route.path || 'unknown')

      route.children.forEach((child) => {
        const childPath = child.path || ''

        if (this.isValidAbsolutePath(childPath)) return

        if (childPath.startsWith('/')) {
          this.logPathError(child, childPath, parentName, level)
        }
      })

      this.validateMenuPaths(route.children, level + 1)
    })
  }

  private isValidAbsolutePath(path: string): boolean {
    return path.startsWith('http://') || path.startsWith('https://')
  }

  private logPathError(
    route: AppRouteRecord,
    path: string,
    parentName: string,
    level: number
  ): void {
    const routeName = String(route.name || path || 'unknown')
    const menuTitle = route.meta?.title || routeName
    const suggestedPath = path.split('/').pop() || path.slice(1)

    console.error(
      `[路由配置错误] 菜单 "${formatMenuTitle(menuTitle)}" (name: ${routeName}, path: ${path}) 配置错误\n` +
        `  位置: ${parentName} > ${routeName}\n` +
        `  问题: ${level + 1} 级菜单的 path 不能以 / 开头\n` +
        `  当前配置: path: '${path}'\n` +
        `  应改为: path: '${suggestedPath}'`
    )
  }

  private buildFullPath(path: string, parentPath: string): string {
    if (!path) return ''

    if (path.startsWith('/')) {
      return path
    }

    if (parentPath) {
      const cleanParent = parentPath.replace(/\/$/, '')
      const cleanChild = path.replace(/^\//, '')
      return `${cleanParent}/${cleanChild}`
    }

    return `/${path}`
  }
}
