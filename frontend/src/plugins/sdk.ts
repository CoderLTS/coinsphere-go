import type { Component } from 'vue'

export interface FrontendPluginModule {
  readonly pages?: Readonly<Record<string, () => Promise<{ default: Component }>>>
  readonly resultPages?: Readonly<Record<string, () => Promise<{ default: Component }>>>
}
