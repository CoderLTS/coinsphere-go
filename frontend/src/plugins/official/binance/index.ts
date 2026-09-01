export const pages = {
  instruments: () => import('./InstrumentsPage.vue'),
  candles: () => import('./CandlesPage.vue'),
  'live-accounts': () => import('./LiveAccountsPage.vue')
}

export const resultPages = {
  paper: () => import('./PaperResultPage.vue')
}

const schemaEditor = () => import('./SchemaNodeEditor.vue')

export const nodeEditors = {
  'official.binance.realtime_candles': () => import('./MarketDataNodeEditor.vue'),
  'official.binance.backfill_candles': () => import('./MarketDataNodeEditor.vue'),
  'official.binance.sync_instruments': schemaEditor,
  'official.binance.account_stream': schemaEditor,
  'official.binance.paper_execute': schemaEditor,
  'official.binance.live_execute': schemaEditor
}

export const providerConfigComponents = { binance: schemaEditor }
