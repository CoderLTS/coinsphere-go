/** 前端插件模块：index。 */
/**
 * 插件统一导出
 * 集中管理第三方库的封装和配置
 */

export * from './echarts'
export * from './registry.generated'
export * from './official'

import { officialFrontendPlugins } from './official'
import { frontendPlugins } from './registry.generated'

export const registeredFrontendPlugins = [...officialFrontendPlugins, ...frontendPlugins]
