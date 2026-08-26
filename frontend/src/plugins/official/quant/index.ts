export const pages = {
  instruments: () => import('@/views/data/market-metadata/index.vue'),
  candles: () => import('@/views/data/market-chart/index.vue')
}

export const resultPages = {
  quant: () => import('./ResultPage.vue'),
  paper: () => import('./PaperResultPage.vue')
}
