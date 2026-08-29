/** 前端接口封装：config。 */
import request from '@/utils/http'

export type AiProviderType = Api.Config.AiProviderType
export type AiModelConfigItem = Api.Config.AiModelConfig
export type AiModelUpsertPayload = Api.Config.AiModelUpsertPayload
export type AiModelAgentBindingPayload = Api.Config.AiModelAgentBindingPayload
export type AiProviderMeta = Api.Config.AiProviderMeta
export type AssistantAgentItem = Api.Config.AssistantAgentItem
export type AssistantAgentUpsertPayload = Api.Config.AssistantAgentUpsertPayload
export type AssistantAgentMeta = Api.Config.AssistantAgentMeta

export interface NotifyOverviewSummary {
  channelCount: number
  enabledChannelCount: number
  latestDeliveryStatus: string
  latestDeliveryAt: string
  deliveryCount: number
}

export interface ConfigOverviewResponse {
  models: AiModelConfigItem[]
  agents: AssistantAgentItem[]
  notifySummary: NotifyOverviewSummary
}

export function fetchConfigOverview() {
  return request.get<ConfigOverviewResponse>({
    url: '/api/v1/config/overview'
  })
}

export function fetchAiModelList() {
  return request.get<AiModelConfigItem[]>({
    url: '/api/v1/config/ai-models'
  })
}

export function fetchCreateAiModel(params: AiModelUpsertPayload) {
  return request.post<{ id: number }>({
    url: '/api/v1/config/ai-models',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateAiModel(configId: number, params: AiModelUpsertPayload) {
  return request.put<{ id: number }>({
    url: `/api/v1/config/ai-models/${configId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteAiModel(configId: number) {
  return request.del<void>({
    url: `/api/v1/config/ai-models/${configId}`,
    showSuccessMessage: true
  })
}

export function fetchEnableAiModel(configId: number) {
  return request.request<void>({
    url: `/api/v1/config/ai-models/${configId}`,
    method: 'PATCH',
    data: { isEnabled: true },
    showSuccessMessage: true
  })
}

export function fetchDisableAiModel(configId: number) {
  return request.request<void>({
    url: `/api/v1/config/ai-models/${configId}`,
    method: 'PATCH',
    data: { isEnabled: false },
    showSuccessMessage: true
  })
}

export function fetchValidateAiModel(configId: number) {
  return request.post<{
    success: boolean
    status: string
    message: string
    validatedAt?: string
  }>({
    url: `/api/v1/config/ai-models/${configId}/validations`,
    showSuccessMessage: true
  })
}

export function fetchBindAiModelAgents(configId: number, params: AiModelAgentBindingPayload) {
  return request.put<void>({
    url: `/api/v1/config/ai-models/${configId}/agent-bindings`,
    params,
    showSuccessMessage: true
  })
}

export function fetchAiProviderMeta() {
  return request.get<AiProviderMeta>({
    url: '/api/v1/config/ai-models/meta'
  })
}

export function fetchAssistantAgentList() {
  return request.get<AssistantAgentItem[]>({
    url: '/api/v1/config/assistant-agents'
  })
}

export function fetchCreateAssistantAgent(params: AssistantAgentUpsertPayload) {
  return request.post<{ id: number }>({
    url: '/api/v1/config/assistant-agents',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateAssistantAgent(agentId: number, params: AssistantAgentUpsertPayload) {
  return request.put<{ id: number }>({
    url: `/api/v1/config/assistant-agents/${agentId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteAssistantAgent(agentId: number) {
  return request.del<void>({
    url: `/api/v1/config/assistant-agents/${agentId}`,
    showSuccessMessage: true
  })
}

export function fetchEnableAssistantAgent(agentId: number) {
  return request.request<void>({
    url: `/api/v1/config/assistant-agents/${agentId}`,
    method: 'PATCH',
    data: { isEnabled: true },
    showSuccessMessage: true
  })
}

export function fetchDisableAssistantAgent(agentId: number) {
  return request.request<void>({
    url: `/api/v1/config/assistant-agents/${agentId}`,
    method: 'PATCH',
    data: { isEnabled: false },
    showSuccessMessage: true
  })
}

export function fetchAssistantAgentMeta() {
  return request.get<AssistantAgentMeta>({
    url: '/api/v1/config/assistant-agents/meta'
  })
}
