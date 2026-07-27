/** 自定义指令模块：auth。 */
import { App, Directive, DirectiveBinding } from 'vue'
import { router } from '@/router'

export type AuthDirective = Directive<HTMLElement, string>

function removeElement(el: HTMLElement): void {
  if (el.parentNode) {
    el.parentNode.removeChild(el)
  }
}

function checkAuthPermission(el: HTMLElement, binding: DirectiveBinding<string>): void {
  const actionList =
    (router.currentRoute.value.meta.actionList as Array<{ permissionCode: string }>) || []

  if (!actionList.some((item) => item.permissionCode === binding.value)) {
    removeElement(el)
  }
}

const authDirective: AuthDirective = {
  mounted: checkAuthPermission,
  updated: checkAuthPermission
}

export function setupAuthDirective(app: App): void {
  app.directive('auth', authDirective)
}
