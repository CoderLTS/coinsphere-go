<template>
  <div class="backtest-analysis art-full-height" v-loading="loading">
    <ElResult v-if="loadError" icon="warning" title="回测分析加载失败" :sub-title="loadError">
      <template #extra>
        <ElSpace>
          <ElButton @click="backToDetail">返回执行详情</ElButton>
          <ElButton type="primary" :icon="Refresh" @click="loadPage">重新加载</ElButton>
        </ElSpace>
      </template>
    </ElResult>

    <template v-else-if="run && detail">
      <header class="analysis-header">
        <div class="analysis-header__main">
          <ElTooltip content="返回执行详情" placement="bottom">
            <ElButton circle plain :icon="ArrowLeft" @click="backToDetail" />
          </ElTooltip>
          <div>
            <h1>{{ workflowName }}</h1>
            <p>Run #{{ run.id }} · {{ detail.instrument }} · {{ detail.interval }}</p>
          </div>
        </div>
        <ElSpace>
          <ElTag type="info" effect="plain">UTC+8</ElTag>
          <ElTooltip content="刷新" placement="bottom">
            <ElButton circle plain :icon="Refresh" @click="loadPage" />
          </ElTooltip>
        </ElSpace>
      </header>

      <div class="analysis-range">
        <span>{{ detail.market === 'usdm' ? 'USD-M' : 'Spot' }}</span>
        <span>{{ formatDateTime(rangeStart) }} 至 {{ formatDateTime(rangeEnd) }}</span>
        <span>完成于 {{ formatDateTime(run.completedAt) }}</span>
      </div>

      <section class="metric-strip" aria-label="回测摘要">
        <div v-for="item in metrics" :key="item.label" class="metric-strip__item">
          <span>{{ item.label }}</span>
          <strong :data-tone="item.tone">{{ item.value }}</strong>
        </div>
      </section>

      <section class="analysis-section">
        <div class="section-heading">
          <div
            ><h2>K 线与成交信号</h2><p>共 {{ candles.length }} 根 K 线</p></div
          >
          <div class="signal-legend"
            ><span data-action="buy">买入</span><span data-action="sell">卖出</span></div
          >
        </div>
        <ArtKLineChart
          :data="chartData"
          :signals="chartSignals"
          :selected-signal-id="selectedPointId"
          :is-empty="!chartData.length"
          height="clamp(360px, 48vh, 520px)"
          :interval="detail.interval"
          :intervals="[detail.interval]"
          fixed-interval
          :main-indicator="mainIndicator"
          :sub-indicator="subIndicator"
          @main-indicator-change="(value) => (mainIndicator = value)"
          @sub-indicator-change="(value) => (subIndicator = value)"
          @signal-click="selectPoint"
        />
      </section>

      <div class="analysis-lower">
        <section class="analysis-section">
          <div class="section-heading"
            ><div
              ><h2>权益曲线</h2><p>共 {{ detail.points.length }} 个回测点</p></div
            ></div
          >
          <ArtLineChart
            :data="equitySeries.data"
            :x-axis-data="equitySeries.labels"
            :is-empty="!equitySeries.data.length"
            :show-area-color="true"
            :show-axis-line="false"
            :smooth="false"
            :colors="['#2563eb']"
            height="300px"
          />
        </section>

        <aside class="signal-detail" aria-label="选中成交信号">
          <template v-if="selectedPoint">
            <div class="section-heading"
              ><div
                ><h2>成交详情</h2><p>#{{ selectedPoint.id + 1 }}</p></div
              ></div
            >
            <dl>
              <div
                ><dt>方向</dt
                ><dd :data-action="selectedPoint.action">{{
                  actionLabel(selectedPoint.action)
                }}</dd></div
              >
              <div
                ><dt>信号时间</dt><dd>{{ formatDateTime(selectedPoint.evaluatedAt) }}</dd></div
              >
              <div
                ><dt>成交时间</dt
                ><dd>{{ formatDateTime(selectedPoint.executionOpenTime) }}</dd></div
              >
              <div
                ><dt>成交价格</dt><dd>{{ decimalText(selectedPoint.executionPrice, 8) }}</dd></div
              >
              <div
                ><dt>目标仓位</dt><dd>{{ decimalText(selectedPoint.targetPosition, 4) }}</dd></div
              >
              <div
                ><dt>数量变化</dt><dd>{{ decimalText(selectedPoint.quantityDelta, 8) }}</dd></div
              >
              <div
                ><dt>手续费</dt><dd>{{ decimalText(selectedPoint.fee, 4) }}</dd></div
              >
              <div
                ><dt>权益</dt><dd>{{ decimalText(selectedPoint.equity) }}</dd></div
              >
            </dl>
          </template>
          <ElEmpty v-else description="暂无成交信号" />
        </aside>
      </div>

      <section class="analysis-section">
        <div class="section-heading"
          ><div
            ><h2>成交记录</h2><p>共 {{ tradePoints.length }} 条</p></div
          ></div
        >
        <ElTable
          :data="tradePoints"
          height="340"
          size="small"
          empty-text="暂无成交记录"
          :row-class-name="tradeRowClass"
          @row-click="handleRowClick"
        >
          <ElTableColumn label="方向" width="84" align="center">
            <template #default="{ row }"
              ><span class="action" :data-action="row.action">{{
                actionLabel(row.action)
              }}</span></template
            >
          </ElTableColumn>
          <ElTableColumn label="信号时间（UTC+8）" min-width="178"
            ><template #default="{ row }">{{
              formatDateTime(row.evaluatedAt)
            }}</template></ElTableColumn
          >
          <ElTableColumn label="成交时间（UTC+8）" min-width="178"
            ><template #default="{ row }">{{
              formatDateTime(row.executionOpenTime)
            }}</template></ElTableColumn
          >
          <ElTableColumn label="成交价格" min-width="130" align="right"
            ><template #default="{ row }">{{
              decimalText(row.executionPrice, 8)
            }}</template></ElTableColumn
          >
          <ElTableColumn label="目标仓位" min-width="110" align="right"
            ><template #default="{ row }">{{
              decimalText(row.targetPosition, 4)
            }}</template></ElTableColumn
          >
          <ElTableColumn label="手续费" min-width="110" align="right"
            ><template #default="{ row }">{{ decimalText(row.fee, 4) }}</template></ElTableColumn
          >
          <ElTableColumn label="权益" min-width="130" align="right"
            ><template #default="{ row }">{{ decimalText(row.equity) }}</template></ElTableColumn
          >
        </ElTable>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
  import { ArrowLeft, Refresh } from '@element-plus/icons-vue'
  import {
    fetchQuantBacktestDetail,
    type QuantBacktestDetail,
    type QuantBacktestPoint,
    type QuantCandle
  } from '@/plugins/official/quant/api'
  import { fetchWorkflowRun, type WorkflowRunDetail } from '@/api/workflows'
  import type { KLineDataItem, KLineSignalItem } from '@/types/component/chart'
  import { formatDateTime } from '@/utils/date'
  import { decimalFixed, decimalPercent } from '@/plugins/official/quant/decimal'
  import { fetchBinanceIndicators } from '@/plugins/official/binance/api'

  defineOptions({ name: 'SchedulerWorkflowBacktestAnalysisPage' })

  type BacktestAction = 'buy' | 'sell' | 'hold'
  type BacktestPoint = QuantBacktestPoint & { id: number }
  type BacktestDetail = Omit<QuantBacktestDetail, 'points'> & { points: BacktestPoint[] }
  type ChartCandle = QuantCandle & {
    indicators?: { main: Record<string, number | null>; sub: Record<string, number | null> }
  }

  const decimalPattern = /^-?\d+(?:\.\d+)?$/
  const route = useRoute()
  const router = useRouter()
  const loading = ref(false)
  const loadError = ref('')
  const run = ref<WorkflowRunDetail | null>(null)
  const detail = ref<BacktestDetail | null>(null)
  const candles = ref<ChartCandle[]>([])
  const selectedPointId = ref<number>()
  const mainIndicator = ref<'none' | 'ma' | 'ema' | 'boll'>('none')
  const subIndicator = ref<'volume' | 'macd' | 'rsi' | 'kdj' | 'obv' | 'wr'>('volume')

  const workflowName = computed(() => String(route.query.workflowName || '工作流回测'))
  const resultSummary = computed<Record<string, unknown>>(() => {
    const node = run.value?.runNodes.find(
      (item) => isRecord(item.outputSummary) && 'backtestId' in item.outputSummary
    )
    const output = run.value?.resultSummary?.output
    return node?.outputSummary || (isRecord(output) ? output : {})
  })
  const rangeStart = computed(() =>
    String(detail.value?.parameters.startTime || run.value?.input.startTime || '')
  )
  const rangeEnd = computed(() =>
    String(detail.value?.parameters.endTime || run.value?.input.endTime || '')
  )
  const tradePoints = computed(() =>
    (detail.value?.points || []).filter((point) => point.action !== 'hold')
  )
  const selectedPoint = computed(
    () => tradePoints.value.find((point) => point.id === selectedPointId.value) || null
  )
  const isRecord = (value: unknown): value is Record<string, unknown> =>
    Boolean(value) && typeof value === 'object' && !Array.isArray(value)

  const percentText = (value: unknown) => {
    const raw = String(value ?? '')
    return decimalPattern.test(raw) ? `${decimalPercent(raw)}%` : '--'
  }
  const decimalText = (value: unknown, places = 2) => {
    const raw = String(value ?? '')
    return decimalPattern.test(raw) ? decimalFixed(raw, places) : '--'
  }
  const metrics = computed(() => {
    const summary = resultSummary.value
    const returnValue = String(summary.totalReturn ?? '')
    return [
      {
        label: '总收益率',
        value: percentText(returnValue),
        tone: returnValue.startsWith('-') ? 'negative' : 'positive'
      },
      { label: '最大回撤', value: percentText(summary.maxDrawdown), tone: 'negative' },
      { label: '最终权益', value: decimalText(summary.finalEquity), tone: 'neutral' },
      { label: '成交次数', value: String(summary.tradeCount ?? '--'), tone: 'neutral' },
      { label: '总手续费', value: decimalText(summary.totalFees), tone: 'neutral' },
      { label: 'K 线数量', value: String(summary.candleCount ?? '--'), tone: 'neutral' }
    ]
  })

  const axisTime = (value: string) => formatDateTime(value).slice(5, 16)
  const timeKey = (value: string) => String(Date.parse(value))
  const chartData = computed<KLineDataItem[]>(() =>
    candles.value.map((item) => ({
      time: item.openTime,
      label: axisTime(item.openTime),
      open: Number(item.open),
      close: Number(item.close),
      high: Number(item.high),
      low: Number(item.low),
      volume: Number(item.volume),
      indicators: item.indicators
    }))
  )
  const candleLabels = computed(
    () => new Map(candles.value.map((item) => [timeKey(item.openTime), item.openTime]))
  )
  const chartSignals = computed<KLineSignalItem[]>(() =>
    tradePoints.value.flatMap((point) => {
      const time = candleLabels.value.get(timeKey(point.executionOpenTime))
      return time
        ? [
            {
              id: point.id,
              time,
              action: point.action,
              name: actionLabel(point.action),
              summary: `目标仓位 ${point.targetPosition}`,
              values: {}
            }
          ]
        : []
    })
  )
  const equitySeries = computed(() => {
    const rows = (detail.value?.points || []).filter((point) =>
      Number.isFinite(Number(point.equity))
    )
    return {
      labels: rows.map((point) => axisTime(point.evaluatedAt)),
      data: rows.map((point) => Number(point.equity))
    }
  })

  const actionLabel = (action: BacktestAction) =>
    ({ buy: '买入', sell: '卖出', hold: '持有' })[action]
  const selectPoint = (id: string | number) => {
    selectedPointId.value = Number(id)
  }
  const handleRowClick = (row: BacktestPoint) => selectPoint(row.id)
  const tradeRowClass = ({ row }: { row: BacktestPoint }) =>
    row.id === selectedPointId.value ? 'is-selected' : ''
  const backToDetail = () =>
    router.push({ path: `/scheduler/execution/${route.params.runId}/detail`, query: route.query })

  const loadPage = async () => {
    const runId = Number(route.params.runId)
    if (!Number.isSafeInteger(runId) || runId <= 0) {
      loadError.value = '运行编号无效'
      return
    }
    loading.value = true
    loadError.value = ''
    try {
      const currentRun = await fetchWorkflowRun(runId)
      if (currentRun.entryPoint !== 'backtest') throw new Error('该运行不是回测任务')
      const nodeOutput = currentRun.runNodes.find(
        (item) => isRecord(item.outputSummary) && 'backtestId' in item.outputSummary
      )?.outputSummary
      const resultOutput = currentRun.resultSummary.output
      const backtestId = Number(
        nodeOutput?.backtestId ?? (isRecord(resultOutput) ? resultOutput.backtestId : undefined)
      )
      if (!Number.isSafeInteger(backtestId) || backtestId <= 0)
        throw new Error(currentRun.status === 'succeeded' ? '回测明细不存在' : '回测尚未完成')
      const stored = await fetchQuantBacktestDetail(backtestId)
      if (stored.schemaVersion !== 2) throw new Error('回测明细格式无效')
      const parsed: BacktestDetail = {
        ...stored,
        points: stored.points.map((point, index) => ({ ...point, id: index }))
      }
      run.value = currentRun
      detail.value = parsed
      const indicatorMap = new Map<
        string,
        { main: Record<string, string | null>; sub: Record<string, string | null> }
      >()
      if (parsed.venue === 'binance') {
        const indicatorResult = await fetchBinanceIndicators({
          market: parsed.market === 'usdm' ? 'usdm' : 'spot',
          instrument: parsed.instrument,
          interval: parsed.interval,
          startTime: parsed.candles[0]?.openTime,
          endTime: parsed.candles.at(-1)?.closeTime,
          limit: parsed.candles.length
        })
        indicatorResult.items.forEach((item) => indicatorMap.set(item.openTime, item))
      }
      candles.value = parsed.candles.map((item) => {
        const indicators = indicatorMap.get(item.openTime)
        return indicators
          ? {
              ...item,
              indicators: {
                main: Object.fromEntries(
                  Object.entries(indicators.main).map(([key, value]) => [
                    key,
                    value === null ? null : Number(value)
                  ])
                ),
                sub: Object.fromEntries(
                  Object.entries(indicators.sub).map(([key, value]) => [
                    key,
                    value === null ? null : Number(value)
                  ])
                )
              }
            }
          : item
      })
      selectedPointId.value = tradePoints.value.at(-1)?.id
    } catch (error: any) {
      run.value = null
      detail.value = null
      loadError.value = error?.message || '回测分析加载失败'
    } finally {
      loading.value = false
    }
  }

  watch(() => route.params.runId, loadPage, { immediate: true })
</script>

<style scoped lang="scss">
  .backtest-analysis {
    box-sizing: border-box;
    min-width: 0;
    padding: 16px 20px 28px;
    overflow: auto;
    color: var(--art-gray-900);
    background: var(--default-bg-color);
  }

  h1,
  h2,
  p,
  dl,
  dt,
  dd {
    margin: 0;
  }

  .analysis-header,
  .analysis-header__main,
  .section-heading,
  .signal-legend {
    display: flex;
    align-items: center;
  }

  .analysis-header {
    justify-content: space-between;
    min-height: 52px;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--art-card-border);
  }

  .analysis-header__main {
    gap: 12px;
    min-width: 0;
  }

  .analysis-header h1 {
    overflow: hidden;
    font-size: 18px;
    line-height: 24px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .analysis-header p,
  .section-heading p {
    margin-top: 2px;
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .analysis-range {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 20px;
    padding: 10px 0;
    font-size: 12px;
    color: var(--art-gray-700);
  }

  .metric-strip {
    display: grid;
    grid-template-columns: repeat(6, minmax(0, 1fr));
    margin-bottom: 18px;
    border-top: 1px solid var(--art-card-border);
    border-bottom: 1px solid var(--art-card-border);
  }

  .metric-strip__item {
    min-width: 0;
    padding: 13px 14px;
    border-right: 1px solid var(--art-card-border);
  }

  .metric-strip__item:last-child {
    border-right: 0;
  }

  .metric-strip__item span {
    display: block;
    margin-bottom: 5px;
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .metric-strip__item strong {
    overflow-wrap: anywhere;
    font-family: 'Cascadia Code', SFMono-Regular, Consolas, monospace;
    font-size: 18px;
    line-height: 22px;
  }

  [data-tone='positive'],
  [data-action='buy'] {
    color: var(--el-color-success);
  }

  [data-tone='negative'],
  [data-action='sell'] {
    color: var(--el-color-danger);
  }

  .analysis-section,
  .signal-detail {
    min-width: 0;
    padding: 14px 0 18px;
    border-top: 1px solid var(--art-card-border);
  }

  .section-heading {
    justify-content: space-between;
    min-height: 38px;
    margin-bottom: 10px;
  }

  .section-heading h2 {
    font-size: 14px;
    line-height: 20px;
  }

  .signal-legend {
    gap: 14px;
    font-size: 12px;
  }

  .signal-legend span::before {
    display: inline-block;
    width: 7px;
    height: 7px;
    margin-right: 6px;
    content: '';
    background: currentColor;
    border-radius: 50%;
  }

  .analysis-lower {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 300px;
    gap: 24px;
    margin-top: 18px;
  }

  .signal-detail dl {
    display: grid;
    gap: 0;
  }

  .signal-detail dl > div {
    display: grid;
    grid-template-columns: 88px minmax(0, 1fr);
    gap: 12px;
    padding: 8px 0;
    border-bottom: 1px solid var(--art-card-border);
  }

  .signal-detail dt {
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .signal-detail dd {
    overflow-wrap: anywhere;
    font-family: 'Cascadia Code', SFMono-Regular, Consolas, monospace;
    font-size: 12px;
    text-align: right;
  }

  .action {
    font-weight: 700;
  }

  :deep(.el-table .is-selected > td.el-table__cell) {
    background: var(--el-color-primary-light-9);
  }

  @media (max-width: 1100px) {
    .metric-strip {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }

    .metric-strip__item:nth-child(3) {
      border-right: 0;
    }

    .analysis-lower {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  @media (max-width: 640px) {
    .backtest-analysis {
      padding: 10px 12px 20px;
    }

    .analysis-header {
      gap: 10px;
    }

    .analysis-header h1 {
      max-width: 48vw;
      font-size: 15px;
    }

    .metric-strip {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .metric-strip__item:nth-child(3) {
      border-right: 1px solid var(--art-card-border);
    }

    .metric-strip__item:nth-child(even) {
      border-right: 0;
    }
  }
</style>
