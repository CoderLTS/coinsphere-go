/** 实时策略信号的人工决策接口。 */
import request from '@/utils/http'

export interface StrategyInstanceItem {
  id: string
  ownerUserId: number
  strategyVersionId: string
  tradingAccountId: string | null
  name: string
  mode: string
  environment: string
  allocationUsdt: string
  parameters: Record<string, unknown>
  isEnabled: boolean
  createdAt: string
  updatedAt: string
  market: string
  instrumentId: string
  symbol: string
  interval: string
  strategyName: string
  strategyVersion: number
  workflowDefinitionId: number
  workflowNodeId: string
}

export interface StrategySignalItem {
  id: string
  strategyInstanceId: string
  strategyVersionId: string
  instrumentId: string
  interval: string
  candleOpenTime: string
  candleCloseTime: string
  target: string
  previousTarget: string
  targetChange: string
  action: 'buy' | 'sell' | 'flat' | 'hold'
  mode: string
  environment: string
  status: string
  expiresAt: string | null
  decidedAt: string | null
  createdAt: string
}

export interface StrategySignalQuery {
  cursor?: string
  limit?: number
  instrumentId?: string
  strategyInstance?: string
  interval?: string
  startTime?: string
  endTime?: string
}

export function fetchStrategyInstances(params: { cursor?: string; limit?: number } = {}) {
  return request.get<Api.Common.PaginatedResponse<StrategyInstanceItem>>({
    url: '/api/v1/strategy-instances',
    params
  })
}

export function fetchStrategySignals(params: StrategySignalQuery) {
  return request.get<Api.Common.PaginatedResponse<StrategySignalItem>>({
    url: '/api/v1/signals',
    params
  })
}

export function fetchApproveStrategySignal(
  signalId: string,
  idempotencyKey: string,
  reauthToken: string
) {
  return request.post<Api.Notifications.StrategySignalDecision>({
    url: `/api/v1/signals/${encodeURIComponent(signalId)}/approve`,
    headers: {
      'Idempotency-Key': idempotencyKey,
      'X-Reauth-Token': reauthToken
    },
    showErrorMessage: false,
    showSuccessMessage: true
  })
}

export function fetchRejectStrategySignal(signalId: string, idempotencyKey: string) {
  return request.post<Api.Notifications.StrategySignalDecision>({
    url: `/api/v1/signals/${encodeURIComponent(signalId)}/reject`,
    headers: { 'Idempotency-Key': idempotencyKey },
    showErrorMessage: false,
    showSuccessMessage: true
  })
}
