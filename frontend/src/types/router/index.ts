/** 前端类型定义：index。 */
import { RouteRecordRaw } from 'vue-router'

export interface RouteMeta extends Record<string | number | symbol, unknown> {
  title: string
  icon?: string
  showBadge?: boolean
  showTextBadge?: string
  isHide?: boolean
  isHideTab?: boolean
  link?: string
  isIframe?: boolean
  keepAlive?: boolean
  actionList?: Array<{
    id?: number
    title: string
    permissionCode: string
    i18nKey?: string
    i18nTexts?: { zh: string; en: string }
    sort?: number
    roles?: string[]
    updatedAt?: string
  }>
  i18nKey?: string
  i18nTexts?: { zh: string; en: string }
  isFirstLevel?: boolean
  roles?: string[]
  fixedTab?: boolean
  activePath?: string
  isFullPage?: boolean
  isAuthButton?: boolean
  permissionCode?: string
  parentPath?: string
}

export interface AppRouteRecord extends Omit<RouteRecordRaw, 'meta' | 'children' | 'component'> {
  id?: number
  parentId?: number | null
  updatedAt?: string
  meta: RouteMeta
  children?: AppRouteRecord[]
  component?: string | (() => Promise<any>)
}
