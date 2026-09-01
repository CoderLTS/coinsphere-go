import request from '@/utils/http'

export type PaperOrderStatus =
  | 'new'
  | 'partially_filled'
  | 'filled'
  | 'canceled'
  | 'rejected'
  | 'expired'

export interface PaperOrder {
  id: number
  account: string
  clientOrderId: string
  market: 'spot' | 'usdm'
  instrument: string
  side: 'buy' | 'sell'
  quantity: string
  executed: string
  averagePrice: string
  notional: string
  status: PaperOrderStatus
  createdAt: string
  updatedAt: string
}

export interface PaperPosition {
  market: 'spot' | 'usdm'
  instrument: string
  quantity: string
  averagePrice: string
  updatedAt: string
}

export interface PaperAccount {
  id: string
  cashBalance: string
  equity: string
  positions: PaperPosition[]
}

export interface PaperResult {
  orders: PaperOrder[]
  accounts: PaperAccount[]
}

const binancePath = (viewId: number) => `/api/v1/result-views/${viewId}/plugins/official.binance`

export const fetchPaperResult = (viewId: number) =>
  request.get<PaperResult>({ url: `${binancePath(viewId)}/paper` })

export const exportPaperResult = (viewId: number) =>
  request.request<Blob>({
    url: `${binancePath(viewId)}/paper/export`,
    method: 'GET',
    responseType: 'blob',
    rawResponse: true
  })
