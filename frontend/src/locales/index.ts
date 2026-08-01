/** 国际化模块：index。 */
import { createI18n } from 'vue-i18n'
import type { I18n, I18nOptions } from 'vue-i18n'
import { LanguageEnum } from '@/enums/appEnum'

import enMessages from './langs/en.json'
import zhMessages from './langs/zh.json'

const messages = {
  [LanguageEnum.EN]: enMessages,
  [LanguageEnum.ZH]: zhMessages
}

export const languageOptions = [
  { value: LanguageEnum.ZH, label: '简体中文' },
  { value: LanguageEnum.EN, label: 'English' }
]

const getDefaultLanguage = (): LanguageEnum => {
  try {
    const userStore = localStorage.getItem('user')
    if (userStore) {
      const { language } = JSON.parse(userStore)
      if (language && Object.values(LanguageEnum).includes(language)) {
        return language
      }
    }
  } catch (error) {
    console.warn('[i18n] 获取语言设置失败:', error)
  }

  return LanguageEnum.ZH
}

const i18nOptions: I18nOptions = {
  locale: getDefaultLanguage(),
  legacy: false,
  globalInjection: true,
  fallbackLocale: LanguageEnum.ZH,
  messages
}

const i18n: I18n = createI18n(i18nOptions)

interface Translation {
  (key: string): string
}

export const $t = i18n.global.t as Translation

function buildNestedMessage(messageMap: Record<string, string>): Record<string, any> {
  const target: Record<string, any> = {}
  Object.entries(messageMap).forEach(([fullKey, text]) => {
    const segments = fullKey.split('.').filter(Boolean)
    if (!segments.length) {
      return
    }
    let cursor: Record<string, any> = target
    segments.forEach((segment, index) => {
      if (index === segments.length - 1) {
        cursor[segment] = text
        return
      }
      if (!cursor[segment] || typeof cursor[segment] !== 'object') {
        cursor[segment] = {}
      }
      cursor = cursor[segment] as Record<string, any>
    })
  })
  return target
}

export function mergeRuntimeMenuI18nDict(dict: Api.System.MenuI18nDict): void {
  const zh = buildNestedMessage(dict.zh || {})
  const en = buildNestedMessage(dict.en || {})
  i18n.global.mergeLocaleMessage(LanguageEnum.ZH, zh)
  i18n.global.mergeLocaleMessage(LanguageEnum.EN, en)
}

export default i18n
