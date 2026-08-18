<template>
  <div class="market-chart-page">
    <header class="page-head">
      <div>
        <div class="eyebrow">MARKET DATA / SIGNAL TRACE</div>
        <h1>K 线与策略信号</h1>
        <p>在同一时间轴上检查闭合 K 线、成交量、目标仓位与策略动作。</p>
      </div>
      <ElButton :icon="Refresh" :loading="loading" @click="loadChart">刷新数据</ElButton>
    </header>

    <section class="chart-toolbar">
      <ElRadioGroup v-model="viewMode" @change="handleModeChange">
        <ElRadioButton label="strategy">策略视图</ElRadioButton>
        <ElRadioButton label="market">币种视图</ElRadioButton>
      </ElRadioGroup>

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

      <ElRadioGroup v-model="selectedInterval" class="interval-control" @change="loadChart">
        <ElRadioButton v-for="item in intervals" :key="item" :label="item">
          {{ item }}
        </ElRadioButton>
      </ElRadioGroup>

      <div class="toolbar-meta">
        <span class="live-dot"></span>
        <span>{{ selectedSymbol?.status === 'trading' ? 'TRADING' : 'SUSPENDED' }}</span>
        <span>{{ selectedSymbol?.market === 'usd_m' ? 'USD-M' : 'SPOT' }}</span>
      </div>
    </section>

    <section class="market-tape" aria-label="行情摘要">
      <div class="instrument-title">
        <strong>{{ selectedSymbol?.nativeSymbol || '--' }}</strong>
        <span>{{
          selectedSymbol
            ? `${selectedSymbol.baseAsset} / ${selectedSymbol.quoteAsset}`
            : '等待选择标的'
        }}</span>
      </div>
      <dl>
        <div>
          <dt>最新收盘</dt>
          <dd>{{ numberText(latestCandle?.close) }}</dd>
        </div>
        <div>
          <dt>区间变化</dt>
          <dd :class="changePercent >= 0 ? 'positive' : 'negative'">
            {{ changePercent >= 0 ? '+' : '' }}{{ changePercent.toFixed(2) }}%
          </dd>
        </div>
        <div>
          <dt>目标仓位</dt>
          <dd class="violet">{{ numberText(latestSignal?.target) }}</dd>
        </div>
        <div>
          <dt>信号数量</dt>
          <dd>{{ signals.length }}</dd>
        </div>
      </dl>
    </section>

    <div class="chart-layout">
      <section class="chart-panel">
        <div class="panel-head">
          <div>
            <div class="eyebrow">PRICE / VOLUME / TARGET</div>
            <h2>{{ selectedStrategy?.name || selectedSymbol?.nativeSymbol || '行情图表' }}</h2>
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
          height="clamp(430px, 58vh, 660px)"
        />
      </section>

      <aside class="signal-rail">
        <div class="panel-head">
          <div>
            <div class="eyebrow">SIGNAL TIMELINE</div>
            <h2>信号轨道</h2>
          </div>
          <span class="signal-count">{{ signals.length }}</span>
        </div>
        <ElScrollbar class="signal-scroll">
          <div v-if="signals.length" class="signal-list">
            <button
              v-for="item in signals"
              :key="item.id"
              type="button"
              class="signal-row"
              :class="`signal-row--${item.action}`"
            >
              <span class="signal-node"></span>
              <span class="signal-main">
                <strong>{{ actionLabel[item.action] }}</strong>
                <small>{{ item.candleOpenTime }}</small>
              </span>
              <span class="signal-target">
                <small>{{ item.previousTarget }}</small>
                <ArtSvgIcon icon="ri:arrow-right-line" />
                <strong>{{ item.target }}</strong>
              </span>
            </button>
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
    buy: 'BUY',
    sell: 'SELL',
    flat: 'FLAT',
    hold: 'HOLD'
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
      const initialStrategy =
        strategyInstances.value.find((item) => item.isEnabled) || strategyInstances.value[0]
      if (initialStrategy) {
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
    --ink: #17191b;
    --muted: #70777b;
    --paper: #e8e7e2;
    --panel: #f4f3ee;
    --line: #c9c9c2;
    --strong-line: #17191b;
    --inverse-panel: #17191b;
    --acid: #c7f46b;
    --signal: #ff705b;
    --violet: #9e8cff;

    display: flex;
    flex-direction: column;
    gap: 18px;
    min-width: 0;
    padding: 24px 28px 32px;
    font-family: 'Space Grotesk', 'PingFang SC', 'Microsoft YaHei', sans-serif;
    color: var(--ink);
    background: var(--paper);
  }

  :global(html.dark .market-chart-page) {
    --ink: #eff4f1;
    --muted: #9da6aa;
    --paper: #0d0f10;
    --panel: #181b1e;
    --line: #343a3d;
    --strong-line: #697276;
    --inverse-panel: #111315;
  }

  .page-head,
  .chart-toolbar,
  .market-tape,
  .market-tape dl,
  .panel-head,
  .chart-legend,
  .chart-legend span,
  .toolbar-meta,
  .signal-target {
    display: flex;
    align-items: center;
  }

  .page-head,
  .market-tape,
  .panel-head {
    gap: 20px;
    justify-content: space-between;
  }

  .eyebrow,
  .market-tape dt,
  .market-tape dd,
  .toolbar-meta,
  .signal-target,
  .signal-main small {
    font-family: 'IBM Plex Mono', 'Cascadia Code', Consolas, monospace;
  }

  .eyebrow {
    font-size: 10px;
    color: var(--muted);
    letter-spacing: 0;
  }

  h1,
  h2,
  p {
    margin: 0;
  }

  h1 {
    margin-top: 7px;
    font-size: 34px;
    font-weight: 600;
    letter-spacing: 0;
  }

  h2 {
    margin-top: 5px;
    font-size: 17px;
    font-weight: 600;
  }

  .page-head p {
    margin-top: 7px;
    font-size: 13px;
    color: var(--muted);
  }

  .chart-toolbar {
    flex-wrap: wrap;
    gap: 9px;
    padding: 12px;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 2px;
  }

  .strategy-select {
    width: min(360px, 100%);
  }

  .symbol-select {
    width: min(260px, 100%);
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

  .toolbar-meta {
    gap: 8px;
    margin-left: auto;
    font-size: 9px;
    color: var(--muted);
  }

  .toolbar-meta span + span {
    padding-left: 8px;
    border-left: 1px solid var(--line);
  }

  .live-dot {
    width: 7px;
    height: 7px;
    padding: 0 !important;
    background: #5eaa74;
    border: 0 !important;
    border-radius: 50%;
  }

  .market-tape {
    padding: 14px 17px;
    color: #eff4f1;
    background: var(--inverse-panel);
    border: 1px solid var(--strong-line);
    border-radius: 2px;
  }

  .instrument-title strong,
  .instrument-title span {
    display: block;
  }

  .instrument-title strong {
    font-family: 'IBM Plex Mono', Consolas, monospace;
    font-size: 17px;
  }

  .instrument-title span {
    margin-top: 4px;
    font-size: 10px;
    color: #899297;
  }

  .market-tape dl {
    flex-wrap: wrap;
    margin: 0;
  }

  .market-tape dl div {
    min-width: 120px;
    padding: 0 17px;
    border-left: 1px solid #30363a;
  }

  .market-tape dt {
    font-size: 9px;
    color: #899297;
  }

  .market-tape dd {
    margin: 5px 0 0;
    font-size: 13px;
  }

  .positive {
    color: var(--acid);
  }

  .negative {
    color: var(--signal);
  }

  .violet {
    color: var(--violet);
  }

  .chart-layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 300px;
    gap: 24px;
    min-width: 0;
  }

  .chart-panel,
  .signal-rail {
    min-width: 0;
    padding-top: 13px;
    border-top: 2px solid var(--strong-line);
  }

  .chart-legend {
    flex-wrap: wrap;
    gap: 14px;
    font-size: 10px;
    color: var(--muted);
  }

  .chart-legend span {
    gap: 5px;
  }

  .chart-legend i {
    width: 16px;
    height: 3px;
  }

  .legend-up {
    background: #5eaa74;
  }

  .legend-down {
    background: var(--signal);
  }

  .legend-target {
    background: var(--violet);
  }

  .signal-count {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    font-family: 'IBM Plex Mono', Consolas, monospace;
    font-size: 10px;
    border: 1px solid var(--line);
  }

  .signal-scroll {
    height: clamp(430px, 58vh, 660px);
    margin-top: 10px;
  }

  .signal-list {
    position: relative;
    padding-left: 14px;
  }

  .signal-list::before {
    position: absolute;
    top: 8px;
    bottom: 8px;
    left: 18px;
    width: 1px;
    content: '';
    background: var(--line);
  }

  .signal-row {
    position: relative;
    display: grid;
    grid-template-columns: 12px minmax(0, 1fr) auto;
    gap: 10px;
    align-items: center;
    width: 100%;
    padding: 12px 6px 12px 0;
    color: inherit;
    text-align: left;
    cursor: default;
    background: transparent;
    border: 0;
    border-bottom: 1px solid var(--line);
  }

  .signal-node {
    z-index: 1;
    width: 9px;
    height: 9px;
    background: #8c9296;
    border: 2px solid var(--paper);
    border-radius: 50%;
    outline: 1px solid #8c9296;
  }

  .signal-row--buy .signal-node {
    background: var(--acid);
    outline-color: #66843a;
  }

  .signal-row--sell .signal-node {
    background: var(--signal);
    outline-color: var(--signal);
  }

  .signal-row--flat .signal-node {
    background: #eab24d;
    outline-color: #9b752d;
  }

  .signal-main {
    min-width: 0;
  }

  .signal-main strong,
  .signal-main small {
    display: block;
  }

  .signal-main strong {
    font-size: 11px;
  }

  .signal-main small {
    margin-top: 4px;
    overflow: hidden;
    font-size: 9px;
    color: var(--muted);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .signal-target {
    gap: 5px;
    font-size: 10px;
  }

  .signal-target small {
    color: var(--muted);
  }

  .signal-empty {
    display: grid;
    gap: 8px;
    place-items: center;
    min-height: 280px;
    color: var(--muted);
    text-align: center;
  }

  .signal-empty :deep(svg) {
    font-size: 30px;
  }

  .signal-empty strong {
    font-size: 12px;
    color: var(--ink);
  }

  .signal-empty span {
    max-width: 220px;
    font-size: 11px;
  }

  @media (max-width: 1040px) {
    .chart-layout {
      grid-template-columns: 1fr;
    }

    .signal-scroll {
      height: 360px;
    }
  }

  @media (max-width: 720px) {
    .market-chart-page {
      padding: 18px 16px 24px;
    }

    .page-head,
    .market-tape {
      flex-direction: column;
      align-items: flex-start;
    }

    h1 {
      font-size: 26px;
    }

    .chart-toolbar > :deep(*) {
      max-width: 100%;
    }

    .toolbar-meta {
      width: 100%;
      margin-left: 0;
    }

    .market-tape dl div:first-child {
      padding-left: 0;
      border-left: 0;
    }

    .interval-control {
      display: grid;
      grid-template-columns: repeat(3, 1fr);
      width: 100%;
    }
  }
</style>
