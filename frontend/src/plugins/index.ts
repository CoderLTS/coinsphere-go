import type { Component } from 'vue'
import type { FrontendPluginModule } from './sdk'
import { officialFrontendPlugins } from './official'
import { frontendPlugins } from './registry.generated'
export * from './echarts'
export * from './registry.generated'
export * from './official'

export const registeredFrontendPlugins = [...officialFrontendPlugins, ...frontendPlugins]
const registrations = new Map(registeredFrontendPlugins.map((plugin) => [plugin.id, plugin]))
if (registrations.size !== registeredFrontendPlugins.length)
  throw new Error('duplicate frontend plugin ID')
const moduleCache = new Map<string, Promise<FrontendPluginModule>>()
type Contribution = keyof FrontendPluginModule
export async function loadPluginComponent(
  pluginID: string,
  contribution: Contribution,
  key: string
): Promise<Component> {
  const registration = registrations.get(pluginID)
  if (!registration) throw new Error(`插件 ${pluginID} 的前端组件未编译`)
  let pending = moduleCache.get(pluginID)
  if (!pending) {
    pending = registration.load().catch((error) => {
      moduleCache.delete(pluginID)
      throw error
    })
    moduleCache.set(pluginID, pending)
  }
  const module = await pending
  const loader = module[contribution]?.[key]
  if (!loader) throw new Error(`插件 ${pluginID} 缺少组件 ${contribution}/${key}`)
  return (await loader()).default
}
export const loadPluginNodeEditor = (type: string, pluginID: string) =>
  loadPluginComponent(pluginID, 'nodeEditors', type)
export const loadPluginNodeRenderer = (type: string, pluginID: string) =>
  loadPluginComponent(pluginID, 'nodeRenderers', type)
export const loadProviderConfigComponent = (
  providerID: string,
  pluginID = `official.${providerID}`
) => loadPluginComponent(pluginID, 'providerConfigComponents', providerID)
