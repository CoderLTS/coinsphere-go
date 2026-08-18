import request from '@/utils/http'
import type { WorkflowExecutionItem } from './scheduler'

export type MarketType = 'spot' | 'usd_m'
export type QuoteAsset = 'USDT' | 'USDC' | 'FDUSD'
export type MarketStatus = 'trading' | 'suspended' | 'all'
export type CandleInterval = '1m' | '5m' | '15m' | '1h' | '4h' | '1d'

export interface MarketSymbol {
  id: string
  venue: 'binance'
  market: MarketType
  nativeSymbol: string
  baseAsset: string
  quoteAsset: QuoteAsset | string
  status: 'trading' | 'suspended'
  priceTick: string
  quantityStep: string
  minQuantity: string
  minNotional: string
  updatedAt: string
}

export interface MarketCandle {
  instrumentId: string
  interval: CandleInterval
  openTime: string
  closeTime: string
  open: string
  high: string
  low: string
  close: string
  baseVolume: string
  isClosed: boolean
}

export interface MarketSyncSettings {
  venue: 'binance'
  marketTypes: MarketType[]
  quoteAssets: QuoteAsset[]
  updatedByUserId: number | null
  createdAt: string
  updatedAt: string
}

export interface MarketSyncStatus {
  lastSyncAt: string | null
  nextSyncAt: string | null
  lastExecution: WorkflowExecutionItem | null
}

export interface MarketSymbolQuery {
  cursor?: string
  limit?: number
  market?: MarketType | ''
  quoteAsset?: QuoteAsset | ''
  status?: MarketStatus
  keyword?: string
}

export interface MarketCandleQuery {
  cursor?: string
  limit?: number
  instrumentId: string
  interval: CandleInterval
  startTime?: string
  endTime?: string
}

export function fetchMarketSymbols(params: MarketSymbolQuery) {
  return request.get<Api.Common.PaginatedResponse<MarketSymbol>>({
    url: '/api/v1/markets/symbols',
    params
  })
}

export function fetchMarketCandles(params: MarketCandleQuery) {
  return request.get<Api.Common.PaginatedResponse<MarketCandle>>({
    url: '/api/v1/markets/candles',
    params
  })
}

export function fetchMarketSyncSettings() {
  return request.get<MarketSyncSettings>({
    url: '/api/v1/markets/metadata-sync/settings'
  })
}

export function fetchUpdateMarketSyncSettings(
  params: Pick<MarketSyncSettings, 'marketTypes' | 'quoteAssets'>
) {
  return request.put<MarketSyncSettings>({
    url: '/api/v1/markets/metadata-sync/settings',
    params,
    showSuccessMessage: true
  })
}

export function fetchMarketSyncStatus() {
  return request.get<MarketSyncStatus>({
    url: '/api/v1/markets/metadata-sync/status'
  })
}

export function fetchRunMarketSync() {
  return request.post<WorkflowExecutionItem>({
    url: '/api/v1/markets/metadata-sync/executions',
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    showSuccessMessage: true
  })
}
