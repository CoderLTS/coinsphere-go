<!-- 工作流执行详情页面或组件：index。 -->
<template>
  <div class="workflow-execution-detail" :style="pageStyle">
    <div class="workflow-execution-detail__layout" v-loading="loading">
      <ElResult v-if="loadError" icon="warning" title="运行日志加载失败" :sub-title="loadError">
        <template #extra>
          <ElSpace>
            <ElButton @click="handleBack">返回工作流定义</ElButton>
            <ElButton type="primary" @click="() => loadPageData()">重新加载</ElButton>
          </ElSpace>
        </template>
      </ElResult>

      <template v-else-if="executionDetail && domainGraph">
        <div class="workflow-execution-detail__stage">
          <section class="workflow-execution-detail__toolbar">
            <ElTooltip content="返回工作流定义" placement="bottom">
              <ElButton plain class="workflow-execution-detail__icon-btn" @click="handleBack">
                <ElIcon><ArrowLeft /></ElIcon>
              </ElButton>
            </ElTooltip>

            <div class="workflow-execution-detail__header-main">
              <div class="workflow-execution-detail__header-copy">
                <div class="workflow-execution-detail__title">{{
                  executionDetail.workflowDefinitionName
                }}</div>
              </div>

              <div class="workflow-execution-detail__header-status">
                <ElTag :type="statusTagType(executionDetail.status)" effect="plain">
                  {{ statusLabel(executionDetail.status) }}
                </ElTag>
                <ElTag type="info" effect="plain">
                  {{ triggerTypeLabel(executionDetail.triggerType) }}
                </ElTag>
                <ElTag v-if="executionDetail.finishedAt" type="info" effect="plain">
                  耗时 {{ formatDuration(executionDetail.durationMs) }}
                </ElTag>
              </div>
            </div>

            <ElTooltip
              :content="inspectorVisible ? '隐藏详情面板' : '显示详情面板'"
              placement="bottom"
            >
              <ElButton
                plain
                class="workflow-execution-detail__panel-btn"
                @click="inspectorVisible = !inspectorVisible"
              >
                <ElIcon>
                  <component :is="inspectorVisible ? Hide : View" />
                </ElIcon>
                <span>{{ inspectorVisible ? '收起面板' : '展开面板' }}</span>
              </ElButton>
            </ElTooltip>
          </section>

          <div
            class="workflow-execution-detail__body"
            :class="{
              'workflow-execution-detail__body--inspector-hidden': !inspectorVisible
            }"
          >
            <div class="workflow-execution-detail__main">
              <div class="workflow-execution-detail__canvas-pane">
                <WorkflowExecutionCanvas
                  :graph="domainGraph"
                  :node-attempts="executionDetail.nodeAttempts"
                  :start-node-id="executionDetail.startNodeId"
                  @selection-change="handleSelectionChange"
                />
              </div>

              <section class="workflow-execution-detail__live-logs" aria-label="实时运行日志">
                <div class="workflow-execution-detail__live-logs-header">
                  <div class="workflow-execution-detail__live-logs-title">
                    <span
                      class="workflow-execution-detail__live-logs-dot"
                      :class="{
                        'workflow-execution-detail__live-logs-dot--active': realtimeConnected
                      }"
                    />
                    <span>实时运行日志</span>
                    <span class="workflow-execution-detail__live-logs-count">
                      {{ liveLogRows.length }} 条
                    </span>
                  </div>
                  <div class="workflow-execution-detail__live-logs-actions">
                    <ElButton size="small" :icon="Clock" @click="handleHistory"
                      >历史日志</ElButton
                    >
                    <ElTag
                      :type="statusTagType(executionDetail.status)"
                      effect="plain"
                      size="small"
                    >
                      {{ statusLabel(executionDetail.status) }}
                    </ElTag>
                  </div>
                </div>
                <div ref="liveLogViewport" class="workflow-execution-detail__live-logs-body">
                  <ElEmpty v-if="!liveLogRows.length" description="等待节点日志" />
                  <div
                    v-for="line in liveLogRows"
                    :key="line.key"
                    class="workflow-execution-detail__live-log-row"
                  >
                    <time>{{ formatDateTime(line.time) }}</time>
                    <span
                      class="workflow-execution-detail__live-log-level"
                      :data-level="line.level"
                    >
                      {{ line.level.toUpperCase() }}
                    </span>
                    <span class="workflow-execution-detail__live-log-node">{{
                      line.nodeName
                    }}</span>
                    <div class="workflow-execution-detail__live-log-content">
                      <span class="workflow-execution-detail__live-log-message">{{
                        line.message
                      }}</span>
                      <pre
                        v-if="line.fields"
                        class="workflow-execution-detail__live-log-fields"
                        >{{ line.fields }}</pre
                      >
                    </div>
                  </div>
                </div>
              </section>
            </div>

            <aside
              v-show="inspectorVisible"
              class="workflow-execution-detail__inspector"
            >
              <div class="workflow-execution-detail__inspector-header">
                <div class="workflow-execution-detail__inspector-title">{{ inspectorTitle }}</div>
                <ElButton v-if="selectedCellId" link type="primary" @click="clearSelection">
                  清除选中
                </ElButton>
              </div>

              <ElScrollbar class="workflow-execution-detail__inspector-scroll">
                <div class="workflow-execution-detail__inspector-body">
                  <template v-if="selectedNode">
                    <div class="workflow-execution-detail__section">
                      <div class="workflow-execution-detail__section-title">节点概览</div>
                      <ElDescriptions :column="1" border size="small">
                        <ElDescriptionsItem label="节点名称">{{
                          selectedNode.data.title
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="执行次数">{{
                          selectedNodeAttempts.length
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="执行状态">
                          <ElTag :type="statusTagType(selectedNodeStatus)" effect="plain">
                            {{ statusLabel(selectedNodeStatus) }}
                          </ElTag>
                        </ElDescriptionsItem>
                      </ElDescriptions>
                    </div>

                    <ElAlert
                      v-if="selectedNodeAttempts.some((item) => item.error)"
                      class="workflow-execution-detail__alert"
                      type="error"
                      :closable="false"
                      :title="
                        selectedNodeAttempts.find((item) => item.error)?.error?.summary ||
                        '节点执行失败'
                      "
                    />

                    <div
                      v-for="attempt in selectedNodeAttempts"
                      :key="attempt.id"
                      class="workflow-execution-detail__section workflow-execution-detail__log-card"
                    >
                      <div class="workflow-execution-detail__section-title">
                        {{ `第 ${attempt.attempt} 次尝试 · 循环 ${attempt.loopIteration}` }}
                      </div>

                      <ElDescriptions :column="1" border size="small">
                        <ElDescriptionsItem label="开始时间">{{
                          formatDateTime(attempt.startedAt)
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="结束时间">{{
                          formatDateTime(attempt.finishedAt)
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="耗时">{{
                          formatDuration(attempt.durationMs)
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="状态">
                          <ElTag :type="statusTagType(attempt.status)" effect="plain">
                            {{ statusLabel(attempt.status) }}
                          </ElTag>
                        </ElDescriptionsItem>
                      </ElDescriptions>

                      <ElAlert
                        v-if="attempt.error"
                        class="workflow-execution-detail__alert"
                        type="error"
                        :closable="false"
                        :title="attempt.error.summary"
                      />

                      <div class="workflow-execution-detail__json-group">
                        <div class="workflow-execution-detail__json-block">
                          <div class="workflow-execution-detail__json-title">输入摘要</div>
                          <pre>{{ formatJSON(attempt.inputSummary) }}</pre>
                        </div>
                        <div class="workflow-execution-detail__json-block">
                          <div class="workflow-execution-detail__json-title">输出摘要</div>
                          <pre>{{ formatJSON(attempt.outputSummary) }}</pre>
                        </div>
                      </div>
                    </div>
                  </template>

                  <template v-else-if="selectedEdge">
                    <div class="workflow-execution-detail__section">
                      <div class="workflow-execution-detail__section-title">边流转概览</div>
                      <ElDescriptions :column="1" border size="small">
                        <ElDescriptionsItem label="源节点">{{
                          selectedEdgeSourceTitle
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="目标节点">{{
                          selectedEdgeTargetTitle
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="连线标签">{{
                          selectedEdge.data.label || '--'
                        }}</ElDescriptionsItem>
                      </ElDescriptions>
                    </div>
                  </template>

                  <template v-else>
                    <div class="workflow-execution-detail__section">
                      <div class="workflow-execution-detail__section-title">执行总览</div>
                      <ElDescriptions :column="1" border size="small">
                        <ElDescriptionsItem label="工作流">{{
                          executionDetail.workflowDefinitionName
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="定义版本">
                          v{{ executionDetail.workflowDefinitionVersion }}
                        </ElDescriptionsItem>
                        <ElDescriptionsItem label="开始入口">{{
                          executionDetail.entryName || '--'
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="开始时间">{{
                          formatDateTime(executionDetail.startedAt || executionDetail.queuedAt)
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="结束时间">{{
                          formatDateTime(executionDetail.finishedAt)
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="执行耗时">{{
                          formatDuration(executionDetail.durationMs)
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="触发方式">
                          {{
                            executionDetail.triggerLabel ||
                            triggerTypeLabel(executionDetail.triggerType)
                          }}
                        </ElDescriptionsItem>
                        <ElDescriptionsItem label="执行状态">
                          <ElTag :type="statusTagType(executionDetail.status)" effect="plain">
                            {{ executionDetail.statusLabel || statusLabel(executionDetail.status) }}
                          </ElTag>
                        </ElDescriptionsItem>
                        <ElDescriptionsItem v-if="executionDetail.error" label="失败摘要">
                          {{ executionDetail.error.summary }}
                        </ElDescriptionsItem>
                        <ElDescriptionsItem label="节点执行数">
                          {{ executedNodeCount }}/{{ domainGraph.nodes.length }}
                        </ElDescriptionsItem>
                      </ElDescriptions>
                    </div>

                    <div v-if="executionDetail.event" class="workflow-execution-detail__section">
                      <div class="workflow-execution-detail__section-title">事件摘要</div>
                      <ElDescriptions :column="1" border size="small">
                        <ElDescriptionsItem label="事件类型">{{
                          executionDetail.event.type
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="事件 ID">{{
                          executionDetail.event.eventId
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="来源">{{
                          executionDetail.event.source
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="事件时间">{{
                          formatDateTime(executionDetail.event.time)
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="分区">{{
                          executionDetail.event.partitionKey
                        }}</ElDescriptionsItem>
                      </ElDescriptions>
                    </div>

                    <div class="workflow-execution-detail__section">
                      <div class="workflow-execution-detail__section-title">结果摘要</div>
                      <div class="workflow-execution-detail__json-block">
                        <pre>{{ formatJSON(executionDetail.resultSummary) }}</pre>
                      </div>
                    </div>

                    <div
                      v-if="executionDetail.artifacts.length"
                      class="workflow-execution-detail__section"
                    >
                      <div class="workflow-execution-detail__section-title">制品引用</div>
                      <ElDescriptions
                        v-for="artifact in executionDetail.artifacts"
                        :key="`${artifact.sha256}-${artifact.nodeInstanceId}`"
                        :column="1"
                        border
                        size="small"
                      >
                        <ElDescriptionsItem label="节点">{{
                          artifact.nodeInstanceId || '--'
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="类型">{{
                          artifact.mediaType
                        }}</ElDescriptionsItem>
                        <ElDescriptionsItem label="大小"
                          >{{ artifact.sizeBytes }} bytes</ElDescriptionsItem
                        >
                        <ElDescriptionsItem label="SHA-256">{{
                          artifact.sha256
                        }}</ElDescriptionsItem>
                      </ElDescriptions>
                    </div>
                  </template>
                </div>
              </ElScrollbar>
            </aside>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
  import { ArrowLeft, Clock, Hide, View } from '@element-plus/icons-vue'
  import { ElMessage } from 'element-plus'
  import type { WorkflowNodeDefinitionItem } from '@/api/scheduler'
  import {
    fetchNodeDefinitions,
    fetchWorkflowExecutionDetail,
    type WorkflowExecutionDetail
  } from '@/api/scheduler'
  import {
    buildWorkflowRunsWsUrl,
    fetchWorkflowRuns,
    WORKFLOW_RUNS_WS_PROTOCOL
  } from '@/api/workflows'
  import { useUserStore } from '@/store/modules/user'
  import { useAutoLayoutHeight } from '@/hooks/core/useLayoutHeight'
  import { formatDateTime } from '@/utils/date'
  import WorkflowExecutionCanvas from './components/WorkflowExecutionCanvas.vue'
  import {
    flattenMaterials,
    mapServerGraphToDomain
  } from '@/views/scheduler/workflow/editor/workflow-editor.mapper'
  import { syncNodeDefinitions } from '@/views/scheduler/workflow/editor/node-registry'
  import type {
    WorkflowActiveCellType,
    WorkflowDomainGraphModel,
    WorkflowDomainNode,
    WorkflowDomainEdge
  } from '@/views/scheduler/workflow/editor/types'

  defineOptions({ name: 'SchedulerWorkflowExecutionDetailPage' })

  const router = useRouter()
  const route = useRoute()
  const loading = ref(false)
  const loadError = ref('')
  const executionDetail = ref<WorkflowExecutionDetail | null>(null)
  const activeExecutionId = ref(Number(route.params.executionId))
  const domainGraph = ref<WorkflowDomainGraphModel | null>(null)
  const selectedCellId = ref<string | null>(null)
  const selectedCellType = ref<WorkflowActiveCellType>(null)
  const inspectorVisible = ref(true)
  const liveLogViewport = ref<HTMLDivElement | null>(null)
  const { containerMinHeight } = useAutoLayoutHeight(undefined, { updateCssVar: false })
  const userStore = useUserStore()
  const realtimeConnected = ref(false)
  let workflowSocket: WebSocket | null = null
  let workflowReconnectTimer: ReturnType<typeof setTimeout> | null = null
  let realtimeRefreshRunning = false
  let realtimeRefreshPending = false

  const pageStyle = computed(() => ({
    height: containerMinHeight.value,
    minHeight: containerMinHeight.value
  }))

  const nodeMap = computed(
    () => new Map((domainGraph.value?.nodes || []).map((item) => [item.id, item]))
  )
  const edgeMap = computed(
    () => new Map((domainGraph.value?.edges || []).map((item) => [item.id, item]))
  )

  const executedNodeCount = computed(() => {
    // 节点执行数按 nodeId 去重，避免 foreach / 重复命中导致同一节点被重复计数。
    const ids = new Set((executionDetail.value?.nodeAttempts || []).map((item) => item.nodeId))
    return ids.size
  })

  const inspectorTitle = computed(() => {
    if (selectedCellType.value === 'node' && selectedNode.value)
      return `节点详情 · ${selectedNode.value.data.title}`
    if (selectedCellType.value === 'edge' && selectedEdge.value) return '连线详情'
    return '执行总览'
  })

  const selectedNode = computed<WorkflowDomainNode | null>(() => {
    if (selectedCellType.value !== 'node' || !selectedCellId.value) return null
    return nodeMap.value.get(selectedCellId.value) || null
  })

  const selectedNodeAttempts = computed(() => {
    if (!selectedNode.value) return []
    return (executionDetail.value?.nodeAttempts || []).filter(
      (item) => item.nodeId === selectedNode.value?.id
    )
  })

  const selectedNodeStatus = computed(() => {
    const logs = selectedNodeAttempts.value
    if (!logs.length) return 'queued'
    if (logs.some((item) => item.status === 'failed')) return 'failed'
    if (logs.some((item) => item.status === 'running')) return 'running'
    if (logs.some((item) => item.status === 'queued')) return 'queued'
    return 'success'
  })

  const selectedEdge = computed<WorkflowDomainEdge | null>(() => {
    if (selectedCellType.value !== 'edge' || !selectedCellId.value) return null
    return edgeMap.value.get(selectedCellId.value) || null
  })

  const selectedEdgeSourceTitle = computed(
    () => nodeMap.value.get(selectedEdge.value?.source || '')?.data.title || '--'
  )
  const selectedEdgeTargetTitle = computed(
    () => nodeMap.value.get(selectedEdge.value?.target || '')?.data.title || '--'
  )

  const handleBack = () => {
    router.push('/scheduler/definition')
  }

  const handleHistory = () => {
    router.push({
      path: '/scheduler/execution',
      query: {
        workflowId: String(activeWorkflowId.value),
        workflowName: executionDetail.value?.workflowDefinitionName || ''
      }
    })
  }

  const clearSelection = () => {
    selectedCellId.value = null
    selectedCellType.value = null
  }

  const handleSelectionChange = (payload: {
    cellId: string | null
    cellType: WorkflowActiveCellType
  }) => {
    // 画布只负责回传“当前选中了什么”，详情面板由当前页面统一切换。
    selectedCellId.value = payload.cellId
    selectedCellType.value = payload.cellType
    if (payload.cellId && payload.cellType) {
      inspectorVisible.value = true
    }
  }

  const statusLabel = (status: string) =>
    (
      ({
        queued: '排队中',
        running: '运行中',
        retry_waiting: '等待重试',
        waiting: '等待中',
        success: '成功',
        failed: '失败',
        canceled: '已取消'
      }) as Record<string, string>
    )[status] ||
    status ||
    '--'

  const statusTagType = (status: string) => {
    if (status === 'failed') return 'danger'
    if (status === 'success') return 'success'
    if (status === 'running' || status === 'retry_waiting' || status === 'waiting') return 'warning'
    return 'info'
  }

  const triggerTypeLabel = (value: string) =>
    (
      ({
        manual: '手动触发',
        schedule: '定时触发',
        event: '事件触发',
        stream: '流式触发',
        webhook: 'Webhook 触发',
        failure: '失败触发'
      }) as Record<string, string>
    )[value] ||
    value ||
    '--'

  const formatDuration = (value?: number | null) => {
    if (!value && value !== 0) return '--'
    if (value < 1000) return `${value} ms`
    return `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)} s`
  }

  const formatJSON = (value: Record<string, unknown>) => JSON.stringify(value || {}, null, 2)

  const formatLogFields = (value: Record<string, unknown>) => {
    const text = JSON.stringify(value || {}, null, 2)
    return text === '{}' ? '' : text
  }

  type LiveLogRow = {
    key: string
    time: string
    level: string
    nodeName: string
    message: string
    fields: string
    order: number
  }

  const liveLogRows = ref<LiveLogRow[]>([])

  const appendLiveLogs = (detail: WorkflowExecutionDetail) => {
    const genericMessages = new Set(['节点开始执行', '节点执行成功'])
    const businessLogs = new Map<number, (typeof detail.logs)[number]>()
    detail.logs.forEach((line) => businessLogs.set(line.id, line))
    detail.nodeAttempts.forEach((attempt) =>
      attempt.logs.forEach((line) => businessLogs.set(line.id, line))
    )
    const rows: LiveLogRow[] = []
    detail.nodeAttempts.forEach((attempt) => {
      const suffix = `第 ${attempt.attempt} 次尝试 · 循环 ${attempt.loopIteration}`
      rows.push({
        key: `run-${detail.id}-attempt-${attempt.id}-start`,
        time: attempt.startedAt,
        level: 'info',
        nodeName: attempt.nodeName,
        message: `开始 · ${suffix}`,
        fields: formatLogFields(attempt.inputSummary),
        order: 0
      })
      if (attempt.finishedAt) {
        const errorFields = attempt.error
          ? { category: attempt.error.category, summary: attempt.error.summary }
          : attempt.outputSummary
        rows.push({
          key: `run-${detail.id}-attempt-${attempt.id}-end`,
          time: attempt.finishedAt,
          level: attempt.error ? 'error' : 'info',
          nodeName: attempt.nodeName,
          message: `结束 · ${statusLabel(attempt.status)} · ${formatDuration(attempt.durationMs)}`,
          fields: formatLogFields(errorFields),
          order: 2
        })
      }
    })
    const nodeNames = new Map(detail.nodeAttempts.map((attempt) => [attempt.id, attempt.nodeName]))
    businessLogs.forEach((line) => {
      if (genericMessages.has(line.message)) return
      rows.push({
        key: `run-${detail.id}-log-${line.id}`,
        time: line.loggedAt,
        level: line.level,
        nodeName: nodeNames.get(line.runNodeId) || '工作流',
        message: line.message,
        fields: formatLogFields(line.fields),
        order: 1
      })
    })
    liveLogRows.value = rows.sort((left, right) => {
      const timeDiff = Date.parse(left.time || '') - Date.parse(right.time || '')
      return timeDiff || left.order - right.order || left.key.localeCompare(right.key)
    })
  }

  const shouldFollowLatest = computed(() => {
    return route.query.followLatest === '1'
  })

  const activeWorkflowId = computed(() =>
    Number(executionDetail.value?.workflowDefinitionId || route.query.workflowId || 0)
  )

  const clearWorkflowReconnect = () => {
    if (workflowReconnectTimer) {
      clearTimeout(workflowReconnectTimer)
      workflowReconnectTimer = null
    }
  }

  const closeWorkflowSocket = () => {
    clearWorkflowReconnect()
    realtimeConnected.value = false
    if (workflowSocket) {
      workflowSocket.close()
      workflowSocket = null
    }
  }

  const connectWorkflowSocket = () => {
    closeWorkflowSocket()
    const workflowId = activeWorkflowId.value
    if (workflowId <= 0 || userStore.accessMode !== 'authenticated' || !userStore.accessToken)
      return
    const socket = new WebSocket(buildWorkflowRunsWsUrl(window.location.origin, workflowId), [
      WORKFLOW_RUNS_WS_PROTOCOL,
      userStore.accessToken
    ])
    workflowSocket = socket
    socket.onopen = () => {
      if (workflowSocket !== socket) return
      if (socket.protocol !== WORKFLOW_RUNS_WS_PROTOCOL) {
        socket.close(1002, 'unexpected websocket protocol')
        return
      }
      realtimeConnected.value = true
      void syncLatestExecution()
    }
    socket.onmessage = (event) => {
      if (workflowSocket !== socket) return
      try {
        const envelope = JSON.parse(event.data) as {
          type?: string
          version?: number
          data?: { workflowId?: number; runId?: number }
        }
        const update = envelope.data
        if (
          envelope.type !== 'workflow.run.updated' ||
          envelope.version !== 1 ||
          !update ||
          update.workflowId !== workflowId ||
          typeof update.runId !== 'number' ||
          !Number.isSafeInteger(update.runId)
        ) {
          return
        }
        const updatedRunId = update.runId
        const currentId = activeExecutionId.value
        if (shouldFollowLatest.value && updatedRunId > currentId) {
          activeExecutionId.value = updatedRunId
          void refreshExecutionFromRealtime()
          return
        }
        if (updatedRunId === currentId) {
          void refreshExecutionFromRealtime()
        }
      } catch {
        // Ignore malformed frames; the next valid update restores the view.
      }
    }
    socket.onclose = () => {
      if (workflowSocket !== socket) return
      workflowSocket = null
      realtimeConnected.value = false
      clearWorkflowReconnect()
      workflowReconnectTimer = setTimeout(() => {
        workflowReconnectTimer = null
        connectWorkflowSocket()
      }, 3000)
    }
    socket.onerror = () => {
      if (workflowSocket === socket) realtimeConnected.value = false
    }
  }

  const refreshExecutionFromRealtime = async () => {
    if (realtimeRefreshRunning) {
      realtimeRefreshPending = true
      return
    }
    realtimeRefreshRunning = true
    do {
      realtimeRefreshPending = false
      await loadPageData({ preserveSelection: true, silent: true })
    } while (realtimeRefreshPending)
    realtimeRefreshRunning = false
  }

  const syncLatestExecution = async () => {
    if (shouldFollowLatest.value) {
      try {
        const result = await fetchWorkflowRuns(activeWorkflowId.value, { limit: 1 })
        const latestRunId = result.records[0]?.id
        if (latestRunId && latestRunId > activeExecutionId.value) {
          activeExecutionId.value = latestRunId
        }
      } catch {
        // Keep the current run visible if the reconnect snapshot fails.
      }
    }
    await refreshExecutionFromRealtime()
  }

  const loadPageData = async (options: { preserveSelection?: boolean; silent?: boolean } = {}) => {
    const executionId = activeExecutionId.value
    if (!Number.isFinite(executionId) || executionId <= 0) {
      loadError.value = '运行日志不存在'
      return
    }

    if (!options.silent || !executionDetail.value) {
      loading.value = true
    }
    if (!options.silent) {
      loadError.value = ''
    }
    if (!options.preserveSelection) {
      clearSelection()
    }

    try {
      const detail = await fetchWorkflowExecutionDetail(executionId)
      if (executionId !== activeExecutionId.value) return
      const previousDetail = executionDetail.value
      const graphChanged =
        !domainGraph.value ||
        previousDetail?.workflowDefinitionId !== detail.workflowDefinitionId ||
        previousDetail.workflowDefinitionVersion !== detail.workflowDefinitionVersion
      if (graphChanged) {
        const nodeDefinitions = await fetchNodeDefinitions().catch(
          () => [] as WorkflowNodeDefinitionItem[]
        )
        if (executionId !== activeExecutionId.value) return
        syncNodeDefinitions(nodeDefinitions)
        domainGraph.value = mapServerGraphToDomain(
          detail.graph || { nodes: [], edges: [] },
          nodeDefinitions,
          flattenMaterials(nodeDefinitions)
        )
      }
      appendLiveLogs(detail)
      executionDetail.value = detail
      if (!options.preserveSelection && detail.status === 'failed') {
        const failed = detail.nodeAttempts.find(
          (item) => item.status === 'failed' || Boolean(item.error)
        )
        if (failed) {
          selectedCellId.value = failed.nodeId
          selectedCellType.value = 'node'
        }
      }
    } catch (error: any) {
      if (!options.silent || !executionDetail.value) {
        executionDetail.value = null
        domainGraph.value = null
        loadError.value = error?.message || '加载运行日志失败'
        if (!options.silent) {
          ElMessage.error(loadError.value)
        }
      }
    } finally {
      if (!options.silent || !executionDetail.value) {
        loading.value = false
      }
    }
  }

  watch(
    () => route.params.executionId,
    (executionId) => {
      // 详情页通过参数切换不同 execution 时，复用页面实例并重新加载数据。
      activeExecutionId.value = Number(executionId)
      liveLogRows.value = []
      void loadPageData()
    },
    { immediate: true }
  )

  watch(activeWorkflowId, connectWorkflowSocket, { immediate: true })

  watch(
    () => liveLogRows.value.map((line) => `${line.key}:${line.message}`),
    () => {
      nextTick(() => {
        const viewport = liveLogViewport.value
        if (viewport) viewport.scrollTop = viewport.scrollHeight
      })
    }
  )

  onBeforeUnmount(() => {
    closeWorkflowSocket()
  })
</script>

<style scoped lang="scss">
  .workflow-execution-detail {
    --workflow-page-bg: var(--default-bg-color);
    --workflow-overlay-bg: var(--workflow-panel-bg);
    --workflow-overlay-raised: var(--workflow-panel-raised);
    --workflow-overlay-soft: var(--workflow-panel-soft);
    --workflow-overlay-soft-2: var(--art-gray-200);
    --workflow-overlay-text: var(--workflow-panel-text);
    --workflow-overlay-regular: var(--art-gray-800);
    --workflow-overlay-muted: var(--workflow-panel-muted);
    --workflow-overlay-placeholder: var(--art-gray-500);
    --workflow-overlay-border: var(--workflow-panel-border);
    --workflow-overlay-border-soft: var(--workflow-panel-border);
    --workflow-overlay-border-subtle: var(--art-card-border);

    flex: 1 1 auto;
    width: 100%;
    min-width: 0;
    height: 100%;
    min-height: 0;
    overflow: hidden;
    background: var(--workflow-page-bg);
  }

  .workflow-execution-detail__layout {
    box-sizing: border-box;
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 12px;
    width: 100%;
    min-width: 0;
    height: 100%;
    min-height: 0;
    max-height: 100%;
    padding: 10px;
    overflow: hidden;
  }

  .workflow-execution-detail__stage {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
  }

  .workflow-execution-detail__toolbar,
  .workflow-execution-detail__inspector {
    background: var(--workflow-overlay-bg);
    border: 1px solid var(--workflow-overlay-border);
    box-shadow: 0 6px 18px rgb(31 35 48 / 0.08);
  }

  .workflow-execution-detail__toolbar {
    box-sizing: border-box;
    display: grid;
    flex: 0 0 auto;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: 10px;
    align-items: center;
    width: 100%;
    padding: 8px 10px;
    border-radius: 8px;
  }

  .workflow-execution-detail__panel-btn {
    gap: 6px;
    height: 36px;
    padding: 0 12px;
    color: var(--workflow-overlay-text);
    background: var(--workflow-overlay-bg);
    border-color: transparent;
    border-radius: 7px;
    box-shadow: none;
  }

  .workflow-execution-detail__icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    padding: 0;
    color: var(--workflow-overlay-text);
    background: transparent;
    backdrop-filter: none;
    border-color: transparent;
    border-radius: 7px;
    box-shadow: none;
  }

  .workflow-execution-detail__icon-btn:hover,
  .workflow-execution-detail__panel-btn:hover {
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .workflow-execution-detail__header-main {
    display: flex;
    gap: 10px;
    align-items: center;
    justify-content: space-between;
    min-width: 0;
  }

  .workflow-execution-detail__header-copy {
    flex: 1 1 auto;
    min-width: 0;
  }

  .workflow-execution-detail__title {
    overflow: hidden;
    font-size: 16px;
    font-weight: 700;
    line-height: 22px;
    color: var(--workflow-overlay-text);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .workflow-execution-detail__header-status {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    align-items: center;
    justify-content: flex-end;
  }

  .workflow-execution-detail__summary {
    display: flex;
    flex-wrap: nowrap;
    gap: 6px;
    align-items: center;
    overflow: hidden;
  }

  .workflow-execution-detail__summary-item {
    display: flex;
    flex: 0 1 auto;
    flex-direction: row;
    gap: 5px;
    align-items: center;
    min-width: 0;
    padding: 3px 7px;
    background: var(--workflow-overlay-soft-2);
    border: 1px solid var(--workflow-overlay-border-soft);
    border-radius: 6px;
  }

  .workflow-execution-detail__summary-label {
    font-size: 10px;
    color: var(--workflow-overlay-muted);
    white-space: nowrap;
  }

  .workflow-execution-detail__summary-item strong {
    overflow: hidden;
    font-size: 11px;
    line-height: 15px;
    color: var(--workflow-overlay-text);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .workflow-execution-detail__body {
    display: grid;
    flex: 1 1 auto;
    grid-template-columns: minmax(0, 1fr) 332px;
    gap: 10px;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
  }

  .workflow-execution-detail__body--inspector-hidden {
    grid-template-columns: minmax(0, 1fr);
  }

  .workflow-execution-detail__main {
    display: grid;
    grid-template-rows: minmax(220px, 1fr) minmax(180px, 32%);
    gap: 10px;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
  }

  .workflow-execution-detail__canvas-pane {
    position: relative;
    display: block;
    min-width: 0;
    min-height: 220px;
    overflow: hidden;
    isolation: isolate;
  }

  .workflow-execution-detail__canvas-pane > * {
    width: 100%;
    min-width: 0;
    height: 100%;
    min-height: 0;
  }

  .workflow-execution-detail__live-logs {
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
    background: var(--workflow-overlay-bg);
    border: 1px solid var(--workflow-overlay-border);
    border-radius: 8px;
    box-shadow: 0 8px 24px rgb(31 35 48 / 0.08);
  }

  .workflow-execution-detail__live-logs-header {
    display: flex;
    flex: 0 0 44px;
    align-items: center;
    justify-content: space-between;
    padding: 0 14px;
    border-bottom: 1px solid var(--workflow-overlay-border-subtle);
  }

  .workflow-execution-detail__live-logs-title {
    display: inline-flex;
    gap: 8px;
    align-items: center;
    font-size: 13px;
    font-weight: 700;
    color: var(--workflow-overlay-text);
  }

  .workflow-execution-detail__live-logs-actions {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .workflow-execution-detail__live-logs-dot {
    width: 7px;
    height: 7px;
    background: var(--workflow-overlay-muted);
    border-radius: 50%;
  }

  .workflow-execution-detail__live-logs-dot--active {
    background: var(--el-color-success);
    box-shadow: 0 0 0 3px var(--el-color-success-light-8);
    animation: workflow-live-log-pulse 1.8s ease-in-out infinite;
  }

  @keyframes workflow-live-log-pulse {
    0%,
    100% {
      opacity: 1;
    }

    50% {
      opacity: 0.45;
    }
  }

  .workflow-execution-detail__live-logs-count {
    font-size: 11px;
    font-weight: 500;
    color: var(--workflow-overlay-muted);
  }

  .workflow-execution-detail__live-logs-body {
    flex: 1 1 auto;
    min-height: 0;
    padding: 6px 0;
    overflow: auto;
    scrollbar-width: thin;
  }

  .workflow-execution-detail__live-log-row {
    display: grid;
    grid-template-columns: 142px 48px 150px minmax(180px, 1fr);
    gap: 10px;
    align-items: start;
    min-height: 30px;
    padding: 6px 14px;
    font-family: 'Cascadia Code', SFMono-Regular, Consolas, monospace;
    font-size: 12px;
    border-bottom: 1px solid var(--workflow-overlay-border-subtle);
  }

  .workflow-execution-detail__live-log-row:last-child {
    border-bottom: 0;
  }

  .workflow-execution-detail__live-log-row time,
  .workflow-execution-detail__live-log-node {
    overflow: hidden;
    color: var(--workflow-overlay-muted);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .workflow-execution-detail__live-log-level {
    font-size: 10px;
    font-weight: 700;
    color: var(--el-color-primary);
  }

  .workflow-execution-detail__live-log-level[data-level='warn'] {
    color: var(--el-color-warning);
  }

  .workflow-execution-detail__live-log-level[data-level='error'] {
    color: var(--el-color-danger);
  }

  .workflow-execution-detail__live-log-message {
    display: block;
    color: var(--workflow-overlay-text);
    word-break: break-word;
  }

  .workflow-execution-detail__live-log-content {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 6px;
  }

  .workflow-execution-detail__live-log-fields {
    max-height: 180px;
    margin: 0;
    padding: 6px 8px;
    overflow: auto;
    font-size: 11px;
    line-height: 16px;
    color: var(--workflow-overlay-muted);
    word-break: break-word;
    white-space: pre-wrap;
    background: var(--workflow-overlay-soft);
    border-radius: 4px;
  }

  .workflow-execution-detail__inspector {
    --el-bg-color: var(--workflow-overlay-bg);
    --el-fill-color-blank: var(--workflow-overlay-raised);
    --el-fill-color-light: var(--workflow-overlay-soft);
    --el-fill-color-lighter: var(--workflow-overlay-soft-2);
    --el-border-color-light: var(--workflow-overlay-border-soft);
    --el-border-color-lighter: var(--workflow-overlay-border-subtle);
    --el-text-color-primary: var(--workflow-overlay-text);
    --el-text-color-regular: var(--workflow-overlay-regular);
    --el-text-color-secondary: var(--workflow-overlay-muted);
    --el-text-color-placeholder: var(--workflow-overlay-placeholder);

    display: flex;
    flex-direction: column;
    width: 100%;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
    isolation: isolate;
    border-radius: 8px;
  }

  .workflow-execution-detail__inspector-header {
    display: flex;
    gap: 12px;
    align-items: center;
    justify-content: space-between;
    padding: 14px 16px;
    border-bottom: 1px solid var(--workflow-overlay-border-subtle);
  }

  .workflow-execution-detail__inspector-title {
    min-width: 0;
    overflow: hidden;
    font-size: 15px;
    font-weight: 700;
    color: var(--workflow-overlay-text);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .workflow-execution-detail__inspector-scroll {
    flex: 1 1 auto;
    min-height: 0;
  }

  .workflow-execution-detail__inspector-scroll :deep(.el-scrollbar__wrap) {
    height: 100%;
    overflow-x: hidden;
  }

  .workflow-execution-detail__inspector-body {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding: 14px;
  }

  .workflow-execution-detail__section {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .workflow-execution-detail__section-title {
    font-size: 13px;
    font-weight: 700;
    color: var(--workflow-overlay-regular);
  }

  .workflow-execution-detail__log-card {
    padding: 14px;
    background: var(--workflow-overlay-raised);
    border: 1px solid var(--workflow-overlay-border-subtle);
    border-radius: 8px;
    box-shadow: none;
  }

  .workflow-execution-detail__json-group {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .workflow-execution-detail__json-block {
    padding: 12px;
    background: var(--workflow-overlay-soft);
    border: 1px solid var(--workflow-overlay-border-soft);
    border-radius: 7px;
  }

  .workflow-execution-detail__json-title {
    margin-bottom: 10px;
    font-size: 12px;
    font-weight: 700;
    color: var(--workflow-overlay-muted);
  }

  .workflow-execution-detail__json-block pre {
    margin: 0;
    font-family: 'Cascadia Code', SFMono-Regular, Consolas, monospace;
    font-size: 12px;
    line-height: 18px;
    color: var(--workflow-overlay-text);
    word-break: break-word;
    white-space: pre-wrap;
  }

  .workflow-execution-detail__alert {
    margin-top: -4px;
  }

  @media (max-width: 1280px) {
    .workflow-execution-detail__body {
      grid-template-columns: minmax(0, 1fr) 304px;
    }

    .workflow-execution-detail__body--inspector-hidden {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  @media (max-width: 980px) {
    .workflow-execution-detail__body,
    .workflow-execution-detail__body--inspector-hidden {
      display: flex;
      flex-direction: column;
      overflow: auto;
    }

    .workflow-execution-detail__main {
      flex: 0 0 620px;
      grid-template-rows: minmax(340px, 1fr) 240px;
      min-height: 620px;
      overflow: visible;
    }

    .workflow-execution-detail__inspector {
      flex: 0 0 380px;
      min-height: 320px;
    }
  }

  @media (max-width: 768px) {
    .workflow-execution-detail__layout {
      padding: 8px;
    }

    .workflow-execution-detail__toolbar {
      gap: 8px;
    }

    .workflow-execution-detail__main {
      flex-basis: 580px;
      grid-template-rows: 320px 250px;
      min-height: 580px;
    }

    .workflow-execution-detail__live-log-row {
      grid-template-columns: 112px 44px minmax(0, 1fr);
      gap: 8px;
    }

    .workflow-execution-detail__live-log-content {
      grid-column: 3;
    }

    .workflow-execution-detail__header-status {
      display: none;
    }

    .workflow-execution-detail__live-logs-header {
      gap: 8px;
      min-height: 44px;
      padding: 6px 10px;
    }
  }
</style>
