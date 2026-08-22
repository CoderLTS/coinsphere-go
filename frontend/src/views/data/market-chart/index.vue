<template>
  <div class="market-chart-page">
    <header class="page-head">
      <div>
        <div class="eyebrow">
          <ArtSvgIcon icon="ri:line-chart-line" />
          行情分析
        </div>
        <h1>K 线与策略信号</h1>
      </div>
      <ElButton type="primary" :icon="Refresh" :loading="loading" @click="loadChart">
        刷新数据
      </ElButton>
    </header>

    <section class="filter-card art-card" aria-label="图表筛选">
      <div class="filter-group">
        <span class="filter-label">查看方式</span>
        <ElRadioGroup v-model="viewMode" @change="handleModeChange">
          <ElRadioButton label="strategy">策略视图</ElRadioButton>
          <ElRadioButton label="market">币种视图</ElRadioButton>
        </ElRadioGroup>
      </div>

      <div class="filter-group filter-group--selector">
        <span class="filter-label">{{ viewMode === 'strategy' ? '策略实例' : '交易标的' }}</span>
        <ElSelect
          v-if="viewMode === 'strategy'"
          v-model="selectedStrategyId"
          class="strategy-select"
          filterable
          placeholder="选择策略实例"
          @change="handleStrategyChange"
        >
          <ElOption
            v-for="item in strategyInstances"
            :key="item.id"
            :label="`${item.name} · ${item.symbol} · ${item.interval}`"
            :value="item.id"
          >
            <div class="strategy-option">
              <strong>{{ item.name }}</strong>
              <span
                >{{ item.strategyName }} v{{ item.strategyVersion }} · {{ item.symbol }} ·
                {{ item.interval }}</span
              >
            </div>
          </ElOption>
        </ElSelect>

        <ElSelect
          v-else
          v-model="selectedInstrumentId"
          class="symbol-select"
          filterable
          placeholder="选择交易对"
          @change="loadChart"
        >
          <ElOption
            v-for="item in symbols"
            :key="item.id"
            :label="`${item.nativeSymbol} · ${item.market === 'usd_m' ? 'USD-M' : 'SPOT'}`"
            :value="item.id"
          />
        </ElSelect>
      </div>

      <div class="filter-group">
        <span class="filter-label">K 线周期</span>
        <ElRadioGroup v-model="selectedInterval" class="interval-control" @change="loadChart">
          <ElRadioButton v-for="item in intervals" :key="item" :label="item">
            {{ item }}
          </ElRadioButton>
        </ElRadioGroup>
      </div>

      <div class="market-status">
        <span
          class="market-status__dot"
          :class="{ 'market-status__dot--offline': selectedSymbol?.status !== 'trading' }"
        ></span>
        <div>
          <strong>
            {{
              !selectedSymbol
                ? '未选择标的'
                : selectedSymbol.status === 'trading'
                  ? '交易中'
                  : '已暂停'
            }}
          </strong>
          <span>{{ selectedSymbol?.market === 'usd_m' ? 'USD-M 合约' : '现货市场' }}</span>
        </div>
      </div>
    </section>

    <section class="metric-grid" aria-label="行情摘要">
      <article class="metric-card art-card metric-card--instrument">
        <span class="metric-icon metric-icon--primary">
          <ArtSvgIcon icon="ri:coins-line" />
        </span>
        <div class="metric-copy">
          <span>当前标的</span>
          <strong>{{ selectedSymbol?.nativeSymbol || '--' }}</strong>
          <small>{{
            selectedSymbol
              ? `${selectedSymbol.baseAsset} / ${selectedSymbol.quoteAsset}`
              : '等待选择标的'
          }}</small>
        </div>
      </article>

      <article class="metric-card art-card">
        <span class="metric-icon metric-icon--price">
          <ArtSvgIcon icon="ri:stock-line" />
        </span>
        <div class="metric-copy">
          <span>最新收盘</span>
          <strong class="metric-number">{{ numberText(latestCandle?.close) }}</strong>
          <small>{{ selectedInterval }} 周期</small>
        </div>
      </article>

      <article class="metric-card art-card">
        <span
          class="metric-icon"
          :class="changePercent >= 0 ? 'metric-icon--positive' : 'metric-icon--negative'"
        >
          <ArtSvgIcon :icon="changePercent >= 0 ? 'ri:arrow-up-line' : 'ri:arrow-down-line'" />
        </span>
        <div class="metric-copy">
          <span>区间变化</span>
          <strong :class="changePercent >= 0 ? 'positive' : 'negative'">
            {{ changePercent >= 0 ? '+' : '' }}{{ changePercent.toFixed(2) }}%
          </strong>
          <small>当前可见区间</small>
        </div>
      </article>

      <article class="metric-card art-card">
        <span class="metric-icon metric-icon--target">
          <ArtSvgIcon icon="ri:focus-3-line" />
        </span>
        <div class="metric-copy">
          <span>目标仓位</span>
          <strong class="target-value">{{ numberText(latestSignal?.target) }}</strong>
          <small>{{ viewMode === 'strategy' ? '最新策略目标' : '币种视图不展示' }}</small>
        </div>
      </article>

      <article class="metric-card art-card">
        <span class="metric-icon metric-icon--signal">
          <ArtSvgIcon icon="ri:pulse-line" />
        </span>
        <div class="metric-copy">
          <span>策略信号</span>
          <strong>{{ signals.length }}</strong>
          <small>当前加载区间</small>
        </div>
      </article>
    </section>

    <div class="chart-layout">
      <section class="chart-panel art-card">
        <div class="panel-head">
          <div class="panel-title">
            <span class="panel-icon"><ArtSvgIcon icon="ri:candlestick-chart-line" /></span>
            <div>
              <h2>{{ selectedStrategy?.name || selectedSymbol?.nativeSymbol || '行情图表' }}</h2>
              <p>价格 · 成交量 · 目标仓位</p>
            </div>
          </div>
          <div class="chart-legend" aria-label="图例">
            <span><i class="legend-up"></i>上涨</span>
            <span><i class="legend-down"></i>下跌</span>
            <span><i class="legend-target"></i>目标仓位</span>
          </div>
        </div>
        <ArtKLineChart
          :data="chartData"
          :signals="chartSignals"
          :loading="loading"
          :is-empty="!chartData.length"
          height="clamp(440px, 58vh, 620px)"
        />
      </section>

      <aside class="signal-rail art-card" v-loading="loading">
        <div class="panel-head">
          <div class="panel-title">
            <span class="panel-icon panel-icon--signal"><ArtSvgIcon icon="ri:pulse-line" /></span>
            <div>
              <h2>策略信号</h2>
              <p>按时间倒序展示</p>
            </div>
          </div>
          <span class="signal-count">{{ signals.length }}</span>
        </div>
        <ElScrollbar class="signal-scroll">
          <div v-if="signals.length" class="signal-list">
            <div
              v-for="item in signals"
              :key="item.id"
              class="signal-row"
              :class="`signal-row--${item.action}`"
            >
              <span class="signal-action">
                <ArtSvgIcon :icon="actionIcon[item.action]" />
              </span>
              <span class="signal-main">
                <strong>{{ actionLabel[item.action] }}</strong>
                <small>{{ axisTime(item.candleOpenTime) }} UTC</small>
              </span>
              <span class="signal-target">
                <small>目标仓位</small>
                <span>
                  {{ numberText(item.previousTarget) }}
                  <ArtSvgIcon icon="ri:arrow-right-line" />
                  <strong>{{ numberText(item.target) }}</strong>
                </span>
              </span>
            </div>
          </div>
          <div v-else class="signal-empty">
            <ArtSvgIcon icon="ri:pulse-line" />
            <strong>{{ viewMode === 'market' ? '币种视图不叠加策略' : '当前区间没有信号' }}</strong>
            <span>{{
              viewMode === 'market'
                ? '切换到策略视图查看目标仓位。'
                : '策略产生信号后会出现在这里。'
            }}</span>
          </div>
        </ElScrollbar>
      </aside>
    </div>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
  import {
    fetchMarketCandles,
    fetchMarketSymbols,
    type CandleInterval,
    type MarketCandle,
    type MarketSymbol
  } from '@/api/market'
  import {
    fetchStrategyInstances,
    fetchStrategySignals,
    type StrategyInstanceItem,
    type StrategySignalItem
  } from '@/api/signals'
  import type { KLineDataItem, KLineSignalItem } from '@/types/component/chart'

  defineOptions({ name: 'MarketChartPage' })

  const route = useRoute()
  const intervals: CandleInterval[] = ['1m', '5m', '15m', '1h', '4h', '1d']
  const viewMode = ref<'strategy' | 'market'>('strategy')
  const selectedStrategyId = ref('')
  const selectedInstrumentId = ref('')
  const selectedInterval = ref<CandleInterval>('1h')
  const symbols = ref<MarketSymbol[]>([])
  const strategyInstances = ref<StrategyInstanceItem[]>([])
  const candles = ref<MarketCandle[]>([])
  const signals = ref<StrategySignalItem[]>([])
  const loading = ref(false)

  const selectedStrategy = computed(
    () => strategyInstances.value.find((item) => item.id === selectedStrategyId.value) || null
  )
  const selectedSymbol = computed(
    () => symbols.value.find((item) => item.id === selectedInstrumentId.value) || null
  )
  const latestCandle = computed(() => candles.value.at(-1) || null)
  const latestSignal = computed(() => signals.value[0] || null)
  const changePercent = computed(() => {
    const first = Number(candles.value[0]?.open || 0)
    const last = Number(latestCandle.value?.close || 0)
    return first ? ((last - first) / first) * 100 : 0
  })

  const actionLabel: Record<StrategySignalItem['action'], string> = {
    buy: '买入',
    sell: '卖出',
    flat: '平仓',
    hold: '持有'
  }
  const actionIcon: Record<StrategySignalItem['action'], string> = {
    buy: 'ri:arrow-up-line',
    sell: 'ri:arrow-down-line',
    flat: 'ri:subtract-line',
    hold: 'ri:pause-line'
  }

  const timeKey = (value: string) => String(new Date(value).getTime())
  const axisTime = (value: string) => {
    const date = new Date(value)
    const pad = (part: number) => String(part).padStart(2, '0')
    return `${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
  }
  const numberText = (value: string | number | null | undefined) => {
    const number = Number(value)
    if (value === null || value === undefined || Number.isNaN(number)) return '--'
    return number.toLocaleString('zh-CN', { maximumFractionDigits: 8 })
  }

  const signalByCandle = computed(
    () => new Map(signals.value.map((item) => [timeKey(item.candleOpenTime), item]))
  )
  const chartData = computed<KLineDataItem[]>(() =>
    candles.value.map((item) => {
      const signal = signalByCandle.value.get(timeKey(item.openTime))
      return {
        time: axisTime(item.openTime),
        open: Number(item.open),
        close: Number(item.close),
        high: Number(item.high),
        low: Number(item.low),
        volume: Number(item.baseVolume),
        target: signal ? Number(signal.target) : null
      }
    })
  )
  const candleLabelByKey = computed(
    () => new Map(candles.value.map((item) => [timeKey(item.openTime), axisTime(item.openTime)]))
  )
  const chartSignals = computed<KLineSignalItem[]>(() =>
    [...signals.value].reverse().map((item) => ({
      time:
        candleLabelByKey.value.get(timeKey(item.candleOpenTime)) || axisTime(item.candleOpenTime),
      action: item.action,
      target: Number(item.target),
      previousTarget: Number(item.previousTarget)
    }))
  )

  const loadChart = async () => {
    if (!selectedInstrumentId.value) {
      candles.value = []
      signals.value = []
      return
    }
    loading.value = true
    try {
      const requests: [ReturnType<typeof fetchMarketCandles>, Promise<any>] = [
        fetchMarketCandles({
          instrumentId: selectedInstrumentId.value,
          interval: selectedInterval.value,
          limit: 200
        }),
        viewMode.value === 'strategy' && selectedStrategyId.value
          ? fetchStrategySignals({
              instrumentId: selectedInstrumentId.value,
              strategyInstance: selectedStrategyId.value,
              interval: selectedInterval.value,
              limit: 200
            })
          : Promise.resolve({ records: [] })
      ]
      const [candleResult, signalResult] = await Promise.all(requests)
      candles.value = [...candleResult.records].reverse()
      signals.value = signalResult.records || []
    } finally {
      loading.value = false
    }
  }

  const handleStrategyChange = () => {
    const strategy = selectedStrategy.value
    if (!strategy) return
    selectedInstrumentId.value = strategy.instrumentId
    if (intervals.includes(strategy.interval as CandleInterval)) {
      selectedInterval.value = strategy.interval as CandleInterval
    }
    void loadChart()
  }

  const handleModeChange = () => {
    if (viewMode.value === 'strategy' && selectedStrategy.value) handleStrategyChange()
    else void loadChart()
  }

  onMounted(async () => {
    loading.value = true
    try {
      const [symbolResult, strategyResult] = await Promise.all([
        fetchMarketSymbols({ limit: 200, status: 'trading' }),
        fetchStrategyInstances({ limit: 200 })
      ])
      symbols.value = symbolResult.records
      strategyInstances.value = strategyResult.records
      const queryInstrumentId = String(route.query.instrumentId || '')
      const queryInterval = String(route.query.interval || '') as CandleInterval
      const querySymbol = symbols.value.find((item) => item.id === queryInstrumentId)
      const initialStrategy =
        strategyInstances.value.find((item) => item.isEnabled) || strategyInstances.value[0]
      if (querySymbol) {
        viewMode.value = 'market'
        selectedInstrumentId.value = querySymbol.id
        selectedInterval.value = intervals.includes(queryInterval) ? queryInterval : '1h'
      } else if (initialStrategy) {
        selectedStrategyId.value = initialStrategy.id
        selectedInstrumentId.value = initialStrategy.instrumentId
        selectedInterval.value = intervals.includes(initialStrategy.interval as CandleInterval)
          ? (initialStrategy.interval as CandleInterval)
          : '1h'
      } else {
        viewMode.value = 'market'
        selectedInstrumentId.value = symbols.value[0]?.id || ''
      }
    } finally {
      loading.value = false
    }
    await loadChart()
  })
</script>

<style scoped lang="scss">
  .market-chart-page {
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
    min-height: 100%;
    padding: 20px;
    font-family: inherit;
    color: var(--art-gray-900);
    background: var(--default-bg-color);
  }

  .page-head,
  .panel-head,
  .chart-legend,
  .chart-legend span,
  .signal-target {
    display: flex;
    align-items: center;
  }

  .page-head,
  .panel-head {
    gap: 16px;
    justify-content: space-between;
  }

  h1,
  h2,
  p {
    margin: 0;
  }

  .strategy-option {
    display: flex;
    flex-direction: column;
    line-height: 1.35;
  }

  .strategy-option span {
    font-size: 11px;
    color: var(--el-text-color-secondary);
  }

  .chart-layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 328px;
    gap: 16px;
    min-width: 0;
  }

  .chart-legend {
    flex-wrap: wrap;
    gap: 12px;
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .chart-legend span {
    gap: 5px;
  }

  .signal-count {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    font-family: inherit;
    font-size: 11px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
    border: 0;
    border-radius: 8px;
  }

  .signal-row {
    display: grid;
    grid-template-columns: 38px minmax(0, 1fr) auto;
    gap: 11px;
    align-items: center;
    width: 100%;
    min-height: 70px;
    padding: 12px 8px;
    color: inherit;
    border-bottom: 1px solid var(--art-card-border);
    border-radius: 6px;
    transition: background-color 0.2s;
  }

  .signal-main {
    min-width: 0;
  }

  .signal-main strong,
  .signal-main small {
    display: block;
  }

  .signal-empty {
    display: grid;
    gap: 8px;
    place-items: center;
    min-height: 360px;
    color: var(--art-gray-600);
    text-align: center;
  }

  .signal-empty span {
    max-width: 220px;
    font-size: 11px;
    line-height: 1.6;
  }

  .page-head {
    min-height: 72px;
  }

  .eyebrow {
    display: flex;
    gap: 6px;
    align-items: center;
    font-family: inherit;
    font-size: 11px;
    font-weight: 600;
    color: var(--theme-color);
  }

  .eyebrow :deep(.art-svg-icon) {
    font-size: 15px;
  }

  h1 {
    margin-top: 6px;
    font-size: 28px;
    line-height: 1.3;
  }

  .page-head p {
    color: var(--art-gray-600);
  }

  .filter-card {
    display: flex;
    flex-wrap: wrap;
    gap: 18px;
    align-items: flex-end;
    padding: 16px 18px;
    background: var(--default-box-color);
  }

  .filter-group {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }

  .filter-group--selector {
    flex: 1;
    min-width: 260px;
  }

  .filter-label {
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .strategy-select,
  .symbol-select {
    width: min(100%, 400px);
  }

  .strategy-option strong {
    color: var(--art-gray-900);
  }

  .market-status {
    display: flex;
    gap: 10px;
    align-items: center;
    min-width: 116px;
    min-height: 32px;
    padding-left: 18px;
    margin-left: auto;
    border-left: 1px solid var(--art-card-border);
  }

  .market-status__dot {
    width: 8px;
    height: 8px;
    background: var(--el-color-success);
    border-radius: 50%;
    box-shadow: 0 0 0 4px color-mix(in srgb, var(--el-color-success) 12%, transparent);
  }

  .market-status__dot--offline {
    background: var(--art-gray-500);
    box-shadow: none;
  }

  .market-status strong,
  .market-status span {
    display: block;
  }

  .market-status strong {
    font-size: 12px;
  }

  .market-status span {
    margin-top: 3px;
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .metric-grid {
    display: grid;
    grid-template-columns: minmax(200px, 1.25fr) repeat(4, minmax(132px, 1fr));
    gap: 16px;
  }

  .metric-card {
    display: flex;
    gap: 13px;
    align-items: center;
    min-width: 0;
    min-height: 104px;
    padding: 16px;
    background: var(--default-box-color);
  }

  .metric-icon,
  .panel-icon,
  .signal-action {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    border-radius: 8px;
  }

  .metric-icon {
    width: 40px;
    height: 40px;
    font-size: 20px;
  }

  .metric-icon--primary,
  .metric-icon--price,
  .metric-icon--target {
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .metric-icon--positive {
    color: var(--el-color-success);
    background: color-mix(in srgb, var(--el-color-success) 10%, transparent);
  }

  .metric-icon--negative {
    color: var(--el-color-danger);
    background: color-mix(in srgb, var(--el-color-danger) 10%, transparent);
  }

  .metric-icon--signal {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .metric-copy {
    min-width: 0;
  }

  .metric-copy > span,
  .metric-copy > strong,
  .metric-copy > small {
    display: block;
  }

  .metric-copy > span,
  .metric-copy > small {
    overflow: hidden;
    color: var(--art-gray-600);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .metric-copy > span {
    font-size: 11px;
  }

  .metric-copy > strong {
    max-width: 100%;
    margin-top: 5px;
    overflow: hidden;
    font-size: 18px;
    font-weight: 600;
    line-height: 1.25;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .metric-copy > small {
    margin-top: 4px;
    font-size: 10px;
  }

  .metric-number,
  .target-value {
    font-family: 'Cascadia Code', Consolas, monospace;
    font-size: 15px !important;
  }

  .positive {
    color: var(--el-color-success);
  }

  .negative {
    color: var(--el-color-danger);
  }

  .target-value {
    color: var(--theme-color);
  }

  .chart-panel,
  .signal-rail {
    min-width: 0;
    padding: 18px;
    background: var(--default-box-color);
    border-top: 1px solid var(--art-card-border);
  }

  .panel-head {
    min-height: 42px;
    margin-bottom: 8px;
  }

  .panel-title {
    display: flex;
    gap: 11px;
    align-items: center;
    min-width: 0;
  }

  .panel-icon {
    width: 36px;
    height: 36px;
    font-size: 18px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .panel-icon--signal {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .panel-title h2 {
    margin: 0;
    overflow: hidden;
    font-size: 15px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .panel-title p {
    margin-top: 4px;
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .chart-legend i {
    width: 8px;
    height: 8px;
    border-radius: 2px;
  }

  .legend-up {
    background: var(--el-color-success);
  }

  .legend-down {
    background: var(--el-color-danger);
  }

  .legend-target {
    background: var(--theme-color);
  }

  .signal-scroll {
    height: clamp(440px, 58vh, 620px);
    margin-top: 8px;
  }

  .signal-list {
    padding-left: 0;
  }

  .signal-list::before {
    display: none;
  }

  .signal-row:hover {
    background: var(--art-hover-color);
  }

  .signal-action {
    width: 34px;
    height: 34px;
    font-size: 17px;
    color: var(--art-gray-600);
    background: var(--art-gray-200);
  }

  .signal-row--buy .signal-action {
    color: var(--el-color-success);
    background: color-mix(in srgb, var(--el-color-success) 10%, transparent);
  }

  .signal-row--sell .signal-action {
    color: var(--el-color-danger);
    background: color-mix(in srgb, var(--el-color-danger) 10%, transparent);
  }

  .signal-row--flat .signal-action {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .signal-main strong {
    font-size: 12px;
    color: var(--art-gray-900);
  }

  .signal-main small {
    margin-top: 5px;
    font-family: inherit;
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .signal-target {
    flex-direction: column;
    gap: 5px;
    align-items: flex-end;
    font-family: inherit;
  }

  .signal-target > small {
    font-size: 9px;
    color: var(--art-gray-600);
  }

  .signal-target > span {
    display: flex;
    gap: 4px;
    align-items: center;
    font-family: 'Cascadia Code', Consolas, monospace;
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .signal-target strong {
    color: var(--art-gray-900);
  }

  .signal-empty :deep(.art-svg-icon) {
    font-size: 34px;
    color: var(--el-color-primary-light-4);
  }

  .signal-empty strong {
    color: var(--art-gray-900);
  }

  @media (max-width: 1280px) {
    .metric-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }

    .metric-card--instrument {
      grid-column: span 2;
    }
  }

  @media (max-width: 1100px) {
    .chart-layout {
      grid-template-columns: 1fr;
    }

    .signal-scroll {
      height: 360px;
    }
  }

  @media (max-width: 760px) {
    .market-chart-page {
      padding: 16px;
    }

    .page-head {
      flex-direction: column;
      align-items: flex-start;
    }

    h1 {
      font-size: 24px;
    }

    .filter-card {
      align-items: stretch;
    }

    .filter-group,
    .filter-group--selector,
    .strategy-select,
    .symbol-select {
      width: 100%;
      min-width: 0;
    }

    .market-status {
      width: 100%;
      padding: 12px 0 0;
      margin-left: 0;
      border-top: 1px solid var(--art-card-border);
      border-left: 0;
    }

    .metric-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .metric-card--instrument {
      grid-column: span 2;
    }

    .chart-panel,
    .signal-rail {
      padding: 14px;
    }

    .panel-head {
      align-items: flex-start;
    }

    .chart-legend {
      justify-content: flex-end;
    }
  }

  @media (max-width: 480px) {
    .metric-grid {
      grid-template-columns: 1fr;
    }

    .metric-card--instrument {
      grid-column: auto;
    }

    .interval-control {
      display: grid;
      grid-template-columns: repeat(3, 1fr);
    }

    .signal-row {
      grid-template-columns: 34px minmax(0, 1fr);
    }

    .signal-target {
      grid-column: 2;
      align-items: flex-start;
    }

    .chart-legend {
      display: none;
    }
  }
</style>
