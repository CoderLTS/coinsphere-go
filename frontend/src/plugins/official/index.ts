import type { FrontendPluginRegistration } from '../registry.generated'

export const officialFrontendPlugins: readonly FrontendPluginRegistration[] = [
  { id: 'official.ai', version: '1.0.0', load: () => import('./ai') },
  { id: 'official.connector', version: '1.0.0', load: () => import('./connector') }
]
