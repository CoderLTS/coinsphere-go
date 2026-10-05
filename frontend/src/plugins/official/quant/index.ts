export const runPanels = {
  quant: () => import('./ResultPage.vue'),
  analysis: () => import('./BacktestAnalysis.vue')
}

const schemaEditor = () => import('./SchemaNodeEditor.vue')

export const nodeEditors = Object.fromEntries(
  [
    'official.quant.evaluate',
    'official.quant.backtest',
    'official.quant.backtest_start',
    'official.quant.code_strategy',
    'official.quant.position',
    'official.quant.output_signal',
    'official.quant.market_signal',
    'official.quant.order_intent',
    'official.quant.volume_spike_condition',
    'official.quant.price_change_condition',
    'official.quant.macd_condition',
    'official.quant.kdj_condition',
    'official.quant.rsi_condition',
    'official.quant.bollinger_condition'
  ].map((type) => [type, schemaEditor])
)
