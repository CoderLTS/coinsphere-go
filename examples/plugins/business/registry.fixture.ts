// CI compiles the business example into the normal frontend registry.
import type { FrontendPluginModule } from './sdk'
export interface FrontendPluginRegistration {
  readonly id: string
  readonly version: string
  readonly load: () => Promise<FrontendPluginModule>
}
export const frontendPlugins: readonly FrontendPluginRegistration[] = [
  {
    id: 'example.business',
    version: '1.0.0',
    load: () => import('./installed/example_business/frontend/index')
  }
]
