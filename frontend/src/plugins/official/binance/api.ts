import request from '@/utils/http'

interface ItemList<T> {
  items: T[]
}

export interface BinanceInstrument {
  market: 'spot' | 'usdm'
  symbol: string
  baseAsset: string
  quoteAsset: string
  status: string
  priceTick: string
  quantityStep: string
  minQuantity: string
  updatedAt: string
}

export interface BinanceCandle {
  venue: string
  market: 'spot' | 'usdm'
  instrument: string
  interval: string
  openTime: string
  closeTime: string
  open: string
  high: string
  low: string
  close: string
  volume: string
}

export interface BinanceLiveAccountRelease {
  account: string
  market: 'spot' | 'usdm'
  enabled: boolean
  confirmedBy: number
  confirmedAt: string
  updatedAt: string
}

const binanceBase = '/api/v1/plugins/official.binance'

export const fetchBinanceInstruments = (market: 'spot' | 'usdm') =>
  request.get<ItemList<BinanceInstrument>>({
    url: `${binanceBase}/instruments`,
    params: { market }
  })

export const fetchBinanceCandles = (params: {
  market: 'spot' | 'usdm'
  instrument: string
  interval: string
  before?: string
  limit?: number
}) => request.get<ItemList<BinanceCandle>>({ url: `${binanceBase}/candles`, params })

export const fetchBinanceLiveAccounts = () =>
  request.get<ItemList<BinanceLiveAccountRelease>>({ url: `${binanceBase}/live-accounts` })

export const updateBinanceLiveAccount = (
  account: string,
  payload: { market: 'spot' | 'usdm'; enabled: boolean; confirmation: string }
) =>
  request.put<BinanceLiveAccountRelease>({
    url: `${binanceBase}/live-accounts/${encodeURIComponent(account)}`,
    data: payload
  })
