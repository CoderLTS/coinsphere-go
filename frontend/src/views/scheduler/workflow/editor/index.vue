<!-- 工作流编辑器页面或组件：index。 -->
<template>
  <div v-loading="loading" class="workflow-editor-page art-full-height">
    <WorkflowCanvas
      class="workflow-editor-page__canvas"
      ref="canvasRef"
      :graph="domainGraph"
      :materials-visible="materialsVisible"
      :materials="materialItems"
      :material-groups="editorMaterialGroups"
      :issues="allIssues"
      :agent-options="agentOptions"
      :notify-user-options="notifyUserOptions"
      :notify-role-options="notifyRoleOptions"
      :notify-channel-options="notifyChannelOptions"
      :notify-options-loading="notifyOptionsLoading"
      :readonly="editorReadonly"
      :dirty-node-ids="dirtyNodeIds"
      :draft-state="draftState"
      :active-cell-id="selection.activeCellId"
      :active-cell-type="selection.activeCellType"
      :edge-editor-cell-id="edgeEditorCellId"
      :pending-edge-draft="pendingEdgeDraft"
      :history-session-key="historySessionKey"
      @request-activate-cell="handleActivationRequest"
      @graph-commit="handleGraphCommit"
      @material-drop="handleMaterialDrop"
      @zoom-change="zoom = $event"
      @rendered="handleCanvasRendered"
      @update-node-draft="handleNodeDraftUpdate"
      @request-commit-node-draft="void commitAndCloseNodeEditor()"
      @request-discard-node-draft="discardNodeDraft"
      @request-close-node-editor="void closeNodeEditor()"
      @request-manage-notify="ElMessage.info('请使用通知渠道配置节点完成该任务。')"
      @request-close-edge-editor="handleCloseEdgeEditor"
      @request-remove-selection="void removeSelection()"
      @request-node-context-action="void handleNodeContextAction($event)"
      @request-open-edge-editor="void handleOpenEdgeEditor($event)"
      @create-pending-edge-draft="handlePendingEdgeDraftCreate"
      @commit-edge-draft="handleEdgeDraftCommit"
    >
      <template #toolbar>
        <WorkflowEditorToolbar
          :mode="mode"
          :saving="saving"
          :validating="validating"
          :status-text="statusText"
          :status-type="statusType"
          :zoom-text="zoomText"
          :materials-visible="materialsVisible"
          :readonly="editorReadonly"
          :activating="activating"
          :cancelling="cancelling"
          :template-mode="Boolean(definitionDetail?.isBuiltin)"
          :action-count="definitionActions.length"
          :can-activate="canActivateDefinition"
          :can-run="canRunDefinition"
          :can-cancel="canCancelExecution"
          @back="handleBack"
          @toggle-materials="handleToggleMaterials"
          @undo="void handleUndo()"
          @redo="void handleRedo()"
          @zoom-out="handleZoomOut"
          @zoom-in="handleZoomIn"
          @center-content="handleCenterContent"
          @fit-view="handleFitView"
          @validate="void handleValidate()"
          @save="void handleSave()"
          @open-meta="void openMetaPopover()"
          @activate="void activateDefinition()"
          @run="runVisible = true"
          @cancel="void cancelLatestExecution()"
          @actions="actionsVisible = true"
        />
      </template>
    </WorkflowCanvas>

    <div v-if="mobileReadonly" class="workflow-editor-page__mobile-note">
      移动端仅支持查看、运行和处置，编辑请使用桌面端。
    </div>

    <section v-if="definitionExecutions.length" class="workflow-editor-page__timeline">
      <header
        ><span>最近执行</span><small>{{ definitionExecutions.length }} 条</small></header
      >
      <button
        v-for="execution in definitionExecutions.slice(0, 5)"
        :key="execution.id"
        type="button"
        @click="router.push(`/runs/${execution.id}`)"
      >
        <i :class="`is-${execution.status}`"></i>
        <span>{{ execution.statusLabel || execution.status }}</span>
        <small>#{{ execution.id }} · {{ formatExecutionTime(execution.queuedAt) }}</small>
      </button>
    </section>

    <div v-if="metaVisible" class="workflow-editor-page__meta">
      <WorkflowMetaPopover
        :visible="metaVisible"
        :model="metaDraft"
        @update:model="metaDraft = $event"
        @submit="handleMetaSubmit"
        @close="metaVisible = false"
      />
    </div>

    <WorkflowRunDialog
      v-model="runVisible"
      :workflow="definitionDetail"
      @started="handleRunStarted"
    />
    <WorkflowActionDrawer
      v-model="actionsVisible"
      :actions="definitionActions"
      @decided="refreshEditorActivity"
    />
  </div>
</template>

<script setup lang="ts">
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { storeToRefs } from 'pinia'
  import { fetchGetRoleList, fetchGetUserList } from '@/api/system'
  import {
    fetchAiModelList,
    fetchAssistantAgentList,
    fetchNotifyChannelList,
    type AiModelConfigItem,
    type AssistantAgentItem,
    type NotifyChannelItem
  } from '@/api/config'
  import { fetchNewsList } from '@/api/data'
  import { fetchMarketSymbols, type MarketSymbol } from '@/api/market'
  import { fetchStrategySignals, type StrategySignalItem } from '@/api/signals'
  import {
    fetchPublishedStrategies,
    fetchStrategyDrafts,
    type StrategyDraftItem,
    type StrategyVersionItem
  } from '@/api/strategy'
  import { fetchTradingAccounts, type TradingAccount } from '@/api/trading'
  import {
    fetchCreateWorkflowDefinition,
    fetchActivateWorkflowDefinition,
    fetchCancelWorkflowExecution,
    fetchNodeDefinitions,
    fetchWorkflowActionList,
    fetchWorkflowDefinitionExecutions,
    fetchWorkflowDefinitionList,
    fetchWorkflowNodeTemplates,
    fetchWorkflowAgentOptions,
    fetchUpdateWorkflowDefinition,
    fetchValidateWorkflowDefinition,
    fetchWorkflowDefinitionDetail
  } from '@/api/scheduler'
  import type {
    WorkflowAgentOption,
    WorkflowDefinitionItem,
    WorkflowExecutionItem,
    WorkflowActionItem,
    RunWorkflowDefinitionResponse,
    WorkflowNodeDefinitionItem,
    WorkflowNodeTemplateItem
  } from '@/api/scheduler'
  import { useMenuStore } from '@/store/modules/menu'
  import { useUserStore } from '@/store/modules/user'
  import { useWorkflowEditorStore } from '@/store/modules/workflow-editor'
  import { buildWorkflowMaterialGroups } from './node-materials'
  import { syncNodeDefinitions } from './node-registry'
  import {
    applyEdgeFormToDomain,
    applyNodeFormToDomain,
    createDomainEdgeFromForm,
    createDefaultDomainGraph,
    createDomainNodeFromType,
    flattenMaterials,
    mapDomainGraphToServer,
    mapDomainNodeToForm,
    mapServerGraphToDomain,
    normalizeDefinitionMeta
  } from './workflow-editor.mapper'
  import { validateNodeFormDraft, validateWorkflowDraft } from './workflow-editor.validator'
  import type {
    WorkflowActiveCellType,
    WorkflowDomainEdge,
    WorkflowDomainGraphModel,
    WorkflowDomainNode,
    WorkflowEditorMetaForm,
    WorkflowEditorMode,
    WorkflowGraphCommitPayload,
    WorkflowMaterialDropPayload,
    WorkflowNodeContextActionPayload,
    WorkflowNodeFormModel,
    WorkflowEdgeFormModel,
    WorkflowNotifyTargetOption
  } from './types'
  import WorkflowCanvas from './components/WorkflowCanvas.vue'
  import WorkflowEditorToolbar from './components/WorkflowEditorToolbar.vue'
  import WorkflowMetaPopover from './components/WorkflowMetaPopover.vue'
  import WorkflowActionDrawer from '@/views/workbench/components/WorkflowActionDrawer.vue'
  import WorkflowRunDialog from '@/views/workbench/components/WorkflowRunDialog.vue'

  defineOptions({ name: 'SchedulerWorkflowDefinitionEditorPage' })

  const route = useRoute()
  const router = useRouter()
  const menuStore = useMenuStore()
  const userStore = useUserStore()
  const editorStore = useWorkflowEditorStore()

  const {
    mode,
    definitionId,
    loading,
    saving,
    validating,
    dirty,
    definitionDetail,
    metaForm,
    nodeDefinitions,
    materialGroups,
    domainGraph,
    selection,
    draftState,
    allIssues,
    baselineGraph
  } = storeToRefs(editorStore)

  const canvasRef = ref<InstanceType<typeof WorkflowCanvas> | null>(null)
  const zoom = ref(1)
  const awaitingInitialRender = ref(false)
  const metaVisible = ref(false)
  const materialsVisible = ref(true)
  const mobileReadonly = ref(false)
  const activating = ref(false)
  const cancelling = ref(false)
  const runVisible = ref(false)
  const actionsVisible = ref(false)
  const definitionExecutions = ref<WorkflowExecutionItem[]>([])
  const actionItems = ref<WorkflowActionItem[]>([])
  const autoOpenedMeta = ref(false)
  const metaDraft = ref<WorkflowEditorMetaForm>(normalizeDefinitionMeta(null))
  const pendingEdgeDraft = ref<WorkflowEdgeFormModel | null>(null)
  const edgeEditorCellId = ref<string | null>(null)
  const undoStack = ref<WorkflowDomainGraphModel[]>([])
  const redoStack = ref<WorkflowDomainGraphModel[]>([])
  const historySessionKey = ref(0)
  const notifyUserOptions = ref<WorkflowNotifyTargetOption[]>([])
  const notifyRoleOptions = ref<WorkflowNotifyTargetOption[]>([])
  const agentOptions = ref<WorkflowAgentOption[]>([])
  const notifyOptionsLoading = ref(false)
  const notifyOptionsLoaded = ref(false)
  const nodeTemplates = ref<WorkflowNodeTemplateItem[]>([])
  const notifyChannels = ref<NotifyChannelItem[]>([])
  const notifyChannelOptions = computed(() => {
    const groups = new Map<string, NotifyChannelItem[]>()
    notifyChannels.value.forEach((item) => {
      const current = groups.get(item.channelType) || []
      current.push(item)
      groups.set(item.channelType, current)
    })
    return Array.from(groups, ([value, items]) => ({
      value,
      label: items.map((item) => item.displayName).join(' / '),
      enabled: items.some((item) => item.isEnabled)
    }))
  })
  const zoomText = computed(() => `${Math.round(zoom.value * 100)}%`)
  const latestExecution = computed(
    () =>
      definitionExecutions.value.find((item) => item.canCancel) ||
      definitionExecutions.value[0] ||
      null
  )
  const definitionActions = computed(() => {
    const executionIds = new Set(definitionExecutions.value.map((item) => item.id))
    return actionItems.value.filter((item) => executionIds.has(item.executionId))
  })
  const hasPermission = (permission: string) => userStore.info.permissions.includes(permission)
  const canSaveDefinition = computed(() => {
    if (currentMode.value === 'create' || definitionDetail.value?.isBuiltin) {
      return hasPermission('scheduler.workflow_definitions.create')
    }
    return hasPermission('scheduler.workflow_definitions.update')
  })
  const editorReadonly = computed(() => mobileReadonly.value || !canSaveDefinition.value)
  const canActivateDefinition = computed(
    () =>
      hasPermission('scheduler.workflow_runtime.activate') &&
      Boolean(
        definitionDetail.value &&
          !definitionDetail.value.isBuiltin &&
          !definitionDetail.value.isActive
      )
  )
  const canRunDefinition = computed(
    () =>
      hasPermission('scheduler.workflow_definitions.run') &&
      Boolean(definitionDetail.value) &&
      !definitionDetail.value?.isBuiltin &&
      (definitionDetail.value?.graph.nodes || []).some((node) => node.type === 'start.manual')
  )
  const canCancelExecution = computed(
    () =>
      hasPermission('scheduler.workflow_definitions.run') &&
      Boolean(latestExecution.value?.canCancel)
  )
  const cloneGraph = (graph: WorkflowDomainGraphModel): WorkflowDomainGraphModel =>
    JSON.parse(JSON.stringify(graph))
  const graphEquals = (left: WorkflowDomainGraphModel, right: WorkflowDomainGraphModel) =>
    JSON.stringify(left) === JSON.stringify(right)
  const resetGraphHistory = () => {
    undoStack.value = []
    redoStack.value = []
  }

  const buildNotifyUserLabel = (item: Api.System.UserListItem) => {
    const displayName = String(item.fullName || item.nickname || '').trim()
    const username = String(item.username || '').trim()
    if (!displayName) return username || `用户 #${item.id}`
    if (!username || username === displayName) return displayName
    return `${displayName} (${username})`
  }

  const buildNotifyRoleLabel = (item: Api.System.RoleListItem) => {
    const displayName = String(item.displayName || '').trim()
    const code = String(item.code || '').trim()
    if (!displayName) return code || `角色 #${item.id}`
    if (!code || code === displayName) return displayName
    return `${displayName} (${code})`
  }

  const currentMode = computed<WorkflowEditorMode>(() => {
    const routeName = String(route.name || '')
    if (routeName === 'WorkflowDefinitionCreate') return 'create'
    return 'edit'
  })

  const currentDefinitionId = computed(() => {
    const raw = route.params.definitionId
    return raw ? Number(raw) : null
  })

  const editorMaterialGroups = computed(() => {
    const baseGroups = materialGroups.value.map((group) => ({ ...group, items: [...group.items] }))
    const materials = baseGroups.flatMap((group) => group.items)
    const templateItems = nodeTemplates.value
      .filter((item) => item.isEnabled)
      .map((item) => {
        const base = materials.find((material) => material.typeCode === item.baseNodeType)
        if (!base) return null
        return {
          ...base,
          title: item.name,
          description: item.description || `基于${item.baseNodeLabel}`,
          presetSubtitle: item.baseNodeLabel,
          presetConfig: item.defaultConfig
        }
      })
      .filter((item): item is NonNullable<typeof item> => Boolean(item))
    return templateItems.length
      ? [{ key: 'my-templates', title: '我的节点模板', items: templateItems }, ...baseGroups]
      : baseGroups
  })

  const setResourceOptions = (
    definitions: WorkflowNodeDefinitionItem[],
    resource: string,
    options: Array<{ value: string | number; label: string }>
  ) => {
    if (!options.length) return
    definitions.forEach((definition) => {
      Object.values(definition.configSchema?.properties || {}).forEach((rawProperty) => {
        const property = rawProperty as Record<string, any>
        if (property.resource !== resource) return
        property.enum = options.map((item) => item.value)
        property.enumLabels = options.map((item) => item.label)
      })
    })
  }

  const enrichNodeDefinitions = (
    source: WorkflowNodeDefinitionItem[],
    strategies: StrategyVersionItem[],
    strategyDrafts: StrategyDraftItem[],
    instruments: MarketSymbol[],
    accounts: TradingAccount[],
    signals: StrategySignalItem[],
    aiModels: AiModelConfigItem[],
    assistants: AssistantAgentItem[],
    channels: NotifyChannelItem[],
    news: Api.Data.NewsListItem[],
    users: Api.System.UserListItem[],
    roles: Api.System.RoleListItem[],
    workflows: WorkflowDefinitionItem[]
  ) => {
    const definitions: WorkflowNodeDefinitionItem[] = JSON.parse(JSON.stringify(source))
    const instrumentOptions = instruments.map((item) => ({
      value: item.id,
      label: `${item.nativeSymbol} · ${item.market === 'usd_m' ? 'USD-M' : 'Spot'} · ${item.status === 'trading' ? '交易中' : '已暂停'}`
    }))
    setResourceOptions(
      definitions,
      'strategy-version',
      strategies.map((item) => ({
        value: item.id,
        label: `${item.name} v${item.versionNumber} · 已发布`
      }))
    )
    setResourceOptions(
      definitions,
      'strategy-draft',
      strategyDrafts.map((item) => ({ value: item.id, label: item.name }))
    )
    setResourceOptions(definitions, 'market-instrument', instrumentOptions)
    setResourceOptions(
      definitions,
      'trading-account',
      accounts.map((item) => ({
        value: item.id,
        label: `${item.name} · ${item.environment} · ${item.status === 'active' ? '运行中' : '已暂停'}`
      }))
    )
    setResourceOptions(
      definitions,
      'strategy-signal',
      signals.map((item) => ({
        value: item.id,
        label: `${item.action.toUpperCase()} · ${item.status} · ${item.id.slice(0, 8)}`
      }))
    )
    setResourceOptions(
      definitions,
      'ai-model',
      aiModels.map((item) => ({
        value: item.id,
        label: `${item.displayName} · ${item.modelIdentifier}`
      }))
    )
    setResourceOptions(
      definitions,
      'assistant',
      assistants.map((item) => ({ value: item.id, label: `${item.displayName} · ${item.code}` }))
    )
    setResourceOptions(
      definitions,
      'notification-channel',
      channels.map((item) => ({
        value: item.id,
        label: `${item.displayName} · ${item.channelTypeLabel}`
      }))
    )
    setResourceOptions(
      definitions,
      'news',
      news.map((item) => ({ value: item.id, label: item.title }))
    )
    setResourceOptions(
      definitions,
      'user',
      users.map((item) => ({ value: item.id, label: buildNotifyUserLabel(item) }))
    )
    setResourceOptions(
      definitions,
      'role',
      roles.map((item) => ({ value: item.id, label: buildNotifyRoleLabel(item) }))
    )
    setResourceOptions(
      definitions,
      'workflow-code',
      workflows.map((item) => ({
        value: item.code,
        label: `${item.displayName} · ${item.isWorkflowActive ? '已激活' : '未激活'}`
      }))
    )
    const entryProperty = definitions.find((item) => item.typeCode === 'workflow.call')
      ?.configSchema?.properties?.entryKey
    if (entryProperty) {
      entryProperty.enumByWorkflowCode = Object.fromEntries(
        workflows.map((workflow) => [
          workflow.code,
          workflow.graph.nodes
            .filter((node) => node.type.startsWith('start.'))
            .map((node) => ({
              value: String(node.config?.entryKey || '').trim(),
              label: String(node.config?.displayName || node.label || '开始入口').trim()
            }))
            .filter((item) => item.value)
        ])
      )
    }
    return definitions
  }

  const materialItems = computed(() => editorMaterialGroups.value.flatMap((group) => group.items))

  const currentNode = computed<WorkflowDomainNode | null>(() => {
    if (selection.value.activeCellType !== 'node' || !selection.value.activeCellId) return null
    return domainGraph.value.nodes.find((node) => node.id === selection.value.activeCellId) || null
  })

  const currentCommittedEdge = computed<WorkflowDomainEdge | null>(() => {
    if (selection.value.activeCellType !== 'edge' || !selection.value.activeCellId) return null
    return domainGraph.value.edges.find((edge) => edge.id === selection.value.activeCellId) || null
  })

  const dirtyNodeIds = computed(() => {
    // 这里专门算出相对基线发生变化的节点，用于画布层高亮“已改动但未保存”的节点。
    const baselineNodeMap = new Map(
      (baselineGraph.value.nodes || []).map((node) => [node.id, node])
    )
    return domainGraph.value.nodes
      .filter((node) => {
        const baselineNode = baselineNodeMap.get(node.id)
        if (!baselineNode) return true
        return (
          JSON.stringify({
            title: node.data.title,
            typeCode: node.data.typeCode,
            config: node.data.config
          }) !==
          JSON.stringify({
            title: baselineNode.data.title,
            typeCode: baselineNode.data.typeCode,
            config: baselineNode.data.config
          })
        )
      })
      .map((node) => node.id)
  })

  const resolveInsertPosition = (typeCode: string, position: { x: number; y: number }) => {
    const material = materialItems.value.find((item) => item.typeCode === typeCode)
    const width = material?.width || 260
    const height = material?.height || 96
    const gap = 144
    const baseX = Math.round(position.x)
    const baseY = Math.round(position.y)

    const overlapsExistingNode = (x: number, y: number) =>
      domainGraph.value.nodes.some((node) => {
        const left = x
        const top = y
        const right = x + width
        const bottom = y + height
        const nodeLeft = node.position.x
        const nodeTop = node.position.y
        const nodeRight = node.position.x + node.size.width
        const nodeBottom = node.position.y + node.size.height

        return !(
          right + gap < nodeLeft ||
          left > nodeRight + gap ||
          bottom + gap < nodeTop ||
          top > nodeBottom + gap
        )
      })

    if (!overlapsExistingNode(baseX, baseY)) {
      return { x: baseX, y: baseY }
    }

    const rightMost = domainGraph.value.nodes.reduce(
      (max, node) => Math.max(max, node.position.x + node.size.width),
      baseX
    )
    const stepX = width + gap
    const stepY = height + gap
    const directions = [
      { x: 1, y: 0 },
      { x: 1, y: 1 },
      { x: 1, y: -1 },
      { x: 0, y: 1 },
      { x: 0, y: -1 },
      { x: -1, y: 1 },
      { x: -1, y: -1 },
      { x: -1, y: 0 }
    ]

    for (let ring = 1; ring <= 8; ring += 1) {
      for (const direction of directions) {
        const candidate = {
          x: Math.max(baseX, rightMost + gap) + direction.x * stepX * (ring - 1),
          y: baseY + direction.y * stepY * ring
        }

        if (!overlapsExistingNode(candidate.x, candidate.y)) {
          return candidate
        }
      }
    }

    return {
      x: rightMost + stepX,
      y: baseY + stepY * 2
    }
  }

  const statusText = computed(() => {
    if (allIssues.value.some((issue) => issue.level === 'error')) return '校验失败'
    if (dirty.value) return '未保存'
    return '已同步'
  })

  const statusType = computed<'default' | 'warning' | 'danger'>(() => {
    if (allIssues.value.some((issue) => issue.level === 'error')) return 'danger'
    if (dirty.value) return 'warning'
    return 'default'
  })

  const syncSelection = (cellId: string | null, cellType: WorkflowActiveCellType) => {
    editorStore.setSelection({
      activeCellId: cellId,
      activeCellType: cellType
    })

    if (cellType !== 'edge' || cellId !== edgeEditorCellId.value) {
      edgeEditorCellId.value = null
    }

    const keepNodeDraft =
      draftState.value.cellType === 'node' &&
      !!draftState.value.cellId &&
      cellType === 'node' &&
      cellId === draftState.value.cellId

    if (!keepNodeDraft) {
      editorStore.clearDraft()
    }
  }

  const openNodeEditorDraft = (cellId: string) => {
    const node = domainGraph.value.nodes.find((item) => item.id === cellId) || null
    if (!node) return false

    const draft = mapDomainNodeToForm(node)
    const validation = validateNodeFormDraft(node, draft, agentOptions.value)
    editorStore.seedDraft({
      cellId,
      cellType: 'node',
      model: draft,
      valid: validation.valid,
      errors: validation.errors
    })
    return true
  }

  const ensureNotifyTargetOptions = async () => {
    if (notifyOptionsLoaded.value || notifyOptionsLoading.value) return

    notifyOptionsLoading.value = true
    try {
      // 通知节点的用户/角色选项按需加载，避免编辑器首次进入时拉太多非必要数据。
      const [userListResult, roleListResult] = await Promise.all([
        fetchGetUserList({ limit: 100, isActive: true }).catch(() => null),
        fetchGetRoleList({ limit: 100, isEnabled: true }).catch(() => null)
      ])

      notifyUserOptions.value = (userListResult?.records || []).map((item) => ({
        value: item.id,
        label: buildNotifyUserLabel(item)
      }))
      notifyRoleOptions.value = (roleListResult?.records || []).map((item) => ({
        value: item.id,
        label: buildNotifyRoleLabel(item)
      }))
      notifyOptionsLoaded.value = true
    } finally {
      notifyOptionsLoading.value = false
    }
  }

  const focusCanvasCell = async (cellId: string | null, cellType: WorkflowActiveCellType) => {
    await nextTick()
    canvasRef.value?.focusCell(cellId, cellType)
  }

  const focusPendingEdge = async () => {
    if (!pendingEdgeDraft.value) return
    await focusCanvasCell(pendingEdgeDraft.value.id, 'edge')
  }

  const clearPendingEdgeDraft = (options?: { clearSelection?: boolean }) => {
    edgeEditorCellId.value = null
    pendingEdgeDraft.value = null
    if (options?.clearSelection) {
      syncSelection(null, null)
    }
  }

  const requirePendingEdgeResolved = async () => {
    if (!pendingEdgeDraft.value) return true
    ElMessage.warning('请先确认或取消当前连线。')
    await focusPendingEdge()
    return false
  }

  const getClientIssues = () => {
    const issues = validateWorkflowDraft(domainGraph.value)

    if (!metaForm.value.displayName.trim()) {
      issues.unshift({
        id: 'client-display-name',
        source: 'client',
        scope: 'graph',
        level: 'error',
        field: 'displayName',
        message: '工作流名称不能为空。'
      })
    }

    return issues
  }

  const refreshClientIssues = () => {
    editorStore.setIssues({
      client: getClientIssues(),
      server: []
    })
  }

  const applyDomainGraph = (
    nextGraph: WorkflowDomainGraphModel,
    options?: {
      recordHistory?: boolean
      clearRedo?: boolean
    }
  ) => {
    // 所有图修改统一经过这里，确保历史栈、重做栈和本地校验一起更新。
    if (graphEquals(domainGraph.value, nextGraph)) {
      editorStore.setDomainGraph(cloneGraph(nextGraph))
      refreshClientIssues()
      return
    }

    if (options?.recordHistory !== false) {
      undoStack.value.push(cloneGraph(domainGraph.value))
      if (undoStack.value.length > 80) {
        undoStack.value.shift()
      }
      if (options?.clearRedo !== false) {
        redoStack.value = []
      }
    }

    editorStore.setDomainGraph(cloneGraph(nextGraph))
    refreshClientIssues()
  }

  const buildPayload = (options?: {
    graph?: WorkflowDomainGraphModel
    meta?: WorkflowEditorMetaForm
  }) => {
    // 发给后端的 payload 永远从 domain graph 映射生成，
    // 不直接依赖 X6 内部状态，保持编辑器单一真源。
    const graph = options?.graph || domainGraph.value
    const meta = options?.meta || metaForm.value
    const trimmedCode = meta.code.trim()
    return {
      code: currentMode.value === 'create' ? undefined : trimmedCode || undefined,
      displayName: meta.displayName.trim(),
      description: meta.description.trim(),
      graph: mapDomainGraphToServer(graph)
    }
  }

  const discardNodeDraft = () => {
    if (!currentNode.value) {
      editorStore.clearDraft()
      return
    }

    const draft = mapDomainNodeToForm(currentNode.value)
    const validation = validateNodeFormDraft(currentNode.value, draft, agentOptions.value)
    editorStore.seedDraft({
      cellId: currentNode.value.id,
      cellType: 'node',
      model: draft,
      valid: validation.valid,
      errors: validation.errors
    })
  }

  const handleNodeDraftUpdate = (model: WorkflowNodeFormModel) => {
    if (!currentNode.value) return
    const nextSnapshot = JSON.stringify(model)
    const currentSnapshot = JSON.stringify(draftState.value.model || null)
    if (nextSnapshot === currentSnapshot) return
    const validation = validateNodeFormDraft(currentNode.value, model, agentOptions.value)
    editorStore.updateDraft({
      model,
      valid: validation.valid,
      errors: validation.errors
    })
  }

  const handlePendingEdgeDraftCreate = (form: WorkflowEdgeFormModel) => {
    pendingEdgeDraft.value = { ...form }
    edgeEditorCellId.value = form.id
  }

  const handleOpenEdgeEditor = async (cellId: string) => {
    const previousSelection = {
      cellId: selection.value.activeCellId,
      cellType: selection.value.activeCellType
    }

    if (pendingEdgeDraft.value && pendingEdgeDraft.value.id !== cellId) {
      ElMessage.warning('请先确认或取消当前连线。')
      await focusPendingEdge()
      return
    }

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) {
      await focusCanvasCell(previousSelection.cellId, previousSelection.cellType)
      return
    }

    syncSelection(cellId, 'edge')
    edgeEditorCellId.value = cellId
    await focusCanvasCell(cellId, 'edge')
  }

  const handleCloseEdgeEditor = () => {
    if (pendingEdgeDraft.value && edgeEditorCellId.value === pendingEdgeDraft.value.id) {
      clearPendingEdgeDraft({ clearSelection: true })
      return
    }

    edgeEditorCellId.value = null
  }

  const commitNodeDraftToGraph = async () => {
    if (!currentNode.value || selection.value.activeCellType !== 'node' || !draftState.value.model)
      return true

    const nextForm = draftState.value.model as WorkflowNodeFormModel
    const validation = validateNodeFormDraft(currentNode.value, nextForm, agentOptions.value)
    editorStore.updateDraft({
      valid: validation.valid,
      errors: validation.errors
    })

    if (!validation.valid) return false

    const nextGraph: WorkflowDomainGraphModel = {
      ...domainGraph.value,
      nodes: domainGraph.value.nodes.map((node) =>
        node.id === currentNode.value?.id ? applyNodeFormToDomain(node, nextForm) : node
      )
    }

    applyDomainGraph(nextGraph)

    const updatedNode = nextGraph.nodes.find((node) => node.id === currentNode.value?.id) || null
    const nextDraft = mapDomainNodeToForm(updatedNode)
    const nextValidation = validateNodeFormDraft(updatedNode, nextDraft, agentOptions.value)
    editorStore.seedDraft({
      cellId: updatedNode?.id || null,
      cellType: updatedNode ? 'node' : null,
      model: nextDraft,
      valid: nextValidation.valid,
      errors: nextValidation.errors
    })
    return true
  }

  const commitAndCloseNodeEditor = async () => {
    const committed = await commitNodeDraftToGraph()
    if (!committed) return false
    editorStore.clearDraft()
    return true
  }

  const confirmInvalidDraftAction = async () => {
    try {
      await ElMessageBox.confirm(
        '当前节点存在未完成且不合法的修改。你可以继续编辑，或者放弃本次修改后继续后续操作。',
        '请先处理草稿',
        {
          type: 'warning',
          confirmButtonText: '继续编辑',
          cancelButtonText: '放弃修改',
          distinguishCancelAndClose: true,
          closeOnClickModal: false,
          closeOnPressEscape: false
        }
      )
      return false
    } catch (error) {
      if (error === 'cancel') {
        discardNodeDraft()
        return true
      }
      return false
    }
  }

  const tryCommitActiveNodeDraft = async () => {
    if (
      selection.value.activeCellType !== 'node' ||
      !draftState.value.cellId ||
      !draftState.value.model
    ) {
      return true
    }

    if (!draftState.value.dirty) return true

    const committed = await commitNodeDraftToGraph()
    if (committed) return true
    return confirmInvalidDraftAction()
  }

  const handleActivationRequest = async (payload: {
    cellId: string | null
    cellType: WorkflowActiveCellType
  }) => {
    // 画布切换选中态前，必须先处理掉未提交节点草稿或待确认边草稿，
    // 否则用户会在节点/边之间切换时丢失上下文。
    const previousSelection = {
      cellId: selection.value.activeCellId,
      cellType: selection.value.activeCellType
    }

    if (pendingEdgeDraft.value) {
      const pendingEdgeId = pendingEdgeDraft.value.id
      const isPendingSelection =
        previousSelection.cellType === 'edge' && previousSelection.cellId === pendingEdgeId
      const isSelectingPendingEdge = payload.cellType === 'edge' && payload.cellId === pendingEdgeId

      if (isSelectingPendingEdge && isPendingSelection) return

      if (!payload.cellId && !payload.cellType) {
        clearPendingEdgeDraft({ clearSelection: isPendingSelection })
        return
      }

      if (isSelectingPendingEdge) {
        const canLeave = await tryCommitActiveNodeDraft()
        if (!canLeave) {
          clearPendingEdgeDraft()
          await focusCanvasCell(previousSelection.cellId, previousSelection.cellType)
          return
        }

        syncSelection(payload.cellId, payload.cellType)
        return
      }

      ElMessage.warning('请先确认或取消当前连线。')
      await focusPendingEdge()
      return
    }

    if (
      payload.cellId === previousSelection.cellId &&
      payload.cellType === previousSelection.cellType
    )
      return

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) {
      await focusCanvasCell(previousSelection.cellId, previousSelection.cellType)
      return
    }

    syncSelection(payload.cellId, payload.cellType)
  }

  const closeNodeEditor = async () => {
    if (!(await requirePendingEdgeResolved())) return

    const previousSelection = {
      cellId: selection.value.activeCellId,
      cellType: selection.value.activeCellType
    }

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) {
      await focusCanvasCell(previousSelection.cellId, previousSelection.cellType)
      return
    }

    editorStore.clearDraft()
  }

  const handleNodeContextAction = async (payload: WorkflowNodeContextActionPayload) => {
    if (!(await requirePendingEdgeResolved())) return

    if (payload.action === 'edit') {
      const isEditingCurrentNode =
        draftState.value.cellType === 'node' &&
        draftState.value.cellId === payload.cellId &&
        !!draftState.value.model

      if (isEditingCurrentNode) {
        syncSelection(payload.cellId, 'node')
        await focusCanvasCell(payload.cellId, 'node')
        return
      }

      const previousSelection = {
        cellId: selection.value.activeCellId,
        cellType: selection.value.activeCellType
      }

      const canLeave = await tryCommitActiveNodeDraft()
      if (!canLeave) {
        await focusCanvasCell(previousSelection.cellId, previousSelection.cellType)
        return
      }

      syncSelection(payload.cellId, 'node')
      const opened = openNodeEditorDraft(payload.cellId)
      const node = domainGraph.value.nodes.find((item) => item.id === payload.cellId) || null
      if (opened && node?.data.kind === 'notify') {
        void ensureNotifyTargetOptions()
      }
      await focusCanvasCell(payload.cellId, 'node')
      return
    }

    const previousSelection = {
      cellId: selection.value.activeCellId,
      cellType: selection.value.activeCellType
    }

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) {
      await focusCanvasCell(previousSelection.cellId, previousSelection.cellType)
      return
    }

    syncSelection(payload.cellId, 'node')
    canvasRef.value?.removeSelection()
  }

  const removeSelection = async () => {
    if (pendingEdgeDraft.value) {
      const isPendingSelection =
        selection.value.activeCellType === 'edge' &&
        selection.value.activeCellId === pendingEdgeDraft.value.id
      if (isPendingSelection) {
        clearPendingEdgeDraft({ clearSelection: true })
        return
      }

      await requirePendingEdgeResolved()
      return
    }

    const previousSelection = {
      cellId: selection.value.activeCellId,
      cellType: selection.value.activeCellType
    }

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) {
      await focusCanvasCell(previousSelection.cellId, previousSelection.cellType)
      return
    }

    canvasRef.value?.removeSelection()
  }

  const handleGraphCommit = (payload: WorkflowGraphCommitPayload) => {
    applyDomainGraph(payload.graph)
  }

  const handleMaterialDrop = async (payload: WorkflowMaterialDropPayload) => {
    if (!(await requirePendingEdgeResolved())) return

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) return

    const resolvedPosition =
      payload.source === 'drag'
        ? {
            x: Math.round(payload.position.x),
            y: Math.round(payload.position.y)
          }
        : resolveInsertPosition(payload.typeCode, payload.position)

    const nextNode = createDomainNodeFromType(
      payload.typeCode,
      resolvedPosition,
      nodeDefinitions.value,
      materialItems.value,
      domainGraph.value.nodes
    )
    if (payload.presetConfig) {
      nextNode.data.config = { ...nextNode.data.config, ...payload.presetConfig }
      nextNode.data.title = payload.title || nextNode.data.title
      nextNode.data.subtitle = payload.presetSubtitle || nextNode.data.subtitle
      nextNode.data.color = payload.color || nextNode.data.color
    }

    const nextGraph: WorkflowDomainGraphModel = {
      ...domainGraph.value,
      nodes: [...domainGraph.value.nodes, nextNode]
    }

    applyDomainGraph(nextGraph)
    syncSelection(nextNode.id, 'node')
  }

  const handleEdgeDraftCommit = (form: WorkflowEdgeFormModel) => {
    if (pendingEdgeDraft.value?.id === form.id) {
      const nextGraph: WorkflowDomainGraphModel = {
        ...domainGraph.value,
        edges: [...domainGraph.value.edges, createDomainEdgeFromForm(form, domainGraph.value.nodes)]
      }

      clearPendingEdgeDraft()
      applyDomainGraph(nextGraph)
      syncSelection(null, null)
      return
    }

    if (!currentCommittedEdge.value) return

    const nextGraph: WorkflowDomainGraphModel = {
      ...domainGraph.value,
      edges: domainGraph.value.edges.map((edge) =>
        edge.id === currentCommittedEdge.value?.id ? applyEdgeFormToDomain(edge, form) : edge
      )
    }

    edgeEditorCellId.value = null
    applyDomainGraph(nextGraph)
    syncSelection(null, null)
  }

  const applyMetaDraft = (nextMeta: WorkflowEditorMetaForm) => {
    const normalizedMeta: WorkflowEditorMetaForm = {
      code: nextMeta.code.trim(),
      displayName: nextMeta.displayName.trim(),
      description: nextMeta.description.trim()
    }

    editorStore.setMetaForm(normalizedMeta)
    metaDraft.value = { ...normalizedMeta }
    refreshClientIssues()
  }

  const openMetaPopover = async () => {
    if (!(await requirePendingEdgeResolved())) return

    const previousSelection = {
      cellId: selection.value.activeCellId,
      cellType: selection.value.activeCellType
    }

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) {
      await focusCanvasCell(previousSelection.cellId, previousSelection.cellType)
      return
    }

    syncSelection(null, null)
    metaDraft.value = { ...metaForm.value }
    metaVisible.value = true
  }

  const handleMetaSubmit = (value: WorkflowEditorMetaForm) => {
    applyMetaDraft(value)
    metaVisible.value = false
  }

  const handleFitView = () => {
    canvasRef.value?.fitView()
  }

  const handleToggleMaterials = async () => {
    materialsVisible.value = !materialsVisible.value
    await nextTick()
    canvasRef.value?.centerContent()
  }

  const handleCenterContent = () => {
    canvasRef.value?.centerContent()
  }

  const handleZoomIn = () => {
    canvasRef.value?.zoomIn()
  }

  const handleZoomOut = () => {
    canvasRef.value?.zoomOut()
  }

  const handleUndo = async () => {
    if (!(await requirePendingEdgeResolved())) return

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) return

    const previous = undoStack.value.pop()
    if (!previous) return

    redoStack.value.push(cloneGraph(domainGraph.value))
    editorStore.setDomainGraph(cloneGraph(previous))
    refreshClientIssues()
    syncSelection(null, null)
  }

  const handleRedo = async () => {
    if (!(await requirePendingEdgeResolved())) return

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) return

    const next = redoStack.value.pop()
    if (!next) return

    undoStack.value.push(cloneGraph(domainGraph.value))
    editorStore.setDomainGraph(cloneGraph(next))
    refreshClientIssues()
    syncSelection(null, null)
  }

  const handleValidate = async () => {
    if (!(await requirePendingEdgeResolved())) return

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) return

    if (metaVisible.value) {
      applyMetaDraft(metaDraft.value)
    }

    const clientIssues = getClientIssues()
    if (clientIssues.some((issue) => issue.level === 'error')) {
      editorStore.setIssues({ client: clientIssues, server: [] })
      ElMessage.error('请先修复本地校验错误。')
      return
    }

    validating.value = true
    try {
      const result = await fetchValidateWorkflowDefinition(buildPayload())
      editorStore.setIssues({
        client: clientIssues,
        server: result.issues.map((issue, index) => ({
          ...issue,
          id: `server-${index}`,
          source: 'server'
        }))
      })
      if (result.valid) {
        ElMessage.success('工作流校验通过。')
      } else {
        ElMessage.error('工作流校验未通过。')
      }
    } finally {
      validating.value = false
    }
  }

  const applySavedDefinition = (definition: WorkflowDefinitionItem) => {
    pendingEdgeDraft.value = null
    edgeEditorCellId.value = null
    editorStore.setDefinitionDetail(definition)
    editorStore.setMetaForm(normalizeDefinitionMeta(definition))
    editorStore.setDomainGraph(
      mapServerGraphToDomain(definition.graph, nodeDefinitions.value, materialItems.value)
    )
    editorStore.setIssues({ client: [], server: [] })
    editorStore.captureBaseline()
    editorStore.enableDirtyTracking()
    metaVisible.value = false
    resetGraphHistory()
    syncSelection(null, null)
  }

  const handleSave = async () => {
    if (!(await requirePendingEdgeResolved())) return

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) return

    if (metaVisible.value) {
      applyMetaDraft(metaDraft.value)
    }

    const clientIssues = getClientIssues()
    if (clientIssues.some((issue) => issue.level === 'error')) {
      editorStore.setIssues({ client: clientIssues, server: [] })
      ElMessage.error('请先修复本地校验错误。')
      return
    }

    validating.value = true
    try {
      const validation = await fetchValidateWorkflowDefinition(buildPayload())
      editorStore.setIssues({
        client: clientIssues,
        server: validation.issues.map((issue, index) => ({
          ...issue,
          id: `server-${index}`,
          source: 'server'
        }))
      })

      if (!validation.valid) {
        ElMessage.error('工作流校验未通过，无法保存。')
        return
      }
    } finally {
      validating.value = false
    }

    saving.value = true
    try {
      const payload = buildPayload()
      let result: WorkflowDefinitionItem

      if (currentMode.value === 'create') {
        result = await fetchCreateWorkflowDefinition(payload)
      } else if (definitionDetail.value?.isBuiltin) {
        result = await fetchCreateWorkflowDefinition(payload)
      } else if (definitionId.value) {
        // 编辑任何版本都不会原地覆盖，而是由后端生成一个新的 definition version。
        result = await fetchUpdateWorkflowDefinition(definitionId.value, payload)
      } else {
        throw new Error('缺少工作流定义 ID。')
      }

      if (currentMode.value === 'create') {
        await router.replace(`/workflows/${result.id}`)
        return
      }

      if (definitionDetail.value?.isBuiltin) {
        ElMessage.success('已从模板创建工作流。')
        await router.replace(`/workflows/${result.id}`)
        return
      }

      if (definitionId.value && result.id !== definitionId.value) {
        const message =
          result.activeDefinitionId && result.activeDefinitionId !== result.id
            ? '已保存为新版本，当前激活版本未切换。'
            : '已保存为新版本。'
        ElMessage.success(message)
        await router.replace(`/workflows/${result.id}`)
        return
      }

      ElMessage.success('工作流定义已更新。')
      applySavedDefinition(result)
    } finally {
      saving.value = false
    }
  }

  const refreshEditorActivity = async () => {
    if (!definitionId.value) {
      definitionExecutions.value = []
      actionItems.value = []
      return
    }
    const [executionResult, actions] = await Promise.all([
      fetchWorkflowDefinitionExecutions(definitionId.value, { limit: 20 }).catch(() => null),
      fetchWorkflowActionList().catch(() => [] as WorkflowActionItem[])
    ])
    definitionExecutions.value = executionResult?.records || []
    actionItems.value = actions
  }

  const activateDefinition = async () => {
    if (!definitionId.value || !definitionDetail.value || definitionDetail.value.isActive) return
    if (dirty.value) {
      ElMessage.warning('请先保存当前修改，再激活该版本。')
      return
    }
    activating.value = true
    try {
      await fetchActivateWorkflowDefinition(definitionId.value)
      definitionDetail.value = await fetchWorkflowDefinitionDetail(definitionId.value)
    } finally {
      activating.value = false
    }
  }

  const cancelLatestExecution = async () => {
    const execution = latestExecution.value
    if (!execution?.canCancel) return
    await ElMessageBox.confirm('取消会在当前节点边界或心跳处生效。', '取消执行', {
      type: 'warning',
      confirmButtonText: '确认取消',
      cancelButtonText: '返回'
    })
    cancelling.value = true
    try {
      await fetchCancelWorkflowExecution(execution.id)
      await refreshEditorActivity()
    } finally {
      cancelling.value = false
    }
  }

  const handleRunStarted = async (result: RunWorkflowDefinitionResponse) => {
    await refreshEditorActivity()
    if (result.executions[0]) await router.push(`/runs/${result.executions[0].id}`)
  }

  const formatExecutionTime = (value: string) =>
    value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '--'

  const handleBack = async () => {
    if (!(await requirePendingEdgeResolved())) return

    const historyBack = window.history.state?.back
    if (typeof historyBack === 'string' && historyBack.startsWith('/')) {
      await router.push(historyBack)
      return
    }

    const activePath =
      typeof route.meta.activePath === 'string' && route.meta.activePath
        ? route.meta.activePath
        : '/workbench'
    const resolvedActiveRoute = router.resolve(activePath)

    if (resolvedActiveRoute.name && resolvedActiveRoute.name !== 'Exception404') {
      await router.push(activePath)
      return
    }

    const homePath = menuStore.getHomePath()
    if (homePath) {
      await router.push(homePath)
      return
    }

    await router.push('/home')
  }

  const handleCanvasRendered = () => {
    if (!awaitingInitialRender.value) return
    awaitingInitialRender.value = false
    canvasRef.value?.fitView()
    editorStore.captureBaseline()
    editorStore.enableDirtyTracking()

    if (currentMode.value === 'create' && !autoOpenedMeta.value) {
      autoOpenedMeta.value = true
      metaDraft.value = { ...metaForm.value }
      metaVisible.value = true
    }
  }

  const loadEditorData = async () => {
    // 创建页和编辑页都走同一套装载流程：先加载节点定义，再决定是读服务端详情还是创建默认图。
    editorStore.resetEditorState()
    editorStore.disableDirtyTracking()
    editorStore.setMode(currentMode.value, currentDefinitionId.value)
    loading.value = true
    metaVisible.value = false
    autoOpenedMeta.value = false
    awaitingInitialRender.value = false
    zoom.value = 1
    pendingEdgeDraft.value = null
    edgeEditorCellId.value = null
    notifyUserOptions.value = []
    notifyRoleOptions.value = []
    notifyOptionsLoading.value = false
    notifyOptionsLoaded.value = false
    nodeTemplates.value = []
    notifyChannels.value = []
    definitionExecutions.value = []
    actionItems.value = []
    historySessionKey.value += 1
    resetGraphHistory()

    try {
      const [
        rawDefinitions,
        agentOptionResult,
        strategyResult,
        strategyDraftResult,
        symbolResult,
        accountResult,
        signalResult,
        aiModelResult,
        assistantResult,
        templateResult,
        channelResult,
        newsResult,
        userResult,
        roleResult,
        workflowResult
      ] = await Promise.all([
        fetchNodeDefinitions(),
        // 智能体列表拿不到不该挡住整个编辑器（没配智能体也能画别的节点）。
        fetchWorkflowAgentOptions().catch(() => [] as WorkflowAgentOption[]),
        fetchPublishedStrategies({ limit: 200 })
          .then((result) => result.records)
          .catch(() => [] as StrategyVersionItem[]),
        fetchStrategyDrafts({ limit: 200 })
          .then((result) => result.records)
          .catch(() => [] as StrategyDraftItem[]),
        fetchMarketSymbols({ limit: 200, quoteAsset: 'USDT', status: 'trading' })
          .then((result) => result.records)
          .catch(() => [] as MarketSymbol[]),
        fetchTradingAccounts().catch(() => [] as TradingAccount[]),
        fetchStrategySignals({ limit: 200 })
          .then((result) => result.records)
          .catch(() => [] as StrategySignalItem[]),
        fetchAiModelList().catch(() => [] as AiModelConfigItem[]),
        fetchAssistantAgentList().catch(() => [] as AssistantAgentItem[]),
        fetchWorkflowNodeTemplates().catch(() => [] as WorkflowNodeTemplateItem[]),
        fetchNotifyChannelList().catch(() => [] as NotifyChannelItem[]),
        fetchNewsList({ limit: 200 })
          .then((result) => result.records)
          .catch(() => [] as Api.Data.NewsListItem[]),
        fetchGetUserList({ limit: 200, isActive: true })
          .then((result) => result.records)
          .catch(() => [] as Api.System.UserListItem[]),
        fetchGetRoleList({ limit: 200, isEnabled: true })
          .then((result) => result.records)
          .catch(() => [] as Api.System.RoleListItem[]),
        fetchWorkflowDefinitionList().catch(() => [] as WorkflowDefinitionItem[])
      ])
      const nodeDefinitionResult = enrichNodeDefinitions(
        rawDefinitions,
        strategyResult,
        strategyDraftResult,
        symbolResult,
        accountResult,
        signalResult,
        aiModelResult,
        assistantResult,
        channelResult,
        newsResult,
        userResult,
        roleResult,
        workflowResult
      )
      agentOptions.value = agentOptionResult
      nodeTemplates.value = templateResult
      notifyChannels.value = channelResult

      // 先把节点定义同步给本地注册表镜像：端口、分支、校验都要按后端声明的图语义来。
      syncNodeDefinitions(nodeDefinitionResult)
      const nextMaterialGroups = buildWorkflowMaterialGroups(nodeDefinitionResult)
      editorStore.setRegistryPayload({
        nodeDefinitions: nodeDefinitionResult,
        materialGroups: nextMaterialGroups
      })

      if (currentMode.value === 'create') {
        const nextMeta = normalizeDefinitionMeta(null)
        editorStore.setMetaForm(nextMeta)
        editorStore.setDomainGraph(createDefaultDomainGraph(nodeDefinitionResult))
      } else if (currentDefinitionId.value) {
        const detail = await fetchWorkflowDefinitionDetail(currentDefinitionId.value)
        editorStore.setDefinitionDetail(detail)
        editorStore.setMetaForm(normalizeDefinitionMeta(detail))
        editorStore.setDomainGraph(
          mapServerGraphToDomain(
            detail.graph,
            nodeDefinitionResult,
            flattenMaterials(nodeDefinitionResult)
          )
        )
        await refreshEditorActivity()
      }

      metaDraft.value = { ...metaForm.value }
      syncSelection(null, null)
      refreshClientIssues()
      awaitingInitialRender.value = true
    } finally {
      loading.value = false
    }
  }

  watch(
    () => route.fullPath,
    () => {
      void loadEditorData()
    },
    { immediate: true }
  )

  let mobileMedia: MediaQueryList | null = null
  let activityTimer: number | null = null
  const syncMobileMode = (matches: boolean) => {
    mobileReadonly.value = matches
    if (matches) materialsVisible.value = false
  }
  const handleMobileChange = (event: MediaQueryListEvent) => syncMobileMode(event.matches)

  onMounted(() => {
    mobileMedia = window.matchMedia('(max-width: 768px)')
    syncMobileMode(mobileMedia.matches)
    mobileMedia.addEventListener('change', handleMobileChange)
    activityTimer = window.setInterval(() => {
      if (!document.hidden && definitionId.value) void refreshEditorActivity()
    }, 5000)
  })

  onBeforeUnmount(() => {
    if (activityTimer) window.clearInterval(activityTimer)
    mobileMedia?.removeEventListener('change', handleMobileChange)
  })

  onBeforeRouteLeave(async () => {
    if (!(await requirePendingEdgeResolved())) return false

    const canLeave = await tryCommitActiveNodeDraft()
    if (!canLeave) return false

    if (dirty.value) {
      try {
        await ElMessageBox.confirm('当前工作流还有未保存的修改，确认离开吗？', '提示', {
          type: 'warning',
          confirmButtonText: '离开',
          cancelButtonText: '继续编辑'
        })
      } catch {
        return false
      }
    }

    return true
  })
</script>

<style scoped lang="scss">
  .workflow-editor-page {
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
    --workflow-overlay-hover: var(--art-hover-color);

    position: relative;
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    min-height: 0;
    overflow: hidden;
    background: var(--workflow-page-bg);
  }

  .workflow-editor-page__canvas {
    flex: 1 1 auto;
    min-height: 0;
  }

  .workflow-editor-page__timeline {
    position: absolute;
    right: 16px;
    bottom: 14px;
    left: 280px;
    z-index: 16;
    display: flex;
    gap: 4px;
    align-items: center;
    min-height: 48px;
    padding: 6px 8px;
    overflow-x: auto;
    background: var(--workflow-overlay-bg);
    border: 1px solid var(--workflow-overlay-border-soft);
    border-radius: 8px;
    box-shadow: 0 8px 20px rgb(31 35 48 / 0.1);

    header {
      display: flex;
      flex-direction: column;
      gap: 2px;
      min-width: 80px;
      padding: 0 8px;

      span {
        font-size: 12px;
        font-weight: 700;
        color: var(--workflow-overlay-text);
      }

      small {
        color: var(--workflow-overlay-muted);
      }
    }

    button {
      display: grid;
      grid-template-columns: 5px minmax(60px, auto);
      gap: 2px 8px;
      align-items: center;
      min-width: 150px;
      padding: 5px 8px;
      color: var(--workflow-overlay-text);
      text-align: left;
      cursor: pointer;
      background: var(--workflow-overlay-soft);
      border: 0;
      border-radius: 5px;

      i {
        grid-row: 1 / 3;
        width: 5px;
        height: 24px;
        background: #10a6a6;
        border-radius: 2px;
      }

      i.is-waiting_action,
      i.is-retry_waiting,
      i.is-waiting_job {
        background: #d69a2d;
      }

      i.is-failed,
      i.is-canceled,
      i.is-cancel_requested {
        background: #d94b55;
      }

      span {
        font-size: 12px;
        font-weight: 600;
      }

      small {
        color: var(--workflow-overlay-muted);
        white-space: nowrap;
      }
    }
  }

  .workflow-editor-page__mobile-note {
    position: absolute;
    top: 66px;
    right: 8px;
    left: 8px;
    z-index: 20;
    padding: 8px 10px;
    font-size: 12px;
    color: var(--el-color-warning-dark-2);
    text-align: center;
    background: var(--el-color-warning-light-9);
    border: 1px solid var(--el-color-warning-light-7);
    border-radius: 6px;
  }

  .workflow-editor-page__meta {
    position: absolute;
    top: 88px;
    left: 50%;
    z-index: 30;
    transform: translateX(-50%);
  }

  @media (max-width: 768px) {
    .workflow-editor-page__timeline {
      right: 8px;
      bottom: 8px;
      left: 8px;
    }

    .workflow-editor-page__meta {
      top: 68px;
      right: 8px;
      left: 8px;
      transform: none;
    }
  }
</style>
