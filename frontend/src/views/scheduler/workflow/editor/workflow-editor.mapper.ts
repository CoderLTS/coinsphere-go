/** 工作流编辑器辅助模块：workflow-editor.mapper。 */
import type {
  TaskDefinitionItem,
  WorkflowDefinitionItem,
  WorkflowGraph,
  WorkflowNodeDefinitionItem,
  WorkflowNodeItem
} from '@/api/scheduler'
import { buildWorkflowMaterialGroups, getNodeMaterialMeta, inferNodeKind } from './node-materials'
import type {
  WorkflowDomainEdge,
  WorkflowDomainGraphModel,
  WorkflowDomainNode,
  WorkflowDomainPort,
  WorkflowEdgeFormModel,
  WorkflowEditorMetaForm,
  WorkflowMaterialItem,
  WorkflowNodeFormModel,
  WorkflowX6EdgeCell,
  WorkflowX6GraphJson,
  WorkflowX6NodeCell
} from './types'

const DEFAULT_NODE_SIZE = {
  width: 260,
  height: 96
}

const STENCIL_NODE_SIZE = {
  width: 208,
  height: 66
}

const START_LABELS: Record<string, string> = {
  'start.manual': '手动开始',
  'start.schedule': '定时开始',
  'start.event': '事件开始',
  'start.webhook': 'Webhook 开始'
}

const LEGACY_NODE_LABELS: Record<string, Record<string, string>> = {
  'start.manual': {
    'Manual Start': '手动开始'
  },
  'start.schedule': {
    'Schedule Start': '定时开始'
  },
  'start.event': {
    'Event Start': '事件开始',
    'Failure Event Start': '失败事件开始'
  },
  'start.webhook': {
    'Webhook Start': 'Webhook 开始'
  },
  'task.run': {
    'Run Task': '执行任务',
    'Fetch News': '抓取新闻'
  },
  'event.publish': {
    'Publish Domain Event': '发布事件',
    'Publish Synced Event': '发布同步事件'
  },
  'condition.branch': {
    'Condition Branch': '条件判断',
    'Has Inserted Items': '存在新增新闻'
  },
  foreach: {
    Foreach: '遍历',
    'Foreach Inserted News': '遍历新增新闻'
  },
  notify: {
    'Send Notification': '发送通知',
    Notify: '发送通知',
    Alert: '发送告警'
  },
  'http.request': {
    'HTTP Request': 'HTTP 请求'
  },
  'delay.wait': {
    Delay: '等待'
  },
  end: {
    End: '结束'
  }
}

const cloneConfig = <T>(value: T): T => JSON.parse(JSON.stringify(value ?? null))

const truncateText = (value: string, max = 32) => {
  const text = String(value || '').trim()
  if (!text) return ''
  return text.length > max ? `${text.slice(0, max - 1)}…` : text
}

const getNodeMaterial = (typeCode: string, materials: WorkflowMaterialItem[]) => {
  return materials.find((item) => item.typeCode === typeCode)
}

const resolveNodeTitle = (typeCode: string, label: unknown, fallbackLabel?: string) => {
  const text = String(label || '').trim()
  if (!text) return String(fallbackLabel || typeCode || '').trim()
  return LEGACY_NODE_LABELS[typeCode]?.[text] || text
}

const createNodePosition = (index: number) => {
  const column = index % 3
  const row = Math.floor(index / 3)
  return {
    x: 180 + column * 320,
    y: 220 + row * 184
  }
}

const createAutoLayoutPositions = (graph: WorkflowGraph) => {
  const maxColumns = 4
  const columnGap = 260
  const bandGap = 208
  const itemGap = 144
  const startX = 140
  const startY = 184

  const nodeIds = (graph.nodes || []).map((node) => String(node.id))
  const indegree = new Map<string, number>()
  const outgoing = new Map<string, string[]>()
  const rank = new Map<string, number>()
  const positions = new Map<string, { x: number; y: number }>()

  nodeIds.forEach((nodeId) => {
    indegree.set(nodeId, 0)
    outgoing.set(nodeId, [])
    rank.set(nodeId, 0)
  })

  ;(graph.edges || []).forEach((edge) => {
    const source = String(edge.source || '')
    const target = String(edge.target || '')
    if (!outgoing.has(source) || !indegree.has(target)) return
    outgoing.get(source)?.push(target)
    indegree.set(target, (indegree.get(target) || 0) + 1)
  })

  const queue = nodeIds.filter((nodeId) => (indegree.get(nodeId) || 0) === 0)
  const visited = new Set<string>()

  while (queue.length) {
    const current = queue.shift() as string
    visited.add(current)
    const currentRank = rank.get(current) || 0

    ;(outgoing.get(current) || []).forEach((nextId) => {
      rank.set(nextId, Math.max(rank.get(nextId) || 0, currentRank + 1))
      indegree.set(nextId, (indegree.get(nextId) || 0) - 1)
      if ((indegree.get(nextId) || 0) <= 0) queue.push(nextId)
    })
  }

  nodeIds.forEach((nodeId) => {
    if (!visited.has(nodeId)) {
      rank.set(nodeId, Math.max(...Array.from(rank.values()), 0) + 1)
    }
  })

  const grouped = new Map<number, string[]>()
  nodeIds.forEach((nodeId) => {
    const level = rank.get(nodeId) || 0
    if (!grouped.has(level)) grouped.set(level, [])
    grouped.get(level)?.push(nodeId)
  })

  Array.from(grouped.entries())
    .sort((a, b) => a[0] - b[0])
    .forEach(([level, ids]) => {
      const column = level % maxColumns
      const band = Math.floor(level / maxColumns)
      const totalHeight = Math.max(0, (ids.length - 1) * itemGap)
      const bandStartY = startY + band * bandGap - totalHeight / 2

      ids.forEach((nodeId, index) => {
        positions.set(nodeId, {
          x: startX + column * columnGap,
          y: Math.round(bandStartY + index * itemGap)
        })
      })
    })

  return positions
}

export function flattenMaterials(nodeDefinitions: WorkflowNodeDefinitionItem[]): WorkflowMaterialItem[] {
  return buildWorkflowMaterialGroups(nodeDefinitions).flatMap((group) => group.items)
}

export function buildDefaultNodeConfig(
  definition: WorkflowNodeDefinitionItem | undefined,
  taskDefinitions: TaskDefinitionItem[]
): Record<string, any> {
  const properties = definition?.configSchema?.properties || {}

  const config = Object.fromEntries(
    Object.entries(properties).map(([key, rawSchema]) => {
      const schema = rawSchema as Record<string, any>
      let fallback: any = ''

      if (key === 'taskDefinitionCode') {
        fallback = taskDefinitions[0]?.code || ''
      } else if (schema.type === 'array') {
        fallback = []
      } else if (schema.type === 'object') {
        fallback = {}
      } else if (schema.type === 'boolean') {
        fallback = false
      } else if (schema.type === 'number' || schema.type === 'integer') {
        fallback = schema.minimum ?? 0
      }

      return [key, schema.default ?? fallback]
    })
  )

  const typeCode = String(definition?.typeCode || '')
  if (typeCode.startsWith('start.')) {
    const suffix = typeCode.split('.', 2)[1] || 'manual'
    config.entryKey = String(config.entryKey || `${suffix}.default`).trim()
    config.displayName = String(config.displayName || START_LABELS[typeCode] || '开始入口').trim()
    if (!config.inputBindings || typeof config.inputBindings !== 'object' || Array.isArray(config.inputBindings)) {
      config.inputBindings = {}
    }
  }

  return config
}

export function buildPortsForType(typeCode: string): WorkflowDomainPort[] {
  if (typeCode.startsWith('start.')) {
    return [{ id: 'out', group: 'out', role: 'out' }]
  }

  if (typeCode === 'end') {
    return [{ id: 'in', group: 'in', role: 'in' }]
  }

  if (typeCode === 'condition.branch') {
    return [
      { id: 'in', group: 'in', role: 'in' },
      { id: 'true', group: 'condition-true', role: 'out', label: 'TRUE' },
      { id: 'false', group: 'condition-false', role: 'out', label: 'FALSE' }
    ]
  }

  if (typeCode === 'foreach') {
    return [
      { id: 'in', group: 'in', role: 'in' },
      { id: 'body', group: 'foreach-body', role: 'out', label: 'BODY' }
    ]
  }

  return [
    { id: 'in', group: 'in', role: 'in' },
    { id: 'out', group: 'out', role: 'out' }
  ]
}

const inferEdgeSourcePort = (nodeType: string, branch?: string) => {
  if (nodeType === 'condition.branch') {
    return branch === 'false' ? 'false' : 'true'
  }
  if (nodeType === 'foreach') return 'body'
  return 'out'
}

const inferSourceTypeFromEdgePorts = (sourcePort?: string, branch?: string) => {
  if (sourcePort === 'body') return 'foreach'
  if (sourcePort === 'true' || sourcePort === 'false' || branch === 'true' || branch === 'false') {
    return 'condition.branch'
  }
  return ''
}

const normalizeEdgeSourcePort = (nodeType: string, sourcePort?: string, branch?: string) => {
  if (nodeType === 'condition.branch') {
    if (sourcePort === 'true' || sourcePort === 'false') return sourcePort
    return inferEdgeSourcePort(nodeType, branch)
  }
  if (nodeType === 'foreach') return 'body'
  return sourcePort || 'out'
}

const normalizeEdgeBranch = (nodeType: string, sourcePort?: string, branch?: string) => {
  if (nodeType === 'condition.branch') {
    return normalizeEdgeSourcePort(nodeType, sourcePort, branch)
  }
  return ''
}

const resolveEdgeSourceType = (
  nodeMap: Map<string, WorkflowDomainNode>,
  sourceId: string,
  sourcePort?: string,
  branch?: string
) => {
  return nodeMap.get(String(sourceId))?.data.typeCode || inferSourceTypeFromEdgePorts(sourcePort, branch)
}

const buildNodeCollapsedSummary = (node: WorkflowDomainNode) => {
  const config = node.data.config || {}

  switch (node.data.kind) {
    case 'start':
      if (node.data.typeCode === 'start.schedule') {
        return truncateText(String(config.cronExpression || config.runAt || config.displayName || '定时触发入口'))
      }
      if (node.data.typeCode === 'start.event') {
        return truncateText(String(config.eventType || config.displayName || '等待指定事件'))
      }
      if (node.data.typeCode === 'start.webhook') {
        return truncateText(String(config.displayName || 'Webhook 入口'))
      }
      return truncateText(String(config.displayName || '手动触发入口'))

    case 'task':
      return truncateText(String(config.taskDefinitionCode || '选择任务定义'))

    case 'condition': {
      const operator = String(config.operator || '').trim()
      const value = String(config.value ?? '').trim()
      return truncateText([config.path, operator, value].filter(Boolean).join(' ') || '配置分支条件')
    }

    case 'foreach':
      return truncateText(String(config.itemsPath || '遍历数组输入'))

    case 'notify': {
      const targets = Array.isArray(config.targets) ? config.targets.length : 0
      const channels = Array.isArray(config.channelTypes) ? config.channelTypes.length : 0
      return `${targets} 个目标 / ${channels} 个渠道`
    }

    case 'event':
      return truncateText(String(config.eventType || '发布事件'))

    case 'http':
      return truncateText(
        [String(config.method || 'POST').toUpperCase(), String(config.url || '').trim()].filter(Boolean).join(' ') ||
          '外部 HTTP 请求'
      )

    case 'delay':
      return `${Number(config.durationMs || 0)} ms`

    case 'end':
      return '结束当前链路'

    default:
      return truncateText(node.data.subtitle || '')
  }
}

const createDomainNode = (
  node: WorkflowNodeItem,
  index: number,
  fallbackPositions: Map<string, { x: number; y: number }>,
  nodeDefinitions: WorkflowNodeDefinitionItem[],
  materials: WorkflowMaterialItem[]
): WorkflowDomainNode => {
  const material = getNodeMaterial(node.type, materials)
  const definition = nodeDefinitions.find((item) => item.typeCode === node.type)
  const position = node.position || fallbackPositions.get(String(node.id)) || createNodePosition(index)

  const domainNode: WorkflowDomainNode = {
    id: node.id,
    position,
    size: {
      width: material?.width || DEFAULT_NODE_SIZE.width,
      height: material?.height || DEFAULT_NODE_SIZE.height
    },
    ports: buildPortsForType(node.type),
    data: {
      typeCode: node.type,
      kind: material?.kind || inferNodeKind(node.type),
      title: resolveNodeTitle(node.type, node.label, definition?.label || node.type),
      subtitle: material?.description || definition?.label || node.type,
      color: material?.color || '#64748b',
      iconText: material?.iconText || 'N',
      config: cloneConfig(node.config || {})
    }
  }

  domainNode.data.subtitle = buildNodeCollapsedSummary(domainNode)
  return domainNode
}

const createDomainEdge = (
  edge: {
    id: string
    source: string
    target: string
    sourcePort?: string
    targetPort?: string
    branch?: string
    label?: string
  },
  nodeMap: Map<string, WorkflowDomainNode>
): WorkflowDomainEdge => {
  const sourceType = resolveEdgeSourceType(nodeMap, String(edge.source), edge.sourcePort, edge.branch)
  const sourcePort = normalizeEdgeSourcePort(sourceType, edge.sourcePort, edge.branch)
  const branch = normalizeEdgeBranch(sourceType, sourcePort, edge.branch)

  return {
    id: String(edge.id),
    source: String(edge.source),
    target: String(edge.target),
    sourcePort,
    targetPort: 'in',
    data: {
      branch,
      label: String(edge.label || '')
    }
  }
}

export function createDomainEdgeFromForm(
  edge: WorkflowEdgeFormModel,
  nodes: WorkflowDomainNode[]
): WorkflowDomainEdge {
  return createDomainEdge(
    {
      id: edge.id,
      source: edge.source,
      target: edge.target,
      sourcePort: edge.sourcePort,
      targetPort: edge.targetPort,
      branch: edge.branch,
      label: edge.label
    },
    new Map(nodes.map((node) => [node.id, node]))
  )
}

export function createDefaultDomainGraph(
  nodeDefinitions: WorkflowNodeDefinitionItem[],
  taskDefinitions: TaskDefinitionItem[],
  materials: WorkflowMaterialItem[]
): WorkflowDomainGraphModel {
  const startTypeCode = 'start.manual'
  const startDefinition = nodeDefinitions.find((item) => item.typeCode === startTypeCode)
  const endDefinition = nodeDefinitions.find((item) => item.typeCode === 'end')
  const startConfig = buildDefaultNodeConfig(startDefinition, taskDefinitions)
  startConfig.entryKey = 'manual.default'
  startConfig.displayName = String(startConfig.displayName || '默认手动入口').trim()

  const startNode: WorkflowDomainNode = {
    id: 'start_1',
    position: { x: 180, y: 220 },
    size: { width: DEFAULT_NODE_SIZE.width, height: DEFAULT_NODE_SIZE.height },
    ports: buildPortsForType(startTypeCode),
    data: {
      typeCode: startTypeCode,
      kind: 'start',
      title: startDefinition?.label || START_LABELS[startTypeCode],
      subtitle: '',
      color: getNodeMaterialMeta(startTypeCode)?.color || '#3b82f6',
      iconText: getNodeMaterialMeta(startTypeCode)?.iconText || 'S',
      config: startConfig
    }
  }
  startNode.data.subtitle = buildNodeCollapsedSummary(startNode)

  const endNode: WorkflowDomainNode = {
    id: 'end_1',
    position: { x: 560, y: 220 },
    size: { width: DEFAULT_NODE_SIZE.width, height: DEFAULT_NODE_SIZE.height },
    ports: buildPortsForType('end'),
    data: {
      typeCode: 'end',
      kind: 'end',
      title: endDefinition?.label || '结束',
      subtitle: '',
      color: getNodeMaterialMeta('end')?.color || '#dc2626',
      iconText: getNodeMaterialMeta('end')?.iconText || 'E',
      config: buildDefaultNodeConfig(endDefinition, taskDefinitions)
    }
  }
  endNode.data.subtitle = buildNodeCollapsedSummary(endNode)

  return {
    nodes: [startNode, endNode],
    edges: [
      {
        id: 'edge_1',
        source: 'start_1',
        target: 'end_1',
        sourcePort: 'out',
        targetPort: 'in',
        data: {
          branch: '',
          label: ''
        }
      }
    ]
  }
}

export function mapServerGraphToDomain(
  graph: WorkflowGraph,
  nodeDefinitions: WorkflowNodeDefinitionItem[],
  materials: WorkflowMaterialItem[]
): WorkflowDomainGraphModel {
  const fallbackPositions = createAutoLayoutPositions(graph)
  const nodes = (graph.nodes || []).map((node, index) =>
    createDomainNode(node, index, fallbackPositions, nodeDefinitions, materials)
  )
  const nodeMap = new Map(nodes.map((node) => [node.id, node]))
  const edges = (graph.edges || []).map((edge) => createDomainEdge(edge, nodeMap))
  return { nodes, edges }
}

export function mapDomainGraphToServer(graph: WorkflowDomainGraphModel): WorkflowGraph {
  const nodeMap = new Map(graph.nodes.map((node) => [node.id, node]))
  return {
    nodes: graph.nodes.map((node) => ({
      id: node.id,
      type: node.data.typeCode,
      label: node.data.title,
      config: cloneConfig(node.data.config || {}),
      position: {
        x: Math.round(node.position.x),
        y: Math.round(node.position.y)
      }
    })),
    edges: graph.edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      branch: normalizeEdgeBranch(
        resolveEdgeSourceType(nodeMap, edge.source, edge.sourcePort, edge.data.branch),
        edge.sourcePort,
        edge.data.branch
      ),
      label: edge.data.label || ''
    }))
  }
}

const createPortGroup = (options: {
  x: number | string
  y: number | string
  stroke: string
  magnet: boolean | 'passive'
  labelColor?: string
  textAnchor?: 'start' | 'end' | 'middle'
  refDx?: number
}) => ({
  markup: [
    { tagName: 'circle', selector: 'portHit' },
    { tagName: 'circle', selector: 'portBody' },
    { tagName: 'text', selector: 'portLabel' }
  ],
  position: { name: 'absolute', args: { x: options.x, y: options.y } },
  attrs: {
    portHit: {
      r: 14,
      magnet: options.magnet,
      fill: 'transparent',
      stroke: 'transparent',
      class: 'workflow-port-hit'
    },
    portBody: {
      r: 4,
      magnet: options.magnet,
      stroke: options.stroke,
      strokeWidth: 1.4,
      fill: '#ffffff',
      style: { visibility: 'hidden' },
      class: 'workflow-port-dot'
    },
    portLabel: {
      fontSize: 10,
      fontWeight: 700,
      fill: options.labelColor || options.stroke,
      textAnchor: options.textAnchor || 'start',
      textVerticalAnchor: 'middle',
      refX: options.refDx ?? 12,
      style: { visibility: 'hidden' },
      class: 'workflow-port-label'
    }
  }
})

const buildPortGroups = () => ({
  in: createPortGroup({
    x: 0,
    y: '50%',
    stroke: '#94a3b8',
    magnet: 'passive',
    textAnchor: 'end',
    refDx: -12
  }),
  out: createPortGroup({
    x: '100%',
    y: '50%',
    stroke: '#3b82f6',
    magnet: true
  }),
  'condition-true': createPortGroup({
    x: '100%',
    y: 28,
    stroke: '#22c55e',
    magnet: true,
    labelColor: '#15803d'
  }),
  'condition-false': createPortGroup({
    x: '100%',
    y: 68,
    stroke: '#ef4444',
    magnet: true,
    labelColor: '#b91c1c'
  }),
  'foreach-body': createPortGroup({
    x: '100%',
    y: '50%',
    stroke: '#ca8a04',
    magnet: true,
    labelColor: '#a16207'
  })
})

const buildStencilAttrs = (material: WorkflowMaterialItem) => ({
  body: {
    stroke: '#5f95ff',
    strokeWidth: 1,
    fill: '#fff',
    rx: 8,
    ry: 8
  },
  iconRect: {
    fill: `${material.color}16`
  },
  iconLabel: {
    text: material.iconText,
    fill: material.color
  },
  title: {
    text: material.title
  },
  desc: {
    text: material.description
  }
})

const buildEdgeLabel = (edge: WorkflowDomainEdge) => {
  const text = edge.data.label || ''
  if (!text) return []
  return [
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
}

export function createStencilNode(material: WorkflowMaterialItem) {
  return {
    shape: 'workflow-stencil-card',
    width: STENCIL_NODE_SIZE.width,
    height: STENCIL_NODE_SIZE.height,
    attrs: buildStencilAttrs(material),
    data: {
      stencilTypeCode: material.typeCode,
      title: material.title,
      description: material.description,
      iconText: material.iconText,
      color: material.color
    }
  }
}

export function mapDomainGraphToX6(
  graph: WorkflowDomainGraphModel,
  issues: { nodeIds: Set<string>; edgeIds: Set<string>; firstMessages: Map<string, string> },
  options?: { dirtyNodeIds?: Set<string>; pendingEdgeDraft?: WorkflowEdgeFormModel | null }
): WorkflowX6GraphJson {
  const dirtyNodeIds = options?.dirtyNodeIds || new Set<string>()
  const renderedEdges = [...graph.edges]
  const cells: WorkflowX6GraphJson['cells'] = []

  graph.nodes.forEach((node) => {
    cells.push({
      id: node.id,
      shape: 'workflow-node-card',
      position: { x: node.position.x, y: node.position.y },
      size: { width: node.size.width, height: node.size.height },
      x: node.position.x,
      y: node.position.y,
      width: node.size.width,
      height: node.size.height,
      attrs: {
        body: {
          fill: 'transparent',
          stroke: 'transparent'
        }
      },
      ports: {
        groups: buildPortGroups(),
        items: node.ports.map((port) => ({
          id: port.id,
          group: port.group,
          attrs: {
            portLabel: {
              text: port.label || ''
            }
          }
        }))
      },
      data: {
        typeCode: node.data.typeCode,
        kind: node.data.kind,
        title: node.data.title,
        subtitle: node.data.subtitle,
        color: node.data.color,
        iconText: node.data.iconText,
        hasIssue: issues.nodeIds.has(node.id),
        issueSummary: issues.firstMessages.get(node.id) || '',
        isDirty: dirtyNodeIds.has(node.id),
        config: cloneConfig(node.data.config || {})
      }
    })
  })

  if (options?.pendingEdgeDraft) {
    renderedEdges.push(createDomainEdgeFromForm(options.pendingEdgeDraft, graph.nodes))
  }

  renderedEdges.forEach((edge) => {
    const hasIssue = issues.edgeIds.has(edge.id)
    cells.push({
      id: edge.id,
      shape: 'edge',
      source: { cell: edge.source, port: edge.sourcePort || 'out' },
      target: { cell: edge.target, port: edge.targetPort || 'in' },
      connector: { name: 'smooth' },
      attrs: {
        line: {
          stroke: hasIssue ? '#ef4444' : '#94a3b8',
          strokeWidth: hasIssue ? 2 : 1.6,
          targetMarker: {
            name: 'block',
            width: 12,
            height: 8
          }
        }
      },
      labels: buildEdgeLabel(edge),
      data: {
        branch: edge.data.branch,
        label: edge.data.label
      }
    })
  })

  return { cells }
}

export function mapX6GraphToDomain(
  graphJson: WorkflowX6GraphJson,
  nodeDefinitions: WorkflowNodeDefinitionItem[],
  materials: WorkflowMaterialItem[]
): WorkflowDomainGraphModel {
  const nodeMap = new Map<string, WorkflowDomainNode>()
  const nodes: WorkflowDomainNode[] = []
  const edges: WorkflowDomainEdge[] = []

  graphJson.cells.forEach((cell) => {
    if ((cell as WorkflowX6EdgeCell).shape === 'edge') return
    const nodeCell = cell as WorkflowX6NodeCell
    const typeCode = String(nodeCell.data?.typeCode || '')
    const material = getNodeMaterial(typeCode, materials)
    const definition = nodeDefinitions.find((item) => item.typeCode === typeCode)
    const position = nodeCell.position || { x: nodeCell.x || 0, y: nodeCell.y || 0 }
    const size = nodeCell.size || {
      width: nodeCell.width || material?.width || DEFAULT_NODE_SIZE.width,
      height: nodeCell.height || material?.height || DEFAULT_NODE_SIZE.height
    }
    const node: WorkflowDomainNode = {
      id: nodeCell.id,
      position: {
        x: Math.round(position.x || 0),
        y: Math.round(position.y || 0)
      },
      size: {
        width: Math.round(size.width || material?.width || DEFAULT_NODE_SIZE.width),
        height: Math.round(size.height || material?.height || DEFAULT_NODE_SIZE.height)
      },
      ports: buildPortsForType(typeCode),
      data: {
        typeCode,
        kind: material?.kind || inferNodeKind(typeCode),
        title: resolveNodeTitle(typeCode, nodeCell.data?.title, definition?.label || typeCode),
        subtitle: '',
        color: String(nodeCell.data?.color || material?.color || '#64748b'),
        iconText: String(nodeCell.data?.iconText || material?.iconText || 'N'),
        config: cloneConfig(nodeCell.data?.config || {})
      }
    }
    node.data.subtitle = buildNodeCollapsedSummary(node)
    nodeMap.set(node.id, node)
    nodes.push(node)
  })

  graphJson.cells.forEach((cell) => {
    if ((cell as WorkflowX6EdgeCell).shape !== 'edge') return
    const edgeCell = cell as WorkflowX6EdgeCell
    edges.push(
      createDomainEdge(
        {
          id: edgeCell.id,
          source: edgeCell.source?.cell || '',
          target: edgeCell.target?.cell || '',
          sourcePort: edgeCell.source?.port,
          targetPort: edgeCell.target?.port,
          branch: edgeCell.data?.branch || '',
          label: edgeCell.data?.label || ''
        },
        nodeMap
      )
    )
  })

  return { nodes, edges }
}

export function mapDomainNodeToForm(node: WorkflowDomainNode | null): WorkflowNodeFormModel | null {
  if (!node) return null
  return {
    id: node.id,
    label: node.data.title,
    typeCode: node.data.typeCode,
    kind: node.data.kind,
    config: cloneConfig(node.data.config || {})
  }
}

export function mapDomainEdgeToForm(edge: WorkflowDomainEdge | null): WorkflowEdgeFormModel | null {
  if (!edge) return null
  return {
    id: edge.id,
    source: edge.source,
    target: edge.target,
    sourcePort: edge.sourcePort || '',
    targetPort: edge.targetPort || '',
    branch: edge.data.branch || '',
    label: edge.data.label || ''
  }
}

export function applyNodeFormToDomain(node: WorkflowDomainNode, form: WorkflowNodeFormModel): WorkflowDomainNode {
  const nextNode: WorkflowDomainNode = {
    ...node,
    data: {
      ...node.data,
      title: form.label,
      config: cloneConfig(form.config || {})
    }
  }
  nextNode.data.subtitle = buildNodeCollapsedSummary(nextNode)
  return nextNode
}

export function applyEdgeFormToDomain(edge: WorkflowDomainEdge, form: WorkflowEdgeFormModel): WorkflowDomainEdge {
  const sourceType = inferSourceTypeFromEdgePorts(form.sourcePort || edge.sourcePort, form.branch || edge.data.branch)
  const sourcePort = normalizeEdgeSourcePort(sourceType, form.sourcePort || edge.sourcePort, form.branch || edge.data.branch)
  return {
    ...edge,
    sourcePort,
    targetPort: 'in',
    data: {
      branch: normalizeEdgeBranch(sourceType, sourcePort, form.branch || edge.data.branch),
      label: form.label
    }
  }
}

const createUniqueStartEntryKey = (
  typeCode: string,
  existingNodes: WorkflowDomainNode[],
  currentNodeId?: string
) => {
  const suffix = typeCode.split('.', 2)[1] || 'manual'
  const used = new Set(
    existingNodes
      .filter((node) => node.id !== currentNodeId)
      .filter((node) => node.data.typeCode.startsWith('start.'))
      .map((node) => String(node.data.config?.entryKey || '').trim())
      .filter(Boolean)
  )
  let index = 1
  let candidate = `${suffix}.entry_${index}`
  while (used.has(candidate)) {
    index += 1
    candidate = `${suffix}.entry_${index}`
  }
  return candidate
}

export function createDomainNodeFromType(
  typeCode: string,
  position: { x: number; y: number },
  nodeDefinitions: WorkflowNodeDefinitionItem[],
  taskDefinitions: TaskDefinitionItem[],
  materials: WorkflowMaterialItem[],
  existingNodes: WorkflowDomainNode[]
): WorkflowDomainNode {
  const material = getNodeMaterial(typeCode, materials)
  const definition = nodeDefinitions.find((item) => item.typeCode === typeCode)
  const base = typeCode.replace(/[^a-zA-Z0-9]+/g, '_')
  let index = 1
  let id = `${base}_${index}`

  while (existingNodes.some((node) => node.id === id)) {
    index += 1
    id = `${base}_${index}`
  }

  const config = buildDefaultNodeConfig(definition, taskDefinitions)
  if (typeCode.startsWith('start.')) {
    config.entryKey = createUniqueStartEntryKey(typeCode, existingNodes)
    config.displayName = String(config.displayName || START_LABELS[typeCode] || definition?.label || '开始入口').trim()
  }

  const node: WorkflowDomainNode = {
    id,
    position,
    size: {
      width: material?.width || DEFAULT_NODE_SIZE.width,
      height: material?.height || DEFAULT_NODE_SIZE.height
    },
    ports: buildPortsForType(typeCode),
    data: {
      typeCode,
      kind: material?.kind || inferNodeKind(typeCode),
      title: resolveNodeTitle(typeCode, definition?.label, typeCode),
      subtitle: '',
      color: material?.color || '#64748b',
      iconText: material?.iconText || 'N',
      config
    }
  }
  node.data.subtitle = buildNodeCollapsedSummary(node)
  return node
}

export function normalizeDefinitionMeta(definition: WorkflowDefinitionItem | null): WorkflowEditorMetaForm {
  return {
    code: definition?.code || '',
    displayName: definition?.displayName || '',
    description: definition?.description || ''
  }
}

export function buildEditorSnapshot(meta: WorkflowEditorMetaForm, graph: WorkflowDomainGraphModel) {
  return JSON.stringify({
    meta,
    graph: mapDomainGraphToServer(graph)
  })
}
