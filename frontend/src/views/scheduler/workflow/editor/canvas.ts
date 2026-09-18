import type { Node, Edge } from '@antv/x6'
import { toRaw } from 'vue'
import type { WorkflowGraph, WorkflowGraphNode, WorkflowNodeDefinition } from '@/api/workflows'

export function canvasNode(node: WorkflowGraphNode, definition?: WorkflowNodeDefinition): Node.Metadata {
  const color = definition?.color || '#64748b'
  return {
    id: node.nodeInstanceId, shape: 'rect', ...node.position, width: 210, height: 70,
    label: node.label || definition?.title || node.nodeType,
    attrs: { body: { fill: 'var(--el-bg-color)', stroke: color, strokeWidth: 2, rx: 8, ry: 8 }, label: { fill: 'var(--el-text-color-primary)', fontSize: 13, textWrap: { width: 186, height: 55, ellipsis: true } } },
    ports: {
      groups: {
        in: { position: 'left', attrs: { circle: { r: 5, magnet: 'passive', stroke: color, fill: 'var(--el-bg-color)' } } },
        out: { position: 'right', attrs: { circle: { r: 5, magnet: true, stroke: color, fill: 'var(--el-bg-color)' }, text: { fill: 'var(--el-text-color-secondary)', fontSize: 10 } }, label: { position: 'right' } }
      },
      items: [
        ...(definition?.inputPorts || []).map(id => ({ id: `in:${id}`, group: 'in' })),
        ...(definition?.outputPorts || []).map(id => ({ id: `out:${id}`, group: 'out', attrs: { text: { text: portLabel(id) } } }))
      ]
    }
  }
}

export const portLabel = (port: string) => (({ out: '', true: '满足', false: '不满足', unavailable: '数据不可用', matched: '跟涨 / 跟跌', timeout: '到期未达到', completed: '完成' } as Record<string, string>)[port] || port)

export function canvasEdges(graph: WorkflowGraph): Edge.Metadata[] {
  return graph.edges.map(edge => ({
    id: edge.edgeId, shape: 'edge',
    source: { cell: edge.sourceNodeInstanceId, port: `out:${edge.sourcePort}` },
    target: { cell: edge.targetNodeInstanceId, port: `in:${edge.targetPort}` },
    router: { name: 'manhattan' }, connector: { name: 'rounded' },
    attrs: { line: { stroke: '#94a3b8', strokeWidth: 1.5, targetMarker: 'block' } },
    labels: edge.label ? [edge.label] : []
  }))
}

export function schemaDefaults(schema: Record<string, any>): Record<string, any> {
  return Object.fromEntries(Object.entries(schema.properties || {}).flatMap(([key, raw]) => {
    const field = raw as Record<string, any>
    return field.default === undefined ? [] : [[key, structuredClone(toRaw(field.default))]]
  }))
}

export function ancestors(graph: WorkflowGraph, id: string): Set<string> {
  const found = new Set<string>()
  const pending = [id]
  while (pending.length) {
    const current = pending.pop()
    for (const edge of graph.edges.filter(edge => edge.targetNodeInstanceId === current)) {
      if (!found.has(edge.sourceNodeInstanceId) && edge.sourceNodeInstanceId !== id) {
        found.add(edge.sourceNodeInstanceId)
        pending.push(edge.sourceNodeInstanceId)
      }
    }
  }
  return found
}

export interface FieldOption { nodeId: string; path: string[]; label: string; schema: Record<string, any> }
export function outputFields(graph: WorkflowGraph, definitions: WorkflowNodeDefinition[], nodeId: string): FieldOption[] {
  const upstream = ancestors(graph, nodeId)
  const fields: FieldOption[] = []
  function visit(node: WorkflowGraphNode, schema: Record<string, any>, path: string[]) {
    if (path.length > 12) return
    if (path.length) fields.push({ nodeId: node.nodeInstanceId, path, label: `${node.label || definitions.find(d => d.type === node.nodeType)?.title || node.nodeType} · ${path.join('.')}`, schema })
    for (const [key, property] of Object.entries(schema.properties || {})) visit(node, property as Record<string, any>, [...path, key])
  }
  for (const node of graph.nodes.filter(n => upstream.has(n.nodeInstanceId))) visit(node, definitions.find(d => d.type === node.nodeType)?.outputSchema || {}, [])
  return fields
}
