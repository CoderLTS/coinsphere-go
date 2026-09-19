/** 自定义指令模块：index。 */
import type { App } from 'vue'
import { setupRippleDirective, type RippleDirective } from './business/ripple'

export function setupGlobDirectives(app: App) {
  setupRippleDirective(app) // 水波纹指令
}

export type { RippleDirective }
