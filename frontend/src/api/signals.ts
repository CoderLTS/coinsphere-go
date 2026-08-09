/** 实时策略信号的人工决策接口。 */
import request from '@/utils/http'

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
