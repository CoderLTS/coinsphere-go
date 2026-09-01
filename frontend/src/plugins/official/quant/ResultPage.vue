<template>
  <section class="quant-result" aria-labelledby="quant-result-title">
    <header class="quant-result__header">
      <div>
        <p>Quant</p>
        <h3 id="quant-result-title">{{ nodeTitle }}</h3>
      </div>
      <span :data-status="result.runNode.status">{{ result.runNode.status }}</span>
    </header>

    <div v-if="latestCandle" class="quant-tape" aria-label="最新闭合 K 线">
      <div
        ><span>Open</span><strong>{{ latestCandle.open }}</strong></div
      >
      <div
        ><span>High</span><strong>{{ latestCandle.high }}</strong></div
      >
      <div
        ><span>Low</span><strong>{{ latestCandle.low }}</strong></div
      >
      <div
        ><span>Close</span><strong>{{ latestCandle.close }}</strong></div
      >
    </div>

    <ElTabs v-model="activeTab" class="quant-result__tabs">
      <ElTabPane label="行情" name="market">
        <div class="quant-toolbar">
          <ElSegmented v-model="market" :options="marketOptions" @change="loadMarket" />
          <ElSelect v-model="instrument" filterable @change="loadCandles">
            <ElOption
              v-for="item in instruments"
              :key="item.symbol"
              :label="item.symbol"
              :value="item.symbol"
            />
          </ElSelect>
          <ElSelect v-model="interval" class="quant-toolbar__interval" @change="loadCandles">
            <ElOption v-for="item in intervals" :key="item" :label="item" :value="item" />
          </ElSelect>
          <ElButton circle title="刷新行情" :loading="marketLoading" @click="loadMarket">
            <ArtSvgIcon icon="ri:refresh-line" />
          </ElButton>
        </div>
        <ElTable :data="candles" height="280" size="small" empty-text="暂无闭合 K 线">
          <ElTableColumn label="UTC+8" min-width="156">
            <template #default="scope">{{ formatTime(scope.row.openTime) }}</template>
          </ElTableColumn>
          <ElTableColumn prop="open" label="Open" min-width="108" />
          <ElTableColumn prop="high" label="High" min-width="108" />
          <ElTableColumn prop="low" label="Low" min-width="108" />
          <ElTableColumn prop="close" label="Close" min-width="108" />
          <ElTableColumn prop="volume" label="Volume" min-width="120" />
        </ElTable>
      </ElTabPane>

      <ElTabPane label="策略" name="strategies">
        <ElTable :data="strategies" height="320" size="small" empty-text="暂无可信策略">
          <ElTableColumn prop="name" label="策略" min-width="160" />
          <ElTableColumn prop="id" label="ID" min-width="230" />
          <ElTableColumn prop="version" label="版本" width="90" />
          <ElTableColumn prop="minimumLookback" label="最小回看" width="100" />
        </ElTable>
      </ElTabPane>

      <ElTabPane label="回测" name="backtests">
        <ElTable :data="backtests" height="320" size="small" empty-text="暂无回测结果">
          <ElTableColumn label="范围" min-width="180">
            <template #default="scope">
              <strong>{{ scope.row.instrument }}</strong>
              <small>{{ scope.row.market }} · {{ scope.row.interval }}</small>
            </template>
          </ElTableColumn>
          <ElTableColumn prop="finalEquity" label="最终权益" min-width="120" />
          <ElTableColumn label="收益 / 回撤" min-width="150">
            <template #default="scope">
              <span>{{ percent(scope.row.totalReturn) }}</span>
              <small>{{ percent(scope.row.maxDrawdown) }}</small>
            </template>
          </ElTableColumn>
          <ElTableColumn prop="tradeCount" label="成交" width="72" />
          <ElTableColumn label="完成时间" min-width="150">
            <template #default="scope">{{ formatTime(scope.row.createdAt) }}</template>
          </ElTableColumn>
        </ElTable>
      </ElTabPane>
    </ElTabs>
  </section>
</template>

<script setup lang="ts">
  import type { WorkflowRunDetail, WorkflowRunNode } from '@/api/workflows'
  import {
    fetchQuantBacktests,
    fetchQuantCandles,
    fetchQuantInstruments,
    fetchQuantStrategies,
    type QuantBacktest,
    type QuantCandle,
    type QuantInstrument,
    type QuantStrategy
  } from '@/api/quant'
  import { formatDateTime as formatTime } from '@/utils/date'
  import { decimalPercent } from './decimal'

  const { result } = defineProps<{
    result: { run: WorkflowRunDetail; runNode: WorkflowRunNode }
  }>()

  const activeTab = ref('market')
  const market = ref<'spot' | 'usdm'>('spot')
  const instrument = ref('BTCUSDT')
  const interval = ref('1h')
  const instruments = ref<QuantInstrument[]>([])
  const candles = ref<QuantCandle[]>([])
  const strategies = ref<QuantStrategy[]>([])
  const backtests = ref<QuantBacktest[]>([])
  const marketLoading = ref(false)
  const marketOptions = [
    { label: 'Spot', value: 'spot' },
    { label: 'USD-M', value: 'usdm' }
  ]
  const intervals = ['1m', '5m', '15m', '1h', '4h', '1d']

  const nodeTitle = computed(
    () =>
      ({
        'official.quant.realtime_candles': 'Binance 实时闭合行情',
        'official.quant.backfill_candles': 'Binance K 线补数',
        'official.quant.sync_instruments': 'Binance 币种元数据采集',
        'official.quant.evaluate': '策略评估',
        'official.quant.backtest': '策略回测'
      })[result.runNode.nodeType] || result.runNode.nodeType
  )
  const latestCandle = computed(() => candles.value.at(-1))
  const percent = (value: string) => `${decimalPercent(value)}%`

  const loadCandles = async () => {
    if (!instrument.value) return
    try {
      candles.value = (
        await fetchQuantCandles({
          market: market.value,
          instrument: instrument.value,
          interval: interval.value,
          limit: 200
        })
      ).items
    } catch {
      candles.value = []
      ElMessage.error('行情加载失败')
    }
  }
  const loadMarket = async () => {
    marketLoading.value = true
    try {
      instruments.value = (await fetchQuantInstruments(market.value)).items
      if (!instruments.value.some((item) => item.symbol === instrument.value))
        instrument.value = instruments.value[0]?.symbol || ''
      await loadCandles()
    } catch {
      instruments.value = []
      candles.value = []
      ElMessage.error('市场元数据加载失败')
    } finally {
      marketLoading.value = false
    }
  }
  onMounted(async () => {
    const [strategyResult, backtestResult] = await Promise.allSettled([
      fetchQuantStrategies(),
      fetchQuantBacktests()
    ])
    if (strategyResult.status === 'fulfilled') strategies.value = strategyResult.value.items
    if (backtestResult.status === 'fulfilled') backtests.value = backtestResult.value.items
    await loadMarket()
  })
</script>

<style scoped>
  .quant-result {
    color: var(--el-text-color-primary);
    letter-spacing: 0;
  }

  .quant-result__header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    padding-bottom: 14px;
    border-bottom: 1px solid var(--el-border-color);
  }

  .quant-result__header p,
  .quant-result__header h3 {
    margin: 0;
  }

  .quant-result__header p,
  .quant-tape span,
  .quant-result small {
    display: block;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .quant-result__header h3 {
    margin-top: 4px;
    font-size: 18px;
    font-weight: 650;
  }

  .quant-result__header > span {
    padding: 4px 8px;
    font-size: 12px;
    font-weight: 600;
    border: 1px solid var(--el-border-color);
    border-radius: 4px;
  }

  .quant-result__header > span[data-status='succeeded'] {
    color: var(--el-color-success-dark-2);
    background: var(--el-color-success-light-9);
    border-color: var(--el-color-success-light-5);
  }

  .quant-tape {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    margin: 12px 0 2px;
    border-block: 1px solid var(--el-border-color-lighter);
  }

  .quant-tape > div {
    min-width: 0;
    padding: 10px 12px;
    border-right: 1px solid var(--el-border-color-lighter);
  }

  .quant-tape > div:last-child {
    border-right: 0;
  }

  .quant-tape strong {
    display: block;
    margin-top: 3px;
    overflow: hidden;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 13px;
    text-overflow: ellipsis;
  }

  .quant-result__tabs {
    margin-top: 8px;
  }

  .quant-toolbar {
    display: grid;
    grid-template-columns: auto minmax(140px, 1fr) 88px 34px;
    gap: 8px;
    align-items: center;
    margin-bottom: 10px;
  }

  @media (max-width: 640px) {
    .quant-tape {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .quant-tape > div:nth-child(2) {
      border-right: 0;
    }

    .quant-toolbar {
      grid-template-columns: minmax(0, 1fr) 82px 34px;
    }

    .quant-toolbar :deep(.el-segmented) {
      grid-column: 1 / -1;
    }
  }
</style>
