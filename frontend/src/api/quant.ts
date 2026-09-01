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
  createdAt: string
}

export interface QuantBacktestPoint {
  evaluatedAt: string
  nodeOutputs: Record<string, unknown>
  previousTargetPosition: string
  targetPosition: string
  action: 'buy' | 'sell' | 'hold'
  executionOpenTime: string
  executionPrice: string
  quantityDelta: string
  fee: string
  equity: string
}

export interface QuantBacktestDetail {
  schemaVersion: 2
  strategyId: string
  strategyVersion: string
  market: 'spot' | 'usdm'
  instrument: string
  interval: string
  parameters: Record<string, unknown>
  candles: QuantCandle[]
  points: QuantBacktestPoint[]
}

export interface QuantMarketSignal {
  id: number
  market: 'spot' | 'usdm'
  instrument: string
  interval: string
  name: string
  indicator: string
  candleCloseTime: string
  summary: string
  values: Record<string, string>
  createdAt: string
}

const quantBase = '/api/v1/plugins/official.quant'

export const fetchQuantInstruments = (market: 'spot' | 'usdm') =>
  request.get<ItemList<QuantInstrument>>({ url: `${quantBase}/instruments`, params: { market } })

export const fetchQuantCandles = (params: {
  market: 'spot' | 'usdm'
  instrument: string
  interval: string
  before?: string
  limit?: number
}) => request.get<ItemList<QuantCandle>>({ url: `${quantBase}/candles`, params })

export const fetchQuantStrategies = () =>
  request.get<ItemList<QuantStrategy>>({ url: `${quantBase}/strategies` })

export const fetchQuantBacktests = (limit = 50) =>
  request.get<ItemList<QuantBacktest>>({ url: `${quantBase}/backtests`, params: { limit } })

export const fetchQuantBacktestDetail = (backtestId: number) =>
  request.get<QuantBacktestDetail>({ url: `${quantBase}/backtests/${backtestId}` })

export const fetchQuantMarketSignals = (params: {
  market: 'spot' | 'usdm'
  instrument: string
  interval: string
  startTime?: string
  endTime?: string
  limit?: number
}) => request.get<ItemList<QuantMarketSignal>>({ url: `${quantBase}/market-signals`, params })
