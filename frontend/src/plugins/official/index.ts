import type { FrontendPluginRegistration } from '../registry.generated'

export const officialFrontendPlugins: readonly FrontendPluginRegistration[] = [
  { id: 'official.ai', version: '3.0.0', load: () => import('./ai') },
  { id: 'official.connector', version: '3.0.0', load: () => import('./connector') },
  { id: 'official.quant', version: '4.0.0', load: () => import('./quant') },
  { id: 'official.binance', version: '4.0.0', load: () => import('./binance') }
]
