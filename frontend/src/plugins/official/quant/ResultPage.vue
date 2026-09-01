<template>
  <section class="quant-result" aria-labelledby="quant-result-title">
    <header class="quant-result__header">
      <div>
        <p>Quant</p>
        <h3 id="quant-result-title">{{ nodeTitle }}</h3>
      </div>
      <span :data-status="result.runNode.status">{{ result.runNode.status }}</span>
    </header>

    <ElTabs v-model="activeTab" class="quant-result__tabs">
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
    fetchQuantStrategies,
    type QuantBacktest,
    type QuantStrategy
  } from './api'
  import { formatDateTime as formatTime } from '@/utils/date'
  import { decimalPercent } from './decimal'

  const { result } = defineProps<{
    result: { run: WorkflowRunDetail; runNode: WorkflowRunNode }
  }>()

  const activeTab = ref('strategies')
  const strategies = ref<QuantStrategy[]>([])
  const backtests = ref<QuantBacktest[]>([])

  const nodeTitle = computed(
    () =>
      ({
        'official.quant.evaluate': '策略评估',
        'official.quant.backtest': '策略回测'
      })[result.runNode.nodeType] || result.runNode.nodeType
  )
  const percent = (value: string) => `${decimalPercent(value)}%`

  onMounted(async () => {
    const [strategyResult, backtestResult] = await Promise.allSettled([
      fetchQuantStrategies(),
      fetchQuantBacktests()
    ])
    if (strategyResult.status === 'fulfilled') strategies.value = strategyResult.value.items
    if (backtestResult.status === 'fulfilled') backtests.value = backtestResult.value.items
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

  .quant-result__tabs {
    margin-top: 8px;
  }
</style>
