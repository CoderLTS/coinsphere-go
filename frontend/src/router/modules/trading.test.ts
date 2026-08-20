import { describe, expect, it } from 'vitest'
import { tradingRoutes } from './trading'

describe('trading routes', () => {
  it('exposes accounts and strategies without the legacy overview', () => {
    expect(tradingRoutes.children?.map((route) => route.path)).toEqual(['accounts', 'strategies'])
    expect(tradingRoutes.children?.map((route) => route.component)).toEqual([
      '/trading/accounts',
      '/strategy/drafts'
    ])
  })
})
