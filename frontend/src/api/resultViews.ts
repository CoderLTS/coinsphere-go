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

export type ResultViewRunStatus =
  | 'queued'
  | 'running'
  | 'waiting'
  | 'retrying'
  | 'succeeded'
  | 'failed'
  | 'cancelled'

export interface ResultViewRun {
  id: number
  status: ResultViewRunStatus
  triggerType: string
  currentNodeInstanceId?: string
  triggeredAt: string
  startedAt?: string
  completedAt?: string
  errorCategory?: string
  errorMessage?: string
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

export const fetchResultViewRuns = (viewId: number) =>
  request.get<{ items: ResultViewRun[] }>({ url: `/api/v1/result-views/${viewId}/runs` })

export const applyResultViewRunAction = (
  viewId: number,
  runId: number,
  action: 'retry' | 'cancel'
) =>
  request.post<ResultViewRun>({
    url: `/api/v1/result-views/${viewId}/runs/${runId}/${action}`,
    showSuccessMessage: true
  })

export const pauseResultViewWorkflow = (viewId: number) =>
  request.post({
    url: `/api/v1/result-views/${viewId}/workflow/pause`,
    showSuccessMessage: true
  })
