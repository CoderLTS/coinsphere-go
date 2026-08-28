<!-- 工作流执行详情页面或组件：index。 -->
<template>
  <div class="workflow-execution-detail" :style="pageStyle">
    <div class="workflow-execution-detail__layout" v-loading="loading">
      <ElResult v-if="loadError" icon="warning" title="运行日志加载失败" :sub-title="loadError">
        <template #extra>
          <ElSpace>
            <ElButton @click="handleBack">返回工作流日志</ElButton>
            <ElButton type="primary" @click="() => loadPageData()">重新加载</ElButton>
          </ElSpace>
        </template>
      </ElResult>

      <template v-else-if="executionDetail && domainGraph">
        <div class="workflow-execution-detail__stage">
          <div class="workflow-execution-detail__back-wrap">
            <ElTooltip content="返回工作流日志" placement="bottom">
              <ElButton plain class="workflow-execution-detail__icon-btn" @click="handleBack">
                <ElIcon><ArrowLeft /></ElIcon>
              </ElButton>
            </ElTooltip>
          </div>

          <section
            class="workflow-execution-detail__toolbar workflow-execution-detail__overlay-card"
            :style="toolbarStyle"
          >
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
          </section>

          <!-- 详情面板只保留这一个开关。早先标题栏里还有一个可点的 Tag 控制同一个状态，
               两处文案还不一样（"隐藏执行总览" / "收起面板"），看着像两个功能。 -->
          <div
            class="workflow-execution-detail__panel-toggle workflow-execution-detail__overlay-card"
          >
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
          </div>

          <div class="workflow-execution-detail__body">
            <div class="workflow-execution-detail__canvas-pane">
              <WorkflowExecutionCanvas
                :graph="domainGraph"
                :node-attempts="executionDetail.nodeAttempts"
                :start-node-id="executionDetail.startNodeId"
                @selection-change="handleSelectionChange"
              />
            </div>

            <aside
              v-show="inspectorVisible"
              class="workflow-execution-detail__inspector workflow-execution-detail__inspector--left workflow-execution-detail__overlay-card"
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

                      <div class="workflow-execution-detail__section-title">节点日志</div>
                      <ElEmpty v-if="!attempt.logs.length" description="暂无节点日志" />
                      <div v-else class="workflow-execution-detail__timeline">
                        <div
                          v-for="line in attempt.logs"
                          :key="line.id"
                          class="workflow-execution-detail__timeline-item"
                        >
                          <span>{{ formatDateTime(line.loggedAt) }}</span>
                          <strong>{{ line.level.toUpperCase() }} · {{ line.message }}</strong>
                          <small>{{ formatLogFields(line.fields) }}</small>
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

                    <div class="workflow-execution-detail__section">
                      <div class="workflow-execution-detail__section-title">运行日志</div>
                      <ElEmpty v-if="!executionTimeline.length" description="暂无运行日志" />
                      <div v-else class="workflow-execution-detail__timeline">
                        <div
                          v-for="item in executionTimeline"
                          :key="item.key"
                          class="workflow-execution-detail__timeline-item"
                        >
                          <span>{{ formatDateTime(item.time) }}</span>
                          <strong>{{ item.title }}</strong>
                          <small>{{ item.detail }}</small>
                        </div>
                      </div>
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
  import { ArrowLeft, Hide, View } from '@element-plus/icons-vue'
  import { ElMessage } from 'element-plus'
  import type { WorkflowNodeDefinitionItem } from '@/api/scheduler'
  import {
    fetchNodeDefinitions,
    fetchWorkflowExecutionDetail,
    type WorkflowExecutionDetail
  } from '@/api/scheduler'
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
  const domainGraph = ref<WorkflowDomainGraphModel | null>(null)
  const selectedCellId = ref<string | null>(null)
  const selectedCellType = ref<WorkflowActiveCellType>(null)
  const inspectorVisible = ref(true)
  const { containerMinHeight } = useAutoLayoutHeight(undefined, { updateCssVar: false })
  let pollTimer: number | null = null

  const pageStyle = computed(() => ({
    height: containerMinHeight.value,
    minHeight: containerMinHeight.value
  }))

  const toolbarStyle = computed(() => ({
    width: '720px'
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
    const workflowId = executionDetail.value?.workflowDefinitionId || route.query.workflowId
    const workflowName = executionDetail.value?.workflowDefinitionName || route.query.workflowName
    router.push({
      path: '/scheduler/execution',
      query: { workflowId: String(workflowId || ''), workflowName: String(workflowName || '') }
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
        webhook: 'Webhook 触发'
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
    const text = JSON.stringify(value || {})
    return text === '{}' ? '' : text
  }

  const executionTimeline = computed(() => {
    const detail = executionDetail.value
    if (!detail) return []
    return detail.nodeAttempts
      .flatMap((attempt) =>
        attempt.logs.map((line) => ({
          key: `log-${line.id}`,
          time: line.loggedAt,
          title: `${attempt.nodeName} · ${line.message}`,
          detail: formatLogFields(line.fields)
        }))
      )
      .sort((left, right) => Date.parse(left.time || '') - Date.parse(right.time || ''))
  })

  const shouldPollExecution = computed(() => {
    const status = executionDetail.value?.status
    return status === 'queued' || status === 'running' || status === 'retry_waiting'
  })

  const clearPollTimer = () => {
    if (pollTimer !== null) {
      window.clearTimeout(pollTimer)
      pollTimer = null
    }
  }

  const schedulePoll = () => {
    clearPollTimer()
    if (!shouldPollExecution.value) return
    pollTimer = window.setTimeout(() => {
      void loadPageData({ preserveSelection: true, silent: true })
    }, 2000)
  }

  const loadPageData = async (options: { preserveSelection?: boolean; silent?: boolean } = {}) => {
    const executionId = Number(route.params.executionId)
    if (!Number.isFinite(executionId) || executionId <= 0) {
      loadError.value = '运行日志不存在'
      return
    }

    clearPollTimer()
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
      // 节点定义和执行详情一起加载，随后把服务端 graph 映射成前端 domain graph，
      // 这样详情画布和编辑器画布可以共用一套节点形状与映射逻辑。
      const [detail, nodeDefinitions] = await Promise.all([
        fetchWorkflowExecutionDetail(executionId),
        fetchNodeDefinitions().catch(() => [] as WorkflowNodeDefinitionItem[])
      ])
      executionDetail.value = detail
      // 详情画布共用编辑器那套映射，注册表镜像也得同步，端口与分支才画得对。
      syncNodeDefinitions(nodeDefinitions)
      domainGraph.value = mapServerGraphToDomain(
        detail.graph || { nodes: [], edges: [] },
        nodeDefinitions,
        flattenMaterials(nodeDefinitions)
      )
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
      schedulePoll()
    }
  }

  watch(
    () => route.params.executionId,
    () => {
      // 详情页通过参数切换不同 execution 时，复用页面实例并重新加载数据。
      clearPollTimer()
      void loadPageData()
    },
    { immediate: true }
  )

  onBeforeUnmount(() => {
    clearPollTimer()
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
    position: relative;
    flex: 1 1 auto;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
  }

  .workflow-execution-detail__toolbar,
  .workflow-execution-detail__inspector {
    background: var(--workflow-overlay-bg);
    border: 1px solid var(--workflow-overlay-border);
    box-shadow: 0 12px 30px rgb(31 35 48 / 0.12);
  }

  .workflow-execution-detail__overlay-card {
    position: absolute;
    z-index: 6;
  }

  .workflow-execution-detail__back-wrap {
    position: absolute;
    top: 12px;
    left: 0;
    z-index: 6;
  }

  .workflow-execution-detail__toolbar {
    top: 12px;
    left: 50%;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    gap: 6px;
    max-width: calc(100% - 184px);
    padding: 8px 10px 10px;
    border-radius: 8px;
    transform: translateX(-50%);
  }

  .workflow-execution-detail__panel-toggle {
    display: none;
  }

  .workflow-execution-detail__panel-btn {
    gap: 6px;
    height: 36px;
    padding: 0 12px;
    color: var(--workflow-overlay-text);
    background: var(--workflow-overlay-bg);
    border-color: transparent;
    border-radius: 7px;
    box-shadow: 0 6px 16px rgb(31 35 48 / 0.08);
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
    position: absolute;
    inset: 0;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
  }

  .workflow-execution-detail__canvas-pane {
    position: absolute;
    inset: 0;
    display: block;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
    isolation: isolate;
  }

  .workflow-execution-detail__canvas-pane > * {
    width: 100%;
    min-width: 0;
    height: 100%;
    min-height: 0;
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

    bottom: 12px;
    display: flex;
    flex-direction: column;
    width: 332px;
    min-width: 332px;
    max-width: 332px;
    overflow: hidden;
    isolation: isolate;
    border-radius: 8px;
  }

  .workflow-execution-detail__inspector--left {
    top: 82px;
    right: 8px;
    left: auto;
  }

  .workflow-execution-detail__inspector--right {
    top: 108px;
    right: 0;
    left: auto;
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

  .workflow-execution-detail__timeline {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .workflow-execution-detail__timeline-item {
    display: grid;
    grid-template-columns: 132px minmax(0, 1fr);
    gap: 4px 10px;
    padding: 10px;
    background: var(--workflow-overlay-soft);
    border-left: 3px solid var(--el-color-primary);
  }

  .workflow-execution-detail__timeline-item span,
  .workflow-execution-detail__timeline-item small {
    font-size: 12px;
    color: var(--workflow-overlay-muted);
  }

  .workflow-execution-detail__timeline-item small {
    grid-column: 2;
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
    .workflow-execution-detail__toolbar {
      max-width: calc(100% - 164px);
    }

    .workflow-execution-detail__inspector {
      width: 304px;
      min-width: 304px;
      max-width: 304px;
    }
  }

  @media (max-width: 768px) {
    .workflow-execution-detail__layout {
      padding: 8px;
    }

    .workflow-execution-detail__toolbar {
      top: 8px;
      right: 8px;
      left: 52px;
      width: auto !important;
      max-width: none;
      transform: none;
    }

    .workflow-execution-detail__summary {
      flex-wrap: wrap;
    }

    .workflow-execution-detail__body {
      position: absolute;
      min-height: 0;
    }

    .workflow-execution-detail__inspector {
      inset: auto 8px 8px;
      width: auto;
      min-width: 0;
      max-width: none;
      height: min(52%, 480px);
      min-height: 260px;
    }

    .workflow-execution-detail__panel-toggle {
      position: absolute;
      top: 74px;
      right: 8px;
      z-index: 8;
      display: block;
    }

    .workflow-execution-detail__header-status {
      display: none;
    }
  }
</style>
