<template>
  <div class="market-chart-page">
    <section class="filter-card art-card" aria-label="图表筛选">
      <div class="filter-group filter-group--selector">
        <span class="filter-label">交易标的</span>
        <ElSelect
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

      <ElTooltip content="刷新数据" placement="top">
        <ElButton
          class="refresh-button"
          type="primary"
          :icon="Refresh"
          :loading="loading"
          circle
          aria-label="刷新数据"
          @click="loadChart"
        />
      </ElTooltip>
    </section>

    <section class="metric-grid" aria-label="行情摘要">
      <article class="metric-card art-card">
        <span class="metric-icon metric-icon--primary"><ArtSvgIcon icon="ri:coins-line" /></span>
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
        <span class="metric-icon metric-icon--price"><ArtSvgIcon icon="ri:stock-line" /></span>
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
          <small>当前加载区间</small>
        </div>
      </article>

      <article class="metric-card art-card">
        <span class="metric-icon metric-icon--signal"><ArtSvgIcon icon="ri:pulse-line" /></span>
        <div class="metric-copy">
          <span>信号</span>
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
              <h2>{{ selectedSymbol?.nativeSymbol || '行情图表' }}</h2>
              <p>价格 · 成交量 · 信号</p>
            </div>
          </div>
          <div class="chart-legend" aria-label="图例">
            <span><i class="legend-up"></i>上涨</span>
            <span><i class="legend-down"></i>下跌</span>
            <span><i class="legend-signal"></i>信号</span>
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
              <h2>信号</h2>
              <p>按命中时间倒序</p>
            </div>
          </div>
          <span class="signal-count">{{ signals.length }}</span>
        </div>
        <ElScrollbar class="signal-scroll">
          <div v-if="signals.length" class="signal-list">
            <article v-for="item in signals" :key="item.id" class="signal-row">
              <span class="signal-action"><ArtSvgIcon icon="ri:pulse-line" /></span>
              <div class="signal-content">
                <div class="signal-heading">
                  <strong>{{ item.name }}</strong>
                  <small>{{ indicatorText(item.indicator) }}</small>
                </div>
                <time>{{ formatDateTime(item.candleCloseTime) }} UTC+8</time>
                <p>{{ item.summary }}</p>
                <dl v-if="signalValues(item).length" class="signal-values">
                  <div v-for="[key, value] in signalValues(item)" :key="key">
                    <dt>{{ valueLabel(key) }}</dt>
                    <dd>{{ valueText(key, value) }}</dd>
                  </div>
                </dl>
              </div>
            </article>
          </div>
          <div v-else class="signal-empty">
            <ArtSvgIcon icon="ri:pulse-line" />
            <strong>当前区间没有信号</strong>
            <span>工作流输出信号后会显示在对应 K 线上。</span>
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
  import { fetchQuantMarketSignals, type QuantMarketSignal } from '@/api/quant'
  import type { KLineDataItem, KLineSignalItem } from '@/types/component/chart'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'MarketChartPage' })

  const route = useRoute()
  const intervals: CandleInterval[] = ['1m', '5m', '15m', '1h', '4h', '1d']
  const selectedInstrumentId = ref('')
  const selectedInterval = ref<CandleInterval>('1h')
  const symbols = ref<MarketSymbol[]>([])
  const candles = ref<MarketCandle[]>([])
  const signals = ref<QuantMarketSignal[]>([])
  const loading = ref(false)

  const selectedSymbol = computed(
    () => symbols.value.find((item) => item.id === selectedInstrumentId.value) || null
  )
  const latestCandle = computed(() => candles.value.at(-1) || null)
  const changePercent = computed(() => {
    const first = Number(candles.value[0]?.open || 0)
    const last = Number(latestCandle.value?.close || 0)
    return first ? ((last - first) / first) * 100 : 0
  })

  const indicatorLabels: Record<string, string> = {
    volume_spike: '放量',
    price_change: '价格波动',
    macd: 'MACD',
    kdj: 'KDJ',
    rsi: 'RSI',
    bollinger: '布林带'
  }
  const valueLabels: Record<string, string> = {
    changePercent: '涨跌幅',
    amplitudePercent: '振幅',
    volume: '成交量',
    averageVolume: '平均成交量',
    ratio: '倍数',
    dif: 'DIF',
    dea: 'DEA',
    k: 'K',
    d: 'D',
    j: 'J',
    rsi: 'RSI',
    close: '收盘',
    middle: '中轨',
    upper: '上轨',
    lower: '下轨'
  }

  const timeKey = (value: string) => String(new Date(value).getTime())
  const axisTime = (value: string) => formatDateTime(value).slice(5, 16)
  const numberText = (value: string | number | null | undefined) => {
    const number = Number(value)
    if (value === null || value === undefined || Number.isNaN(number)) return '--'
    return number.toLocaleString('zh-CN', { maximumFractionDigits: 8 })
  }
  const indicatorText = (value: string) => indicatorLabels[value] || value
  const valueLabel = (key: string) => valueLabels[key] || key
  const valueText = (key: string, value: string) => {
    const text = numberText(value)
    return key.endsWith('Percent') && text !== '--' ? `${text}%` : text
  }
  const signalValues = (signal: QuantMarketSignal) => Object.entries(signal.values || {})

  const chartData = computed<KLineDataItem[]>(() =>
    candles.value.map((item) => ({
      time: axisTime(item.openTime),
      open: Number(item.open),
      close: Number(item.close),
      high: Number(item.high),
      low: Number(item.low),
      volume: Number(item.baseVolume)
    }))
  )
  const candleLabelByClose = computed(
    () =>
      new Map(
        candles.value.map((item) => [timeKey(item.closeTime), axisTime(item.openTime)] as const)
      )
  )
  const chartSignals = computed<KLineSignalItem[]>(() =>
    signals.value.flatMap((item) => {
      const time = candleLabelByClose.value.get(timeKey(item.candleCloseTime))
      return time
        ? [{ time, name: item.name, summary: item.summary, values: item.values || {} }]
        : []
    })
  )

  const loadChart = async () => {
    const symbol = selectedSymbol.value
    if (!selectedInstrumentId.value || !symbol) {
      candles.value = []
      signals.value = []
      return
    }
    loading.value = true
    candles.value = []
    signals.value = []
    try {
      const candleResult = await fetchMarketCandles({
        instrumentId: selectedInstrumentId.value,
        interval: selectedInterval.value,
        limit: 200
      })
      candles.value = candleResult.records
      if (!candles.value.length) {
        signals.value = []
        return
      }
      const signalResult = await fetchQuantMarketSignals({
        market: symbol.market === 'usd_m' ? 'usdm' : 'spot',
        instrument: symbol.nativeSymbol,
        interval: selectedInterval.value,
        startTime: candles.value[0].openTime,
        endTime: candles.value.at(-1)?.closeTime,
        limit: 500
      })
      signals.value = signalResult.items
    } finally {
      loading.value = false
    }
  }

  onMounted(async () => {
    loading.value = true
    try {
      const result = await fetchMarketSymbols({ limit: 5000, status: 'trading' })
      symbols.value = result.records
      const queryInstrumentId = String(route.query.instrumentId || '')
      const queryInterval = String(route.query.interval || '') as CandleInterval
      selectedInstrumentId.value =
        symbols.value.find((item) => item.id === queryInstrumentId)?.id ||
        symbols.value[0]?.id ||
        ''
      selectedInterval.value = intervals.includes(queryInterval) ? queryInterval : '1h'
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
    color: var(--art-gray-900);
    background: var(--default-bg-color);
  }

  h2,
  p,
  dl,
  dt,
  dd {
    margin: 0;
  }

  .filter-card {
    display: flex;
    flex-wrap: wrap;
    gap: 16px;
    align-items: flex-end;
    padding: 14px 16px;
    background: var(--default-box-color);
  }

  .filter-group {
    display: flex;
    flex-direction: column;
    gap: 7px;
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

  .symbol-select {
    width: min(100%, 400px);
  }

  .market-status,
  .panel-head,
  .panel-title,
  .chart-legend,
  .chart-legend span,
  .signal-heading {
    display: flex;
    align-items: center;
  }

  .market-status {
    gap: 10px;
    min-width: 116px;
    min-height: 32px;
    padding-left: 16px;
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

  .refresh-button {
    flex: 0 0 auto;
    width: 32px;
    height: 32px;
  }

  .metric-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 16px;
  }

  .metric-card {
    display: flex;
    gap: 12px;
    align-items: center;
    min-width: 0;
    min-height: 88px;
    padding: 14px 16px;
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
    width: 38px;
    height: 38px;
    font-size: 19px;
  }

  .metric-icon--primary,
  .metric-icon--price {
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
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .metric-copy > span,
  .metric-copy > small {
    color: var(--art-gray-600);
  }

  .metric-copy > span {
    font-size: 11px;
  }

  .metric-copy > strong {
    margin: 4px 0 2px;
    font-size: 18px;
    line-height: 1.2;
  }

  .metric-copy > small {
    font-size: 10px;
  }

  .metric-number {
    font-variant-numeric: tabular-nums;
  }

  .positive {
    color: var(--el-color-success);
  }

  .negative {
    color: var(--el-color-danger);
  }

  .chart-layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 320px;
    gap: 16px;
    min-width: 0;
  }

  .chart-panel,
  .signal-rail {
    min-width: 0;
    padding: 16px;
    background: var(--default-box-color);
  }

  .signal-rail {
    display: flex;
    flex-direction: column;
    min-height: 0;
  }

  .panel-head {
    gap: 14px;
    justify-content: space-between;
    min-height: 44px;
    margin-bottom: 12px;
  }

  .panel-title {
    gap: 10px;
    min-width: 0;
  }

  .panel-icon,
  .signal-action {
    width: 34px;
    height: 34px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .panel-icon--signal,
  .signal-action {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .panel-title h2 {
    overflow: hidden;
    font-size: 15px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .panel-title p {
    margin-top: 3px;
    font-size: 10px;
    color: var(--art-gray-600);
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

  .chart-legend i {
    width: 8px;
    height: 8px;
    border-radius: 2px;
  }

  .legend-up {
    background: #13deb9;
  }

  .legend-down {
    background: #fa896b;
  }

  .legend-signal {
    background: var(--theme-color);
    border-radius: 50% !important;
  }

  .signal-count {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    width: 30px;
    height: 30px;
    font-size: 11px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
    border-radius: 8px;
  }

  .signal-scroll {
    flex: 1;
    min-height: 0;
  }

  .signal-list {
    padding-right: 4px;
  }

  .signal-row {
    display: grid;
    grid-template-columns: 34px minmax(0, 1fr);
    gap: 10px;
    padding: 12px 4px;
    border-bottom: 1px solid var(--art-card-border);
  }

  .signal-content {
    min-width: 0;
  }

  .signal-heading {
    gap: 8px;
    justify-content: space-between;
  }

  .signal-heading strong {
    min-width: 0;
    overflow: hidden;
    font-size: 12px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .signal-heading small,
  .signal-content time {
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .signal-heading small {
    flex: 0 0 auto;
  }

  .signal-content time {
    display: block;
    margin-top: 4px;
    font-variant-numeric: tabular-nums;
  }

  .signal-content p {
    margin-top: 7px;
    overflow-wrap: anywhere;
    font-size: 11px;
    line-height: 1.55;
    color: var(--art-gray-700);
  }

  .signal-values {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 10px;
    margin-top: 8px;
  }

  .signal-values > div {
    display: flex;
    gap: 4px;
    min-width: 0;
    font-size: 10px;
  }

  .signal-values dt {
    color: var(--art-gray-600);
  }

  .signal-values dd {
    color: var(--art-gray-900);
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }

  .signal-empty {
    display: grid;
    gap: 8px;
    place-items: center;
    min-height: 320px;
    color: var(--art-gray-600);
    text-align: center;
  }

  .signal-empty :deep(.art-svg-icon) {
    font-size: 28px;
    color: var(--el-color-warning);
  }

  .signal-empty strong {
    font-size: 13px;
    color: var(--art-gray-800);
  }

  .signal-empty span {
    max-width: 220px;
    font-size: 11px;
    line-height: 1.6;
  }

  @media (max-width: 1100px) {
    .metric-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 980px) {
    .chart-layout {
      grid-template-columns: 1fr;
    }

    .signal-scroll {
      flex: none;
      height: 360px;
    }
  }

  @media (max-width: 760px) {
    .market-chart-page {
      padding: 16px;
    }

    .filter-card {
      align-items: stretch;
    }

    .filter-group,
    .filter-group--selector,
    .symbol-select {
      width: 100%;
      min-width: 0;
    }

    .market-status {
      flex: 1;
      width: auto;
      padding: 8px 0 0;
      margin-left: 0;
      border-top: 1px solid var(--art-card-border);
      border-left: 0;
    }

    .refresh-button {
      align-self: flex-end;
      margin-top: 8px;
    }

    .chart-panel,
    .signal-rail {
      padding: 14px;
    }
  }

  @media (max-width: 520px) {
    .metric-grid {
      grid-template-columns: 1fr;
    }

    .interval-control {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }

    .chart-legend {
      display: none;
    }
  }
</style>
