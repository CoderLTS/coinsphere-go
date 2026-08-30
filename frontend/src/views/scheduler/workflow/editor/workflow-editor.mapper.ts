/** 工作流编辑器辅助模块：workflow-editor.mapper。 */
import type {
  WorkflowDefinitionItem,
  WorkflowGraph,
  WorkflowNodeDefinitionItem,
  WorkflowNodeItem
} from '@/api/scheduler'
import {
  buildWorkflowMaterialGroups,
  getNodeMaterialMeta,
  inferNodeFormKind
} from './node-materials'
import {
  LOOP_NEXT_BRANCH,
  getNodeBranches,
  getNodeGraphKind,
  hasDynamicBranches
} from './node-registry'
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
  width: 184,
  height: 58
}

const START_LABELS: Record<string, string> = {
  'start.manual': '手动开始',
  'start.schedule': '定时开始',
  'start.event': '事件开始',
  'start.webhook': 'Webhook 开始'
}

const INDICATOR_CONDITION_TYPES = new Set([
  'official.quant.volume_spike_condition',
  'official.quant.price_change_condition',
  'official.quant.macd_condition',
  'official.quant.kdj_condition',
  'official.quant.rsi_condition',
  'official.quant.bollinger_condition'
])
const NOTIFICATION_NODE_TYPES = new Set([
  'official.notification.in_app',
  'official.notification.dingtalk',
  'official.notification.smtp'
])

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

export const buildEdgeDisplayLabel = (condition: string) => {
  const expression = String(condition || '').trim()
  if (!expression) return ''
  if (expression.includes('.endsWith(":59:59.999Z")')) return '每小时一次'
  if (expression.includes('input.triggered == true')) return '首次命中时'
  return '满足条件时'
}

const notificationCondition = (condition: string) => {
  const original = String(condition || '').trim()
  const trigger = 'input.triggered == true'
  if (original.split('&&').some((part) => part.trim() === trigger)) return original
  return original ? `(${original}) && ${trigger}` : trigger
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

export function flattenMaterials(
  nodeDefinitions: WorkflowNodeDefinitionItem[]
): WorkflowMaterialItem[] {
  return buildWorkflowMaterialGroups(nodeDefinitions).flatMap((group) => group.items)
}

export function buildDefaultNodeConfig(
  definition: WorkflowNodeDefinitionItem | undefined
): Record<string, any> {
  const properties = definition?.configSchema?.properties || {}

  const config = Object.fromEntries(
    Object.entries(properties).map(([key, rawSchema]) => {
      const schema = rawSchema as Record<string, any>
      let fallback: any = ''

      if (schema.type === 'array') {
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
    if (
      !config.inputBindings ||
      typeof config.inputBindings !== 'object' ||
      Array.isArray(config.inputBindings)
    ) {
      config.inputBindings = {}
    }
  }

  return config
}

/**
 * 按后端声明的图语义生成节点端口。
 * 分支节点每个 branch 一个出口，循环节点是 BODY + NEXT，起始/终止各只有一侧。
 * 这里不再出现具体类型编码 —— 后端加一种三分支节点，端口自动就是三个。
 */
export function buildPortsForType(
  typeCode: string,
  config?: Record<string, any>
): WorkflowDomainPort[] {
  const inPort: WorkflowDomainPort = { id: 'in', group: 'in', role: 'in' }

  switch (getNodeGraphKind(typeCode)) {
    case 'start':
      return [{ id: 'out', group: 'out', role: 'out' }]

    case 'terminal':
      return [inPort]

    case 'branch': {
      const branches = getNodeBranches(typeCode, config)
      return [
        inPort,
        ...branches.map((branch, index) => ({
          id: branch,
          group: resolveBranchPortGroup(branch, index, branches.length),
          role: 'out' as const,
          label: branch.toUpperCase()
        }))
      ]
    }

    case 'loop':
      // BODY 连循环体（每个元素跑一遍）；NEXT 连"整个循环跑完之后"继续的节点，可以不连。
      return [
        inPort,
        { id: 'body', group: 'loop-body', role: 'out', label: 'BODY' },
        { id: LOOP_NEXT_BRANCH, group: 'loop-next', role: 'out', label: 'NEXT' }
      ]

    default:
      return [inPort, { id: 'out', group: 'out', role: 'out' }]
  }
}

/**
 * 分支端口的样式分组。true/false 这种常见语义给固定的绿/红，其余按顺序摊在节点右侧。
 * 分组名同时决定端口在卡片上的纵向位置，见 buildPortGroups。
 */
const resolveBranchPortGroup = (branch: string, index: number, total: number) => {
  if (branch === 'true') return 'branch-true'
  if (branch === 'false') return 'branch-false'
  if (total <= 2) return index === 0 ? 'branch-true' : 'branch-false'
  return `branch-slot-${Math.min(index, MAX_BRANCH_SLOT)}`
}

/** 通用分支端口最多摊 4 个槽位，再多就叠在最后一个槽位上（画布上极少见）。 */
const MAX_BRANCH_SLOT = 3

const inferEdgeSourcePort = (nodeType: string, branch?: string) => {
  const kind = getNodeGraphKind(nodeType)
  if (kind === 'branch') {
    // 这里拿不到节点配置（只有边),动态分支就以边自带的 branch 为准，取不到再退回第一个静态分支。
    const branches = getNodeBranches(nodeType)
    if (branch) return branch
    return branches[0] || 'out'
  }
  if (kind === 'loop') return branch === LOOP_NEXT_BRANCH ? LOOP_NEXT_BRANCH : 'body'
  return 'out'
}

/**
 * 从边自带的端口/分支反推源节点类型。
 * 用在「节点还没建好索引」的场景（例如从服务端 graph 反序列化到一半），拿不准就返回空串。
 */
const inferSourceTypeFromEdgePorts = (sourcePort?: string, branch?: string) => {
  if (sourcePort === 'body' || sourcePort === LOOP_NEXT_BRANCH || branch === LOOP_NEXT_BRANCH)
    return 'foreach'
  if (sourcePort === 'true' || sourcePort === 'false' || branch === 'true' || branch === 'false') {
    return 'condition.branch'
  }
  return ''
}

const normalizeEdgeSourcePort = (nodeType: string, sourcePort?: string, branch?: string) => {
  const kind = getNodeGraphKind(nodeType)
  if (kind === 'branch') {
    if (sourcePort) return sourcePort
    return inferEdgeSourcePort(nodeType, branch)
  }
  if (kind === 'loop') {
    return sourcePort === LOOP_NEXT_BRANCH || branch === LOOP_NEXT_BRANCH
      ? LOOP_NEXT_BRANCH
      : 'body'
  }
  return sourcePort || 'out'
}

const normalizeEdgeBranch = (nodeType: string, sourcePort?: string, branch?: string) => {
  const kind = getNodeGraphKind(nodeType)
  // 分支节点的 branch 就是端口名；循环节点只有 NEXT 那条标 'next'，
  // 循环体入口边不带 branch（与既有数据保持一致，不需要迁移）。
  if (kind === 'branch') return normalizeEdgeSourcePort(nodeType, sourcePort, branch)
  if (kind === 'loop') {
    return normalizeEdgeSourcePort(nodeType, sourcePort, branch) === LOOP_NEXT_BRANCH
      ? LOOP_NEXT_BRANCH
      : ''
  }
  return ''
}

const resolveEdgeSourceType = (
  nodeMap: Map<string, WorkflowDomainNode>,
  sourceId: string,
  sourcePort?: string,
  branch?: string
) => {
  return (
    nodeMap.get(String(sourceId))?.data.typeCode || inferSourceTypeFromEdgePorts(sourcePort, branch)
  )
}

/** 没有定制表单的节点，卡片摘要按类型挑一两个关键配置显示。 */
const buildGenericNodeSummary = (typeCode: string, config: Record<string, any>) => {
  switch (typeCode) {
    case 'condition.switch': {
      const cases = Array.isArray(config.cases) ? config.cases : []
      return cases.length ? `${cases.length} 个分支 + default` : '配置分支列表'
    }
    case 'state.set': {
      const assignments = Array.isArray(config.assignments) ? config.assignments : []
      const keys = assignments.map((item: any) => String(item?.key || '')).filter(Boolean)
      return keys.length ? truncateText(keys.join(', ')) : '配置要设置的变量'
    }
    case 'state.append':
      return truncateText(String(config.key || '配置目标数组变量'))
    case 'array.filter':
      return truncateText(String(config.itemsPath || '配置源数组路径'))
    case 'log.message':
      return truncateText(String(config.message || '配置日志内容'))
    case 'workflow.call': {
      const code = String(config.workflowCode || '').trim()
      if (!code) return '选择目标工作流'
      return '已配置子工作流'
    }
    default:
      return ''
  }
}

const buildNodeCollapsedSummary = (node: WorkflowDomainNode) => {
  const config = node.data.config || {}

  switch (node.data.kind) {
    case 'start':
      if (node.data.typeCode === 'start.schedule') {
        return truncateText(
          String(config.cronExpression || config.runAt || config.displayName || '定时触发入口')
        )
      }
      if (node.data.typeCode === 'start.event') {
        return truncateText(String(config.eventType || config.displayName || '等待指定事件'))
      }
      if (node.data.typeCode === 'start.webhook') {
        return truncateText(String(config.displayName || 'Webhook 入口'))
      }
      return truncateText(String(config.displayName || '手动触发入口'))

    case 'agent': {
      const agentCode = String(config.agentCode || '').trim()
      if (!agentCode) return '选择智能体'
      const mode = config.analyze ? '结构化分析' : '自定义提示词'
      return truncateText(`${agentCode} / ${mode}`)
    }

    case 'condition': {
      // 多条件优先展示条数，单条件展示 "路径 运算 值"（值可能来自 valuePath）。
      const clauses = Array.isArray(config.conditions) ? config.conditions : []
      if (clauses.length) {
        const logic = String(config.logic || 'and').toUpperCase()
        return `${clauses.length} 个条件 / ${logic}`
      }
      const operator = String(config.operator || '').trim()
      const valuePath = String(config.valuePath ?? '').trim()
      const value = valuePath ? `→ ${valuePath}` : String(config.value ?? '').trim()
      return truncateText(
        [config.path, operator, value].filter(Boolean).join(' ') || '配置分支条件'
      )
    }

    case 'indicator-condition':
      return truncateText(
        [String(config.name || '').trim(), String(config.interval || '').trim()]
          .filter(Boolean)
          .join(' / ') || '配置指标条件'
      )

    case 'foreach':
      return truncateText(String(config.itemsPath || '遍历数组输入'))

    case 'notify': {
      const targets = Array.isArray(config.targets) ? config.targets.length : 0
      return targets ? `${targets} 个通知目标` : '通知工作流创建者'
    }

    case 'event':
      return truncateText(String(config.eventType || '发布事件'))

    case 'http':
      return truncateText(
        [String(config.method || 'POST').toUpperCase(), String(config.url || '').trim()]
          .filter(Boolean)
          .join(' ') || '外部 HTTP 请求'
      )

    case 'delay':
      return `${Number(config.durationMs || 0)} ms`

    case 'end':
      return '结束当前链路'

    case 'generic':
      return buildGenericNodeSummary(node.data.typeCode, config)

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
  const position =
    node.position || fallbackPositions.get(String(node.id)) || createNodePosition(index)

  const domainNode: WorkflowDomainNode = {
    id: node.id,
    position,
    size: {
      width: material?.width || DEFAULT_NODE_SIZE.width,
      height: material?.height || DEFAULT_NODE_SIZE.height
    },
    ports: buildPortsForType(node.type, node.config || {}),
    data: {
      typeCode: node.type,
      kind: material?.kind || inferNodeFormKind(node.type),
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
    condition?: string
  },
  nodeMap: Map<string, WorkflowDomainNode>
): WorkflowDomainEdge => {
  const sourceType = resolveEdgeSourceType(
    nodeMap,
    String(edge.source),
    edge.sourcePort,
    edge.branch
  )
  const sourcePort = normalizeEdgeSourcePort(sourceType, edge.sourcePort, edge.branch)
  const branch = normalizeEdgeBranch(sourceType, sourcePort, edge.branch)
  const sourceNode = nodeMap.get(String(edge.source))
  const targetNode = nodeMap.get(String(edge.target))
  const condition =
    INDICATOR_CONDITION_TYPES.has(sourceNode?.data.typeCode || '') &&
    NOTIFICATION_NODE_TYPES.has(targetNode?.data.typeCode || '') &&
    branch === 'true'
      ? notificationCondition(edge.condition || '')
      : String(edge.condition || '').trim()

  return {
    id: String(edge.id),
    source: String(edge.source),
    target: String(edge.target),
    sourcePort,
    targetPort: 'in',
    data: {
      branch,
      label: buildEdgeDisplayLabel(condition),
      condition
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
      label: edge.label,
      condition: edge.condition
    },
    new Map(nodes.map((node) => [node.id, node]))
  )
}

export function createDefaultDomainGraph(
  nodeDefinitions: WorkflowNodeDefinitionItem[]
): WorkflowDomainGraphModel {
  const startTypeCode = 'start.manual'
  const startDefinition = nodeDefinitions.find((item) => item.typeCode === startTypeCode)
  const endDefinition = nodeDefinitions.find((item) => item.typeCode === 'end')
  const startConfig = buildDefaultNodeConfig(startDefinition)
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
      config: buildDefaultNodeConfig(endDefinition)
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
          label: '',
          condition: ''
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
  const nodeOrder = new Map(graph.nodes.map((node, index) => [node.id, index]))
  const incomingConditions = (targetID: string, branch?: string) =>
    graph.edges
      .filter((edge) => edge.target === targetID && (!branch || edge.data.branch === branch))
      .map((edge) => {
        const source = nodeMap.get(edge.source)
        return INDICATOR_CONDITION_TYPES.has(source?.data.typeCode || '')
          ? {
              nodeInstanceId: edge.source,
              ...(edge.data.branch ? { branch: edge.data.branch } : {})
            }
          : null
      })
      .filter(Boolean)
      .sort(
        (left, right) =>
          (nodeOrder.get(left!.nodeInstanceId) || 0) - (nodeOrder.get(right!.nodeInstanceId) || 0)
      ) as { nodeInstanceId: string; branch?: string }[]
  const inputBindingsForNode = (node: WorkflowDomainNode) => {
    const existing: Record<string, any> =
      node.data.config?.__inputBindings && typeof node.data.config.__inputBindings === 'object'
        ? { ...node.data.config.__inputBindings }
        : {}
    const conditionSources = incomingConditions(node.id)
    if (INDICATOR_CONDITION_TYPES.has(node.data.typeCode)) {
      if (!existing.eventTime) {
        existing.eventTime = {
          kind: 'cel',
          expression: '"time" in event ? event.time : event.triggeredAt'
        }
      }
      if (conditionSources.length) {
        existing.pathEntered = { kind: 'condition_entry', sources: conditionSources }
      } else if (existing.pathEntered?.kind === 'condition_entry') {
        delete existing.pathEntered
      }
    }
    const notificationSources = incomingConditions(node.id, 'true')
    if (NOTIFICATION_NODE_TYPES.has(node.data.typeCode) && notificationSources.length) {
      existing.subjectKey = { kind: 'condition_subject', sources: notificationSources }
      existing.message = { kind: 'condition_message', sources: notificationSources }
    } else if (NOTIFICATION_NODE_TYPES.has(node.data.typeCode)) {
      if (existing.subjectKey?.kind === 'condition_subject') delete existing.subjectKey
      if (existing.message?.kind === 'condition_message') delete existing.message
    }
    return existing
  }
  return {
    nodes: graph.nodes.map((node) => ({
      id: node.id,
      type: node.data.typeCode,
      label: node.data.title,
      config: {
        ...cloneConfig(node.data.config || {}),
        __inputBindings: inputBindingsForNode(node)
      },
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
      label: buildEdgeDisplayLabel(
        INDICATOR_CONDITION_TYPES.has(nodeMap.get(edge.source)?.data.typeCode || '') &&
          NOTIFICATION_NODE_TYPES.has(nodeMap.get(edge.target)?.data.typeCode || '') &&
          edge.data.branch === 'true'
          ? notificationCondition(edge.data.condition)
          : edge.data.condition || ''
      ),
      condition:
        INDICATOR_CONDITION_TYPES.has(nodeMap.get(edge.source)?.data.typeCode || '') &&
        NOTIFICATION_NODE_TYPES.has(nodeMap.get(edge.target)?.data.typeCode || '') &&
        edge.data.branch === 'true'
          ? notificationCondition(edge.data.condition)
          : edge.data.condition || ''
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
  /**
   * 端口标签是否常显。默认只在 hover 节点时出现，避免画布糊成一片；
   * 但分支/循环节点的出口语义（TRUE/FALSE、BODY/NEXT）不看标签根本分不出来，必须常显。
   */
  alwaysVisible?: boolean
}) => {
  const visibility = options.alwaysVisible ? 'visible' : 'hidden'
  return {
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
        style: { visibility },
        class: 'workflow-port-dot'
      },
      portLabel: {
        fontSize: 10,
        fontWeight: 700,
        fill: options.labelColor || options.stroke,
        textAnchor: options.textAnchor || 'start',
        textVerticalAnchor: 'middle',
        refX: options.refDx ?? 12,
        style: { visibility },
        class: 'workflow-port-label'
      }
    }
  }
}

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
  // 下面这些是「有语义的出口」：分支走哪条、循环体还是循环之后，
  // 光看小圆点分不出来，所以标签常显（alwaysVisible）。
  'branch-true': createPortGroup({
    x: '100%',
    y: 28,
    stroke: '#22c55e',
    magnet: true,
    labelColor: '#15803d',
    alwaysVisible: true
  }),
  'branch-false': createPortGroup({
    x: '100%',
    y: 68,
    stroke: '#ef4444',
    magnet: true,
    labelColor: '#b91c1c',
    alwaysVisible: true
  }),
  // 通用分支槽位：给 true/false 之外的自定义分支用，纵向依次排开。
  'branch-slot-0': createPortGroup({
    x: '100%',
    y: 22,
    stroke: '#6366f1',
    magnet: true,
    labelColor: '#4338ca',
    alwaysVisible: true
  }),
  'branch-slot-1': createPortGroup({
    x: '100%',
    y: 42,
    stroke: '#8b5cf6',
    magnet: true,
    labelColor: '#6d28d9',
    alwaysVisible: true
  }),
  'branch-slot-2': createPortGroup({
    x: '100%',
    y: 62,
    stroke: '#a855f7',
    magnet: true,
    labelColor: '#7e22ce',
    alwaysVisible: true
  }),
  'branch-slot-3': createPortGroup({
    x: '100%',
    y: 82,
    stroke: '#c026d3',
    magnet: true,
    labelColor: '#a21caf',
    alwaysVisible: true
  }),
  'loop-body': createPortGroup({
    x: '100%',
    y: 28,
    stroke: '#ca8a04',
    magnet: true,
    labelColor: '#a16207',
    alwaysVisible: true
  }),
  'loop-next': createPortGroup({
    x: '100%',
    y: 68,
    stroke: '#0ea5e9',
    magnet: true,
    labelColor: '#0369a1',
    alwaysVisible: true
  })
})

const buildStencilAttrs = (material: WorkflowMaterialItem) => ({
  body: {
    stroke: 'var(--workflow-panel-border, #dfe4ec)',
    strokeWidth: 1,
    fill: 'var(--workflow-panel-raised, #f9fafc)',
    rx: 7,
    ry: 7
  },
  iconRect: {
    fill: `${material.color}16`
  },
  iconLabel: {
    text: material.iconText,
    fill: material.color
  },
  title: {
    text: material.title,
    fill: 'var(--workflow-panel-text, #263247)'
  },
  desc: {
    text: material.description,
    fill: 'var(--workflow-panel-muted, #78859a)'
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
      stencilTitle: material.title,
      stencilPresetConfig: material.presetConfig,
      stencilPresetSubtitle: material.presetSubtitle,
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
        label: edge.data.label,
        condition: edge.data.condition
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
      ports: buildPortsForType(typeCode, nodeCell.data?.config || {}),
      data: {
        typeCode,
        kind: material?.kind || inferNodeFormKind(typeCode),
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
          label: edgeCell.data?.label || '',
          condition: edgeCell.data?.condition || ''
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
    label: edge.data.label || '',
    condition: edge.data.condition || ''
  }
}

export function applyNodeFormToDomain(
  node: WorkflowDomainNode,
  form: WorkflowNodeFormModel
): WorkflowDomainNode {
  const nextNode: WorkflowDomainNode = {
    ...node,
    data: {
      ...node.data,
      title: form.label,
      config: cloneConfig(form.config || {})
    }
  }
  // 分支随配置变化的节点（多路 switch）在配置提交后要重建端口，否则新增的 case 没有出口可连。
  if (hasDynamicBranches(nextNode.data.typeCode)) {
    nextNode.ports = buildPortsForType(nextNode.data.typeCode, nextNode.data.config)
  }
  nextNode.data.subtitle = buildNodeCollapsedSummary(nextNode)
  return nextNode
}

export function applyEdgeFormToDomain(
  edge: WorkflowDomainEdge,
  form: WorkflowEdgeFormModel
): WorkflowDomainEdge {
  const sourceType = inferSourceTypeFromEdgePorts(
    form.sourcePort || edge.sourcePort,
    form.branch || edge.data.branch
  )
  const sourcePort = normalizeEdgeSourcePort(
    sourceType,
    form.sourcePort || edge.sourcePort,
    form.branch || edge.data.branch
  )
  return {
    ...edge,
    sourcePort,
    targetPort: 'in',
    data: {
      branch: normalizeEdgeBranch(sourceType, sourcePort, form.branch || edge.data.branch),
      label: buildEdgeDisplayLabel(form.condition),
      condition: form.condition
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

  const config = buildDefaultNodeConfig(definition)
  if (typeCode.startsWith('start.')) {
    config.entryKey = createUniqueStartEntryKey(typeCode, existingNodes)
    config.displayName = String(
      config.displayName || START_LABELS[typeCode] || definition?.label || '开始入口'
    ).trim()
  }

  const node: WorkflowDomainNode = {
    id,
    position,
    size: {
      width: material?.width || DEFAULT_NODE_SIZE.width,
      height: material?.height || DEFAULT_NODE_SIZE.height
    },
    ports: buildPortsForType(typeCode, config),
    data: {
      typeCode,
      kind: material?.kind || inferNodeFormKind(typeCode),
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

export function normalizeDefinitionMeta(
  definition: WorkflowDefinitionItem | null
): WorkflowEditorMetaForm {
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
