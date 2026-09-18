import type { Component } from 'vue'

export type FrontendComponentLoader = () => Promise<{ default: Component }>

export interface FrontendPluginModule {
  /** Profile types and their plugin-owned management page capability. */
  readonly profileTypes?: readonly string[]
  readonly pages?: Readonly<Record<string, FrontendComponentLoader>>
  readonly resultPages?: Readonly<Record<string, FrontendComponentLoader>>
  readonly nodeEditors?: Readonly<Record<string, FrontendComponentLoader>>
  readonly nodeRenderers?: Readonly<Record<string, FrontendComponentLoader>>
  readonly providerConfigComponents?: Readonly<Record<string, FrontendComponentLoader>>
}
