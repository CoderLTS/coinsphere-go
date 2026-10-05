import type { FrontendPluginRegistration } from '../registry.generated'
import catalog from '../../../../backend/version/builtin.json'
const loaders: Record<string, FrontendPluginRegistration['load']> = {
  'official.ai': () => import('./ai'),
  'official.connector': () => import('./connector'),
  'official.quant': () => import('./quant'),
  'official.binance': () => import('./binance')
}
export const officialFrontendPlugins: readonly FrontendPluginRegistration[] = catalog
  .filter((plugin) => loaders[plugin.id])
  .map((plugin) => ({ id: plugin.id, version: plugin.version, load: loaders[plugin.id] }))
