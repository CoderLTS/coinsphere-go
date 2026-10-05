import request from '@/utils/http'
export interface ResultPageContribution {
  pageKey: string
  title: string
  componentEntry: string
  configComponentEntry?: string
  scopeSchema: Record<string, unknown>
  filterSchema: Record<string, unknown>
  actions: string[]
  actionPermissions: Record<string, string>
  permissionCode: string
}
export interface RunPanelContribution {
  panelKey: string
  title: string
  nodeTypes: string[]
  componentEntry: string
}
export interface PluginCatalogItem {
  id: string
  version: string
  resultPages: ResultPageContribution[]
  runPanels: RunPanelContribution[]
}
export const fetchPluginCatalog = () =>
  request.get<{ items: PluginCatalogItem[] }>({ url: '/api/v1/plugins/catalog' })
