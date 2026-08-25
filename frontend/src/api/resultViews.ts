import request from '@/utils/http'

export type ResultViewStatus = 'active' | 'revoked'

export interface ResultView {
  id: number
  name: string
  pluginId: string
  pageKey: string
  scope?: Record<string, unknown>
  filters?: Record<string, unknown>
  allowedActions: string[]
  status: ResultViewStatus
  userIds?: number[]
  roleCodes?: string[]
  createdAt: string
  revokedAt?: string
}

export interface ResultViewCreatePayload {
  name: string
  pluginId: string
  pageKey: string
  scope: Record<string, unknown>
  filters: Record<string, unknown>
  allowedActions: string[]
  userIds: number[]
  roleCodes: string[]
}

export interface ResultViewGrantPayload {
  userIds: number[]
  roleCodes: string[]
}

export type ResultViewBatchStatus =
  | 'queued'
  | 'running'
  | 'waiting'
  | 'retrying'
  | 'succeeded'
  | 'failed'
  | 'cancelled'

export interface ResultViewBatch {
  id: number
  status: ResultViewBatchStatus
  triggerType: string
  currentNodeInstanceId?: string
  triggeredAt: string
  startedAt?: string
  completedAt?: string
  errorCategory?: string
}

export const fetchResultViews = () =>
  request.get<{ items: ResultView[] }>({ url: '/api/v1/result-views' })

export const createResultView = (params: ResultViewCreatePayload) =>
  request.post<ResultView>({ url: '/api/v1/result-views', params, showSuccessMessage: true })

export const replaceResultViewGrants = (viewId: number, params: ResultViewGrantPayload) =>
  request.put<ResultView>({
    url: `/api/v1/result-views/${viewId}/grants`,
    params,
    showSuccessMessage: true
  })

export const revokeResultView = (viewId: number) =>
  request.post<ResultView>({
    url: `/api/v1/result-views/${viewId}/revoke`,
    showSuccessMessage: true
  })

export const fetchResultViewBatches = (viewId: number) =>
  request.get<{ items: ResultViewBatch[] }>({ url: `/api/v1/result-views/${viewId}/batches` })

export const applyResultViewBatchAction = (
  viewId: number,
  batchId: number,
  action: 'retry' | 'cancel'
) =>
  request.post<ResultViewBatch>({
    url: `/api/v1/result-views/${viewId}/batches/${batchId}/${action}`,
    showSuccessMessage: true
  })

export const pauseResultViewWorkflow = (viewId: number) =>
  request.post({
    url: `/api/v1/result-views/${viewId}/workflow/pause`,
    showSuccessMessage: true
  })
