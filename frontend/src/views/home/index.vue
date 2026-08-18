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
                <span class="definition-state" :class="{ 'definition-state--active': item.isActive }"></span>
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
            <dd :class="statusClass(latestExecution?.status)">{{ statusLabel(latestExecution?.status) }}</dd>
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
            <span :class="statusClass(selectedNodeLog.status)">{{ statusLabel(selectedNodeLog.status) }}</span>
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
  const failedExecutions = computed(() => executions.value.filter((item) => item.status === 'failed'))
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
    return mapServerGraphToDomain(graph, nodeDefinitions.value, flattenMaterials(nodeDefinitions.value))
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
    })[status || ''] || status || '--'
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
      const signalMap = new Map(signalResult.records.map((item) => [timeKey(item.candleOpenTime), item]))
      const labelMap = new Map(candles.map((item) => [timeKey(item.openTime), axisTime(item.openTime)]))
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
    --ink: #17191b;
    --muted: #70777b;
    --paper: #e8e7e2;
    --panel: #f4f3ee;
    --line: #c9c9c2;
    --strong-line: #17191b;
    --lift-shadow: rgb(23 25 27 / 0.13);
    --acid: #c7f46b;
    --signal: #ff705b;
    --gold: #eab24d;
    --violet: #9e8cff;
    --dark: #111315;
    --dark-2: #181b1e;

    display: flex;
    flex-direction: column;
    gap: 18px;
    min-width: 0;
    min-height: 100%;
    padding: 24px 28px 32px;
    color: var(--ink);
    background: var(--paper);
    font-family: 'Space Grotesk', 'PingFang SC', 'Microsoft YaHei', sans-serif;
  }

  :global(html.dark .workflow-home) {
    --ink: #eff4f1;
    --muted: #9da6aa;
    --paper: #0d0f10;
    --panel: #181b1e;
    --line: #343a3d;
    --strong-line: #697276;
    --lift-shadow: rgb(0 0 0 / 0.38);
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
    justify-content: space-between;
    gap: 20px;
  }

  .eyebrow,
  .ops-strip dt,
  .ops-strip dd,
  .canvas-head,
  .run-copy small,
  .run-duration,
  .error-body span,
  .error-body small,
  .node-popover {
    font-family: 'IBM Plex Mono', 'Cascadia Code', Consolas, monospace;
  }

  .eyebrow {
    color: var(--muted);
    font-size: 10px;
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
    font-size: 16px;
    font-weight: 600;
  }

  .workbench-head p {
    margin-top: 7px;
    color: var(--muted);
    font-size: 13px;
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
    background: #92989b;
    border-radius: 50%;
  }

  .definition-state--active {
    background: #5eaa74;
  }

  .ops-strip {
    padding: 14px 17px;
    color: #eff4f1;
    background: var(--dark);
    border: 1px solid var(--strong-line);
    border-radius: 2px;
  }

  .ops-identity {
    gap: 11px;
    min-width: 210px;
  }

  .ops-live {
    width: 9px;
    height: 9px;
    background: #5eaa74;
    border-radius: 50%;
  }

  .ops-live--running {
    background: var(--gold);
    box-shadow: 0 0 0 4px rgb(234 178 77 / 0.16);
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
    color: #899297;
    font-size: 10px;
  }

  .ops-strip dl {
    flex-wrap: wrap;
    margin: 0;
  }

  .ops-strip dl div {
    min-width: 118px;
    padding: 0 16px;
    border-left: 1px solid #30363a;
  }

  .ops-strip dt {
    color: #899297;
    font-size: 9px;
  }

  .ops-strip dd {
    margin: 5px 0 0;
    font-size: 12px;
  }

  .workbench-stage {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 310px;
    min-height: 520px;
    overflow: hidden;
    background: var(--dark);
    border: 1px solid var(--strong-line);
    border-radius: 2px;
    box-shadow: 8px 8px 0 var(--lift-shadow);
  }

  .canvas-pane {
    position: relative;
    min-width: 0;
    min-height: 520px;
    overflow: hidden;
    border-right: 1px solid #30363a;
  }

  .canvas-head {
    position: absolute;
    top: 13px;
    right: 16px;
    left: 16px;
    z-index: 5;
    color: #899297;
    font-size: 9px;
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
    background: #899297;
  }

  .state-dot--running,
  .run-dot--running,
  .run-dot--retry_waiting {
    background: var(--gold);
  }

  .state-dot--success,
  .run-dot--success {
    background: var(--acid);
  }

  .state-dot--failed,
  .run-dot--failed,
  .run-dot--canceled {
    background: var(--signal);
  }

  .workflow-canvas {
    width: 100%;
    height: 100%;
    min-height: 520px;
  }

  .node-popover {
    position: absolute;
    right: 14px;
    bottom: 14px;
    z-index: 6;
    width: min(340px, calc(100% - 28px));
    padding: 12px;
    color: #eff4f1;
    background: rgb(24 27 30 / 0.96);
    border: 1px solid #4a565b;
    border-radius: 2px;
  }

  .node-popover > div:first-child {
    justify-content: space-between;
    gap: 8px;
  }

  .node-popover span,
  .node-popover small {
    color: #899297;
    font-size: 9px;
  }

  .node-popover p {
    margin: 8px 0;
    color: var(--signal);
    font-size: 10px;
  }

  .run-rail {
    display: flex;
    flex-direction: column;
    min-width: 0;
    padding: 16px;
    color: #eff4f1;
    background: var(--dark-2);
  }

  .run-rail .eyebrow {
    color: #899297;
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
    padding: 12px 4px;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
    border-bottom: 1px solid #30363a;
  }

  .run-row:hover,
  .run-row--selected {
    background: #1e2327;
  }

  .run-dot {
    margin-top: 3px;
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
    font-size: 11px;
    font-style: normal;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .run-copy small,
  .run-duration {
    margin-top: 4px;
    color: #899297;
    font-size: 9px;
  }

  .run-copy em {
    display: -webkit-box;
    margin-top: 5px;
    overflow: hidden;
    color: var(--signal);
    font-size: 9px;
    font-style: normal;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
  }

  .rail-empty,
  .error-empty {
    display: grid;
    place-items: center;
    gap: 8px;
    flex: 1;
    min-height: 220px;
    color: #899297;
    text-align: center;
  }

  .rail-empty strong {
    color: #eff4f1;
    font-size: 12px;
  }

  .rail-empty span {
    max-width: 220px;
    font-size: 10px;
  }

  .detail-link {
    align-self: flex-start;
    margin-top: auto;
    color: var(--acid);
  }

  .lower-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.45fr) minmax(260px, 0.55fr);
    gap: 24px;
  }

  .signal-preview,
  .error-console {
    min-width: 0;
    padding-top: 13px;
    border-top: 2px solid var(--strong-line);
  }

  .preview-meta {
    gap: 8px;
    color: var(--muted);
    font-size: 10px;
  }

  .error-count {
    display: grid;
    width: 28px;
    height: 28px;
    place-items: center;
    color: var(--signal);
    border: 1px solid var(--signal);
  }

  .error-body {
    display: grid;
    gap: 8px;
    padding: 15px 0;
  }

  .error-body span,
  .error-body small {
    color: var(--muted);
    font-size: 9px;
  }

  .error-body strong {
    color: var(--signal);
    font-size: 12px;
    line-height: 1.55;
  }

  .error-empty {
    min-height: 190px;
  }

  .status-text--running,
  .status-text--retry_waiting {
    color: var(--gold);
  }

  .status-text--success {
    color: var(--acid);
  }

  .status-text--failed,
  .status-text--canceled {
    color: var(--signal);
  }

  .guest-console {
    display: grid;
    grid-template-columns: 180px minmax(0, 1fr);
    gap: 36px;
    align-items: center;
    min-height: 420px;
    padding: 48px;
    color: #eff4f1;
    background: var(--dark);
    border: 1px solid var(--strong-line);
    border-radius: 2px;
  }

  .guest-console__signal {
    display: flex;
    gap: 16px;
  }

  .guest-console__signal span {
    width: 32px;
    height: 128px;
    background: var(--acid);
  }

  .guest-console__signal span:nth-child(2) {
    height: 82px;
    background: var(--signal);
  }

  .guest-console__signal span:nth-child(3) {
    height: 104px;
    background: var(--violet);
  }

  .guest-console .eyebrow {
    color: #899297;
  }

  .guest-console p {
    margin: 10px 0 22px;
    color: #899297;
  }

  @media (max-width: 1100px) {
    .workbench-stage,
    .lower-grid {
      grid-template-columns: 1fr;
    }

    .canvas-pane {
      border-right: 0;
      border-bottom: 1px solid #30363a;
    }

    .run-rail {
      min-height: 330px;
    }
  }

  @media (max-width: 720px) {
    .workflow-home {
      padding: 18px 16px 24px;
    }

    .workbench-head,
    .ops-strip {
      align-items: flex-start;
      flex-direction: column;
    }

    h1 {
      font-size: 26px;
    }

    .head-actions,
    .workflow-select {
      width: 100%;
    }

    .ops-strip dl div:first-child {
      padding-left: 0;
      border-left: 0;
    }

    .canvas-head {
      align-items: flex-start;
      flex-direction: column;
      gap: 6px;
    }

    .guest-console {
      grid-template-columns: 1fr;
      padding: 28px;
    }
  }
</style>
