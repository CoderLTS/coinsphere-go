/** 前端工具模块：mittBus。 */
import mitt, { type Emitter } from 'mitt'

type Events = {
  triggerFireworks: string | undefined
  openSetting: void
  openSearchDialog: void
  openAssistant: void
  openLockScreen: void
}

const mittBus: Emitter<Events> = mitt<Events>()

export default mittBus
