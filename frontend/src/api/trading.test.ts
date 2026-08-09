import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn()
}))

vi.mock('@/utils/http', () => ({ default: mocks }))

import {
  fetchCreateTradingAccount,
  fetchSetTradingAutomation,
  fetchUpdateTradingRisk,
  type TradingAccountCreatePayload
} from './trading'

describe('trading API', () => {
  beforeEach(() => {
    mocks.get.mockReset()
    mocks.post.mockReset()
    mocks.put.mockReset()
  })

  it('binds idempotency and reauthentication headers to risk commands', () => {
    const risk = {
      instrumentIds: ['instrument-1'],
      maxTotalNotional: '1000',
      maxSymbolNotional: '500',
      maxOrderNotional: '250',
      maxDailyLoss: '100',
      maxDrawdown: '200',
      maxQuoteAgeSeconds: 30
    }

    fetchUpdateTradingRisk('account/1', risk, 'risk-command-1', 'reauth-1')

    expect(mocks.put).toHaveBeenCalledWith({
      url: '/api/v1/trading/accounts/account%2F1/risk',
      params: risk,
      headers: {
        'Idempotency-Key': 'risk-command-1',
        'X-Reauth-Token': 'reauth-1'
      },
      showSuccessMessage: true
    })
  })

  it('never sends a reauthentication header when disabling automation', () => {
    fetchSetTradingAutomation('account-1', false, 'disable-command-1')

    expect(mocks.post).toHaveBeenCalledWith({
      url: '/api/v1/trading/accounts/account-1/automation/disable',
      headers: { 'Idempotency-Key': 'disable-command-1' },
      showSuccessMessage: true
    })
  })

  it('creates Paper accounts with one caller-owned idempotency key', () => {
    const payload: TradingAccountCreatePayload = {
      name: 'Paper Spot',
      market: 'spot',
      initialBalance: '10000',
      paperFeeRate: '0.001',
      risk: {
        instrumentIds: ['instrument-1'],
        maxTotalNotional: '5000',
        maxSymbolNotional: '2500',
        maxOrderNotional: '1000',
        maxDailyLoss: '500',
        maxDrawdown: '1000',
        maxQuoteAgeSeconds: 30
      }
    }

    fetchCreateTradingAccount(payload, 'create-command-1')

    expect(mocks.post).toHaveBeenCalledWith({
      url: '/api/v1/trading/accounts',
      params: payload,
      headers: { 'Idempotency-Key': 'create-command-1' },
      showSuccessMessage: true
    })
  })
})
