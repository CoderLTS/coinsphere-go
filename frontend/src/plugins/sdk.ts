import type { Component } from 'vue'

export interface FrontendPluginModule {
  readonly resultPages: Readonly<Record<string, () => Promise<{ default: Component }>>>
}
