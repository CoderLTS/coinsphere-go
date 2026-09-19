/** 前端工具模块：jump。 */
/**
 * 导航跳转工具模块
 *
 * 提供统一的页面跳转和导航功能
 *
 * ## 主要功能
 *
 * - 菜单项内部路由跳转
 * - 递归查找并跳转到第一个可见的子菜单
 * - 统一处理内部路由跳转
 *
 * @module utils/navigation/jump
 * @author Art Design Pro Team
 */
import { AppRouteRecord } from '@/types/router'
import { router } from '@/router'
import { isNavigableMenuItem } from './route'

/**
 * 菜单跳转
 * @param item 菜单项
 * @param jumpToFirst 是否跳转到第一个子菜单
 * @returns
 */
export const handleMenuJump = (item: AppRouteRecord, jumpToFirst: boolean = false) => {
  // 如果不需要跳转到第一个子菜单，或者没有子菜单，直接跳转当前路径
  if (!jumpToFirst || !item.children?.length) {
    return router.push(item.path)
  }

  // 递归查找第一个可导航的叶子节点菜单
  const findFirstLeafMenu = (items: AppRouteRecord[]): AppRouteRecord | undefined => {
    for (const child of items) {
      if (isNavigableMenuItem(child)) {
        return child.children?.length ? findFirstLeafMenu(child.children) || child : child
      }
    }
    return undefined
  }

  const firstChild = findFirstLeafMenu(item.children)

  // 如果子菜单都不可见，则回退到父级页面自身。
  if (!firstChild) {
    return router.push(item.path)
  }

  // 跳转到子菜单路径
  router.push(firstChild.path)
}
