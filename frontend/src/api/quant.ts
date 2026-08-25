import request from '@/utils/http'

interface ItemList<T> {
  items: T[]
}

export interface QuantInstrument {
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

export interface QuantCandle {
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

export interface QuantStrategy {
  id: string
  version: string
  name: string
  minimumLookback: number
  parameterSchema: Record<string, unknown>
}

export interface QuantBacktest {
  id: number
  strategyId: string
  strategyVersion: string
  market: 'spot' | 'usdm'
  instrument: string
  interval: string
  startTime: string
  endTime: string
  initialCapital: string
  finalEquity: string
  totalReturn: string
  maxDrawdown: string
  totalFees: string
  tradeCount: number
  candleCount: number
  detailSha256: string
  detailSizeBytes: number
  createdAt: string
}

const quantBase = '/api/v1/plugins/official.quant'

export const fetchQuantInstruments = (market: 'spot' | 'usdm') =>
  request.get<ItemList<QuantInstrument>>({ url: `${quantBase}/instruments`, params: { market } })

export const fetchQuantCandles = (params: {
  market: 'spot' | 'usdm'
  instrument: string
  interval: string
  limit?: number
}) => request.get<ItemList<QuantCandle>>({ url: `${quantBase}/candles`, params })

export const fetchQuantStrategies = () =>
  request.get<ItemList<QuantStrategy>>({ url: `${quantBase}/strategies` })

export const fetchQuantBacktests = (limit = 50) =>
  request.get<ItemList<QuantBacktest>>({ url: `${quantBase}/backtests`, params: { limit } })
