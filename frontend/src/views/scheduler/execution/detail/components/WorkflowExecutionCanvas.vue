<!-- 工作流执行详情页面或组件：WorkflowExecutionCanvas。 -->
<template>
  <div class="workflow-execution-canvas">
    <div ref="graphRef" class="workflow-execution-canvas__graph" />

    <div v-if="!graph.nodes.length" class="workflow-execution-canvas__empty">
      <ElEmpty description="当前执行缺少可回放的流程图" />
    </div>
  </div>
</template>

<script setup lang="ts">
  import { Graph } from '@antv/x6'
  import { useElementSize } from '@vueuse/core'
  import type { WorkflowExecutionNodeLog, WorkflowExecutionTransitionLog } from '@/api/scheduler'
  import { mapDomainGraphToX6 } from '@/views/scheduler/workflow/editor/workflow-editor.mapper'
  import type { WorkflowDomainGraphModel } from '@/views/scheduler/workflow/editor/types'
  import {
    ensureWorkflowGraphEdgeRegistered,
    ensureWorkflowNodeShapeRegistered
  } from '@/views/scheduler/workflow/editor/components/workflow-graph-shapes'

  type ActiveCellType = 'node' | 'edge' | null

  const props = defineProps<{
    graph: WorkflowDomainGraphModel
    nodeLogs: WorkflowExecutionNodeLog[]
    transitionLogs: WorkflowExecutionTransitionLog[]
    startNodeId: string
  }>()

  const emit = defineEmits<{
    (
      e: 'selection-change',
      payload: {
        cellId: string | null
        cellType: ActiveCellType
      }
    ): void
  }>()

  const graphRef = ref<HTMLDivElement | null>(null)
  const graphInstance = ref<Graph | null>(null)
  const renderSnapshot = ref('')
  const activeCellId = ref<string | null>(null)
  const activeCellType = ref<ActiveCellType>(null)
  let fitTimer: ReturnType<typeof setTimeout> | null = null
  const { width: graphHostWidth, height: graphHostHeight } = useElementSize(graphRef)

  const nodeLogsById = computed(() => {
    const map = new Map<string, WorkflowExecutionNodeLog[]>()
    props.nodeLogs.forEach((item) => {
      const list = map.get(item.nodeId) || []
      list.push(item)
      map.set(item.nodeId, list)
    })
    map.forEach((list) => {
      list.sort((a, b) => {
        if ((a.startedAt || '') === (b.startedAt || '')) return a.id - b.id
        return String(a.startedAt || '').localeCompare(String(b.startedAt || ''))
      })
    })
    return map
  })

  const transitionLogsByEdgeId = computed(() => {
    const map = new Map<string, WorkflowExecutionTransitionLog[]>()
    props.transitionLogs.forEach((item) => {
      const list = map.get(item.edgeId) || []
      list.push(item)
      map.set(item.edgeId, list)
    })
    map.forEach((list) => {
      list.sort((a, b) => {
        if (a.traversalIndex === b.traversalIndex) return a.id - b.id
        return a.traversalIndex - b.traversalIndex
      })
    })
    return map
  })

  const failedNodeIds = computed(() => {
    const set = new Set<string>()
    nodeLogsById.value.forEach((logs, nodeId) => {
      if (logs.some((item) => item.status === 'failed')) set.add(nodeId)
    })
    return set
  })

  const buildTextLabel = (text: string) => {
    if (!text) return []
    return [
      {
        position: 0.5,
        attrs: {
          body: {
            fill: '#181b1e',
            stroke: '#5b6468',
            strokeWidth: 1,
            rx: 10,
            ry: 10
          },
          label: {
            text,
            fill: '#d8dddf',
            fontSize: 11,
            fontWeight: 600
          }
        }
      }
    ]
  }

  const buildCountLabel = (count: number) => {
    if (count <= 1) return []
    return [
      {
        position: 0.76,
        attrs: {
          body: {
            fill: '#27222f',
            stroke: '#9e8cff',
            strokeWidth: 1,
            rx: 10,
            ry: 10
          },
          label: {
            text: `x${count}`,
            fill: '#c5baff',
            fontSize: 11,
            fontWeight: 700
          }
        }
      }
    ]
  }

  const resolveNodeExecutionState = (logs: WorkflowExecutionNodeLog[]) => {
    if (!logs.length) return ''
    if (logs.some((item) => item.status === 'failed')) return 'failed'
    if (logs.some((item) => item.status === 'running')) return 'running'
    if (logs.some((item) => item.status === 'retry_waiting')) return 'retry_waiting'
    if (logs.some((item) => item.status === 'queued' || item.status === 'pending')) return 'queued'
    return 'success'
  }

  const syncGraphSize = () => {
    const graph = graphInstance.value
    const host = graphRef.value
    if (!graph || !host) return
    const width = host.clientWidth
    const height = host.clientHeight
    if (!width || !height) return
    graph.resize(width, height)
  }

  const fitGraphView = () => {
    const graph = graphInstance.value
    if (!graph) return
    syncGraphSize()
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        ;(graph as any).zoomToFit?.({
          padding: {
            top: 44,
            right: 52,
            bottom: 44,
            left: 52
          },
          maxScale: 0.98
        })
        ;(graph as any).centerContent?.()
      })
    })
  }

  const scheduleFitGraphView = () => {
    const graph = graphInstance.value
    if (!graph) return
    if (fitTimer) clearTimeout(fitTimer)
    fitTimer = setTimeout(() => {
      syncGraphSize()
      ;(graph as any).zoomToFit?.({
        padding: {
          top: 44,
          right: 52,
          bottom: 44,
          left: 52
        },
        maxScale: 0.98
      })
      ;(graph as any).centerContent?.()
    }, 140)
  }

  const emitSelection = () => {
    emit('selection-change', {
      cellId: activeCellId.value,
      cellType: activeCellType.value
    })
  }

  const syncNodeVisuals = () => {
    const graph = graphInstance.value
    if (!graph) return
    graph.getNodes().forEach((node) => {
      const logs = nodeLogsById.value.get(node.id) || []
      const nextData = {
        ...(node.getData() || {}),
        executionState: resolveNodeExecutionState(logs),
        executionCount: logs.length,
        isExecutionEntry: props.startNodeId === node.id,
        isDimmed: false,
        isSelected: activeCellType.value === 'node' && activeCellId.value === node.id
      }
      node.setData(nextData)
    })
  }

  const syncEdgeVisuals = () => {
    const graph = graphInstance.value
    if (!graph) return
    graph.getEdges().forEach((edge) => {
      const transitions = transitionLogsByEdgeId.value.get(edge.id) || []
      const count = transitions.length
      const executed = count > 0
      const selected = activeCellType.value === 'edge' && activeCellId.value === edge.id
      const targetNodeId = String(edge.getTargetCellId() || '')
      const leadsToFailure = executed && failedNodeIds.value.has(targetNodeId)
      const stroke = leadsToFailure ? '#ff705b' : executed ? '#c7f46b' : '#6f777b'
      const opacity = selected ? 1 : executed ? 0.96 : 0.62
      const strokeWidth = selected ? (executed ? 3 : 2.2) : executed ? 1.9 : 1.1
      const baseLabel = String((edge.getData() || {}).label || '')
      const labels = [...buildTextLabel(baseLabel), ...buildCountLabel(count)]

      edge.setAttrs({
        line: {
          stroke,
          strokeWidth,
          opacity,
          strokeDasharray: executed ? '6 6' : '4 5',
          strokeLinecap: 'round',
          strokeLinejoin: 'round',
          class: executed
            ? 'workflow-execution-canvas__edge-line--flow'
            : 'workflow-execution-canvas__edge-line',
          targetMarker: {
            name: 'block',
            width: 9,
            height: 6,
            fill: stroke,
            stroke
          }
        }
      })
      edge.setLabels(labels)
      edge.setZIndex(executed ? 3 : 1)
    })
  }

  const syncVisuals = () => {
    syncNodeVisuals()
    syncEdgeVisuals()
  }

  const setSelection = (cellType: ActiveCellType, cellId: string | null) => {
    activeCellType.value = cellType
    activeCellId.value = cellId
    syncVisuals()
    emitSelection()
  }

  const renderGraph = () => {
    const graph = graphInstance.value
    if (!graph) return

    const nextJson = mapDomainGraphToX6(
      props.graph,
      {
        nodeIds: new Set<string>(),
        edgeIds: new Set<string>(),
        firstMessages: new Map<string, string>()
      },
      {
        dirtyNodeIds: new Set<string>()
      }
    )
    const nextSnapshot = JSON.stringify(nextJson)
    if (nextSnapshot === renderSnapshot.value) {
      syncVisuals()
      return
    }

    renderSnapshot.value = nextSnapshot
    graph.fromJSON(nextJson as any)
    syncVisuals()
    fitGraphView()
    scheduleFitGraphView()
  }

  const ensureGraph = () => {
    if (!graphRef.value || graphInstance.value) return
    ensureWorkflowNodeShapeRegistered()
    ensureWorkflowGraphEdgeRegistered()

    const graph = new Graph({
      container: graphRef.value,
      background: {
        color: 'transparent'
      },
      grid: {
        visible: false
      },
      panning: {
        enabled: true
      },
      mousewheel: {
        enabled: true,
        minScale: 0.42,
        maxScale: 2
      },
      connecting: {
        allowBlank: false,
        allowLoop: false,
        allowNode: false,
        allowEdge: false
      },
      interacting: {
        nodeMovable: false,
        edgeMovable: false,
        arrowheadMovable: false,
        vertexMovable: false
      }
    })

    graph.on('node:click', ({ node }) => {
      setSelection('node', node.id)
    })
    graph.on('edge:click', ({ edge }) => {
      setSelection('edge', edge.id)
    })
    graph.on('blank:click', () => {
      setSelection(null, null)
    })

    graphInstance.value = graph
    syncGraphSize()
    renderGraph()
  }

  const handleWindowResize = () => {
    syncGraphSize()
    fitGraphView()
  }

  watch(
    () => props.graph,
    () => {
      renderGraph()
    },
    { deep: true }
  )

  watch(
    [nodeLogsById, transitionLogsByEdgeId, () => props.startNodeId],
    () => {
      syncVisuals()
    },
    { deep: true }
  )

  watch([graphHostWidth, graphHostHeight], () => {
    syncGraphSize()
  })

  onMounted(() => {
    ensureGraph()
    nextTick(() => {
      syncGraphSize()
      fitGraphView()
      scheduleFitGraphView()
    })
    window.addEventListener('resize', handleWindowResize)
  })

  onBeforeUnmount(() => {
    window.removeEventListener('resize', handleWindowResize)
    if (fitTimer) clearTimeout(fitTimer)
    graphInstance.value?.dispose()
  })
</script>

<style scoped lang="scss">
  .workflow-execution-canvas {
    position: relative;
    display: block;
    width: 100%;
    height: 100%;
    overflow: hidden;
    background-color: #111315;
    background-image:
      linear-gradient(rgb(255 255 255 / 0.045) 1px, transparent 1px),
      linear-gradient(90deg, rgb(255 255 255 / 0.045) 1px, transparent 1px);
    background-size: 24px 24px;
    border: 1px solid #34393c;
    border-radius: 2px;
    contain: layout paint;
  }

  .workflow-execution-canvas__graph {
    position: absolute;
    inset: 0;
    min-width: 0;
    min-height: 0;
  }

  .workflow-execution-canvas__empty {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    pointer-events: none;
  }

  :deep(.x6-graph) {
    background: transparent;
  }

  :deep(.x6-edge path) {
    vector-effect: non-scaling-stroke;
  }

  :deep(.workflow-execution-canvas__edge-line--flow) {
    animation: workflow-execution-edge-flow 1.7s linear infinite;
  }

  @keyframes workflow-execution-edge-flow {
    from {
      stroke-dashoffset: 0;
    }

    to {
      stroke-dashoffset: -32;
    }
  }
</style>

<!-- 画布节点卡片样式：X6 渲染的节点不带组件 scope，必须走全局样式。 -->
<style lang="scss">
  @use '@/views/scheduler/workflow/editor/components/workflow-node-card';
</style>
