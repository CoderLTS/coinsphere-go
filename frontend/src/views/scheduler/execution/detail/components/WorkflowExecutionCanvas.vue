<template>
  <div class="execution-canvas"><div ref="host" class="execution-canvas__graph" /><ElEmpty v-if="!graph.nodes.length" description="此运行没有图数据" /></div>
</template>
<script setup lang="ts">
import { Graph } from '@antv/x6'
import { useElementSize } from '@vueuse/core'
import type { WorkflowExecutionNodeAttempt } from '@/api/scheduler'
import { fetchWorkflowNodeDefinitions, type WorkflowGraph, type WorkflowNodeDefinition } from '@/api/workflows'
import { canvasNode, canvasEdges } from '@/views/scheduler/workflow/editor/canvas'
const props = defineProps<{ graph: WorkflowGraph; nodeAttempts: WorkflowExecutionNodeAttempt[]; startNodeId: string }>()
const emit = defineEmits<{ (event: 'selection-change', payload: { cellId: string | null; cellType: 'node' | 'edge' | null }): void }>()
const host = ref<HTMLDivElement>(), canvas = shallowRef<Graph>()
const definitions = ref<WorkflowNodeDefinition[]>([])
const selected = ref('')
const { width, height } = useElementSize(host)
const latestAttempts = computed(() => {
  const map = new Map<string, WorkflowExecutionNodeAttempt>()
  for (const attempt of props.nodeAttempts) if ((map.get(attempt.nodeId)?.id || 0) < attempt.id) map.set(attempt.nodeId, attempt)
  return map
})
function styleNodes() {
  for (const node of canvas.value?.getNodes() || []) {
    const status = latestAttempts.value.get(node.id)?.status || ''
    const colors: Record<string, string> = { success: '#16a34a', failed: '#dc2626', running: '#2563eb', waiting: '#d97706', retry_waiting: '#d97706', canceled: '#94a3b8', skipped: '#94a3b8' }
    node.attr('body/stroke', colors[status] || '#94a3b8')
    node.attr('body/strokeWidth', selected.value === node.id ? 4 : node.id === props.startNodeId ? 3 : 2)
    node.attr('body/opacity', status ? 1 : 0.5)
  }
  for (const edge of canvas.value?.getEdges() || []) edge.attr('line/strokeWidth', selected.value === edge.id ? 3 : 1.5)
}
function render() {
  if (!canvas.value) return
  const nodes = props.graph.nodes.map(node => {
    const definition = definitions.value.find(d => d.type === node.nodeType && d.version === node.nodeVersion)
    const metadata = canvasNode(node, definition)
    // 修订可能使用已移除的插件，历史画布仍依据图中保存的端口显示。
    if (!definition && metadata.ports && !Array.isArray(metadata.ports)) metadata.ports.items = [
      ...new Set(props.graph.edges.filter(e => e.targetNodeInstanceId === node.nodeInstanceId).map(e => e.targetPort))
    ].map(id => ({ id: `in:${id}`, group: 'in' })).concat([
      ...new Set(props.graph.edges.filter(e => e.sourceNodeInstanceId === node.nodeInstanceId).map(e => e.sourcePort))
    ].map(id => ({ id: `out:${id}`, group: 'out' })))
    return metadata
  })
  canvas.value.fromJSON({ nodes, edges: canvasEdges(props.graph) }); styleNodes(); canvas.value.zoomToFit({ padding: 60, maxScale: 1 })
}
watch(() => props.graph, render, { deep: true })
watch([latestAttempts, () => props.startNodeId], styleNodes)
watch([width, height], ([w, h]) => { if (w && h) canvas.value?.resize(w, h) })
onMounted(async () => {
  if (!host.value) return
  canvas.value = new Graph({ container: host.value, panning: true, mousewheel: { enabled: true, minScale: 0.25, maxScale: 2 }, interacting: false })
  canvas.value.on('node:click', ({ node }) => { selected.value = node.id; styleNodes(); emit('selection-change', { cellId: node.id, cellType: 'node' }) })
  canvas.value.on('edge:click', ({ edge }) => { selected.value = edge.id; styleNodes(); emit('selection-change', { cellId: edge.id, cellType: 'edge' }) })
  canvas.value.on('blank:click', () => { selected.value = ''; styleNodes(); emit('selection-change', { cellId: null, cellType: null }) })
  try { definitions.value = (await fetchWorkflowNodeDefinitions()).items } finally { render() }
})
onBeforeUnmount(() => canvas.value?.dispose())
</script>
<style scoped>
.execution-canvas { position: relative; height: 100%; width: 100%; background: var(--el-fill-color-lighter); border-radius: 8px; }
.execution-canvas__graph { position: absolute; inset: 0; }
</style>
