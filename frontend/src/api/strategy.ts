/** 策略草稿、发布版本与创建接口。 */
import request from '@/utils/http'

export interface StrategyDraftItem {
  id: string
  name: string
  sourceCode: string
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
  lookbackBars: number
  parameterSchema: Record<string, unknown>
  publishedAt?: string
  createdAt: string
}

export interface StrategyDraftPayload {
  name: string
  sourceCode: string
  lookbackBars: number
  parameterSchema: Record<string, unknown>
}

export interface StrategyBacktestItem {
  id: string
  strategyVersionId: string
  instrumentId: string
  market: string
  symbol: string
  interval: string
  status: string
  startTime: string
  endTime: string
  createdAt: string
  summary?: Record<string, unknown> | null
}

export interface StrategyBacktestPayload {
  strategyVersionId: string
  instrumentId: string
  interval: string
  parameters: Record<string, unknown>
  startTime: string
  endTime: string
  allocationUsdt: string
  initialEquity: string
  feeRate: string
  slippageRate: string
  fundingRates: string[]
  stopLossRatio: string
  maintenanceMarginRatio: string
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

export function fetchStrategyDraft(strategyId: string) {
  return request.get<StrategyDraftItem>({
    url: `/api/v1/admin/strategies/${encodeURIComponent(strategyId)}`
  })
}

export function fetchPublishedStrategy(strategyVersionId: string) {
  return request.get<StrategyVersionItem>({
    url: `/api/v1/strategies/${encodeURIComponent(strategyVersionId)}`
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

export function fetchStrategyBacktests(params: { cursor?: string; limit?: number } = {}) {
  return request.get<Api.Common.PaginatedResponse<StrategyBacktestItem>>({
    url: '/api/v1/backtests',
    params
  })
}

export function fetchCreateStrategyBacktest(params: StrategyBacktestPayload) {
  return request.post<StrategyBacktestItem>({
    url: '/api/v1/backtests',
    params,
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    showSuccessMessage: true
  })
}
