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
      <article class="metric-card">
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

      <article class="metric-card">
        <span class="metric-icon metric-icon--price"><ArtSvgIcon icon="ri:stock-line" /></span>
        <div class="metric-copy">
          <span>最新收盘</span>
          <strong class="metric-number">{{ numberText(latestCandle?.close) }}</strong>
          <small>{{ selectedInterval }} 周期</small>
        </div>
      </article>

      <article class="metric-card">
        <span
          class="metric-icon"
          :class="todayChangePercent >= 0 ? 'metric-icon--positive' : 'metric-icon--negative'"
        >
          <ArtSvgIcon :icon="todayChangePercent >= 0 ? 'ri:arrow-up-line' : 'ri:arrow-down-line'" />
        </span>
        <div class="metric-copy">
          <span>今日涨跌幅</span>
          <strong :class="todayChangePercent >= 0 ? 'positive' : 'negative'">
            {{ todayChangePercent >= 0 ? '+' : '' }}{{ todayChangePercent.toFixed(2) }}%
          </strong>
          <small>UTC+0 自然日</small>
        </div>
      </article>

      <article class="metric-card">
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
          <ElButton class="indicator-settings" size="small" plain @click="openIndicatorSettings">
            <ArtSvgIcon icon="ri:equalizer-2-line" />
            指标参数
          </ElButton>
        </div>
        <ArtKLineChart
          :data="chartData"
          :signals="chartSignals"
          :loading="loading"
          :is-empty="!chartData.length"
          height="clamp(440px, 58vh, 620px)"
          :interval="selectedInterval"
          :intervals="intervals"
          :data-zoom-start="0"
          :main-indicator="mainIndicator"
          :sub-indicator="subIndicator"
          :indicator-config="indicatorConfig"
          @interval-change="handleIntervalChange"
          @main-indicator-change="(value) => (mainIndicator = value)"
          @sub-indicator-change="(value) => (subIndicator = value)"
          @load-more="loadMore"
        />
      </section>

      <aside class="signal-rail art-card" v-loading="signalsLoading">
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

    <ElDialog v-model="indicatorDialogVisible" title="指标参数" width="min(640px, 92vw)">
      <ElForm label-position="top" class="indicator-form">
        <div class="indicator-form__section">
          <strong>主图</strong>
          <div class="indicator-form__grid">
            <ElFormItem label="MA 周期 1"
              ><ElInputNumber v-model="draftIndicatorConfig.maPeriods[0]" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="MA 周期 2"
              ><ElInputNumber v-model="draftIndicatorConfig.maPeriods[1]" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="MA 周期 3"
              ><ElInputNumber v-model="draftIndicatorConfig.maPeriods[2]" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="EMA 周期 1"
              ><ElInputNumber v-model="draftIndicatorConfig.emaPeriods[0]" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="EMA 周期 2"
              ><ElInputNumber v-model="draftIndicatorConfig.emaPeriods[1]" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="EMA 周期 3"
              ><ElInputNumber v-model="draftIndicatorConfig.emaPeriods[2]" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="BOLL 周期"
              ><ElInputNumber v-model="draftIndicatorConfig.bollPeriod" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="BOLL 倍数"
              ><ElInputNumber
                v-model="draftIndicatorConfig.bollMultiplier"
                :min="0.1"
                :max="10"
                :step="0.1"
            /></ElFormItem>
          </div>
        </div>
        <div class="indicator-form__section">
          <strong>副图</strong>
          <div class="indicator-form__grid">
            <ElFormItem label="MACD 快线"
              ><ElInputNumber v-model="draftIndicatorConfig.macdFast" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="MACD 慢线"
              ><ElInputNumber v-model="draftIndicatorConfig.macdSlow" :min="2" :max="120"
            /></ElFormItem>
            <ElFormItem label="MACD 信号"
              ><ElInputNumber v-model="draftIndicatorConfig.macdSignal" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="RSI 周期"
              ><ElInputNumber v-model="draftIndicatorConfig.rsiPeriod" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="KDJ 周期"
              ><ElInputNumber v-model="draftIndicatorConfig.kdjPeriod" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="KDJ K 平滑"
              ><ElInputNumber v-model="draftIndicatorConfig.kdjK" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="KDJ D 平滑"
              ><ElInputNumber v-model="draftIndicatorConfig.kdjD" :min="1" :max="120"
            /></ElFormItem>
            <ElFormItem label="WR 周期"
              ><ElInputNumber v-model="draftIndicatorConfig.wrPeriod" :min="1" :max="120"
            /></ElFormItem>
          </div>
        </div>
      </ElForm>
      <template #footer>
        <ElButton @click="indicatorDialogVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="loading" @click="applyIndicatorSettings"
          >应用并刷新</ElButton
        >
      </template>
    </ElDialog>
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
  import { indicatorQueryParams } from './api'
  import { fetchQuantMarketSignals, type QuantMarketSignal } from '@/plugins/official/quant/api'
  import type {
    KLineDataItem,
    KLineIndicatorConfig,
    KLineSignalItem
  } from '@/types/component/chart'
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
  const indicatorConfig = ref<KLineIndicatorConfig>({
    maPeriods: [7, 25, 99],
    emaPeriods: [7, 25, 99],
    bollPeriod: 20,
    bollMultiplier: 2,
    macdFast: 12,
    macdSlow: 26,
    macdSignal: 9,
    rsiPeriod: 14,
    kdjPeriod: 9,
    kdjK: 3,
    kdjD: 3,
    wrPeriod: 14
  })
  const draftIndicatorConfig = reactive<KLineIndicatorConfig>(
    JSON.parse(JSON.stringify(indicatorConfig.value))
  )
  const indicatorDialogVisible = ref(false)
  const signals = ref<QuantMarketSignal[]>([])
  const signalsLoading = ref(false)
  const socket = ref<WebSocket | null>(null)
  const streamConnected = ref(false)
  let streamReconnectTimer: ReturnType<typeof setTimeout> | null = null
  let loadGeneration = 0

  const selectedSymbol = computed(
    () => symbols.value.find((item) => item.id === selectedInstrumentId.value) || null
  )
  const latestCandle = computed(() => candles.value.at(-1) || null)
  const utcDayStart = (timestamp = Date.now()) => {
    const date = new Date(timestamp)
    return Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate())
  }
  const todayChangePercent = computed(() => {
    const latest = latestCandle.value
    const current = Number(latest?.close)
    if (!latest || !Number.isFinite(current) || current <= 0) return 0

    const dayStart = utcDayStart()
    const openingCandle = candles.value.find((candle) => {
      const openTime = Date.parse(candle.openTime)
      const closeTime = Date.parse(candle.closeTime)
      return (
        Number.isFinite(openTime) &&
        Number.isFinite(closeTime) &&
        openTime <= dayStart &&
        dayStart < closeTime
      )
    })
    const todayCandle = candles.value.find((candle) => Date.parse(candle.openTime) >= dayStart)
    const previousCandle = [...candles.value]
      .reverse()
      .find((candle) => Date.parse(candle.closeTime) <= dayStart)
    const reference = Number(openingCandle?.open ?? todayCandle?.open ?? previousCandle?.close)
    return Number.isFinite(reference) && reference > 0
      ? ((current - reference) / reference) * 100
      : 0
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
  const timeKey = (value: string) => String(Date.parse(value))
  const indicatorText = (value: string) => indicatorLabels[value] || value
  const valueLabel = (key: string) => valueLabels[key] || key
  const valueText = (key: string, value: string) => {
    const text = numberText(value)
    return key.endsWith('Percent') && text !== '--' ? `${text}%` : text
  }
  const signalValues = (signal: QuantMarketSignal) => Object.entries(signal.values || {})
  const candleByClose = computed(
    () => new Map(candles.value.map((item) => [timeKey(item.closeTime), item.openTime] as const))
  )
  const chartSignals = computed<KLineSignalItem[]>(() =>
    signals.value.flatMap((item) => {
      const time = candleByClose.value.get(timeKey(item.candleCloseTime))
      return time
        ? [{ id: item.id, time, name: item.name, summary: item.summary, values: item.values || {} }]
        : []
    })
  )
  const openIndicatorSettings = () => {
    Object.assign(draftIndicatorConfig, JSON.parse(JSON.stringify(indicatorConfig.value)))
    indicatorDialogVisible.value = true
  }
  const applyIndicatorSettings = () => {
    if (draftIndicatorConfig.macdFast >= draftIndicatorConfig.macdSlow) {
      ElMessage.warning('MACD 快线必须小于慢线')
      return
    }
    indicatorConfig.value = JSON.parse(JSON.stringify(draftIndicatorConfig))
    indicatorDialogVisible.value = false
    void loadChart()
  }
  const loadChart = async () => {
    const generation = ++loadGeneration
    const symbol = selectedSymbol.value
    if (!selectedInstrumentId.value || !symbol) {
      candles.value = []
      signals.value = []
      signalsLoading.value = false
      hasMore.value = false
      return
    }
    loading.value = true
    signalsLoading.value = false
    candles.value = []
    signals.value = []
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
        limit: 500,
        indicatorConfig: indicatorConfig.value
      })
      if (generation !== loadGeneration) return
      candles.value = candleResult.records
      nextBefore.value = candleResult.nextCursor
      hasMore.value = candleResult.hasMore
      if (!candles.value.length) {
        connectStream()
        return
      }
      signalsLoading.value = true
      try {
        const signalResult = await fetchQuantMarketSignals({
          market: symbol.market === 'usd_m' ? 'usdm' : 'spot',
          instrument: symbol.nativeSymbol,
          interval: selectedInterval.value,
          startTime: candles.value[0]?.openTime,
          endTime: candles.value.at(-1)?.closeTime,
          limit: 500
        })
        if (generation === loadGeneration) signals.value = signalResult.items
      } catch {
        if (generation === loadGeneration) signals.value = []
      } finally {
        if (generation === loadGeneration) signalsLoading.value = false
      }
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
        limit: 500,
        indicatorConfig: indicatorConfig.value
      })
      if (generation !== loadGeneration) return
      const existing = new Set(candles.value.map((item) => item.openTime))
      candles.value = [
        ...result.records.filter((item) => !existing.has(item.openTime)),
        ...candles.value
      ]
      nextBefore.value = result.nextCursor
      hasMore.value = result.hasMore
      if (result.records.length) {
        try {
          const signalResult = await fetchQuantMarketSignals({
            market: selectedSymbol.value.market === 'usd_m' ? 'usdm' : 'spot',
            instrument: selectedSymbol.value.nativeSymbol,
            interval: selectedInterval.value,
            startTime: result.records[0].openTime,
            endTime: result.records.at(-1)?.closeTime,
            limit: 500
          })
          if (generation === loadGeneration) {
            signals.value = [...signals.value, ...signalResult.items]
              .filter(
                (item, index, items) => items.findIndex((row) => row.id === item.id) === index
              )
              .sort(
                (left, right) =>
                  Date.parse(right.candleCloseTime) - Date.parse(left.candleCloseTime)
              )
          }
        } catch {
          // K 线历史加载不依赖可选的信号列表。
        }
      }
    } finally {
      if (generation === loadGeneration) loadingMore.value = false
    }
  }

  const connectStream = () => {
    const userStore = useUserStore()
    if (!selectedSymbol.value || !userStore.accessToken) return
    const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const symbol = selectedSymbol.value
    const indicatorQuery = new URLSearchParams(
      Object.entries(indicatorQueryParams(indicatorConfig.value) || {}).map(
        ([key, value]) => [key, String(value)] as [string, string]
      )
    )
    const url = `${scheme}://${window.location.host}/api/v1/plugins/official.binance/candles/stream?market=${symbol.market === 'usd_m' ? 'usdm' : 'spot'}&instrument=${encodeURIComponent(symbol.nativeSymbol)}&interval=${selectedInterval.value}&${indicatorQuery.toString()}`
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
    gap: 10px;
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
    display: grid;
    grid-template-columns: minmax(280px, 1fr) auto auto;
    gap: 24px;
    align-items: center;
    padding: 12px 16px;
    background: var(--default-box-color);
  }

  .filter-group {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
  }

  .filter-group--selector {
    width: min(100%, 420px);
  }

  .filter-label {
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .symbol-select {
    width: 100%;
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
    min-width: 216px;
    min-height: 32px;
    padding-left: 20px;
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
    gap: 1px;
    min-width: 0;
    padding: 1px;
    overflow: hidden;
    background: var(--art-card-border);
    border: 1px solid var(--art-card-border);
    border-radius: 8px;
  }

  .metric-card {
    display: flex;
    gap: 12px;
    align-items: center;
    min-width: 0;
    min-height: 72px;
    padding: 10px 18px;
    background: var(--default-box-color);
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

  .metric-icon--signal,
  .panel-icon--signal {
    color: #8b5cf6;
    background: color-mix(in srgb, #8b5cf6 12%, transparent);
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

  .chart-panel {
    min-width: 0;
    padding: 14px 20px 18px;
    background: var(--default-box-color);
  }

  .signal-rail {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    padding: 14px 16px;
    background: var(--default-box-color);
  }

  .signal-count {
    min-width: 24px;
    padding: 2px 7px;
    font-size: 11px;
    color: var(--theme-color);
    text-align: center;
    background: var(--el-color-primary-light-9);
    border-radius: 10px;
  }

  .signal-scroll {
    min-height: 260px;
    max-height: clamp(440px, 58vh, 620px);
  }

  .signal-row {
    display: flex;
    gap: 9px;
    padding: 12px 0;
    border-bottom: 1px solid var(--art-card-border);
  }

  .signal-action {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    width: 26px;
    height: 26px;
    color: #8b5cf6;
    background: color-mix(in srgb, #8b5cf6 12%, transparent);
    border-radius: 50%;
  }

  .signal-content {
    min-width: 0;
  }

  .signal-heading {
    gap: 8px;
    justify-content: space-between;
  }

  .signal-heading strong {
    overflow: hidden;
    font-size: 12px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .signal-heading small,
  .signal-content time,
  .signal-content p,
  .signal-values dt {
    color: var(--art-gray-600);
  }

  .signal-heading small,
  .signal-content time,
  .signal-content p {
    font-size: 10px;
  }

  .signal-content time,
  .signal-content p {
    display: block;
    margin-top: 4px;
  }

  .signal-content p {
    line-height: 1.45;
  }

  .signal-values {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 4px 10px;
    margin-top: 7px;
  }

  .signal-values div {
    display: flex;
    gap: 5px;
    justify-content: space-between;
    min-width: 0;
    font-size: 10px;
  }

  .signal-values dd {
    overflow: hidden;
    font-variant-numeric: tabular-nums;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .signal-empty {
    display: flex;
    flex-direction: column;
    gap: 7px;
    align-items: center;
    justify-content: center;
    min-height: 260px;
    color: var(--art-gray-600);
    text-align: center;
  }

  .signal-empty > svg {
    font-size: 24px;
  }

  .signal-empty strong {
    font-size: 12px;
    color: var(--art-gray-800);
  }

  .indicator-settings {
    flex: 0 0 auto;
  }

  .indicator-form {
    max-height: 60vh;
    overflow: auto;
  }

  .indicator-form__section + .indicator-form__section {
    padding-top: 14px;
    margin-top: 4px;
    border-top: 1px solid var(--art-card-border);
  }

  .indicator-form__section > strong {
    display: block;
    margin-bottom: 10px;
    font-size: 13px;
  }

  .indicator-form__grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 0 12px;
  }

  .indicator-form :deep(.el-form-item) {
    margin-bottom: 12px;
  }

  .indicator-form :deep(.el-input-number) {
    width: 100%;
  }

  .panel-head {
    gap: 14px;
    justify-content: space-between;
    min-height: 38px;
    margin-bottom: 8px;
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
    background: #0ecb81;
  }

  .legend-down {
    background: #f6465d;
  }

  .legend-signal {
    background: #8b5cf6;
  }

  @media (max-width: 1100px) {
    .filter-card {
      grid-template-columns: minmax(220px, 1fr) auto auto;
      gap: 16px;
    }

    .metric-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .chart-layout {
      grid-template-columns: minmax(0, 1fr);
    }

    .signal-scroll {
      max-height: 360px;
    }
  }

  @media (max-width: 760px) {
    .market-chart-page {
      padding: 0;
    }

    .filter-card {
      display: flex;
      flex-wrap: wrap;
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

    .signal-rail {
      padding: 14px;
    }

    .indicator-form__grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 520px) {
    .metric-grid {
      grid-template-columns: 1fr;
    }

    .metric-card {
      min-height: 64px;
    }

    .chart-legend {
      display: none;
    }
  }
</style>
