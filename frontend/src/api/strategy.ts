/** 策略草稿、发布版本与创建接口。 */
import request from '@/utils/http'

export interface StrategyDraftItem {
  id: string
  name: string
  sourceCode: string
  market: 'spot' | 'usd_m'
  instrumentId: string
  interval: string
  lookbackBars: number
  parameterSchema: Record<string, unknown>
  runtimeVersion: string
  createdAt: string
  updatedAt: string
}

export interface StrategyVersionItem {
  id: string
  strategyId: string
  versionNumber: number
  status: string
  name: string
  sourceCode: string
  codeSha256: string
  runtimeVersion: string
  market: 'spot' | 'usd_m'
  instrumentId: string
  symbol: string
  interval: string
  lookbackBars: number
  parameterSchema: Record<string, unknown>
  publishedAt?: string
  createdAt: string
}

export interface StrategyDraftPayload {
  name: string
  sourceCode: string
  market: 'spot' | 'usd_m'
  instrumentId: string
  interval: string
  lookbackBars: number
  parameterSchema: Record<string, unknown>
}

export function parseStrategyParameterSchema(value: string): Record<string, unknown> | null {
  try {
    const parsed: unknown = JSON.parse(value)
    return parsed && !Array.isArray(parsed) && typeof parsed === 'object'
      ? (parsed as Record<string, unknown>)
      : null
  } catch {
    return null
  }
}

export function fetchStrategyDrafts(params: { cursor?: string; limit?: number } = {}) {
  return request.get<Api.Common.PaginatedResponse<StrategyDraftItem>>({
    url: '/api/v1/admin/strategies',
    params
  })
}

export function fetchPublishedStrategies(params: { cursor?: string; limit?: number } = {}) {
  return request.get<Api.Common.PaginatedResponse<StrategyVersionItem>>({
    url: '/api/v1/strategies',
    params
  })
}

export function fetchCreateStrategyDraft(params: StrategyDraftPayload) {
  return request.post<StrategyDraftItem>({
    url: '/api/v1/admin/strategies',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateStrategyDraft(strategyId: string, params: StrategyDraftPayload) {
  return request.put<StrategyDraftItem>({
    url: `/api/v1/admin/strategies/${encodeURIComponent(strategyId)}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchPublishStrategy(strategyId: string) {
  return request.post<StrategyVersionItem>({
    url: `/api/v1/admin/strategies/${encodeURIComponent(strategyId)}/publish`,
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    showSuccessMessage: true
  })
}
