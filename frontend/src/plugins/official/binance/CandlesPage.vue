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

      <div class="market-status">
        <span
          class="market-status__dot"
          :class="{
            'market-status__dot--offline': selectedSymbol?.status !== 'trading' || !streamConnected
          }"
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
          <span>
            {{ selectedSymbol?.market === 'usd_m' ? 'USD-M 合约' : '现货市场' }} ·
            {{ streamConnected ? '实时行情已连接' : '实时行情重连中' }}
          </span>
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
    </section>

    <div class="chart-layout">
      <section class="chart-panel art-card">
        <div class="panel-head">
          <div class="panel-title">
            <span class="panel-icon"><ArtSvgIcon icon="ri:candlestick-chart-line" /></span>
            <div>
              <h2>{{ selectedSymbol?.nativeSymbol || '行情图表' }}</h2>
              <p>价格 · 成交量</p>
            </div>
          </div>
          <div class="chart-legend" aria-label="图例">
            <span><i class="legend-up"></i>上涨</span>
            <span><i class="legend-down"></i>下跌</span>
          </div>
        </div>
        <ArtKLineChart
          :data="chartData"
          :loading="loading"
          :is-empty="!chartData.length"
          height="clamp(440px, 58vh, 620px)"
          :interval="selectedInterval"
          :intervals="intervals"
          :main-indicator="mainIndicator"
          :sub-indicator="subIndicator"
          @interval-change="handleIntervalChange"
          @main-indicator-change="(value) => (mainIndicator = value)"
          @sub-indicator-change="(value) => (subIndicator = value)"
          @load-more="loadMore"
        />
      </section>
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
  } from './market-api'
  import type { KLineDataItem } from '@/types/component/chart'
  import { formatDateTime } from '@/utils/date'
  import { useUserStore } from '@/store/modules/user'

  defineOptions({ name: 'MarketChartPage' })

  const route = useRoute()
  const intervals: CandleInterval[] = [
    '1m',
    '3m',
    '5m',
    '15m',
    '30m',
    '1h',
    '2h',
    '4h',
    '6h',
    '8h',
    '12h',
    '1d',
    '3d',
    '1w'
  ]
  const selectedInstrumentId = ref('')
  const selectedInterval = ref<CandleInterval>('1h')
  const symbols = ref<MarketSymbol[]>([])
  const candles = ref<MarketCandle[]>([])
  const loading = ref(false)
  const loadingMore = ref(false)
  const nextBefore = ref('')
  const hasMore = ref(false)
  const mainIndicator = ref<'none' | 'ma' | 'ema' | 'boll'>('none')
  const subIndicator = ref<'volume' | 'macd' | 'rsi' | 'kdj' | 'obv' | 'wr'>('volume')
  const socket = ref<WebSocket | null>(null)
  const streamConnected = ref(false)
  let streamReconnectTimer: ReturnType<typeof setTimeout> | null = null
  let loadGeneration = 0

  const selectedSymbol = computed(
    () => symbols.value.find((item) => item.id === selectedInstrumentId.value) || null
  )
  const latestCandle = computed(() => candles.value.at(-1) || null)
  const changePercent = computed(() => {
    const first = Number(candles.value[0]?.open || 0)
    const last = Number(latestCandle.value?.close || 0)
    return first ? ((last - first) / first) * 100 : 0
  })

  const axisTime = (value: string) => formatDateTime(value).slice(5, 16)
  const numberText = (value: string | number | null | undefined) => {
    const number = Number(value)
    if (value === null || value === undefined || Number.isNaN(number)) return '--'
    return number.toLocaleString('zh-CN', { maximumFractionDigits: 8 })
  }

  const chartData = computed<KLineDataItem[]>(() =>
    candles.value.map((item) => ({
      time: item.openTime,
      label: axisTime(item.openTime),
      open: Number(item.open),
      close: Number(item.close),
      high: Number(item.high),
      low: Number(item.low),
      volume: Number(item.baseVolume),
      indicators: item.indicators
    }))
  )
  const loadChart = async () => {
    const generation = ++loadGeneration
    const symbol = selectedSymbol.value
    if (!selectedInstrumentId.value || !symbol) {
      candles.value = []
      hasMore.value = false
      return
    }
    loading.value = true
    candles.value = []
    nextBefore.value = ''
    if (streamReconnectTimer) clearTimeout(streamReconnectTimer)
    streamReconnectTimer = null
    const previousSocket = socket.value
    socket.value = null
    streamConnected.value = false
    previousSocket?.close()
    try {
      const candleResult = await fetchMarketCandles({
        instrumentId: selectedInstrumentId.value,
        interval: selectedInterval.value,
        limit: 500
      })
      if (generation !== loadGeneration) return
      candles.value = candleResult.records
      nextBefore.value = candleResult.nextCursor
      hasMore.value = candleResult.hasMore
      connectStream()
    } catch {
      if (generation === loadGeneration) {
        candles.value = []
        nextBefore.value = ''
        hasMore.value = false
      }
    } finally {
      if (generation === loadGeneration) loading.value = false
    }
  }

  const handleIntervalChange = (value: string) => {
    if (intervals.includes(value as CandleInterval)) {
      selectedInterval.value = value as CandleInterval
      void loadChart()
    }
  }

  const loadMore = async () => {
    if (loadingMore.value || !hasMore.value || !nextBefore.value || !selectedSymbol.value) return
    const generation = loadGeneration
    loadingMore.value = true
    try {
      const result = await fetchMarketCandles({
        instrumentId: selectedInstrumentId.value,
        interval: selectedInterval.value,
        endTime: nextBefore.value,
        limit: 500
      })
      if (generation !== loadGeneration) return
      const existing = new Set(candles.value.map((item) => item.openTime))
      candles.value = [
        ...result.records.filter((item) => !existing.has(item.openTime)),
        ...candles.value
      ]
      nextBefore.value = result.nextCursor
      hasMore.value = result.hasMore
    } finally {
      if (generation === loadGeneration) loadingMore.value = false
    }
  }

  const connectStream = () => {
    const userStore = useUserStore()
    if (!selectedSymbol.value || !userStore.accessToken) return
    const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const symbol = selectedSymbol.value
    const url = `${scheme}://${window.location.host}/api/v1/plugins/official.binance/candles/stream?market=${symbol.market === 'usd_m' ? 'usdm' : 'spot'}&instrument=${encodeURIComponent(symbol.nativeSymbol)}&interval=${selectedInterval.value}`
    const current = new WebSocket(url, [
      'coinsphere.plugin.official.binance.v1',
      userStore.accessToken
    ])
    socket.value = current
    current.onopen = () => {
      if (socket.value !== current) {
        current.close()
        return
      }
      if (current.protocol !== 'coinsphere.plugin.official.binance.v1') {
        current.close(1002, 'unexpected websocket protocol')
        return
      }
      streamConnected.value = true
    }
    current.onmessage = (event) => {
      try {
        const envelope = JSON.parse(event.data)
        const item = envelope?.data
        if (envelope?.type !== 'kline' || !item?.openTime || socket.value !== current) return
        const next = {
          instrumentId: selectedInstrumentId.value,
          interval: selectedInterval.value,
          openTime: item.openTime,
          closeTime: item.closeTime,
          open: item.open,
          high: item.high,
          low: item.low,
          close: item.close,
          baseVolume: item.volume,
          isClosed: Boolean(item.closed),
          indicators: item.indicators
            ? {
                main: Object.fromEntries(
                  Object.entries(item.indicators.main || {}).map(([key, value]) => [
                    key,
                    value === null ? null : Number(value)
                  ])
                ),
                sub: Object.fromEntries(
                  Object.entries(item.indicators.sub || {}).map(([key, value]) => [
                    key,
                    value === null ? null : Number(value)
                  ])
                )
              }
            : undefined
        } as MarketCandle
        const index = candles.value.findIndex((candle) => candle.openTime === next.openTime)
        if (index >= 0) {
          candles.value.splice(index, 1, next)
        } else {
          const insertAt = candles.value.findIndex((candle) => candle.openTime > next.openTime)
          if (insertAt < 0) candles.value.push(next)
          else candles.value.splice(insertAt, 0, next)
        }
      } catch {
        // Ignore malformed market frames; the next REST refresh repairs the view.
      }
    }
    current.onclose = () => {
      if (socket.value !== current) return
      socket.value = null
      streamConnected.value = false
      streamReconnectTimer = setTimeout(() => {
        streamReconnectTimer = null
        connectStream()
      }, 3000)
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

  onBeforeUnmount(() => {
    if (streamReconnectTimer) clearTimeout(streamReconnectTimer)
    socket.value?.close()
    socket.value = null
    streamConnected.value = false
  })
</script>

<style scoped lang="scss">
  .market-chart-page {
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    gap: 12px;
    min-width: 0;
    min-height: 100%;
    padding: 0;
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
  .chart-legend span {
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
    display: flex;
    flex-wrap: wrap;
    min-width: 0;
    border-top: 1px solid var(--art-card-border);
    border-bottom: 1px solid var(--art-card-border);
  }

  .metric-card {
    display: flex;
    gap: 12px;
    align-items: center;
    flex: 1 1 220px;
    min-width: 0;
    min-height: 66px;
    padding: 8px 16px;
    background: transparent;
    border-right: 1px solid var(--art-card-border);
  }

  .metric-card:last-child {
    border-right: 0;
  }

  .metric-icon,
  .panel-icon {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    border-radius: 8px;
  }

  .metric-icon {
    width: 30px;
    height: 30px;
    font-size: 16px;
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
    grid-template-columns: minmax(0, 1fr);
    gap: 16px;
    min-width: 0;
  }

  .chart-panel {
    min-width: 0;
    padding: 16px;
    background: var(--default-box-color);
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

  .panel-icon {
    width: 34px;
    height: 34px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
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

  @media (max-width: 1100px) {
    .metric-card:nth-child(2) {
      border-right: 0;
    }
  }

  @media (max-width: 760px) {
    .market-chart-page {
      padding: 0;
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

    .chart-panel {
      padding: 14px;
    }
  }

  @media (max-width: 520px) {
    .metric-card {
      flex-basis: 100%;
      border-right: 0;
    }

    .chart-legend {
      display: none;
    }
  }
</style>
