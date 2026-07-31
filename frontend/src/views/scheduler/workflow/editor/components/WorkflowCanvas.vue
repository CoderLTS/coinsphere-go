<!-- 工作流编辑器页面或组件：WorkflowCanvas。 -->
<template>
  <div
    ref="shellRef"
    :class="['workflow-canvas', { 'workflow-canvas--materials-hidden': !props.materialsVisible }]"
  >
    <div
      ref="graphRef"
      class="workflow-canvas__graph"
      @pointerdown.capture="handleGraphPointerDown"
    ></div>

    <div class="workflow-canvas__toolbar-slot">
      <slot name="toolbar" />
    </div>

    <div v-show="props.materialsVisible" class="workflow-canvas__stencil">
      <div v-if="!materialCount" class="workflow-canvas__stencil-empty">
        <ElEmpty description="暂无可用物料" :image-size="40" />
      </div>

      <div ref="stencilRef" class="workflow-canvas__stencil-body"></div>
      <div v-if="stencilScrollbar.visible" class="workflow-canvas__stencil-scrollbar">
        <div
          class="workflow-canvas__stencil-scrollbar-thumb"
          :style="stencilScrollbarThumbStyle"
        ></div>
      </div>
    </div>

    <div
      v-if="showNodeEditor && selectedNode && nodeEditorStyle"
      class="workflow-canvas__overlay"
      :style="nodeEditorStyle"
    >
      <WorkflowNodeEditorCard
        :node="selectedNode"
        :model="nodeDraftModel"
        :task-definitions="taskDefinitions"
        :agent-options="agentOptions"
        :notify-user-options="notifyUserOptions"
        :notify-role-options="notifyRoleOptions"
        :notify-options-loading="notifyOptionsLoading"
        :issues="selectedNodeIssues"
        :errors="draftState.errors"
        @update:model="$emit('update-node-draft', $event)"
        @request-commit="$emit('request-commit-node-draft')"
        @request-discard="$emit('request-discard-node-draft')"
        @request-close="$emit('request-close-node-editor')"
        @request-remove="$emit('request-remove-selection')"
      />
    </div>

    <div
      v-if="showEdgeBubble && selectedEdge && edgeBubbleStyle"
      class="workflow-canvas__overlay"
      :style="edgeBubbleStyle"
    >
      <WorkflowEdgeBubble
        :edge="selectedEdge"
        @confirm="$emit('commit-edge-draft', $event)"
        @cancel="$emit('request-close-edge-editor')"
      />
    </div>

    <div
      v-if="nodeContextMenu.visible && nodeContextMenuStyle"
      class="workflow-canvas__overlay"
      :style="nodeContextMenuStyle"
    >
      <div class="workflow-canvas__context-menu" @pointerdown.stop @contextmenu.prevent>
        <div class="workflow-canvas__context-menu-title">
          {{ nodeContextMenuNode?.data.title || '节点菜单' }}
        </div>
        <button
          type="button"
          class="workflow-canvas__context-menu-item"
          @click="emitNodeContextAction('edit')"
        >
          编辑属性
        </button>
        <button
          type="button"
          class="workflow-canvas__context-menu-item workflow-canvas__context-menu-item--danger"
          @click="emitNodeContextAction('delete')"
        >
          删除节点
        </button>
      </div>
    </div>

    <div v-if="!ready" class="workflow-canvas__loading">
      <ElSkeleton animated :rows="4" />
    </div>

    <ArtMenuRight
      ref="nodeContextMenuRef"
      :menu-items="nodeContextMenuItems"
      :menu-width="144"
      :border-radius="10"
      @select="handleNodeContextMenuSelect"
    />

    <ArtMenuRight
      ref="edgeContextMenuRef"
      :menu-items="edgeContextMenuItems"
      :menu-width="144"
      :border-radius="10"
      @select="handleEdgeContextMenuSelect"
    />
  </div>
</template>

<script setup lang="ts">
  import { ElEmpty, ElSkeleton } from 'element-plus'
  import { Edge, Graph, History, Keyboard, Selection, Snapline } from '@antv/x6'
  import type { CSSProperties } from 'vue'
  import type { TaskDefinitionItem, WorkflowAgentOption } from '@/api/scheduler'
  import ArtMenuRight from '@/components/core/others/art-menu-right/index.vue'
  import {
    ensureWorkflowGraphEdgeRegistered,
    ensureWorkflowNodeShapeRegistered
  } from './workflow-graph-shapes'
  import {
    createConnectionValidator,
    getPortDisplayColor,
    graphKindOfCell,
    validateMagnet
  } from '../canvas-connection-rules'
  import { LOOP_NEXT_BRANCH } from '../node-registry'
  import {
    createDomainEdgeFromForm,
    mapDomainGraphToX6,
    mapX6GraphToDomain
  } from '../workflow-editor.mapper'
  import {
    STENCIL_PANEL_WIDTH,
    ensureStencilShapeRegistered,
    useWorkflowStencil
  } from './useWorkflowStencil'
  import { useCanvasContextMenu } from './useCanvasContextMenu'
  import type {
    WorkflowActiveCellType,
    WorkflowDomainEdge,
    WorkflowDomainGraphModel,
    WorkflowDomainNode,
    WorkflowEdgeFormModel,
    WorkflowEditorDraftState,
    WorkflowEditorIssue,
    WorkflowGraphCommitPayload,
    WorkflowMaterialDropPayload,
    WorkflowMaterialGroup,
    WorkflowMaterialItem,
    WorkflowNodeContextActionPayload,
    WorkflowNodeFormModel,
    WorkflowNotifyTargetOption
  } from '../types'
  import WorkflowEdgeBubble from './WorkflowEdgeBubble.vue'
  import WorkflowNodeEditorCard from './WorkflowNodeEditorCard.vue'

  interface Props {
    graph: WorkflowDomainGraphModel
    materials: WorkflowMaterialItem[]
    materialGroups: WorkflowMaterialGroup[]
    issues: WorkflowEditorIssue[]
    taskDefinitions: TaskDefinitionItem[]
    agentOptions: WorkflowAgentOption[]
    notifyUserOptions: WorkflowNotifyTargetOption[]
    notifyRoleOptions: WorkflowNotifyTargetOption[]
    notifyOptionsLoading: boolean
    jsonDefinitionVisible: boolean
    dirtyNodeIds: string[]
    draftState: WorkflowEditorDraftState
    materialsVisible: boolean
    activeCellId: string | null
    activeCellType: WorkflowActiveCellType
    edgeEditorCellId: string | null
    pendingEdgeDraft: WorkflowEdgeFormModel | null
    historySessionKey: number
  }

  interface Emits {
    (
      e: 'request-activate-cell',
      payload: { cellId: string | null; cellType: WorkflowActiveCellType }
    ): void
    (e: 'graph-commit', payload: WorkflowGraphCommitPayload): void
    (e: 'zoom-change', value: number): void
    (e: 'material-drop', payload: WorkflowMaterialDropPayload): void
    (e: 'rendered'): void
    (e: 'update-node-draft', value: WorkflowNodeFormModel): void
    (e: 'request-commit-node-draft'): void
    (e: 'request-discard-node-draft'): void
    (e: 'request-close-node-editor'): void
    (e: 'request-close-edge-editor'): void
    (e: 'request-remove-selection'): void
    (e: 'request-node-context-action', payload: WorkflowNodeContextActionPayload): void
    (e: 'request-open-edge-editor', cellId: string): void
    (e: 'commit-edge-draft', value: WorkflowEdgeFormModel): void
    (e: 'create-pending-edge-draft', value: WorkflowEdgeFormModel): void
  }

  const props = defineProps<Props>()
  const emit = defineEmits<Emits>()

  let shapeRegistered = false
  // JSON 定义面板的宽度，算画布可视区内边距时要减掉它。
  const JSON_PANEL_WIDTH = 380

  const ensureShapesRegistered = () => {
    if (shapeRegistered) return

    ensureWorkflowNodeShapeRegistered()
    ensureStencilShapeRegistered()
    ensureWorkflowGraphEdgeRegistered()

    shapeRegistered = true
  }

  const shellRef = ref<HTMLDivElement | null>(null)
  const graphRef = ref<HTMLDivElement | null>(null)
  const stencilRef = ref<HTMLDivElement | null>(null)
  const ready = ref(false)

  const graphInstance = shallowRef<Graph | null>(null)
  const selectionPlugin = shallowRef<Selection | null>(null)
  const nodeContextMenuRef = ref<InstanceType<typeof ArtMenuRight> | null>(null)
  const edgeContextMenuRef = ref<InstanceType<typeof ArtMenuRight> | null>(null)
  const applyingExternalGraph = ref(false)
  const localRenderSnapshot = ref('')
  const suppressRemovedCellId = ref<string | null>(null)
  const nodeEditorStyle = ref<CSSProperties | null>(null)
  const edgeBubbleStyle = ref<CSSProperties | null>(null)
  const positionDirty = ref(false)
  const suppressBlankClick = ref(false)
  const hoveredNodeId = ref<string | null>(null)
  const blankPanState = reactive({
    pressed: false,
    armed: false,
    active: false,
    moved: false,
    pointerId: -1,
    startClientX: 0,
    startClientY: 0,
    startTx: 0,
    startTy: 0
  })
  let blankPanHoldTimer: number | null = null
  let detachBlankPanListeners: (() => void) | null = null

  // 右键菜单与物料面板各自成体系，逻辑放在同目录的两个 composable 里，
  // 本组件只负责把画布相关的引用接过去。
  const contextMenu = useCanvasContextMenu({
    nodeMenuRef: nodeContextMenuRef,
    edgeMenuRef: edgeContextMenuRef,
    shellRef,
    nodes: () => props.graph.nodes,
    onNodeAction: (payload) => emit('request-node-context-action', payload),
    onEdgeEdit: (cellId) => emit('request-open-edge-editor', cellId),
    onActivateCell: (cellId, cellType) => emit('request-activate-cell', { cellId, cellType })
  })
  const {
    nodeMenu: nodeContextMenu,
    nodeMenuItems: nodeContextMenuItems,
    edgeMenuItems: edgeContextMenuItems,
    nodeMenuNode: nodeContextMenuNode,
    nodeMenuStyle: nodeContextMenuStyle,
    openNodeMenu: openNodeContextMenu,
    openEdgeMenu: openEdgeContextMenu,
    hideNodeMenu: hideNodeContextMenu,
    hideEdgeMenu: hideEdgeContextMenu,
    emitNodeAction: emitNodeContextAction,
    handleWindowPointerDown,
    handleNodeMenuSelect: handleNodeContextMenuSelect,
    handleEdgeMenuSelect: handleEdgeContextMenuSelect
  } = contextMenu

  const stencil = useWorkflowStencil({
    stencilRef,
    graphInstance,
    materialGroups: () => props.materialGroups,
    materialsVisible: () => props.materialsVisible,
    viewportCenter: () => getViewportCenterClientPoint(),
    onInsert: (payload) => emit('material-drop', payload)
  })
  const {
    scrollbar: stencilScrollbar,
    thumbStyle: stencilScrollbarThumbStyle,
    materialCount,
    createStencil,
    updateScrollbar: updateStencilScrollbar,
    destroyStencil
  } = stencil

  const getTranslateState = () => {
    const current = graphInstance.value?.translate()
    return {
      tx: Number(
        (current as { tx?: number; x?: number } | undefined)?.tx ??
          (current as { x?: number } | undefined)?.x ??
          0
      ),
      ty: Number(
        (current as { ty?: number; y?: number } | undefined)?.ty ??
          (current as { y?: number } | undefined)?.y ??
          0
      )
    }
  }

  const getViewportCenterClientPoint = () => {
    const shell = shellRef.value
    if (!shell) {
      return { x: 0, y: 0 }
    }

    const rect = shell.getBoundingClientRect()
    const padding = getViewportPadding()
    const viewportWidth = Math.max(120, shell.clientWidth - padding.left - padding.right)
    const viewportHeight = Math.max(120, shell.clientHeight - padding.top - padding.bottom)

    return {
      x: rect.left + padding.left + viewportWidth / 2,
      y: rect.top + padding.top + viewportHeight / 2
    }
  }

  const getViewportPadding = () => ({
    top: 132,
    right: props.jsonDefinitionVisible ? 20 + JSON_PANEL_WIDTH + 36 : 56,
    bottom: 80,
    left: props.materialsVisible ? 20 + STENCIL_PANEL_WIDTH + 36 : 20
  })

  const getContentBounds = (graphModel: WorkflowDomainGraphModel) => {
    if (!graphModel.nodes.length) return null

    const minX = Math.min(...graphModel.nodes.map((node) => node.position.x))
    const minY = Math.min(...graphModel.nodes.map((node) => node.position.y))
    const maxX = Math.max(...graphModel.nodes.map((node) => node.position.x + node.size.width))
    const maxY = Math.max(...graphModel.nodes.map((node) => node.position.y + node.size.height))

    return {
      x: minX,
      y: minY,
      width: Math.max(1, maxX - minX),
      height: Math.max(1, maxY - minY)
    }
  }

  const issueSignature = computed(() =>
    JSON.stringify(
      props.issues.map((item) => ({
        id: item.id,
        nodeId: item.nodeId || '',
        edgeId: item.edgeId || '',
        level: item.level,
        message: item.message
      }))
    )
  )

  const graphSignature = computed(() =>
    JSON.stringify({
      graph: props.graph,
      pendingEdgeDraft: props.pendingEdgeDraft
    })
  )

  const selectedNode = computed<WorkflowDomainNode | null>(() => {
    if (props.activeCellType !== 'node' || !props.activeCellId) return null
    return props.graph.nodes.find((node) => node.id === props.activeCellId) || null
  })

  const selectedEdge = computed<WorkflowDomainEdge | null>(() => {
    if (props.activeCellType !== 'edge' || !props.activeCellId) return null
    const committedEdge = props.graph.edges.find((edge) => edge.id === props.activeCellId) || null
    if (committedEdge) return committedEdge
    if (props.pendingEdgeDraft?.id === props.activeCellId) {
      return createDomainEdgeFromForm(props.pendingEdgeDraft, props.graph.nodes)
    }
    return null
  })

  const showNodeEditor = computed(
    () => !!selectedNode.value && props.draftState.cellType === 'node' && !!props.draftState.model
  )
  const showEdgeBubble = computed(
    () => !!selectedEdge.value && props.edgeEditorCellId === selectedEdge.value.id
  )
  const nodeDraftModel = computed(
    () => (props.draftState.model as WorkflowNodeFormModel | null) || null
  )

  const selectedNodeIssues = computed(() => {
    if (!selectedNode.value) return []
    return props.issues.filter((issue) => issue.nodeId === selectedNode.value?.id)
  })

  const buildIssueSets = () => {
    const nodeIds = new Set<string>()
    const edgeIds = new Set<string>()
    const firstMessages = new Map<string, string>()

    props.issues.forEach((issue) => {
      if (issue.nodeId) {
        nodeIds.add(String(issue.nodeId))
        if (!firstMessages.has(String(issue.nodeId))) {
          firstMessages.set(String(issue.nodeId), issue.message)
        }
      }

      if (issue.edgeId) {
        edgeIds.add(String(issue.edgeId))
        if (!firstMessages.has(String(issue.edgeId))) {
          firstMessages.set(String(issue.edgeId), issue.message)
        }
      }
    })

    return { nodeIds, edgeIds, firstMessages }
  }

  const buildRenderSnapshot = (graphModel: WorkflowDomainGraphModel) => {
    const issueSets = buildIssueSets()
    return JSON.stringify(
      mapDomainGraphToX6(graphModel, issueSets, {
        dirtyNodeIds: new Set(props.dirtyNodeIds),
        pendingEdgeDraft: props.pendingEdgeDraft
      })
    )
  }

  const buildEdgeId = () => `edge_${Math.random().toString(36).slice(2, 10)}`

  const waitForPaint = () =>
    new Promise<void>((resolve) => {
      requestAnimationFrame(() => {
        requestAnimationFrame(() => resolve())
      })
    })

  const runWithHistoryMuted = (callback: () => void) => {
    const graph = graphInstance.value as any
    if (!graph) return
    graph.disableHistory?.()
    try {
      callback()
    } finally {
      graph.enableHistory?.()
    }
  }

  const resetHistoryStack = () => {
    ;(graphInstance.value as any)?.cleanHistory?.()
  }

  const isPortConnected = (nodeId: string, portId: string) => {
    const graph = graphInstance.value
    const node = graph?.getCellById(nodeId)
    if (!graph || !node?.isNode()) return false
    return graph
      .getConnectedEdges(node)
      .some(
        (edge) =>
          (edge.getSourceCellId() === nodeId && edge.getSourcePortId() === portId) ||
          (edge.getTargetCellId() === nodeId && edge.getTargetPortId() === portId)
      )
  }

  const setPortVisible = (nodeId: string, portId: string, visible: boolean) => {
    const graph = graphInstance.value
    const node = graph?.getCellById(nodeId)
    if (!node?.isNode()) return
    node.setPortProp(portId, 'attrs/portBody/style/visibility', visible ? 'visible' : 'hidden')
    node.setPortProp(portId, 'attrs/portLabel/style/visibility', visible ? 'visible' : 'hidden')
  }

  const setPortColor = (
    nodeId: string,
    portId: string,
    stroke: string,
    fill = '#ffffff',
    labelColor?: string
  ) => {
    const graph = graphInstance.value
    const node = graph?.getCellById(nodeId)
    if (!node?.isNode()) return
    node.setPortProp(portId, 'attrs/portBody/stroke', stroke)
    node.setPortProp(portId, 'attrs/portBody/fill', fill)
    node.setPortProp(portId, 'attrs/portLabel/fill', labelColor || stroke)
  }

  const syncPortDot = (nodeId: string, portId: string, visible: boolean, connected?: boolean) => {
    const stateConnected = connected ?? isPortConnected(nodeId, portId)
    const color = getPortDisplayColor(portId, stateConnected)
    setPortVisible(nodeId, portId, visible)
    setPortColor(nodeId, portId, color.stroke, color.fill, color.label)
  }

  const showNodePorts = (nodeId: string, show: boolean) => {
    const graph = graphInstance.value
    const node = graph?.getCellById(nodeId)
    if (!node?.isNode()) return

    node.getPorts().forEach((port) => {
      const portId = String(port.id || '')
      if (!portId) return
      if (show) {
        syncPortDot(nodeId, portId, true)
        return
      }
      const connected = isPortConnected(nodeId, portId)
      syncPortDot(nodeId, portId, connected, connected)
    })
  }

  const syncPortVisibility = () => {
    props.graph.nodes.forEach((node) => {
      const show =
        hoveredNodeId.value === node.id ||
        (props.activeCellType === 'node' && props.activeCellId === node.id)
      showNodePorts(node.id, show)
    })
  }

  const buildEdgeFormFromCell = (edge: Edge): WorkflowEdgeFormModel => {
    const sourcePort = String(edge.getSourcePortId() || '')
    const data = edge.getData() || {}
    const sourceCell = graphInstance.value?.getCellById(String(edge.getSourceCellId() || ''))
    // 分支节点的出口名就是 branch；循环节点只有 NEXT 那条算 branch，BODY 不带（与后端一致）。
    const kind = sourceCell ? graphKindOfCell(sourceCell) : 'plain'
    const branchFromPort =
      kind === 'branch'
        ? sourcePort
        : kind === 'loop' && sourcePort === LOOP_NEXT_BRANCH
          ? LOOP_NEXT_BRANCH
          : ''
    return {
      id: edge.id,
      source: String(edge.getSourceCellId() || ''),
      target: String(edge.getTargetCellId() || ''),
      sourcePort,
      targetPort: String(edge.getTargetPortId() || 'in'),
      branch: branchFromPort || String(data.branch || ''),
      label: String(data.label || '')
    }
  }

  // 连线规则集中在 canvas-connection-rules.ts：那里按后端下发的图语义判断，
  // 不再在本文件里写死 condition.branch / foreach 这些具体类型编码。
  const validateConnection = createConnectionValidator(
    () => graphInstance.value,
    () => props.pendingEdgeDraft?.id
  )

  const createEdge = () => {
    const graph = graphInstance.value
    if (graph) {
      return graph.createEdge({
        id: buildEdgeId(),
        shape: 'workflow-editor-edge',
        zIndex: 3,
        data: {
          branch: '',
          label: ''
        }
      }) as Edge
    }

    return new Edge({
      id: buildEdgeId(),
      shape: 'workflow-editor-edge',
      zIndex: 3,
      data: {
        branch: '',
        label: ''
      }
    })
  }

  const updateZoomText = () => {
    const scale = graphInstance.value?.zoom() || 1
    emit('zoom-change', scale)
  }

  const updateOverlayPositions = () => {
    const graph = graphInstance.value
    const shell = shellRef.value
    if (!graph || !shell) return

    if (selectedNode.value) {
      const cell = graph.getCellById(selectedNode.value.id)
      if (cell?.isNode()) {
        const bbox = cell.getBBox()
        const topLeft = graph.localToClient({ x: bbox.x, y: bbox.y }) as { x: number; y: number }
        const shellRect = shell.getBoundingClientRect()
        const panelWidth = Math.min(352, Math.max(300, shellRect.width - 24))
        const panelHeight = Math.min(520, Math.max(320, shellRect.height - 96))
        const anchorLeft = topLeft.x - shellRect.left
        const anchorTop = topLeft.y - shellRect.top

        nodeEditorStyle.value = {
          left: `${Math.max(12, Math.min(anchorLeft, shellRect.width - panelWidth - 12))}px`,
          top: `${Math.max(76, Math.min(anchorTop, shellRect.height - panelHeight - 12))}px`,
          width: `${panelWidth}px`,
          height: `${panelHeight}px`
        }
      }
    } else {
      nodeEditorStyle.value = null
    }

    if (selectedEdge.value) {
      const edge = graph.getCellById(selectedEdge.value.id)
      if (edge?.isEdge()) {
        const bbox = edge.getBBox()
        const center = graph.localToClient({
          x: bbox.x + bbox.width / 2,
          y: bbox.y + bbox.height / 2
        }) as {
          x: number
          y: number
        }
        const shellRect = shell.getBoundingClientRect()
        const width = 240
        const height = 180
        edgeBubbleStyle.value = {
          left: `${Math.max(12, Math.min(center.x - shellRect.left - width / 2, shellRect.width - width - 12))}px`,
          top: `${Math.max(76, Math.min(center.y - shellRect.top - height / 2, shellRect.height - height - 12))}px`
        }
      }
    } else {
      edgeBubbleStyle.value = null
    }
  }

  const stopBlankPanning = () => {
    if (blankPanHoldTimer !== null) {
      window.clearTimeout(blankPanHoldTimer)
      blankPanHoldTimer = null
    }
    blankPanState.pressed = false
    blankPanState.armed = false
    if (blankPanState.pointerId >= 0) {
      try {
        graphRef.value?.releasePointerCapture(blankPanState.pointerId)
      } catch {
        // 指针已经被释放过，忽略即可
      }
    }
    blankPanState.pointerId = -1
    detachBlankPanListeners?.()
    detachBlankPanListeners = null
    blankPanState.active = false
    blankPanState.moved = false
    shellRef.value?.classList.remove('workflow-canvas--panning')
  }

  const startBlankPanning = (rawEvent: PointerEvent) => {
    if (
      rawEvent.button !== 0 ||
      blankPanState.active ||
      blankPanState.armed ||
      !graphInstance.value
    )
      return

    blankPanState.pressed = true
    blankPanState.pointerId = rawEvent.pointerId
    const { tx, ty } = getTranslateState()
    blankPanState.armed = true
    blankPanState.active = false
    blankPanState.moved = false
    blankPanState.startClientX = rawEvent.clientX
    blankPanState.startClientY = rawEvent.clientY
    blankPanState.startTx = tx
    blankPanState.startTy = ty
    try {
      graphRef.value?.setPointerCapture(rawEvent.pointerId)
    } catch {
      // 浏览器拒绝捕获指针时退化成普通拖拽，不影响功能
    }

    const handlePointerMove = (event: PointerEvent) => {
      if (event.pointerId !== blankPanState.pointerId) return
      if ((event.buttons & 1) !== 1) {
        stopBlankPanning()
        suppressBlankClick.value = false
        return
      }

      if (!blankPanState.pressed || !blankPanState.active || !graphInstance.value) return

      const dx = event.clientX - blankPanState.startClientX
      const dy = event.clientY - blankPanState.startClientY

      if (!blankPanState.moved && Math.abs(dx) + Math.abs(dy) >= 3) {
        blankPanState.moved = true
        suppressBlankClick.value = true
      }

      graphInstance.value.translate(blankPanState.startTx + dx, blankPanState.startTy + dy)
      updateOverlayPositions()
    }

    const handlePointerUp = (event: PointerEvent) => {
      if (event.pointerId !== blankPanState.pointerId) return
      stopBlankPanning()
      if (suppressBlankClick.value) {
        window.setTimeout(() => {
          suppressBlankClick.value = false
        }, 0)
      }
    }

    const handleWindowBlur = () => {
      stopBlankPanning()
      suppressBlankClick.value = false
    }

    window.addEventListener('pointermove', handlePointerMove)
    window.addEventListener('pointerup', handlePointerUp)
    window.addEventListener('pointercancel', handlePointerUp)
    window.addEventListener('blur', handleWindowBlur)
    detachBlankPanListeners = () => {
      window.removeEventListener('pointermove', handlePointerMove)
      window.removeEventListener('pointerup', handlePointerUp)
      window.removeEventListener('pointercancel', handlePointerUp)
      window.removeEventListener('blur', handleWindowBlur)
    }

    blankPanHoldTimer = window.setTimeout(() => {
      blankPanHoldTimer = null
      if (!blankPanState.armed || !blankPanState.pressed) return
      blankPanState.armed = false
      blankPanState.active = true
      shellRef.value?.classList.add('workflow-canvas--panning')
    }, 140)
  }

  const handleGraphPointerDown = (event: PointerEvent) => {
    const target = event.target as HTMLElement | null
    if (!target) return

    if (
      target.closest('.x6-node') ||
      target.closest('.x6-edge') ||
      target.closest('.x6-port') ||
      target.closest('.workflow-canvas__overlay') ||
      target.closest('.workflow-canvas__toolbar-slot') ||
      target.closest('.workflow-canvas__stencil')
    ) {
      return
    }

    startBlankPanning(event)
  }

  const clearSelectedNodeHighlight = () => {
    const graph = graphInstance.value
    if (!graph) return

    graph.getNodes().forEach((node) => {
      const data = node.getData() || {}
      if (!data.isSelected) return
      node.setData({ ...data, isSelected: false })
    })
  }

  const applySelectedNodeHighlight = (cellId: string | null) => {
    const graph = graphInstance.value
    if (!graph) return

    clearSelectedNodeHighlight()
    if (!cellId) return

    const cell = graph.getCellById(cellId)
    if (!cell?.isNode()) return

    const data = cell.getData() || {}
    cell.setData({ ...data, isSelected: true })
  }

  const syncVisualSelection = () => {
    const graph = graphInstance.value
    const selection = selectionPlugin.value
    if (!graph || !selection) return

    if (!props.activeCellId || !props.activeCellType) {
      selection.clean()
      clearSelectedNodeHighlight()
      syncPortVisibility()
      return
    }

    const targetCell = graph.getCellById(props.activeCellId)
    if (!targetCell) {
      selection.clean()
      clearSelectedNodeHighlight()
      syncPortVisibility()
      return
    }

    selection.reset([targetCell] as any)
    if (targetCell.isNode()) {
      targetCell.toFront()
      applySelectedNodeHighlight(targetCell.id)
    } else {
      clearSelectedNodeHighlight()
    }
    syncPortVisibility()
    updateOverlayPositions()
  }

  const syncDomainGraphFromCanvas = (reason: WorkflowGraphCommitPayload['reason']) => {
    const graph = graphInstance.value
    if (!graph || applyingExternalGraph.value) return
    const graphJson = graph.toJSON() as { cells?: Array<Record<string, any>> }
    const filteredGraphJson = props.pendingEdgeDraft?.id
      ? {
          ...graphJson,
          cells: (graphJson.cells || []).filter((cell) => cell.id !== props.pendingEdgeDraft?.id)
        }
      : graphJson
    const nextGraph = mapX6GraphToDomain(filteredGraphJson as any, [], props.materials)
    localRenderSnapshot.value = buildRenderSnapshot(nextGraph)
    if (props.activeCellId && props.activeCellType) {
      const activeCellExists =
        props.activeCellType === 'node'
          ? nextGraph.nodes.some((node) => node.id === props.activeCellId)
          : nextGraph.edges.some((edge) => edge.id === props.activeCellId)
      if (!activeCellExists) {
        emit('request-activate-cell', { cellId: null, cellType: null })
      }
    }
    emit('graph-commit', { graph: nextGraph, reason })
  }

  const updateEdgePresentation = (edge: Edge) => {
    const data = edge.getData() || {}
    const text = String(data.label || '')
    edge.setConnector({
      name: 'smooth'
    })
    edge.setAttrs({
      line: {
        stroke: '#94a3b8',
        strokeWidth: 1.6,
        targetMarker: {
          name: 'block',
          width: 12,
          height: 8
        }
      }
    })
    edge.setLabels(
      text
        ? [
            {
              position: 0.5,
              attrs: {
                body: {
                  fill: '#ffffff',
                  stroke: '#cbd5e1',
                  strokeWidth: 1,
                  rx: 10,
                  ry: 10
                },
                label: {
                  text,
                  fill: '#475569',
                  fontSize: 11,
                  fontWeight: 600
                }
              }
            }
          ]
        : []
    )
  }

  const fitView = () => {
    const graph = graphInstance.value
    const shell = shellRef.value
    if (!graph || !shell) return
    const bounds = getContentBounds(props.graph)
    if (!bounds) return

    const padding = getViewportPadding()
    const viewportWidth = Math.max(120, shell.clientWidth - padding.left - padding.right)
    const viewportHeight = Math.max(120, shell.clientHeight - padding.top - padding.bottom)
    const scale = Math.max(
      0.42,
      Math.min(viewportWidth / bounds.width, viewportHeight / bounds.height, 1)
    )

    graph.zoom(scale, {
      absolute: true,
      center: { x: 0, y: 0 }
    })

    graph.translate(
      Math.round(padding.left + (viewportWidth - bounds.width * scale) / 2 - bounds.x * scale),
      Math.round(padding.top + (viewportHeight - bounds.height * scale) / 2 - bounds.y * scale)
    )

    updateZoomText()
    updateOverlayPositions()
  }

  const centerContent = () => {
    const graph = graphInstance.value
    const shell = shellRef.value
    const bounds = getContentBounds(props.graph)
    if (!graph || !shell || !bounds) return

    const padding = getViewportPadding()
    const viewportWidth = Math.max(120, shell.clientWidth - padding.left - padding.right)
    const viewportHeight = Math.max(120, shell.clientHeight - padding.top - padding.bottom)
    const scale = graph.zoom() || 1

    graph.translate(
      Math.round(padding.left + (viewportWidth - bounds.width * scale) / 2 - bounds.x * scale),
      Math.round(padding.top + (viewportHeight - bounds.height * scale) / 2 - bounds.y * scale)
    )
    updateOverlayPositions()
  }

  const zoomIn = () => {
    graphInstance.value?.zoom(0.1, {
      maxScale: 2
    })
    updateZoomText()
    updateOverlayPositions()
  }

  const zoomOut = () => {
    graphInstance.value?.zoom(-0.1, {
      minScale: 0.15
    })
    updateZoomText()
    updateOverlayPositions()
  }

  const undo = () => {
    const graph = graphInstance.value as any
    if (!graph) return
    if (typeof graph.canUndo === 'function' && !graph.canUndo()) return
    hideNodeContextMenu()
    graph.undo?.()
    syncDomainGraphFromCanvas('undo')
    updateOverlayPositions()
  }

  const redo = () => {
    const graph = graphInstance.value as any
    if (!graph) return
    if (typeof graph.canRedo === 'function' && !graph.canRedo()) return
    hideNodeContextMenu()
    graph.redo?.()
    syncDomainGraphFromCanvas('redo')
    updateOverlayPositions()
  }

  const focusCell = (cellId: string | null, cellType: WorkflowActiveCellType) => {
    if (!cellId || !cellType) {
      selectionPlugin.value?.clean()
      clearSelectedNodeHighlight()
      updateOverlayPositions()
      return
    }

    const cell = graphInstance.value?.getCellById(cellId)
    if (!cell) return
    selectionPlugin.value?.reset([cell] as any)
    if (cell.isNode()) {
      cell.toFront()
      applySelectedNodeHighlight(cell.id)
    } else {
      clearSelectedNodeHighlight()
    }
    updateOverlayPositions()
  }

  const removeSelection = () => {
    const graph = graphInstance.value
    if (!graph || !props.activeCellId) return
    const cell = graph.getCellById(props.activeCellId)
    if (!cell) return
    cell.remove()
    emit('request-activate-cell', { cellId: null, cellType: null })
  }

  defineExpose({
    fitView,
    centerContent,
    zoomIn,
    zoomOut,
    undo,
    redo,
    focusCell,
    removeSelection
  })

  const initializeGraph = () => {
    if (!graphRef.value) return
    ensureShapesRegistered()

    const graph = new Graph({
      container: graphRef.value,
      interacting: {
        nodeMovable: true,
        edgeMovable: false,
        arrowheadMovable: false,
        vertexMovable: false
      },
      grid: {
        visible: false,
        size: 20
      },
      background: {
        color: 'transparent'
      },
      preventDefaultBlankAction: false,
      async: false,
      highlighting: {
        magnetAvailable: {
          name: 'stroke',
          args: {
            padding: 4,
            attrs: {
              stroke: '#60a5fa',
              strokeWidth: 3
            }
          }
        },
        magnetAdsorbed: {
          name: 'stroke',
          args: {
            padding: 5,
            attrs: {
              stroke: '#10b981',
              strokeWidth: 3
            }
          }
        }
      },
      mousewheel: {
        enabled: true,
        minScale: 0.4,
        maxScale: 2
      },
      connecting: {
        allowBlank: false,
        allowLoop: false,
        allowEdge: false,
        snap: { radius: 24 },
        highlight: true,
        connectionPoint: 'anchor',
        createEdge,
        validateConnection,
        validateMagnet
      }
    })

    const selection = new Selection({
      enabled: true,
      multiple: true,
      rubberband: false,
      showNodeSelectionBox: false,
      movable: false,
      movingRouterFallback: 'orth'
    } as any)

    graph.use(new Snapline({ enabled: true }))
    graph.use(new History({ enabled: true, beforeAddCommand: () => true }))
    graph.use(new Keyboard({ enabled: true }))
    graph.use(selection)

    graphInstance.value = graph
    selectionPlugin.value = selection

    graph.on('node:click', ({ node, e }) => {
      const target = e?.target as HTMLElement | null
      if (target?.closest('.x6-port')) return
      hideNodeContextMenu()
      hideEdgeContextMenu()
      emit('request-activate-cell', { cellId: node.id, cellType: 'node' })
    })

    graph.on('node:contextmenu', ({ node, e }) => {
      const target = e?.target as HTMLElement | null
      if (target?.closest('.x6-port')) return
      e?.preventDefault()
      e?.stopPropagation()
      hideEdgeContextMenu()
      openNodeContextMenu(node.id, e as unknown as MouseEvent)
    })

    graph.on('node:mouseenter', ({ node }) => {
      hoveredNodeId.value = node.id
      showNodePorts(node.id, true)
    })

    graph.on('node:mouseleave', ({ node }) => {
      if (hoveredNodeId.value === node.id) {
        hoveredNodeId.value = null
      }
      showNodePorts(node.id, props.activeCellType === 'node' && props.activeCellId === node.id)
    })

    graph.on('edge:click', ({ edge }) => {
      hideNodeContextMenu()
      hideEdgeContextMenu()
      emit('request-activate-cell', { cellId: edge.id, cellType: 'edge' })
    })

    graph.on('edge:contextmenu', ({ edge, e }) => {
      e?.preventDefault()
      e?.stopPropagation()
      hideNodeContextMenu()
      openEdgeContextMenu(edge.id, e as unknown as MouseEvent)
    })

    graph.on('edge:mouseenter', ({ edge }) => {
      edge.addTools([
        {
          name: 'button-remove',
          args: {
            distance: -40
          }
        }
      ])
    })

    graph.on('edge:mouseleave', ({ edge }) => {
      edge.removeTools()
    })

    graph.on('blank:click', () => {
      if (suppressBlankClick.value) return
      hideNodeContextMenu()
      hideEdgeContextMenu()
      emit('request-activate-cell', { cellId: null, cellType: null })
    })

    graph.on('blank:contextmenu', ({ e }) => {
      e?.preventDefault()
      hideNodeContextMenu()
      hideEdgeContextMenu()
    })

    graph.on('node:change:position', () => {
      if (applyingExternalGraph.value) return
      positionDirty.value = true
      updateOverlayPositions()
    })

    graph.on('node:mouseup', () => {
      if (!positionDirty.value) return
      positionDirty.value = false
      syncDomainGraphFromCanvas('drag-end')
      updateOverlayPositions()
    })

    graph.on('edge:connected', ({ edge, isNew }) => {
      updateEdgePresentation(edge)
      const sourceCellId = edge.getSourceCellId()
      const sourcePortId = edge.getSourcePortId()
      const targetCellId = edge.getTargetCellId()
      const targetPortId = edge.getTargetPortId()
      if (sourceCellId && sourcePortId) {
        syncPortDot(sourceCellId, sourcePortId, true, true)
      }
      if (targetCellId && targetPortId) {
        syncPortDot(targetCellId, targetPortId, true, true)
      }
      if (isNew) {
        emit('create-pending-edge-draft', buildEdgeFormFromCell(edge))
        emit('request-activate-cell', { cellId: edge.id, cellType: 'edge' })
        updateOverlayPositions()
        return
      }

      syncDomainGraphFromCanvas('connect-end')
      updateOverlayPositions()
    })

    graph.on('cell:removed', ({ cell }) => {
      if (applyingExternalGraph.value) return
      if (suppressRemovedCellId.value && suppressRemovedCellId.value === cell.id) {
        suppressRemovedCellId.value = null
        return
      }

      if (cell.isNode?.() && cell.getData()?.stencilTypeCode) return
      if (cell.isEdge?.()) {
        const sourceCellId = cell.getSourceCellId?.()
        const sourcePortId = cell.getSourcePortId?.()
        const targetCellId = cell.getTargetCellId?.()
        const targetPortId = cell.getTargetPortId?.()
        if (sourceCellId && sourcePortId) {
          const connected = isPortConnected(sourceCellId, sourcePortId)
          syncPortDot(sourceCellId, sourcePortId, connected, connected)
        }
        if (targetCellId && targetPortId) {
          const connected = isPortConnected(targetCellId, targetPortId)
          syncPortDot(targetCellId, targetPortId, connected, connected)
        }
        if (cell.id === props.pendingEdgeDraft?.id) {
          emit('request-activate-cell', { cellId: null, cellType: null })
          updateOverlayPositions()
          return
        }
      }
      syncDomainGraphFromCanvas('delete-end')
      updateOverlayPositions()
    })

    graph.on('node:added', ({ node }) => {
      const typeCode = String(node.getData()?.stencilTypeCode || '')
      if (!typeCode) return
      const position = node.position()
      suppressRemovedCellId.value = node.id
      node.remove()
      emit('material-drop', {
        typeCode,
        source: 'drag',
        position: {
          x: position.x,
          y: position.y
        }
      })
    })

    graph.on('scale', () => {
      updateZoomText()
      updateOverlayPositions()
    })

    graph.on('translate', () => {
      updateOverlayPositions()
    })

    graph.bindKey(['delete', 'backspace'], (event) => {
      event.preventDefault()
      emit('request-remove-selection')
      return false
    })

    graph.bindKey('esc', (event) => {
      event.preventDefault()
      emit('request-activate-cell', { cellId: null, cellType: null })
      return false
    })

    createStencil()
    ready.value = true
  }

  watch(
    () => props.materialGroups,
    () => {
      if (!graphInstance.value) return
      createStencil()
    },
    { deep: true }
  )

  watch(
    () => props.materialsVisible,
    async (visible) => {
      if (!visible) {
        stencilScrollbar.visible = false
        return
      }
      await nextTick()
      updateStencilScrollbar()
    }
  )

  watch(
    [graphSignature, issueSignature, () => props.dirtyNodeIds.join('|')],
    async () => {
      const graph = graphInstance.value
      if (!graph) return

      const issueSets = buildIssueSets()
      const nextJson = mapDomainGraphToX6(props.graph, issueSets, {
        dirtyNodeIds: new Set(props.dirtyNodeIds),
        pendingEdgeDraft: props.pendingEdgeDraft
      })
      const nextSnapshot = JSON.stringify(nextJson)
      if (nextSnapshot === localRenderSnapshot.value) {
        syncVisualSelection()
        updateOverlayPositions()
        return
      }

      applyingExternalGraph.value = true
      positionDirty.value = false
      runWithHistoryMuted(() => {
        graph.fromJSON(nextJson as any)
      })
      graph.getEdges().forEach((edge) => updateEdgePresentation(edge))
      localRenderSnapshot.value = nextSnapshot
      await nextTick()
      applyingExternalGraph.value = false
      syncVisualSelection()
      syncPortVisibility()
      updateOverlayPositions()
      updateStencilScrollbar()
      emit('rendered')
    },
    { deep: true, immediate: false }
  )

  watch(
    () => props.historySessionKey,
    () => {
      nextTick(() => {
        resetHistoryStack()
      })
    }
  )

  watch(
    () => [props.activeCellId, props.activeCellType],
    () => {
      syncVisualSelection()
      updateOverlayPositions()
    }
  )

  watch(
    () => [selectedNode.value?.id, selectedEdge.value?.id],
    () => {
      nextTick(() => {
        updateOverlayPositions()
      })
    }
  )

  onMounted(async () => {
    initializeGraph()

    if (!graphInstance.value) return
    const issueSets = buildIssueSets()
    const nextJson = mapDomainGraphToX6(props.graph, issueSets, {
      dirtyNodeIds: new Set(props.dirtyNodeIds),
      pendingEdgeDraft: props.pendingEdgeDraft
    })
    applyingExternalGraph.value = true
    positionDirty.value = false
    runWithHistoryMuted(() => {
      graphInstance.value?.fromJSON(nextJson as any)
    })
    graphInstance.value.getEdges().forEach((edge) => updateEdgePresentation(edge))
    resetHistoryStack()
    localRenderSnapshot.value = JSON.stringify(nextJson)
    await nextTick()
    await waitForPaint()
    applyingExternalGraph.value = false
    fitView()
    updateStencilScrollbar()
    syncVisualSelection()
    syncPortVisibility()
    updateZoomText()
    emit('rendered')
    window.addEventListener('pointerdown', handleWindowPointerDown)
    window.addEventListener('resize', updateOverlayPositions)
  })

  onBeforeUnmount(() => {
    window.removeEventListener('pointerdown', handleWindowPointerDown)
    window.removeEventListener('resize', updateOverlayPositions)
    stopBlankPanning()
    destroyStencil()
    graphInstance.value?.dispose()
  })
</script>

<style scoped lang="scss">
  .workflow-canvas {
    position: relative;
    width: 100%;
    height: 100%;
    overflow: hidden;
    background:
      radial-gradient(
        circle at top center,
        rgba(255, 255, 255, 0.92) 0%,
        rgba(251, 253, 255, 0) 38%
      ),
      linear-gradient(180deg, #fbfdff 0%, #f5f8fd 100%);
  }

  .workflow-canvas--panning {
    cursor: grabbing;
  }

  .workflow-canvas__graph {
    position: absolute;
    inset: 0;
  }

  .workflow-canvas__graph :deep(.x6-node) {
    transition:
      filter 0.18s ease,
      opacity 0.18s ease;
  }

  .workflow-canvas__graph :deep(.x6-widget-selection-box) {
    display: none !important;
  }

  .workflow-canvas__toolbar-slot {
    position: absolute;
    top: 20px;
    left: 20px;
    right: 20px;
    z-index: 20;
    pointer-events: none;
  }

  .workflow-canvas__stencil {
    position: absolute;
    top: 60px;
    left: 20px;
    bottom: 14px;
    width: 252px;
    display: flex;
    flex-direction: column;
    gap: 0;
    padding: 0 0 0 10px;
    border: 1px solid rgba(212, 216, 226, 0.92);
    border-radius: 14px;
    background: rgba(255, 255, 255, 0.96);
    box-shadow: 0 12px 26px rgba(15, 23, 42, 0.08);
    backdrop-filter: blur(12px);
    z-index: 18;
    transition:
      width 0.2s ease,
      height 0.2s ease,
      padding 0.2s ease,
      transform 0.2s ease;
    overflow: visible;
  }

  .workflow-canvas__stencil-body {
    min-height: 0;
    flex: 1;
    position: relative;
    z-index: 1;
    overflow: hidden;
  }

  .workflow-canvas__stencil-scrollbar {
    position: absolute;
    top: 6px;
    right: 0;
    bottom: 10px;
    width: 10px;
    pointer-events: none;
    z-index: 3;
  }

  .workflow-canvas__stencil-scrollbar::before {
    content: '';
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    width: 2px;
    background: transparent;
  }

  .workflow-canvas__stencil-scrollbar-thumb {
    position: absolute;
    right: 0;
    width: 4px;
    border-radius: 999px;
    background: rgba(148, 163, 184, 0.96);
  }

  .workflow-canvas__stencil-empty {
    flex: 0 0 auto;
    padding: 2px 0 0;
  }

  .workflow-canvas__overlay {
    position: absolute;
    z-index: 22;
  }

  .workflow-canvas__context-menu {
    min-width: 196px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 10px;
    border: 1px solid rgba(203, 213, 225, 0.88);
    border-radius: 16px;
    background: rgba(255, 255, 255, 0.96);
    box-shadow: 0 18px 32px rgba(15, 23, 42, 0.12);
    backdrop-filter: blur(16px);
  }

  .workflow-canvas__context-menu-title {
    padding: 2px 4px 8px;
    border-bottom: 1px solid rgba(226, 232, 240, 0.92);
    font-size: 13px;
    font-weight: 700;
    line-height: 18px;
    color: #0f172a;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .workflow-canvas__context-menu-item {
    height: 36px;
    display: flex;
    align-items: center;
    width: 100%;
    padding: 0 12px;
    border: 0;
    border-radius: 10px;
    background: transparent;
    color: #334155;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    transition:
      background-color 0.15s ease,
      color 0.15s ease;

    &:hover {
      background: #eef4ff;
      color: #3158d6;
    }
  }

  .workflow-canvas__context-menu-item--danger {
    &:hover {
      background: #fff1f0;
      color: #dc2626;
    }
  }

  .workflow-canvas__loading {
    position: absolute;
    inset: 88px 24px 24px 24px;
    display: grid;
    place-items: center;
    z-index: 24;
    border-radius: 20px;
    background: rgba(255, 255, 255, 0.76);
    backdrop-filter: blur(10px);
  }

  :deep(.x6-graph) {
    background: transparent;
  }

  :deep(.workflow-port-hit) {
    cursor: crosshair;
  }

  :deep(.workflow-port-dot) {
    filter: drop-shadow(0 0 10px rgba(37, 99, 235, 0.12));
  }

  :deep(.x6-edge[shape='workflow-editor-edge'] path) {
    vector-effect: non-scaling-stroke;
  }

  :deep(.x6-widget-stencil) {
    height: 100%;
    border: 0;
    background: transparent;
    overflow: hidden;
  }

  :deep(.x6-widget-stencil-title) {
    display: none;
  }

  :deep(.x6-widget-stencil-content) {
    top: 0 !important;
    right: 0 !important;
    height: 100%;
    margin-right: 0 !important;
    padding: 0;
    box-sizing: border-box;
    overflow: auto;
    scrollbar-width: none;
  }

  :deep(.x6-widget-stencil-content::-webkit-scrollbar) {
    width: 0 !important;
    height: 0 !important;
    display: none;
  }

  :deep(.x6-widget-stencil-group-title) {
    margin: 0;
    padding: 0 0 6px 0;
    background: transparent !important;
    color: #475569;
    font-size: 11px;
    line-height: 16px;
    font-weight: 700;
    letter-spacing: 0.04em;
  }

  :deep(.x6-widget-stencil-group) {
    margin: 0;
    padding-bottom: 10px;
  }

  :deep(.x6-widget-stencil-group > .x6-widget-stencil-group-title) {
    position: relative;
    height: auto;
    min-height: 0;
    margin: 0 0 2px;
    padding: 0 0 6px 26px !important;
    line-height: 16px !important;
  }

  :deep(.x6-widget-stencil-group.collapsable > .x6-widget-stencil-group-title) {
    cursor: pointer;
  }

  :deep(.x6-widget-stencil-group.collapsable > .x6-widget-stencil-group-title::before) {
    content: '';
    position: absolute;
    left: 0;
    top: 50%;
    width: 16px;
    height: 16px;
    border: 1px solid rgba(148, 163, 184, 0.75);
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.96);
    box-shadow: 0 2px 8px rgba(15, 23, 42, 0.06);
    transform: translateY(-58%);
  }

  :deep(.x6-widget-stencil-group.collapsable > .x6-widget-stencil-group-title::after) {
    content: '';
    position: absolute;
    left: 4px;
    top: 50%;
    width: 8px;
    height: 8px;
    transform: translateY(-58%);
    background: linear-gradient(#64748b, #64748b) center / 8px 1.5px no-repeat;
  }

  :deep(.x6-widget-stencil-group.collapsable.collapsed > .x6-widget-stencil-group-title::after) {
    background:
      linear-gradient(#64748b, #64748b) center / 8px 1.5px no-repeat,
      linear-gradient(#64748b, #64748b) center / 1.5px 8px no-repeat;
  }

  :deep(.x6-widget-stencil-group-content) {
    padding-right: 0;
    padding-top: 0;
    overflow: visible;
  }

  :deep(.workflow-stencil-card) {
    box-sizing: border-box;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 10px;
    border: 1px solid #5f95ff;
    border-radius: 8px;
    background: #ffffff;
    box-shadow: 0 4px 12px rgba(95, 149, 255, 0.08);
    transition:
      transform 0.18s ease,
      box-shadow 0.18s ease,
      border-color 0.18s ease;
  }

  :deep(.workflow-stencil-card__icon) {
    flex: 0 0 auto;
    width: 32px;
    height: 32px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 8px;
    font-size: 12px;
    font-weight: 600;
    line-height: 1;
  }

  :deep(.workflow-stencil-card__body) {
    min-width: 0;
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  :deep(.workflow-stencil-card__title) {
    min-width: 0;
    font-size: 14px;
    line-height: 18px;
    font-weight: 600;
    color: #141414;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  :deep(.workflow-stencil-card__description) {
    font-size: 12px;
    line-height: 16px;
    color: rgba(0, 0, 0, 0.65);
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    overflow: hidden;
  }

  :deep(.x6-widget-stencil-node:hover .workflow-stencil-card) {
    transform: translateY(-1px);
    border-color: #3d7eff;
    box-shadow: 0 8px 20px rgba(95, 149, 255, 0.18);
  }

  :deep(.x6-widget-transform) {
    display: none !important;
  }
</style>

<!-- 画布节点卡片样式：X6 渲染的节点不带组件 scope，必须走全局样式。 -->
<style lang="scss">
  @use '@/views/scheduler/workflow/editor/components/workflow-node-card.scss';
</style>
