/** 前端工具模块：mittBus。 */
import mitt, { type Emitter } from 'mitt'

export interface OpenChatPayload {
  agentCode: Api.Assistant.AgentCode
  newsId?: number
  newsTitle?: string
  autoRun?: boolean
  modelConfigId?: number
}

type Events = {
  triggerFireworks: string | undefined
  openSetting: void
  openSearchDialog: void
  openChat: OpenChatPayload
  openLockScreen: void
}

const mittBus: Emitter<Events> = mitt<Events>()

export default mittBus
