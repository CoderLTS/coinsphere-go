import type { WorkflowGraph, WorkflowGraphNode, WorkflowNodeDefinition } from '@/api/workflows'

export const cloneGraph = (graph: WorkflowGraph): WorkflowGraph => JSON.parse(JSON.stringify(graph))
export const emptyGraph = (): WorkflowGraph => ({
  schemaVersion: 3,
  entryPoints: {},
  nodes: [],
  edges: []
})

export function configDefaults(schema: Record<string, any>): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(schema.properties ?? {}).flatMap(([key, field]: [string, any]) =>
      field.default === undefined ? [] : [[key, JSON.parse(JSON.stringify(field.default))]]
    )
  )
}

export function addNode(graph: WorkflowGraph, definition: WorkflowNodeDefinition): WorkflowGraph {
  const next = cloneGraph(graph)
  const id = `node-${crypto.randomUUID()}`
  next.nodes.push({
    nodeInstanceId: id,
    nodeType: definition.type,
    nodeVersion: definition.version,
    config: configDefaults(definition.configSchema),
    inputBindings: {},
    position: { x: 100 + next.nodes.length * 40, y: 100 }
  })
  if (definition.kind === 'trigger' && !next.entryPoints.main) next.entryPoints.main = id
  return next
}

export function removeNode(graph: WorkflowGraph, id: string): WorkflowGraph {
  const next = cloneGraph(graph)
  next.nodes = next.nodes.filter((node) => node.nodeInstanceId !== id)
  next.edges = next.edges.filter(
    (edge) => edge.sourceNodeInstanceId !== id && edge.targetNodeInstanceId !== id
  )
  for (const [name, nodeID] of Object.entries(next.entryPoints))
    if (nodeID === id) delete next.entryPoints[name]
  return next
}

export function canvasCells(
  graph: WorkflowGraph,
  definitions: WorkflowNodeDefinition[],
  states: Record<string, string> = {}
) {
  const colors: Record<string, string> = {
    succeeded: '#168477',
    failed: '#bf414c',
    waiting: '#b27717',
    retrying: '#b27717',
    running: '#356eb8',
    skipped: '#8b97a7'
  }
  return [
    ...graph.nodes.map((node: WorkflowGraphNode) => {
      const definition = definitions.find((item) => item.type === node.nodeType)
      const state = states[node.nodeInstanceId]
      return {
        id: node.nodeInstanceId,
        shape: 'rect',
        ...node.position,
        width: 210,
        height: 68,
        label: `${definition?.title ?? node.nodeType}\n${state ?? node.nodeInstanceId}`,
        attrs: {
          body: {
            fill: 'var(--el-bg-color, #fff)',
            stroke: colors[state] ?? definition?.color ?? '#8090a6',
            strokeWidth: 2,
            rx: 6,
            ry: 6
          },
          label: { fill: 'var(--el-text-color-primary, #24364b)', fontSize: 12 }
        },
        ports: {
          groups: {
            in: {
              position: 'left',
              attrs: { circle: { r: 5, magnet: true, stroke: '#8090a6', fill: '#fff' } }
            },
            out: {
              position: 'right',
              attrs: { circle: { r: 5, magnet: true, stroke: '#8090a6', fill: '#fff' } }
            }
          },
          items: [
            ...(definition?.inputPorts ?? ['in']).map((id) => ({
              id: `in:${id}`,
              group: 'in',
              label: id
            })),
            ...(definition?.outputPorts ?? ['out']).map((id) => ({
              id: `out:${id}`,
              group: 'out',
              label: id
            }))
          ]
        }
      }
    }),
    ...graph.edges.map((edge) => ({
      id: edge.edgeId,
      shape: 'edge',
      source: { cell: edge.sourceNodeInstanceId, port: `out:${edge.sourcePort}` },
      target: { cell: edge.targetNodeInstanceId, port: `in:${edge.targetPort}` },
      labels: edge.condition ? [edge.condition] : [],
      attrs: { line: { stroke: '#8090a6', strokeWidth: 1.5, targetMarker: 'block' } }
    }))
  ]
}
