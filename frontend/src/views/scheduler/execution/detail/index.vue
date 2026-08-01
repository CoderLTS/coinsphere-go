<!-- 工作流执行详情页面或组件：index。 -->
<template>
  <div class="workflow-execution-detail" :style="pageStyle">
    <div class="workflow-execution-detail__layout" v-loading="loading">
      <ElResult
        v-if="loadError"
        icon="warning"
        title="执行详情加载失败"
        :sub-title="loadError"
      >
        <template #extra>
          <ElSpace>
            <ElButton @click="handleBack">返回执行记录</ElButton>
            <ElButton type="primary" @click="() => loadPageData()">重新加载</ElButton>
          </ElSpace>
        </template>
      </ElResult>

      <template v-else-if="executionDetail && domainGraph">
        <div class="workflow-execution-detail__stage">
          <div class="workflow-execution-detail__back-wrap">
            <ElTooltip content="返回执行记录" placement="bottom">
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
              <div class="workflow-execution-detail__title">{{ executionDetail.workflowDefinitionName }}</div>
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
              <ElTooltip :content="inspectorVisible ? '隐藏执行总览' : '显示执行总览'" placement="bottom">
                <ElTag
                  type="info"
                  effect="plain"
                  :class="[
                    'workflow-execution-detail__toggle-tag',
                    { 'workflow-execution-detail__toggle-tag--active': inspectorVisible }
                  ]"
                  @click="inspectorVisible = !inspectorVisible"
                >
                  {{ inspectorVisible ? '隐藏执行总览' : '显示执行总览' }}
                </ElTag>
              </ElTooltip>
            </div>
          </div>

        </section>

          <div class="workflow-execution-detail__panel-toggle workflow-execution-detail__overlay-card">
            <ElTooltip :content="inspectorVisible ? '隐藏详情面板' : '显示详情面板'" placement="bottom">
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
              :node-logs="executionDetail.nodeLogs"
              :transition-logs="executionDetail.transitionLogs"
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
              <ElButton
                v-if="selectedCellId"
                link
                type="primary"
                @click="clearSelection"
              >
                清除选中
              </ElButton>
            </div>

            <ElScrollbar class="workflow-execution-detail__inspector-scroll">
              <div class="workflow-execution-detail__inspector-body">
                <template v-if="selectedNode">
                  <div class="workflow-execution-detail__section">
                    <div class="workflow-execution-detail__section-title">节点概览</div>
                    <ElDescriptions :column="1" border size="small">
                      <ElDescriptionsItem label="节点名称">{{ selectedNode.data.title }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="节点类型">{{ selectedNode.data.typeCode }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="执行次数">{{ selectedNodeLogs.length }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="执行状态">
                        <ElTag :type="statusTagType(selectedNodeStatus)" effect="plain">
                          {{ statusLabel(selectedNodeStatus) }}
                        </ElTag>
                      </ElDescriptionsItem>
                    </ElDescriptions>
                  </div>

                  <div class="workflow-execution-detail__section">
                    <div class="workflow-execution-detail__section-title">节点配置</div>
                    <div class="workflow-execution-detail__json-block">
                      <pre>{{ formatJsonText(selectedNode.data.config) }}</pre>
                    </div>
                  </div>

                  <ElAlert
                    v-if="selectedNodeLogs.some((item) => item.errorMessage)"
                    class="workflow-execution-detail__alert"
                    type="error"
                    :closable="false"
                    :title="selectedNodeLogs.find((item) => item.errorMessage)?.errorMessage || '节点执行失败'"
                  />

                  <div
                    v-for="(log, index) in selectedNodeLogs"
                    :key="log.id"
                    class="workflow-execution-detail__section workflow-execution-detail__log-card"
                  >
                    <div class="workflow-execution-detail__section-title">
                      {{ selectedNodeLogs.length > 1 ? `第 ${index + 1} 次节点执行` : '节点执行详情' }}
                    </div>

                    <ElDescriptions :column="1" border size="small">
                      <ElDescriptionsItem label="开始时间">{{ log.startedAt || '--' }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="结束时间">{{ log.finishedAt || '--' }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="耗时">{{ formatDuration(log.durationMs) }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="状态">
                        <ElTag :type="statusTagType(log.status)" effect="plain">
                          {{ statusLabel(log.status) }}
                        </ElTag>
                      </ElDescriptionsItem>
                    </ElDescriptions>

                    <div class="workflow-execution-detail__json-group">
                      <div class="workflow-execution-detail__json-block">
                        <div class="workflow-execution-detail__json-title">输入快照</div>
                        <pre>{{ formatJsonText(log.inputSnapshotJson) }}</pre>
                      </div>
                      <div class="workflow-execution-detail__json-block">
                        <div class="workflow-execution-detail__json-title">输出快照</div>
                        <pre>{{ formatJsonText(log.outputSnapshotJson) }}</pre>
                      </div>
                    </div>
                  </div>
                </template>

                <template v-else-if="selectedEdge">
                  <div class="workflow-execution-detail__section">
                    <div class="workflow-execution-detail__section-title">边流转概览</div>
                    <ElDescriptions :column="1" border size="small">
                      <ElDescriptionsItem label="源节点">{{ selectedEdgeSourceTitle }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="目标节点">{{ selectedEdgeTargetTitle }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="连线标签">{{ selectedEdge.data.label || '--' }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="命中次数">{{ selectedEdgeTransitions.length }}</ElDescriptionsItem>
                    </ElDescriptions>
                  </div>

                  <ElEmpty
                    v-if="!selectedEdgeTransitions.length"
                    description="这条边在本次执行中没有被实际命中"
                  />

                  <div
                    v-for="(transition, index) in selectedEdgeTransitions"
                    :key="transition.id"
                    class="workflow-execution-detail__section workflow-execution-detail__log-card"
                  >
                    <div class="workflow-execution-detail__section-title">
                      {{ transitionTitle(transition, index) }}
                    </div>

                    <ElDescriptions :column="1" border size="small">
                      <ElDescriptionsItem label="流转顺序">{{ transition.traversalIndex }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="循环轮次">
                        {{ transition.iterationIndex ?? '--' }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="分支标识">
                        {{ transition.branchKey || '--' }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="记录时间">
                        {{ transition.createdAt || '--' }}
                      </ElDescriptionsItem>
                    </ElDescriptions>

                    <div class="workflow-execution-detail__json-block">
                      <div class="workflow-execution-detail__json-title">边上传递的数据</div>
                      <pre>{{ formatJsonText(transition.payloadSnapshotJson) }}</pre>
                    </div>
                  </div>
                </template>

                <template v-else>
                  <div class="workflow-execution-detail__section">
                    <div class="workflow-execution-detail__section-title">执行总览</div>
                    <ElDescriptions :column="1" border size="small">
                      <ElDescriptionsItem label="工作流">{{ executionDetail.workflowDefinitionName }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="定义版本">
                        {{ executionDetail.workflowDefinitionCode }} / v{{ executionDetail.workflowDefinitionVersion }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="开始入口">{{ executionDetail.startEntryKey || '--' }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="开始时间">{{ executionDetail.startedAt || executionDetail.queuedAt || '--' }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="结束时间">{{ executionDetail.finishedAt || '--' }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="执行耗时">{{ formatDuration(executionDetail.durationMs) }}</ElDescriptionsItem>
                      <ElDescriptionsItem label="触发方式">
                        {{ triggerTypeLabel(executionDetail.triggerType) }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="执行状态">
                        <ElTag :type="statusTagType(executionDetail.status)" effect="plain">
                          {{ statusLabel(executionDetail.status) }}
                        </ElTag>
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="当前尝试次数">
                        {{ executionDetail.attemptCount }}/{{ executionDetail.maxAttempts }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="当前 Worker">
                        {{ executionDetail.workerId || '--' }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="下次重试时间">
                        {{ executionDetail.nextRetryAt || '--' }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="失败分类">
                        {{ executionDetail.failureCategory || '--' }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="节点执行数">
                        {{ executedNodeCount }}/{{ domainGraph.nodes.length }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="边流转数">
                        {{ executionDetail.transitionLogs.length }}/{{ domainGraph.edges.length }}
                      </ElDescriptionsItem>
                      <ElDescriptionsItem label="边流转日志">
                        {{ executionDetail.transitionLogs.length ? `${executionDetail.transitionLogs.length} 条` : '暂无流转日志' }}
                      </ElDescriptionsItem>
                    </ElDescriptions>
                  </div>

                  <div class="workflow-execution-detail__section">
                    <div class="workflow-execution-detail__section-title">尝试历史</div>
                    <ElEmpty
                      v-if="!executionDetail.attempts.length"
                      description="当前执行还没有落库的 attempt 历史"
                    />
                    <ElTable v-else :data="executionDetail.attempts" stripe size="small">
                      <ElTableColumn prop="attempt" label="尝试" width="72" align="center" />
                      <ElTableColumn prop="workerId" label="Worker" min-width="150" show-overflow-tooltip />
                      <ElTableColumn label="状态" width="110" align="center">
                        <template #default="{ row }">
                          <ElTag :type="statusTagType(row.status)" effect="plain">
                            {{ statusLabel(row.status) }}
                          </ElTag>
                        </template>
                      </ElTableColumn>
                      <ElTableColumn prop="startedAt" label="开始时间" min-width="160" />
                      <ElTableColumn prop="finishedAt" label="结束时间" min-width="160" />
                      <ElTableColumn prop="failureCategory" label="失败分类" min-width="120" />
                    </ElTable>
                  </div>

                  <ElAlert
                    v-if="!executionDetail.transitionLogs.length"
                    class="workflow-execution-detail__alert"
                    type="info"
                    :closable="false"
                    title="当前执行没有边级流转日志，图上未命中边会自动保持弱化显示。"
                  />

                  <div class="workflow-execution-detail__section">
                    <div class="workflow-execution-detail__section-title">执行输入</div>
                    <div class="workflow-execution-detail__json-block">
                      <pre>{{ formatJsonText(executionDetail.inputSnapshotJson) }}</pre>
                    </div>
                  </div>

                  <div class="workflow-execution-detail__section">
                    <div class="workflow-execution-detail__section-title">执行上下文</div>
                    <div class="workflow-execution-detail__json-block">
                      <pre>{{ formatJsonText(executionDetail.contextSnapshotJson) }}</pre>
                    </div>
                  </div>

                  <div class="workflow-execution-detail__section">
                    <div class="workflow-execution-detail__section-title">执行结果</div>
                    <div class="workflow-execution-detail__json-block">
                      <pre>{{ formatJsonText(executionDetail.resultSnapshotJson) }}</pre>
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
  import type { WorkflowExecutionTransitionLog, WorkflowNodeDefinitionItem } from '@/api/scheduler'
  import { fetchNodeDefinitions, fetchWorkflowExecutionDetail, type WorkflowExecutionDetail } from '@/api/scheduler'
  import { useAutoLayoutHeight } from '@/hooks/core/useLayoutHeight'
  import WorkflowExecutionCanvas from './components/WorkflowExecutionCanvas.vue'
  import { flattenMaterials, mapServerGraphToDomain } from '@/views/scheduler/workflow/editor/workflow-editor.mapper'
  import type { WorkflowActiveCellType, WorkflowDomainGraphModel, WorkflowDomainNode, WorkflowDomainEdge } from '@/views/scheduler/workflow/editor/types'

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

  const nodeMap = computed(() => new Map((domainGraph.value?.nodes || []).map((item) => [item.id, item])))
  const edgeMap = computed(() => new Map((domainGraph.value?.edges || []).map((item) => [item.id, item])))

  const executedNodeCount = computed(() => {
    // 节点执行数按 nodeId 去重，避免 foreach / 重复命中导致同一节点被重复计数。
    const ids = new Set((executionDetail.value?.nodeLogs || []).map((item) => item.nodeId))
    return ids.size
  })

  const inspectorTitle = computed(() => {
    if (selectedCellType.value === 'node' && selectedNode.value) return `节点详情 · ${selectedNode.value.data.title}`
    if (selectedCellType.value === 'edge' && selectedEdge.value) return '连线流转详情'
    return '执行总览'
  })

  const selectedNode = computed<WorkflowDomainNode | null>(() => {
    if (selectedCellType.value !== 'node' || !selectedCellId.value) return null
    return nodeMap.value.get(selectedCellId.value) || null
  })

  const selectedNodeLogs = computed(() => {
    if (!selectedNode.value) return []
    return (executionDetail.value?.nodeLogs || []).filter((item) => item.nodeId === selectedNode.value?.id)
  })

  const selectedNodeStatus = computed(() => {
    const logs = selectedNodeLogs.value
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

  const selectedEdgeTransitions = computed(() => {
    if (!selectedEdge.value) return []
    return (executionDetail.value?.transitionLogs || []).filter((item) => item.edgeId === selectedEdge.value?.id)
  })

  const selectedEdgeSourceTitle = computed(() => nodeMap.value.get(selectedEdge.value?.source || '')?.data.title || '--')
  const selectedEdgeTargetTitle = computed(() => nodeMap.value.get(selectedEdge.value?.target || '')?.data.title || '--')

  const handleBack = () => {
    if (window.history.length > 1) {
      router.back()
      return
    }
    router.push('/scheduler/execution')
  }

  const clearSelection = () => {
    selectedCellId.value = null
    selectedCellType.value = null
  }

  const handleSelectionChange = (payload: { cellId: string | null; cellType: WorkflowActiveCellType }) => {
    // 画布只负责回传“当前选中了什么”，详情面板由当前页面统一切换。
    selectedCellId.value = payload.cellId
    selectedCellType.value = payload.cellType
    if (payload.cellId && payload.cellType) {
      inspectorVisible.value = true
    }
  }

  const statusLabel = (status: string) =>
    (
      {
        queued: '排队中',
        running: '运行中',
        retry_waiting: '等待重试',
        success: '成功',
        failed: '失败'
      } as Record<string, string>
    )[status] || status || '--'

  const statusTagType = (status: string) => {
    if (status === 'failed') return 'danger'
    if (status === 'success') return 'success'
    if (status === 'running' || status === 'retry_waiting') return 'warning'
    return 'info'
  }

  const triggerTypeLabel = (value: string) =>
    (
      {
        manual: '手动触发',
        schedule: '定时触发',
        event: '事件触发',
        webhook: 'Webhook 触发'
      } as Record<string, string>
    )[value] || value || '--'

  const formatDuration = (value?: number | null) => {
    if (!value && value !== 0) return '--'
    if (value < 1000) return `${value} ms`
    return `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)} s`
  }

  const formatJsonText = (value: unknown) => {
    if (value === null || value === undefined || value === '') return '{}'
    if (typeof value === 'string') {
      try {
        return JSON.stringify(JSON.parse(value), null, 2)
      } catch {
        return value
      }
    }
    try {
      return JSON.stringify(value, null, 2)
    } catch {
      return String(value)
    }
  }

  const transitionTitle = (transition: WorkflowExecutionTransitionLog, index: number) => {
    if (transition.iterationIndex !== null && transition.iterationIndex !== undefined) {
      return `第 ${transition.iterationIndex + 1} 轮流转`
    }
    return `第 ${index + 1} 次流转`
  }

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
      loadError.value = '执行记录不存在'
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
      domainGraph.value = mapServerGraphToDomain(
        detail.graph || { nodes: [], edges: [] },
        nodeDefinitions,
        flattenMaterials(nodeDefinitions)
      )
    } catch (error: any) {
      if (!options.silent || !executionDetail.value) {
        executionDetail.value = null
        domainGraph.value = null
        loadError.value = error?.message || '加载执行详情失败'
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
    width: 100%;
    height: 100%;
    flex: 1 1 auto;
    min-height: 0;
    min-width: 0;
    overflow: hidden;
    background: linear-gradient(180deg, #fbfdff 0%, #f5f8fd 100%);
  }

  .workflow-execution-detail__layout {
    display: flex;
    flex-direction: column;
    flex: 1 1 auto;
    width: 100%;
    height: 100%;
    max-height: 100%;
    min-height: 0;
    min-width: 0;
    padding: 12px 10px 8px;
    box-sizing: border-box;
    gap: 12px;
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
    border: 1px solid rgba(203, 213, 225, 0.86);
    background: rgba(255, 255, 255, 0.96);
    box-shadow: 0 12px 26px rgba(15, 23, 42, 0.08);
    backdrop-filter: blur(12px);
  }

  .workflow-execution-detail__overlay-card {
    position: absolute;
    z-index: 6;
  }

  .workflow-execution-detail__back-wrap {
    position: absolute;
    z-index: 6;
    top: 12px;
    left: 0;
  }

  .workflow-execution-detail__toolbar {
    top: 12px;
    left: 50%;
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px 10px 10px;
    border-radius: 18px;
    box-sizing: border-box;
    max-width: calc(100% - 184px);
  }

  .workflow-execution-detail__panel-toggle {
    display: none;
  }

  .workflow-execution-detail__panel-btn {
    height: 36px;
    border-radius: 12px;
    border-color: transparent;
    background: rgba(255, 255, 255, 0.96);
    color: #334155;
    box-shadow: 0 10px 24px rgba(15, 23, 42, 0.08);
    backdrop-filter: blur(12px);
  }

  .workflow-execution-detail__icon-btn {
    width: 34px;
    height: 34px;
    padding: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    border-color: transparent;
    background: transparent;
    color: #334155;
    box-shadow: none;
    backdrop-filter: none;
  }

  .workflow-execution-detail__panel-btn {
    gap: 6px;
    padding: 0 12px;
  }

  .workflow-execution-detail__toggle-tag {
    cursor: pointer;
    user-select: none;
  }

  .workflow-execution-detail__toggle-tag--active {
    color: var(--el-color-primary);
    border-color: var(--el-color-primary-light-5);
    background: var(--el-color-primary-light-9);
  }

  .workflow-execution-detail__icon-btn:hover,
  .workflow-execution-detail__panel-btn:hover {
    background: #f3f4fb;
    color: #1f3fb7;
  }

  .workflow-execution-detail__header-main {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
    justify-content: space-between;
  }

  .workflow-execution-detail__header-copy {
    min-width: 0;
    flex: 1 1 auto;
  }

  .workflow-execution-detail__title {
    font-size: 16px;
    font-weight: 700;
    line-height: 22px;
    color: #0f172a;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .workflow-execution-detail__header-status {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .workflow-execution-detail__summary {
    display: flex;
    align-items: center;
    flex-wrap: nowrap;
    gap: 6px;
    overflow: hidden;
  }

  .workflow-execution-detail__summary-item {
    min-width: 0;
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 5px;
    padding: 3px 7px;
    border-radius: 12px;
    border: 1px solid rgba(226, 232, 240, 0.8);
    background: #f8fafc;
    flex: 0 1 auto;
  }

  .workflow-execution-detail__summary-label {
    font-size: 10px;
    color: #64748b;
    white-space: nowrap;
  }

  .workflow-execution-detail__summary-item strong {
    font-size: 11px;
    line-height: 15px;
    color: #0f172a;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .workflow-execution-detail__body {
    position: absolute;
    inset: 0;
    min-height: 0;
    min-width: 0;
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
    height: 100%;
    min-width: 0;
    min-height: 0;
  }

  .workflow-execution-detail__inspector {
    bottom: 12px;
    width: 332px;
    min-width: 332px;
    max-width: 332px;
    display: flex;
    flex-direction: column;
    border-radius: 20px;
    overflow: hidden;
    isolation: isolate;
  }

  .workflow-execution-detail__inspector--left {
    top: 82px;
    left: 8px;
    right: auto;
  }

  .workflow-execution-detail__inspector--right {
    top: 108px;
    right: 0;
    left: auto;
  }

  .workflow-execution-detail__inspector-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid rgba(226, 232, 240, 0.86);
  }

  .workflow-execution-detail__inspector-title {
    min-width: 0;
    font-size: 15px;
    font-weight: 700;
    color: #0f172a;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
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
    color: #334155;
  }

  .workflow-execution-detail__log-card {
    padding: 14px;
    border: 1px solid rgba(226, 232, 240, 0.92);
    border-radius: 16px;
    background: #fff;
    box-shadow: 0 8px 20px rgba(15, 23, 42, 0.05);
  }

  .workflow-execution-detail__json-group {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .workflow-execution-detail__json-block {
    padding: 12px;
    border: 1px solid rgba(226, 232, 240, 0.88);
    border-radius: 14px;
    background: linear-gradient(180deg, #fcfdff 0%, #f8fbff 100%);
  }

  .workflow-execution-detail__json-title {
    margin-bottom: 10px;
    font-size: 12px;
    font-weight: 700;
    color: #475569;
  }

  .workflow-execution-detail__json-block pre {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 12px;
    line-height: 18px;
    color: #0f172a;
    font-family: 'Cascadia Code', 'SFMono-Regular', Consolas, monospace;
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

  @media (max-width: 1080px) {
    .workflow-execution-detail__layout {
      padding: 10px;
    }

    .workflow-execution-detail__stage {
      display: flex;
      flex-direction: column;
      gap: 14px;
    }

    .workflow-execution-detail__overlay-card {
      position: static;
      transform: none;
    }

    .workflow-execution-detail__toolbar {
      width: auto !important;
      max-width: none;
    }

    .workflow-execution-detail__summary {
      flex-wrap: wrap;
    }

    .workflow-execution-detail__body {
      position: relative;
      flex: 1 1 auto;
      min-height: 420px;
    }

    .workflow-execution-detail__inspector {
      top: auto;
      right: auto;
      bottom: auto;
      width: auto;
      min-width: 0;
      max-width: none;
      min-height: 420px;
    }
  }
</style>
