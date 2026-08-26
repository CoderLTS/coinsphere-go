import type { WorkflowExecutionItem } from './scheduler'
import { fetchQuantCandles, fetchQuantInstruments, type QuantInstrument } from './quant'

export type MarketType = 'spot' | 'usd_m'
export type QuoteAsset = 'USDT'
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
  spotRestBaseUrl: string
  usdmRestBaseUrl: string
  proxyEnabled: boolean
  proxyUrl: string
  proxyUsername: string
  proxyPasswordConfigured: boolean
  proxyLastCheckStatus: 'unchecked' | 'healthy' | 'failed'
  proxyLastCheckedAt: string | null
  proxyLastLatencyMs: number | null
  proxyLastError: string
  updatedByUserId: number | null
  createdAt: string
  updatedAt: string
}

export interface MarketSyncSettingsUpdate {
  marketTypes: MarketType[]
  quoteAssets: ['USDT']
  spotRestBaseUrl: string
  usdmRestBaseUrl: string
  proxyEnabled: boolean
  proxyUrl: string
  proxyUsername: string
  proxyPassword?: string
  clearProxyPassword: boolean
}

export interface MarketProxyStatus {
  mode: 'direct' | 'proxy'
  status: 'healthy' | 'failed'
  latencyMs: number | null
  checkedAt: string
  message: string
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
  quoteAsset?: 'USDT' | ''
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

const toMarketSymbol = (item: QuantInstrument): MarketSymbol => ({
  id: `${item.market}:${item.symbol}`,
  venue: 'binance',
  market: item.market === 'usdm' ? 'usd_m' : 'spot',
  nativeSymbol: item.symbol,
  baseAsset: item.baseAsset,
  quoteAsset: item.quoteAsset,
  status: item.status.toUpperCase() === 'TRADING' ? 'trading' : 'suspended',
  priceTick: item.priceTick,
  quantityStep: item.quantityStep,
  minQuantity: item.minQuantity,
  minNotional: '--',
  updatedAt: item.updatedAt
})

export async function fetchMarketSymbols(params: MarketSymbolQuery) {
  const markets = params.market ? [params.market] : (['spot', 'usd_m'] as MarketType[])
  const results = await Promise.all(
    markets.map((market) => fetchQuantInstruments(market === 'usd_m' ? 'usdm' : 'spot'))
  )
  const keyword = String(params.keyword || '')
    .trim()
    .toUpperCase()
  const records = results
    .flatMap((result) => result.items)
    .map(toMarketSymbol)
    .filter((item) => !params.quoteAsset || item.quoteAsset === params.quoteAsset)
    .filter((item) => !params.status || params.status === 'all' || item.status === params.status)
    .filter(
      (item) => !keyword || item.nativeSymbol.includes(keyword) || item.baseAsset.includes(keyword)
    )
  const offset = Math.max(0, Number(params.cursor || 0) || 0)
  const limit = Math.max(1, params.limit || 100)
  const page = records.slice(offset, offset + limit)
  const next = offset + page.length
  return {
    records: page,
    nextCursor: next < records.length ? String(next) : '',
    hasMore: next < records.length,
    total: records.length
  }
}

export async function fetchMarketCandles(params: MarketCandleQuery) {
  const [market, instrument] = params.instrumentId.split(':', 2)
  const result = await fetchQuantCandles({
    market: market === 'usdm' || market === 'usd_m' ? 'usdm' : 'spot',
    instrument,
    interval: params.interval,
    limit: params.limit
  })
  const records: MarketCandle[] = result.items.map((item) => ({
    instrumentId: params.instrumentId,
    interval: item.interval as CandleInterval,
    openTime: item.openTime,
    closeTime: item.closeTime,
    open: item.open,
    high: item.high,
    low: item.low,
    close: item.close,
    baseVolume: item.volume,
    isClosed: true
  }))
  return { records, nextCursor: '', hasMore: false, total: records.length }
}

const pluginSettings: MarketSyncSettings = {
  venue: 'binance',
  marketTypes: ['spot', 'usd_m'],
  quoteAssets: ['USDT'],
  spotRestBaseUrl: 'official.quant',
  usdmRestBaseUrl: 'official.quant',
  proxyEnabled: false,
  proxyUrl: '',
  proxyUsername: '',
  proxyPasswordConfigured: false,
  proxyLastCheckStatus: 'unchecked',
  proxyLastCheckedAt: null,
  proxyLastLatencyMs: null,
  proxyLastError: '',
  updatedByUserId: null,
  createdAt: '',
  updatedAt: ''
}

export const fetchMarketSyncSettings = async () => pluginSettings

export function fetchUpdateMarketSyncSettings(params: MarketSyncSettingsUpdate) {
  return Promise.resolve({ ...pluginSettings, marketTypes: params.marketTypes })
}

export function fetchCheckMarketProxy() {
  return Promise.resolve<MarketProxyStatus>({
    mode: 'direct',
    status: 'healthy',
    latencyMs: null,
    checkedAt: '',
    message: '由采集工作流管理'
  })
}

export function fetchMarketSyncStatus() {
  return Promise.resolve<MarketSyncStatus>({
    lastSyncAt: null,
    nextSyncAt: null,
    lastExecution: null
  })
}
