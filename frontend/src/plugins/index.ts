/** 前端插件模块：index。 */
import type { Component } from 'vue'
import type { FrontendPluginModule } from './sdk'
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

const moduleCache = new Map<string, Promise<FrontendPluginModule>>()

const loadPluginComponent = async (
  contribution: 'nodeEditors' | 'nodeRenderers' | 'providerConfigComponents',
  key: string
): Promise<Component | undefined> => {
  for (const plugin of registeredFrontendPlugins) {
    const module = await (moduleCache.get(plugin.id) ||
      (() => {
        const pending = plugin.load()
        moduleCache.set(plugin.id, pending)
        return pending
      })())
    const loader = module[contribution]?.[key]
    if (loader) return (await loader()).default
  }
  return undefined
}

export const loadPluginNodeEditor = (typeCode: string) =>
  loadPluginComponent('nodeEditors', typeCode)

export const loadPluginNodeRenderer = (typeCode: string) =>
  loadPluginComponent('nodeRenderers', typeCode)

export const loadProviderConfigComponent = (providerID: string) =>
  loadPluginComponent('providerConfigComponents', providerID)
