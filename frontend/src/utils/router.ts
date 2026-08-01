/** 前端工具模块：router。 */
import { RouteLocationNormalized, RouteRecordRaw } from 'vue-router'
import AppConfig from '@/config'
import NProgress from 'nprogress'
import 'nprogress/nprogress.css'
import i18n, { $t } from '@/locales'

export type AppRouteRecordRaw = RouteRecordRaw & {
  hidden?: boolean
}

export const configureNProgress = () => {
  NProgress.configure({
    easing: 'ease',
    speed: 600,
    showSpinner: false,
    parent: 'body'
  })
}

export const setPageTitle = (to: RouteLocationNormalized): void => {
  const { title } = to.meta
  if (!title) {
    return
  }

  setTimeout(() => {
    document.title = `${formatMenuTitle(String(title))} - ${AppConfig.systemInfo.name}`
  }, 150)
}

export const formatMenuTitle = (title: string): string => {
  if (!title) {
    return ''
  }

  // Access locale during render so callers react immediately to language switches.
  const currentLocale = i18n.global.locale
  if (typeof currentLocale !== 'string') {
    void currentLocale.value
  }

  if (i18n.global.te(title)) {
    return $t(title)
  }

  return title
}
