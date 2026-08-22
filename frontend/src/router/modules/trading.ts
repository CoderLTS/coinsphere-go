import { AppRouteRecord } from '@/types/router'

export const tradingRoutes: AppRouteRecord = {
  path: '/trading',
  name: 'TradingCenter',
  component: '/index/index',
  meta: {
    title: 'menus.trading.title',
    icon: 'ri:exchange-funds-line',
    roles: ['R_SUPER', 'R_USER']
  },
  children: [
    {
      path: 'accounts',
      name: 'TradingAccounts',
      component: '/trading/accounts',
      meta: {
        title: 'menus.trading.accounts',
        keepAlive: true,
        roles: ['R_SUPER', 'R_USER']
      }
    },
    {
      path: 'strategies',
      name: 'StrategyManagement',
      component: '/strategy/drafts',
      meta: {
        title: 'menus.trading.strategies',
        keepAlive: true,
        roles: ['R_SUPER', 'R_USER']
      }
    }
  ]
}
