<template>
  <div class="graph-surface">
    <div class="graph-actions"
      ><ElButton @click="canvas?.zoom(-0.1)">缩小</ElButton
      ><ElButton @click="canvas?.zoom(0.1)">放大</ElButton
      ><ElButton @click="canvas?.zoomToFit({ maxScale: 1 })">适应画布</ElButton></div
    >
    <div ref="container" class="graph-canvas" role="region" aria-label="工作流画布" />
  </div>
</template>
<script setup lang="ts">
  import { Graph } from '@antv/x6'
  import type { WorkflowGraph, WorkflowNodeDefinition } from '@/api/workflows'
  import { canvasCells, cloneGraph } from './graph'
  const props = defineProps<{
    graph: WorkflowGraph
    definitions: WorkflowNodeDefinition[]
    readonly?: boolean
    states?: Record<string, string>
  }>()
  const emit = defineEmits<{
    (event: 'update:graph', graph: WorkflowGraph): void
    (event: 'select', id: string, kind: 'node' | 'edge'): void
  }>()
  const container = ref<HTMLElement>()
  let canvas: Graph | undefined
  let rendering = false
  const render = () => {
    if (!canvas) return
    rendering = true
    canvas.fromJSON({ cells: canvasCells(props.graph, props.definitions, props.states) })
    rendering = false
  }
  onMounted(() => {
    if (!container.value) return
    canvas = new Graph({
      container: container.value,
      autoResize: true,
      grid: true,
      panning: true,
      mousewheel: { enabled: true, modifiers: ['ctrl', 'meta'] },
      interacting: !props.readonly,
      connecting: {
        allowBlank: false,
        allowLoop: false,
        allowNode: false,
        allowEdge: false,
        snap: true,
        validateConnection: ({ sourcePort, targetPort }) =>
          Boolean(sourcePort?.startsWith('out:') && targetPort?.startsWith('in:'))
      }
    })
    canvas.on('node:click', ({ node }) => emit('select', node.id, 'node'))
    canvas.on('edge:click', ({ edge }) => emit('select', edge.id, 'edge'))
    canvas.on('node:mouseup', ({ node }) => {
      if (props.readonly || rendering) return
      const next = cloneGraph(props.graph)
      const item = next.nodes.find((item) => item.nodeInstanceId === node.id)
      if (item) {
        item.position = node.position()
        emit('update:graph', next)
      }
    })
    canvas.on('edge:connected', ({ edge }) => {
      if (props.readonly || rendering) return
      const source = edge.getSourceCellId(),
        target = edge.getTargetCellId()
      if (!source || !target) return
      const next = cloneGraph(props.graph)
      const item = {
        edgeId: edge.id,
        sourceNodeInstanceId: source,
        sourcePort: (edge.getSourcePortId() ?? '').replace(/^out:/, ''),
        targetNodeInstanceId: target,
        targetPort: (edge.getTargetPortId() ?? '').replace(/^in:/, '')
      }
      const old = next.edges.findIndex((item) => item.edgeId === edge.id)
      if (old < 0) next.edges.push(item)
      else next.edges[old] = { ...next.edges[old], ...item }
      emit('update:graph', next)
    })
    render()
    canvas.zoomToFit({ maxScale: 1 })
  })
  watch(() => [props.graph, props.definitions, props.states], render, { deep: true })
  onBeforeUnmount(() => canvas?.dispose())
</script>
<style scoped>
  .graph-surface {
    position: relative;
    min-width: 0;
    height: 100%;
    min-height: 420px;
    background: var(--el-fill-color-lighter);
  }
  .graph-canvas {
    width: 100%;
    height: 100%;
    min-height: 420px;
  }
  .graph-actions {
    position: absolute;
    z-index: 1;
    right: 16px;
    bottom: 16px;
  }
</style>
