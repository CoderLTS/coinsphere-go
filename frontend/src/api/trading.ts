import request from '@/utils/http'

export interface MarketSymbol {
  id: string
  market: 'spot' | 'usd_m'
  nativeSymbol: string
  baseAsset: string
  quoteAsset: string
  status: string
}

export interface TradingRisk {
  instrumentIds: string[]
  maxTotalNotional: string | null
  maxSymbolNotional: string | null
  maxOrderNotional: string | null
  maxDailyLoss: string | null
  maxDrawdown: string | null
  maxQuoteAgeSeconds: number | null
  leverage: number | null
  complete: boolean
}

export interface TradingAccount {
  id: string
  name: string
  market: 'spot' | 'usd_m'
  environment: 'paper' | 'testnet'
  status: 'active' | 'paused'
  pauseReason: string
  automationEnabled: boolean
  automationAuthorized: boolean
  automationAuthorizedAt: string | null
  credentialsConfigured: boolean
  credentialStatus: string
  credentialVerificationStatus: string
  credentialsUpdatedAt: string | null
  initialBalance: string
  paperFeeRate: string
  risk: TradingRisk
  createdAt: string
  updatedAt: string
}

export interface TradingControl {
  emergencyStopped: boolean
  stopReason: string
  stoppedAt: string
  stoppedByUserId: number | null
  releasedAt: string | null
  releasedByUserId: number | null
  updatedAt: string
}

export interface TradingIntent {
  id: string
  accountId: string
  strategySignalId: string
  strategyInstanceId: string
  instrumentId: string
  symbol: string
  market: string
  mode: string
  target: string
  status: string
  blockReason: string
  clientOrderId: string
  completedAt: string | null
  createdAt: string
}

export interface PaperOrder {
  id: string
  accountId: string
  intentId: string
  instrumentId: string
  symbol: string
  clientOrderId: string
  side: string
  quantity: string
  filledQuantity: string
  averagePrice: string
  status: string
  createdAt: string
  updatedAt: string
}

export interface PaperPosition {
  accountId: string
  instrumentId: string
  symbol: string
  ownerStrategyInstanceId: string | null
  quantity: string
  averageEntryPrice: string
  lastPrice: string
  realizedPnl: string
  unrealizedPnl: string
  updatedAt: string
}

export interface PaperBalance {
  accountId: string
  cashBalance: string
  equity: string
  peakEquity: string
  dayStartDate: string
  dayStartEquity: string
  realizedPnl: string
  unrealizedPnl: string
  fees: string
  funding: string
  updatedAt: string
}

export interface TradingOverview {
  control: TradingControl
  accounts: TradingAccount[]
  intents: TradingIntent[]
  orders: PaperOrder[]
  positions: PaperPosition[]
  balances: PaperBalance[]
}

export interface TradingRiskPayload {
  instrumentIds: string[]
  maxTotalNotional: string
  maxSymbolNotional: string
  maxOrderNotional: string
  maxDailyLoss: string
  maxDrawdown: string
  maxQuoteAgeSeconds: number
  leverage?: number
}

export interface TradingAccountCreatePayload {
  name: string
  market: 'spot' | 'usd_m'
  environment: 'paper' | 'testnet'
  initialBalance: string
  paperFeeRate: string
  risk: TradingRiskPayload
}

export interface TradingCredentialPayload {
  apiKey: string
  apiSecret: string
  withdrawalDisabled: boolean
  ipWhitelistConfigured: boolean
}

export interface TradingCredentialStatus {
  accountId: string
  configured: boolean
  status: string
  verificationStatus: string
  verificationErrorCode?: string
  updatedAt: string
  lastVerifiedAt?: string
}

const commandHeaders = (idempotencyKey: string, reauthToken?: string) => ({
  'Idempotency-Key': idempotencyKey,
  ...(reauthToken ? { 'X-Reauth-Token': reauthToken } : {})
})

export function fetchTradingOverview() {
  return request.get<TradingOverview>({ url: '/api/v1/trading/overview' })
}

export function fetchMarketSymbols(market: 'spot' | 'usd_m') {
  return request.get<Api.Common.PaginatedResponse<MarketSymbol>>({
    url: '/api/v1/markets/symbols',
    params: { market, limit: 100 }
  })
}

export function fetchCreateTradingAccount(
  payload: TradingAccountCreatePayload,
  idempotencyKey: string
) {
  return request.post<TradingAccount>({
    url: '/api/v1/trading/accounts',
    params: payload,
    headers: commandHeaders(idempotencyKey),
    showSuccessMessage: true
  })
}

export function fetchUpdateTradingRisk(
  accountId: string,
  payload: TradingRiskPayload,
  idempotencyKey: string,
  reauthToken: string
) {
  return request.put<TradingAccount>({
    url: `/api/v1/trading/accounts/${encodeURIComponent(accountId)}/risk`,
    params: payload,
    headers: commandHeaders(idempotencyKey, reauthToken),
    showSuccessMessage: true
  })
}

export function fetchSetTradingAutomation(
  accountId: string,
  enabled: boolean,
  idempotencyKey: string,
  reauthToken?: string
) {
  return request.post<TradingAccount>({
    url: `/api/v1/trading/accounts/${encodeURIComponent(accountId)}/automation/${enabled ? 'enable' : 'disable'}`,
    headers: commandHeaders(idempotencyKey, reauthToken),
    showSuccessMessage: true
  })
}

export function fetchResumeTradingAccount(
  accountId: string,
  idempotencyKey: string,
  reauthToken: string
) {
  return request.post<TradingAccount>({
    url: `/api/v1/trading/accounts/${encodeURIComponent(accountId)}/resume`,
    headers: commandHeaders(idempotencyKey, reauthToken),
    showSuccessMessage: true
  })
}

export function fetchSaveTradingCredentials(
  accountId: string,
  payload: TradingCredentialPayload,
  idempotencyKey: string,
  reauthToken: string
) {
  return request.put<TradingCredentialStatus>({
    url: `/api/v1/trading/accounts/${encodeURIComponent(accountId)}/credentials`,
    params: payload,
    headers: commandHeaders(idempotencyKey, reauthToken),
    showSuccessMessage: true
  })
}

export function fetchRevokeTradingCredentials(
  accountId: string,
  idempotencyKey: string,
  reauthToken: string
) {
  return request.post<TradingCredentialStatus>({
    url: `/api/v1/trading/accounts/${encodeURIComponent(accountId)}/credentials/revoke`,
    headers: commandHeaders(idempotencyKey, reauthToken),
    showSuccessMessage: true
  })
}

export function fetchActivateTradingEmergencyStop(reason: string, idempotencyKey: string) {
  return request.post<TradingControl>({
    url: '/api/v1/trading/emergency-stop',
    params: { reason },
    headers: commandHeaders(idempotencyKey),
    showSuccessMessage: true
  })
}

export function fetchReleaseTradingEmergencyStop(idempotencyKey: string, reauthToken: string) {
  return request.post<TradingControl>({
    url: '/api/v1/admin/trading/emergency-stop/release',
    headers: commandHeaders(idempotencyKey, reauthToken),
    showSuccessMessage: true
  })
}

export function fetchSetTradingAuthorization(
  accountId: string,
  authorized: boolean,
  idempotencyKey: string,
  reauthToken: string
) {
  return request.post<TradingAccount>({
    url: `/api/v1/admin/trading/accounts/${encodeURIComponent(accountId)}/${authorized ? 'authorize' : 'revoke'}`,
    headers: commandHeaders(idempotencyKey, reauthToken),
    showSuccessMessage: true
  })
}
