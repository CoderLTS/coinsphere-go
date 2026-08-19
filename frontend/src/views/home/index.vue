<template>
  <div class="workflow-home">
    <template v-if="isGuest">
      <section class="guest-console">
        <div class="guest-console__signal"><span></span><span></span><span></span></div>
        <div>
          <div class="eyebrow">COINSPHERE / WORKFLOW OPERATIONS</div>
          <h1>工作流执行台</h1>
          <p>登录后查看工作流流转、策略信号与 K 线状态。</p>
          <ElButton type="primary" @click="goLogin">登录工作台</ElButton>
        </div>
      </section>
    </template>

    <template v-else>
      <header class="workbench-head">
        <div>
          <div class="eyebrow">WORKFLOW OPERATIONS / LIVE</div>
          <h1>工作流工作台</h1>
          <p>从触发到行情、策略和通知，在一个画布上检查运行状态。</p>
        </div>
        <div class="head-actions">
          <ElSelect
            v-model="selectedDefinitionId"
            class="workflow-select"
            filterable
            placeholder="选择工作流"
            @change="handleDefinitionChange"
          >
            <ElOption
              v-for="item in definitions"
              :key="item.id"
              :label="`${item.displayName} · v${item.version}`"
              :value="item.id"
            >
              <div class="definition-option">
                <span
                  class="definition-state"
                  :class="{ 'definition-state--active': item.isActive }"
                ></span>
                <strong>{{ item.displayName }}</strong>
                <small>v{{ item.version }}</small>
              </div>
            </ElOption>
          </ElSelect>
          <ElTooltip content="编辑工作流" placement="bottom">
            <ElButton
              :icon="EditPen"
              :disabled="!selectedDefinition || !canEdit"
              circle
              aria-label="编辑工作流"
              @click="openEditor"
            />
          </ElTooltip>
          <ElButton
            type="primary"
            :icon="VideoPlay"
            :disabled="!manualEntry || !canRun"
            :loading="runningWorkflow"
            @click="runWorkflow"
          >
            运行工作流
          </ElButton>
        </div>
      </header>

      <section class="ops-strip">
        <div class="ops-identity">
          <span class="ops-live" :class="{ 'ops-live--running': activeExecution }"></span>
          <div>
            <strong>{{ selectedDefinition?.displayName || '未选择工作流' }}</strong>
            <span>{{ selectedDefinition?.code || '--' }}</span>
          </div>
        </div>
        <dl>
          <div>
            <dt>激活版本</dt>
            <dd>v{{ selectedDefinition?.activeVersion || selectedDefinition?.version || '--' }}</dd>
          </div>
          <div>
            <dt>最近状态</dt>
            <dd :class="statusClass(latestExecution?.status)">{{
              statusLabel(latestExecution?.status)
            }}</dd>
          </div>
          <div>
            <dt>总执行</dt>
            <dd>{{ selectedDefinition?.executionCount || 0 }}</dd>
          </div>
          <div>
            <dt>当前耗时</dt>
            <dd>{{ durationText(selectedExecution?.durationMs || 0) }}</dd>
          </div>
        </dl>
      </section>

      <section class="workbench-stage">
        <div class="canvas-pane">
          <div class="canvas-head">
            <span>FLOW / {{ selectedDefinition?.code || 'NO WORKFLOW' }}</span>
            <div class="state-legend">
              <span><i class="state-dot state-dot--pending"></i>待执行</span>
              <span><i class="state-dot state-dot--running"></i>运行中</span>
              <span><i class="state-dot state-dot--success"></i>成功</span>
              <span><i class="state-dot state-dot--failed"></i>失败</span>
            </div>
          </div>
          <WorkflowExecutionCanvas
            class="workflow-canvas"
            :graph="previewGraph"
            :node-logs="selectedExecution?.nodeLogs || []"
            :transition-logs="selectedExecution?.transitionLogs || []"
            :start-node-id="selectedExecution?.startNodeId || ''"
            @selection-change="handleCanvasSelection"
          />
          <div v-if="selectedNodeLog" class="node-popover">
            <div>
              <span>{{ selectedNodeLog.nodeType }}</span>
              <strong>{{ selectedNodeLog.nodeId }}</strong>
            </div>
            <span :class="statusClass(selectedNodeLog.status)">{{
              statusLabel(selectedNodeLog.status)
            }}</span>
            <p v-if="selectedNodeLog.errorMessage">{{ selectedNodeLog.errorMessage }}</p>
            <small>{{ durationText(selectedNodeLog.durationMs) }}</small>
          </div>
        </div>

        <aside class="run-rail">
          <div class="rail-head">
            <div>
              <div class="eyebrow">EXECUTION TRACE</div>
              <h2>最近执行</h2>
            </div>
            <ElTooltip content="刷新执行记录" placement="bottom">
              <ElButton
                :icon="Refresh"
                circle
                aria-label="刷新执行记录"
                :loading="executionsLoading"
                @click="loadExecutions"
              />
            </ElTooltip>
          </div>
          <div v-if="executions.length" class="run-list">
            <button
              v-for="item in executions"
              :key="item.id"
              type="button"
              class="run-row"
              :class="{ 'run-row--selected': selectedExecution?.id === item.id }"
              @click="selectExecution(item.id)"
            >
              <span class="run-dot" :class="`run-dot--${item.status}`"></span>
              <span class="run-copy">
                <strong>#{{ item.id }} · {{ triggerLabel(item.triggerType) }}</strong>
                <small>{{ item.startedAt || item.queuedAt || '--' }}</small>
                <em v-if="item.errorMessage">{{ item.errorMessage }}</em>
              </span>
              <span class="run-duration">{{ durationText(item.durationMs) }}</span>
            </button>
          </div>
          <div v-else class="rail-empty">
            <ArtSvgIcon icon="ri:git-commit-line" />
            <strong>还没有执行记录</strong>
            <span>运行手动入口后，流转轨迹会显示在这里。</span>
          </div>
          <ElButton
            v-if="selectedExecution"
            class="detail-link"
            text
            @click="router.push(`/scheduler/execution/${selectedExecution.id}/detail`)"
          >
            查看完整执行详情
            <ArtSvgIcon icon="ri:arrow-right-line" />
          </ElButton>
        </aside>
      </section>

      <section class="lower-grid">
        <div class="signal-preview">
          <div class="panel-head">
            <div>
              <div class="eyebrow">MARKET SIGNAL PREVIEW</div>
              <h2>{{ previewStrategy?.symbol || 'K 线信号预览' }}</h2>
            </div>
            <div class="preview-meta">
              <span>{{ previewStrategy?.name || '暂无启用策略' }}</span>
              <ElButton text @click="router.push('/data/market-chart')">打开完整图表</ElButton>
            </div>
          </div>
          <ArtKLineChart
            :data="previewCandles"
            :signals="previewSignals"
            :loading="marketLoading"
            :show-volume="true"
            :show-target="false"
            :show-data-zoom="false"
            height="250px"
          />
        </div>

        <div class="error-console">
          <div class="panel-head">
            <div>
              <div class="eyebrow">LATEST FAILURE</div>
              <h2>错误定位</h2>
            </div>
            <span class="error-count">{{ failedExecutions.length }}</span>
          </div>
          <div v-if="latestFailure" class="error-body">
            <span>#{{ latestFailure.id }} · {{ latestFailure.failureCategory || 'workflow' }}</span>
            <strong>{{ latestFailure.errorMessage || '工作流执行失败' }}</strong>
            <small>{{ latestFailure.finishedAt || latestFailure.startedAt }}</small>
            <ElButton text @click="selectExecution(latestFailure.id)">在画布中定位</ElButton>
          </div>
          <div v-else class="error-empty">
            <ArtSvgIcon icon="ri:checkbox-circle-line" />
            <span>最近执行未发现错误</span>
          </div>
        </div>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
  import { EditPen, Refresh, VideoPlay } from '@element-plus/icons-vue'
  import { ElMessage } from 'element-plus'
  import {
    fetchNodeDefinitions,
    fetchRunWorkflowDefinition,
    fetchWorkflowDefinitionExecutions,
    fetchWorkflowDefinitionList,
    fetchWorkflowExecutionDetail,
    fetchWorkflowRuntime,
    type WorkflowDefinitionItem,
    type WorkflowExecutionDetail,
    type WorkflowExecutionItem,
    type WorkflowExecutionNodeLog,
    type WorkflowNodeDefinitionItem,
    type WorkflowRuntimeEntryItem
  } from '@/api/scheduler'
  import { fetchMarketCandles } from '@/api/market'
  import {
    fetchStrategyInstances,
    fetchStrategySignals,
    type StrategyInstanceItem
  } from '@/api/signals'
  import { useUserStore } from '@/store/modules/user'
  import type { KLineDataItem, KLineSignalItem } from '@/types/component/chart'
  import WorkflowExecutionCanvas from '@/views/scheduler/execution/detail/components/WorkflowExecutionCanvas.vue'
  import { flattenMaterials } from '@/views/scheduler/workflow/editor/workflow-editor.mapper'
  import { mapServerGraphToDomain } from '@/views/scheduler/workflow/editor/workflow-editor.mapper'
  import type { WorkflowDomainGraphModel } from '@/views/scheduler/workflow/editor/types'

  defineOptions({ name: 'HomePage' })

  const router = useRouter()
  const userStore = useUserStore()
  const isGuest = computed(() => userStore.accessMode === 'guest')
  const canRun = computed(() =>
    userStore.info.permissions.includes('scheduler.workflow_definitions.run')
  )
  const canEdit = computed(() =>
    userStore.info.permissions.includes('scheduler.workflow_definitions.update')
  )
  const definitions = ref<WorkflowDefinitionItem[]>([])
  const nodeDefinitions = ref<WorkflowNodeDefinitionItem[]>([])
  const selectedDefinitionId = ref<number | null>(null)
  const runtimeEntries = ref<WorkflowRuntimeEntryItem[]>([])
  const executions = ref<WorkflowExecutionItem[]>([])
  const selectedExecution = ref<WorkflowExecutionDetail | null>(null)
  const executionsLoading = ref(false)
  const runningWorkflow = ref(false)
  const selectedCanvasNodeId = ref('')
  const pollTimer = ref<ReturnType<typeof setInterval> | null>(null)
  const previewStrategy = ref<StrategyInstanceItem | null>(null)
  const previewCandles = ref<KLineDataItem[]>([])
  const previewSignals = ref<KLineSignalItem[]>([])
  const marketLoading = ref(false)

  const selectedDefinition = computed(
    () => definitions.value.find((item) => item.id === selectedDefinitionId.value) || null
  )
  const manualEntry = computed(
    () => runtimeEntries.value.find((item) => item.startType === 'manual' && item.isEnabled) || null
  )
  const latestExecution = computed(() => executions.value[0] || null)
  const activeExecution = computed(() =>
    ['queued', 'running', 'retry_waiting'].includes(latestExecution.value?.status || '')
  )
  const failedExecutions = computed(() =>
    executions.value.filter((item) => item.status === 'failed')
  )
  const latestFailure = computed(() => failedExecutions.value[0] || null)
  const selectedNodeLog = computed<WorkflowExecutionNodeLog | null>(() => {
    if (!selectedCanvasNodeId.value) return null
    const logs = selectedExecution.value?.nodeLogs.filter(
      (item) => item.nodeId === selectedCanvasNodeId.value
    )
    return logs?.at(-1) || null
  })
  const previewGraph = computed<WorkflowDomainGraphModel>(() => {
    const graph = selectedExecution.value?.graph || selectedDefinition.value?.graph
    if (!graph) return { nodes: [], edges: [] }
    return mapServerGraphToDomain(
      graph,
      nodeDefinitions.value,
      flattenMaterials(nodeDefinitions.value)
    )
  })

  const statusLabel = (status?: string) =>
    ({
      queued: '待执行',
      pending: '待执行',
      running: '运行中',
      retry_waiting: '等待重试',
      success: '成功',
      failed: '失败',
      canceled: '已取消'
    })[status || ''] ||
    status ||
    '--'
  const statusClass = (status?: string) => `status-text status-text--${status || 'idle'}`
  const triggerLabel = (type: string) =>
    ({ manual: '手动', schedule: '定时', event: '事件', webhook: 'Webhook' })[type] || type
  const durationText = (duration: number) => {
    if (!duration) return '--'
    if (duration < 1000) return `${duration}ms`
    return `${(duration / 1000).toFixed(duration < 10000 ? 1 : 0)}s`
  }
  const timeKey = (value: string) => String(new Date(value).getTime())
  const axisTime = (value: string) => {
    const date = new Date(value)
    const pad = (part: number) => String(part).padStart(2, '0')
    return `${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
  }

  const stopPolling = () => {
    if (pollTimer.value) clearInterval(pollTimer.value)
    pollTimer.value = null
  }

  const syncPolling = () => {
    stopPolling()
    if (!activeExecution.value) return
    pollTimer.value = setInterval(async () => {
      await loadExecutions()
      if (selectedExecution.value) {
        selectedExecution.value = await fetchWorkflowExecutionDetail(selectedExecution.value.id)
      }
      if (!activeExecution.value) stopPolling()
    }, 2000)
  }

  const loadExecutions = async () => {
    const definition = selectedDefinition.value
    if (!definition) return
    executionsLoading.value = true
    try {
      const result = await fetchWorkflowDefinitionExecutions(definition.id, { limit: 8 })
      executions.value = result.records
      if (!selectedExecution.value && result.records[0]) {
        selectedExecution.value = await fetchWorkflowExecutionDetail(result.records[0].id)
      }
    } finally {
      executionsLoading.value = false
    }
  }

  const selectExecution = async (executionId: number) => {
    selectedExecution.value = await fetchWorkflowExecutionDetail(executionId)
    selectedCanvasNodeId.value = ''
    syncPolling()
  }

  const loadSelectedDefinition = async () => {
    stopPolling()
    selectedExecution.value = null
    selectedCanvasNodeId.value = ''
    const definition = selectedDefinition.value
    if (!definition) return
    const runtime = await fetchWorkflowRuntime(definition.id)
    runtimeEntries.value = runtime.entries || []
    await loadExecutions()
    syncPolling()
  }

  const handleDefinitionChange = () => void loadSelectedDefinition()

  const runWorkflow = async () => {
    const definition = selectedDefinition.value
    if (!definition || !manualEntry.value) return
    runningWorkflow.value = true
    try {
      const result = await fetchRunWorkflowDefinition(definition.id, {
        startEntryKeys: [manualEntry.value.entryKey],
        inputs: {}
      })
      const execution = result.executions[0]
      if (execution) await selectExecution(execution.id)
      await loadExecutions()
    } finally {
      runningWorkflow.value = false
    }
  }

  const loadMarketPreview = async () => {
    marketLoading.value = true
    try {
      const instanceResult = await fetchStrategyInstances({ limit: 50 })
      previewStrategy.value =
        instanceResult.records.find((item) => item.isEnabled) || instanceResult.records[0] || null
      if (!previewStrategy.value) return
      const [candleResult, signalResult] = await Promise.all([
        fetchMarketCandles({
          instrumentId: previewStrategy.value.instrumentId,
          interval: previewStrategy.value.interval as any,
          limit: 80
        }),
        fetchStrategySignals({
          strategyInstance: previewStrategy.value.id,
          instrumentId: previewStrategy.value.instrumentId,
          interval: previewStrategy.value.interval,
          limit: 80
        })
      ])
      const candles = [...candleResult.records].reverse()
      const signalMap = new Map(
        signalResult.records.map((item) => [timeKey(item.candleOpenTime), item])
      )
      const labelMap = new Map(
        candles.map((item) => [timeKey(item.openTime), axisTime(item.openTime)])
      )
      previewCandles.value = candles.map((item) => ({
        time: axisTime(item.openTime),
        open: Number(item.open),
        close: Number(item.close),
        high: Number(item.high),
        low: Number(item.low),
        volume: Number(item.baseVolume),
        target: signalMap.has(timeKey(item.openTime))
          ? Number(signalMap.get(timeKey(item.openTime))?.target)
          : null
      }))
      previewSignals.value = signalResult.records.map((item) => ({
        time: labelMap.get(timeKey(item.candleOpenTime)) || axisTime(item.candleOpenTime),
        action: item.action,
        target: Number(item.target),
        previousTarget: Number(item.previousTarget)
      }))
    } finally {
      marketLoading.value = false
    }
  }

  const handleCanvasSelection = (payload: { cellId: string | null; cellType: string | null }) => {
    selectedCanvasNodeId.value = payload.cellType === 'node' ? payload.cellId || '' : ''
  }

  const openEditor = () => {
    if (selectedDefinition.value) {
      router.push(`/scheduler/workflow/${selectedDefinition.value.id}/edit`)
    }
  }

  const goLogin = () => router.push({ name: 'Login', query: { redirect: '/home' } })

  onMounted(async () => {
    if (isGuest.value) return
    try {
      const [definitionResult, nodeDefinitionResult] = await Promise.all([
        fetchWorkflowDefinitionList(),
        fetchNodeDefinitions()
      ])
      definitions.value = definitionResult
      nodeDefinitions.value = nodeDefinitionResult
      selectedDefinitionId.value =
        definitions.value.find((item) => item.isActive)?.id || definitions.value[0]?.id || null
      await Promise.all([loadSelectedDefinition(), loadMarketPreview()])
    } catch {
      ElMessage.error('工作台数据加载失败')
    }
  })

  onBeforeUnmount(stopPolling)
</script>

<style scoped lang="scss">
  .workflow-home {
    --home-text: var(--art-gray-900);
    --home-muted: var(--art-gray-600);
    --home-card: var(--default-box-color);
    --home-line: var(--art-card-border);
    --home-soft: var(--art-gray-100);
    --home-hover: var(--art-hover-color);
    --home-shadow: 0 8px 24px rgb(31 35 48 / 0.05);
    --home-success: var(--el-color-success);
    --home-warning: var(--el-color-warning);
    --home-danger: var(--el-color-danger);

    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
    min-height: 100%;
    padding: 20px;
    color: var(--home-text);
    background: var(--default-bg-color);
  }

  .workbench-head,
  .head-actions,
  .ops-strip,
  .ops-identity,
  .ops-strip dl,
  .canvas-head,
  .state-legend,
  .state-legend span,
  .rail-head,
  .panel-head,
  .preview-meta,
  .definition-option,
  .node-popover > div:first-child {
    display: flex;
    align-items: center;
  }

  .workbench-head,
  .ops-strip,
  .canvas-head,
  .rail-head,
  .panel-head {
    gap: 16px;
    justify-content: space-between;
  }

  h1,
  h2,
  p {
    margin: 0;
  }

  h1 {
    margin-top: 6px;
    font-size: 28px;
    font-weight: 600;
    line-height: 1.3;
  }

  h2 {
    margin-top: 4px;
    font-size: 16px;
    font-weight: 600;
  }

  .eyebrow {
    font-size: 11px;
    font-weight: 600;
    color: var(--theme-color);
  }

  .workbench-head {
    min-height: 74px;
  }

  .workbench-head p {
    margin-top: 7px;
    font-size: 13px;
    color: var(--home-muted);
  }

  .head-actions {
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .workflow-select {
    width: min(320px, 100%);
  }

  .definition-option {
    gap: 8px;
  }

  .definition-option small {
    margin-left: auto;
    color: var(--el-text-color-secondary);
  }

  .definition-state {
    width: 7px;
    height: 7px;
    background: var(--art-gray-500);
    border-radius: 50%;
  }

  .definition-state--active {
    background: var(--home-success);
  }

  .ops-strip {
    padding: 16px 18px;
    background: var(--home-card);
    border: 1px solid var(--home-line);
    border-radius: calc(var(--custom-radius) / 2 + 4px);
    box-shadow: var(--home-shadow);
  }

  .ops-identity {
    gap: 11px;
    min-width: 210px;
  }

  .ops-live {
    width: 9px;
    height: 9px;
    background: var(--home-success);
    border-radius: 50%;
  }

  .ops-live--running {
    background: var(--home-warning);
    box-shadow: 0 0 0 4px color-mix(in srgb, var(--home-warning) 15%, transparent);
  }

  .ops-identity strong,
  .ops-identity span {
    display: block;
  }

  .ops-identity strong {
    font-size: 13px;
  }

  .ops-identity span {
    margin-top: 3px;
    font-size: 11px;
    color: var(--home-muted);
  }

  .ops-strip dl {
    flex-wrap: wrap;
    margin: 0;
  }

  .ops-strip dl div {
    min-width: 112px;
    padding: 0 16px;
    border-left: 1px solid var(--home-line);
  }

  .ops-strip dt {
    font-size: 11px;
    color: var(--home-muted);
  }

  .ops-strip dd {
    margin: 5px 0 0;
    font-size: 13px;
    font-weight: 600;
  }

  .workbench-stage {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 310px;
    min-height: 520px;
    overflow: hidden;
    background: var(--home-card);
    border: 1px solid var(--home-line);
    border-radius: calc(var(--custom-radius) / 2 + 4px);
    box-shadow: var(--home-shadow);
  }

  .canvas-pane {
    position: relative;
    min-width: 0;
    min-height: 520px;
    overflow: hidden;
    background: var(--workflow-canvas-bg);
    border-right: 1px solid var(--home-line);
  }

  .canvas-head {
    position: absolute;
    top: 14px;
    right: 16px;
    left: 16px;
    z-index: 5;
    font-size: 11px;
    color: var(--home-muted);
    pointer-events: none;
  }

  .state-legend {
    flex-wrap: wrap;
    gap: 12px;
  }

  .state-legend span {
    gap: 5px;
  }

  .state-dot,
  .run-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
  }

  .state-dot--pending,
  .run-dot--queued,
  .run-dot--pending {
    background: var(--art-gray-500);
  }

  .state-dot--running,
  .run-dot--running,
  .run-dot--retry_waiting {
    background: var(--home-warning);
  }

  .state-dot--success,
  .run-dot--success {
    background: var(--home-success);
  }

  .state-dot--failed,
  .run-dot--failed,
  .run-dot--canceled {
    background: var(--home-danger);
  }

  .workflow-canvas {
    width: 100%;
    height: 100%;
    min-height: 520px;
  }

  .workflow-canvas :deep(.workflow-execution-canvas) {
    border: 0;
    border-radius: 0;
  }

  .node-popover {
    position: absolute;
    right: 14px;
    bottom: 14px;
    z-index: 6;
    width: min(340px, calc(100% - 28px));
    padding: 12px;
    color: var(--home-text);
    background: color-mix(in srgb, var(--home-card) 94%, transparent);
    backdrop-filter: blur(10px);
    border: 1px solid var(--home-line);
    border-radius: 8px;
    box-shadow: 0 10px 28px rgb(31 35 48 / 0.1);
  }

  .node-popover > div:first-child {
    gap: 8px;
    justify-content: space-between;
  }

  .node-popover span,
  .node-popover small {
    font-size: 10px;
    color: var(--home-muted);
  }

  .node-popover p {
    margin: 8px 0;
    font-size: 11px;
    color: var(--home-danger);
  }

  .run-rail {
    display: flex;
    flex-direction: column;
    min-width: 0;
    padding: 18px;
    color: var(--home-text);
    background: var(--home-card);
  }

  .run-list {
    margin-top: 12px;
  }

  .run-row {
    display: grid;
    grid-template-columns: 8px minmax(0, 1fr) auto;
    gap: 10px;
    align-items: start;
    width: 100%;
    padding: 12px 8px;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
    border-bottom: 1px solid var(--home-line);
    border-radius: 6px;
  }

  .run-row:hover,
  .run-row--selected {
    background: var(--home-hover);
  }

  .run-row--selected {
    box-shadow: inset 3px 0 0 var(--theme-color);
  }

  .run-dot {
    margin-top: 4px;
  }

  .run-copy {
    min-width: 0;
  }

  .run-copy strong,
  .run-copy small,
  .run-copy em {
    display: block;
  }

  .run-copy strong {
    overflow: hidden;
    font-size: 12px;
    font-style: normal;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .run-copy small,
  .run-duration {
    margin-top: 4px;
    font-size: 10px;
    color: var(--home-muted);
  }

  .run-copy em {
    display: -webkit-box;
    margin-top: 5px;
    overflow: hidden;
    font-size: 10px;
    font-style: normal;
    color: var(--home-danger);
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
  }

  .rail-empty,
  .error-empty {
    display: grid;
    flex: 1;
    gap: 8px;
    place-items: center;
    min-height: 220px;
    color: var(--home-muted);
    text-align: center;
  }

  .rail-empty :deep(.art-svg-icon),
  .error-empty :deep(.art-svg-icon) {
    font-size: 30px;
    color: var(--el-color-primary-light-4);
  }

  .rail-empty strong {
    font-size: 13px;
    color: var(--home-text);
  }

  .rail-empty span {
    max-width: 220px;
    font-size: 11px;
    line-height: 1.6;
  }

  .detail-link {
    align-self: flex-start;
    margin-top: auto;
  }

  .lower-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.45fr) minmax(280px, 0.55fr);
    gap: 16px;
  }

  .signal-preview,
  .error-console {
    min-width: 0;
    padding: 18px;
    background: var(--home-card);
    border: 1px solid var(--home-line);
    border-radius: calc(var(--custom-radius) / 2 + 4px);
    box-shadow: var(--home-shadow);
  }

  .preview-meta {
    gap: 8px;
    font-size: 11px;
    color: var(--home-muted);
  }

  .error-count {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    font-size: 12px;
    color: var(--home-danger);
    background: color-mix(in srgb, var(--home-danger) 8%, transparent);
    border-radius: 8px;
  }

  .error-body {
    display: grid;
    gap: 8px;
    padding: 18px 0 4px;
  }

  .error-body span,
  .error-body small {
    font-size: 10px;
    color: var(--home-muted);
  }

  .error-body strong {
    font-size: 12px;
    line-height: 1.55;
    color: var(--home-danger);
  }

  .error-empty {
    min-height: 190px;
  }

  .status-text--running,
  .status-text--retry_waiting {
    color: var(--home-warning);
  }

  .status-text--success {
    color: var(--home-success);
  }

  .status-text--failed,
  .status-text--canceled {
    color: var(--home-danger);
  }

  .guest-console {
    position: relative;
    display: grid;
    grid-template-columns: 160px minmax(0, 1fr);
    gap: 36px;
    align-items: center;
    min-height: 420px;
    padding: 48px;
    overflow: hidden;
    background: var(--home-card);
    border: 1px solid var(--home-line);
    border-radius: calc(var(--custom-radius) / 2 + 6px);
    box-shadow: var(--home-shadow);
  }

  .guest-console__signal {
    display: flex;
    gap: 14px;
    align-items: flex-end;
    justify-content: center;
    height: 140px;
    padding: 18px;
    background: var(--el-color-primary-light-9);
    border-radius: 12px;
  }

  .guest-console__signal span {
    width: 26px;
    height: 104px;
    background: var(--theme-color);
    border-radius: 6px 6px 2px 2px;
  }

  .guest-console__signal span:nth-child(2) {
    height: 68px;
    background: var(--el-color-success);
  }

  .guest-console__signal span:nth-child(3) {
    height: 88px;
    background: var(--el-color-warning);
  }

  .guest-console p {
    margin: 10px 0 22px;
    color: var(--home-muted);
  }

  @media (max-width: 1100px) {
    .workbench-stage,
    .lower-grid {
      grid-template-columns: 1fr;
    }

    .canvas-pane {
      border-right: 0;
      border-bottom: 1px solid var(--home-line);
    }

    .run-rail {
      min-height: 330px;
    }
  }

  @media (max-width: 720px) {
    .workflow-home {
      padding: 16px;
    }

    .workbench-head,
    .ops-strip {
      flex-direction: column;
      align-items: flex-start;
    }

    h1 {
      font-size: 24px;
    }

    .head-actions,
    .workflow-select {
      width: 100%;
    }

    .ops-strip dl {
      width: 100%;
    }

    .ops-strip dl div {
      min-width: 50%;
      padding: 10px 0;
      border-top: 1px solid var(--home-line);
      border-left: 0;
    }

    .canvas-head {
      flex-direction: column;
      gap: 6px;
      align-items: flex-start;
    }

    .guest-console {
      grid-template-columns: 1fr;
      padding: 28px;
    }
  }
</style>
