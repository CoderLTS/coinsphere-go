import request from '@/utils/http'

export type PaperSignalStatus = 'pending' | 'superseded' | 'approved' | 'rejected' | 'executed'

export interface PaperSignal {
  id: number
  strategyId: string
  strategyVersion: string
  market: 'spot' | 'usdm'
  instrument: string
  target: string
  evaluatedAt: string
  status: PaperSignalStatus
  taskId?: number
  rejectionReason?: string
  createdAt: string
  decidedAt?: string
  executedAt?: string
}

export interface PaperPosition {
  market: 'spot' | 'usdm'
  instrument: string
  quantity: string
  averagePrice: string
  lastPrice: string
  updatedAt: string
}

export interface PaperAccount {
  id: number
  status: 'active' | 'paused'
  initialBalance: string
  cashBalance: string
  equity: string
  peakEquity: string
  dayStartEquity: string
  updatedAt: string
  positions: PaperPosition[]
}

export interface PaperResult {
  signals: PaperSignal[]
  accounts: PaperAccount[]
}

const quantPath = (viewId: number) => `/api/v1/result-views/${viewId}/plugins/official.quant`

export const fetchPaperResult = (viewId: number) =>
  request.get<PaperResult>({ url: `${quantPath(viewId)}/paper` })

export const decidePaperSignal = (viewId: number, signalId: number, action: 'approve' | 'reject') =>
  request.post<{ signalId: number; taskId: number; decision: string }>({
    url: `${quantPath(viewId)}/signals/${signalId}/${action}`,
    showSuccessMessage: true
  })

export const exportPaperResult = (viewId: number) =>
  request.request<Blob>({
    url: `${quantPath(viewId)}/paper/export`,
    method: 'GET',
    responseType: 'blob',
    rawResponse: true
  })
