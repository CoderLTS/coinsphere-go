export const resultPages = {
  quant: () => import('./ResultPage.vue')
}

export const pages = {
  profiles: () => import('@/views/scheduler/workflow/profiles/ProfileManager.vue')
}

export const profileTypes = ['quant.backtest'] as const

const schemaEditor = () => import('./MarketNodeEditor.vue')

export const nodeEditors = Object.fromEntries(
  ['official.quant.code_strategy'].map((type) => [type, schemaEditor])
)
