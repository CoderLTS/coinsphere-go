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
      path: 'overview',
      name: 'PaperTrading',
      component: '/trading/overview',
      meta: {
        title: 'menus.trading.overview',
        keepAlive: true,
        roles: ['R_SUPER', 'R_USER']
      }
    }
  ]
}
