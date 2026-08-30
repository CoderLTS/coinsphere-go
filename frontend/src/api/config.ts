/** 前端接口封装：config。 */
import request from '@/utils/http'

export type AiModelConfigItem = Api.Config.AiModelConfig
export type AiModelUpsertPayload = Api.Config.AiModelUpsertPayload

export function fetchAiModelList() {
  return request.get<AiModelConfigItem[]>({
    url: '/api/v1/config/ai-models'
  })
}

export function fetchCreateAiModel(params: AiModelUpsertPayload) {
  return request.post<AiModelConfigItem>({
    url: '/api/v1/config/ai-models',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateAiModel(configId: number, params: AiModelUpsertPayload) {
  return request.put<AiModelConfigItem>({
    url: `/api/v1/config/ai-models/${configId}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteAiModel(configId: number) {
  return request.del<{ id: number }>({
    url: `/api/v1/config/ai-models/${configId}`,
    showSuccessMessage: true
  })
}

export function fetchEnableAiModel(configId: number) {
  return request.request<AiModelConfigItem>({
    url: `/api/v1/config/ai-models/${configId}`,
    method: 'PATCH',
    data: { isEnabled: true },
    showSuccessMessage: true
  })
}

export function fetchDisableAiModel(configId: number) {
  return request.request<AiModelConfigItem>({
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
    showSuccessMessage: false
  })
}
