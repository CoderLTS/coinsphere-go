/** 前端类型定义：directive.d。 */
import type { RippleDirective } from '@/directives'

declare module 'vue' {
  export interface GlobalDirectives {
    vRipple: RippleDirective
  }
}
