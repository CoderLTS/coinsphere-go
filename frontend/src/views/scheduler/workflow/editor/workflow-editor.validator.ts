/** 工作流编辑器辅助模块：workflow-editor.validator。 */
import type { TaskDefinitionItem } from '@/api/scheduler'
import type {
  WorkflowDomainEdge,
  WorkflowDomainGraphModel,
  WorkflowDomainNode,
  WorkflowDraftValidationResult,
  WorkflowEditorIssue,
  WorkflowEdgeFormModel,
  WorkflowNodeFormModel
} from './types'

const createIssue = (partial: Omit<WorkflowEditorIssue, 'id' | 'source'> & { id?: string }): WorkflowEditorIssue => ({
  id: partial.id || `client-${Math.random().toString(36).slice(2, 10)}`,
  source: 'client',
  ...partial
})

const dedupeIssues = (issues: WorkflowEditorIssue[]) => {
  const seen = new Set<string>()
  return issues.filter((issue) => {
    const key = [issue.scope, issue.level, issue.nodeId || '', issue.edgeId || '', issue.field || '', issue.message].join(':')
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

const isStartNode = (node: WorkflowDomainNode) => node.data.typeCode.startsWith('start.')

const buildAdjacency = (graph: WorkflowDomainGraphModel) => {
  const nodeMap = new Map(graph.nodes.map((node) => [node.id, node]))
  const incoming = new Map<string, WorkflowDomainEdge[]>()
  const outgoing = new Map<string, WorkflowDomainEdge[]>()
  const adjacency = new Map<string, string[]>()

  graph.nodes.forEach((node) => {
    incoming.set(node.id, [])
    outgoing.set(node.id, [])
    adjacency.set(node.id, [])
  })

  graph.edges.forEach((edge) => {
    incoming.get(edge.target)?.push(edge)
    outgoing.get(edge.source)?.push(edge)
    adjacency.get(edge.source)?.push(edge.target)
  })

  return { nodeMap, incoming, outgoing, adjacency }
}

export function validateWorkflowDraft(graph: WorkflowDomainGraphModel): WorkflowEditorIssue[] {
  const issues: WorkflowEditorIssue[] = []
  const nodes = graph.nodes
  const edges = graph.edges
  const { nodeMap, incoming, outgoing, adjacency } = buildAdjacency(graph)

  if (!nodes.length) {
    issues.push(createIssue({ scope: 'graph', level: 'error', message: '至少需要一个节点。' }))
    return issues
  }

  const nodeIdSet = new Set<string>()
  nodes.forEach((node) => {
    if (!node.id.trim()) {
      issues.push(createIssue({ scope: 'node', level: 'error', message: '节点 ID 不能为空。' }))
      return
    }
    if (nodeIdSet.has(node.id)) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          field: 'id',
          message: '节点 ID 不能重复。'
        })
      )
    }
    nodeIdSet.add(node.id)

    if (!node.data.title.trim()) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          field: 'label',
          message: '节点名称不能为空。'
        })
      )
    }
  })

  edges.forEach((edge) => {
    if (!nodeMap.has(edge.source) || !nodeMap.has(edge.target)) {
      issues.push(
        createIssue({
          scope: 'edge',
          level: 'error',
          edgeId: edge.id,
          message: '连线的起点或终点不存在。'
        })
      )
      return
    }
    if (edge.source === edge.target) {
      issues.push(
        createIssue({
          scope: 'edge',
          level: 'error',
          edgeId: edge.id,
          message: '不允许自环连接。'
        })
      )
    }
  })

  const startNodes = nodes.filter(isStartNode)
  const endNodes = nodes.filter((node) => node.data.typeCode === 'end')

  if (!startNodes.length) {
    issues.push(createIssue({ scope: 'graph', level: 'error', message: '工作流至少需要一个开始节点。' }))
  }
  if (!endNodes.length) {
    issues.push(createIssue({ scope: 'graph', level: 'error', message: '工作流至少需要一个结束节点。' }))
  }

  const entryKeyMap = new Map<string, string>()
  startNodes.forEach((node) => {
    const nodeIncoming = incoming.get(node.id) || []
    const entryKey = String(node.data.config?.entryKey || '').trim()
    if (nodeIncoming.length > 0) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          message: '开始节点不能存在入边。'
        })
      )
    }
    if (!entryKey) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          field: 'entryKey',
          message: '开始节点必须填写 entryKey。'
        })
      )
    } else if (entryKeyMap.has(entryKey)) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          field: 'entryKey',
          message: `开始节点 entryKey 不能重复：${entryKey}`
        })
      )
    } else {
      entryKeyMap.set(entryKey, node.id)
    }
  })

  nodes.forEach((node) => {
    const nodeIncoming = incoming.get(node.id) || []
    const nodeOutgoing = outgoing.get(node.id) || []

    if (!isStartNode(node) && nodeIncoming.length === 0) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          message: '除开始节点外，所有节点都必须可达。'
        })
      )
    }

    if (nodeIncoming.length > 1) {
      const allFromStarts = nodeIncoming.every((edge) => {
        const sourceNode = nodeMap.get(edge.source)
        return !!sourceNode && isStartNode(sourceNode)
      })
      if (!allFromStarts) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: node.id,
            message: '当前仅允许多个开始节点汇聚到同一后续节点。'
          })
        )
      }
    }

    if (node.data.typeCode === 'end' && nodeOutgoing.length > 0) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          message: '结束节点不能再连出后续节点。'
        })
      )
    }

    if (node.data.typeCode === 'condition.branch') {
      const branches = new Set(nodeOutgoing.map((edge) => edge.data.branch || edge.sourcePort || ''))
      if (!branches.has('true') || !branches.has('false') || nodeOutgoing.length !== 2) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: node.id,
            message: '条件节点必须同时包含 true 和 false 两条分支。'
          })
        )
      }
    }

    if (node.data.typeCode === 'foreach') {
      if (nodeOutgoing.length !== 1) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: node.id,
            message: 'foreach 节点当前只支持一个后继节点。'
          })
        )
      }
    }
  })

  const visiting = new Set<string>()
  const visited = new Set<string>()
  let cycleFound = false

  const walkCycle = (nodeId: string) => {
    if (cycleFound) return
    if (visiting.has(nodeId)) {
      cycleFound = true
      issues.push(createIssue({ scope: 'graph', level: 'error', message: '当前版本仅支持 DAG，不允许出现环。' }))
      return
    }
    if (visited.has(nodeId)) return
    visiting.add(nodeId)
    adjacency.get(nodeId)?.forEach((nextId) => walkCycle(nextId))
    visiting.delete(nodeId)
    visited.add(nodeId)
  }

  startNodes.forEach((node) => walkCycle(node.id))
  nodes.forEach((node) => walkCycle(node.id))

  const reachable = new Set<string>()
  const markReachable = (nodeId: string) => {
    if (reachable.has(nodeId)) return
    reachable.add(nodeId)
    adjacency.get(nodeId)?.forEach((nextId) => markReachable(nextId))
  }
  startNodes.forEach((node) => markReachable(node.id))

  nodes.forEach((node) => {
    if (!reachable.has(node.id)) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          message: '存在孤立节点或不可达节点。'
        })
      )
    }
  })

  const ensureForeachBranch = (foreachNodeId: string) => {
    const firstTarget = outgoing.get(foreachNodeId)?.[0]?.target
    if (!firstTarget) return

    const stack = [firstTarget]
    const seen = new Set<string>()

    while (stack.length) {
      const currentId = stack.pop() as string
      if (seen.has(currentId)) continue
      seen.add(currentId)

      const currentNode = nodeMap.get(currentId)
      if (!currentNode) continue

      if (currentNode.data.typeCode === 'foreach') {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: foreachNodeId,
            message: 'foreach 节点暂不支持嵌套 foreach。'
          })
        )
        return
      }

      const nextEdges = outgoing.get(currentId) || []
      if (!nextEdges.length && currentNode.data.typeCode !== 'end') {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: foreachNodeId,
            message: 'foreach 分支必须最终由 end 节点结束。'
          })
        )
        return
      }

      nextEdges.forEach((edge) => stack.push(edge.target))
    }
  }

  nodes
    .filter((node) => node.data.typeCode === 'foreach')
    .forEach((node) => ensureForeachBranch(node.id))

  startNodes
    .filter((node) => node.data.typeCode === 'start.event')
    .forEach((node) => {
      const eventType = String(node.data.config?.eventType || '').trim()
      if (!eventType) return
      const stack = [...(adjacency.get(node.id) || [])]
      const seen = new Set<string>()
      while (stack.length) {
        const currentId = stack.pop() as string
        if (seen.has(currentId)) continue
        seen.add(currentId)
        const currentNode = nodeMap.get(currentId)
        if (!currentNode) continue
        if (
          currentNode.data.typeCode === 'event.publish' &&
          String(currentNode.data.config?.eventType || '').trim() === eventType
        ) {
          issues.push(
            createIssue({
              scope: 'node',
              level: 'error',
              nodeId: node.id,
              message: `事件开始节点不能在其后续链路中再次发布同名事件：${eventType}`
            })
          )
          break
        }
        ;(adjacency.get(currentId) || []).forEach((nextId) => stack.push(nextId))
      }
    })

  return dedupeIssues(issues)
}

export function validateNodeFormDraft(
  node: WorkflowDomainNode | null,
  form: WorkflowNodeFormModel | null,
  taskDefinitions: TaskDefinitionItem[]
): WorkflowDraftValidationResult {
  if (!node || !form) return { valid: true, errors: [] }

  const errors: string[] = []
  const config = form.config || {}

  if (!form.label.trim()) {
    errors.push('节点名称不能为空。')
  }

  switch (form.kind) {
    case 'start': {
      const entryKey = String(config.entryKey || '').trim()
      if (!entryKey) errors.push('开始节点必须填写 entryKey。')
      if (!/^[a-z0-9._-]{1,64}$/i.test(entryKey)) {
        errors.push('entryKey 仅支持字母、数字、点、下划线和中横线，长度不超过 64。')
      }
      if (form.typeCode === 'start.event' && !String(config.eventType || '').trim()) {
        errors.push('事件开始节点必须填写事件类型。')
      }
      if (form.typeCode === 'start.schedule') {
        const scheduleType = String(config.scheduleType || '').trim()
        if (!scheduleType) {
          errors.push('定时开始节点必须选择计划类型。')
        } else if (scheduleType === 'cron' && !String(config.cronExpression || '').trim()) {
          errors.push('Cron 计划必须填写 cronExpression。')
        } else if (scheduleType === 'interval') {
          if (Number(config.value || 0) <= 0) errors.push('间隔计划的 value 必须大于 0。')
          if (!['seconds', 'minutes', 'hours', 'days'].includes(String(config.unit || '').trim())) {
            errors.push('间隔计划的 unit 不正确。')
          }
        } else if (scheduleType === 'once' && !String(config.runAt || '').trim()) {
          errors.push('单次计划必须填写 runAt。')
        }
      }
      break
    }

    case 'task':
      if (!String(config.taskDefinitionCode || '').trim()) {
        errors.push('请选择任务定义。')
      } else if (!taskDefinitions.some((item) => item.code === config.taskDefinitionCode)) {
        errors.push('所选任务定义不存在。')
      }
      break

    case 'condition':
      if (!String(config.path || '').trim()) errors.push('条件节点必须填写字段路径。')
      if (!String(config.operator || '').trim()) errors.push('条件节点必须选择比较运算。')
      if (String(config.operator || '') !== 'truthy' && !String(config.value ?? '').trim()) {
        errors.push('条件节点必须填写比较值。')
      }
      break

    case 'foreach':
      if (!String(config.itemsPath || '').trim()) errors.push('foreach 节点必须填写数组路径。')
      break

    case 'notify':
      if (!Array.isArray(config.targets) || !config.targets.length) {
        errors.push('通知节点至少需要一个通知目标。')
      } else {
        config.targets.forEach((target: Record<string, any>, index: number) => {
          const targetType = String(target?.targetType || '').trim()
          const targetId = Number(target?.targetId)
          if (!['user', 'role'].includes(targetType)) {
            errors.push(`通知目标 ${index + 1} 的类型无效。`)
          }
          if (!Number.isInteger(targetId) || targetId <= 0) {
            errors.push(`通知目标 ${index + 1} 的 ID 必须为正整数。`)
          }
        })
      }
      if (!Array.isArray(config.channelTypes) || !config.channelTypes.length) {
        errors.push('通知节点至少需要一个通知渠道。')
      }
      if (!String(config.titleTemplate || '').trim()) errors.push('通知节点必须填写标题模板。')
      if (!String(config.contentTemplate || '').trim()) errors.push('通知节点必须填写内容模板。')
      if (!['markdown', 'plain_text'].includes(String(config.messageFormat || 'markdown').trim())) {
        errors.push('通知节点消息格式仅支持 markdown 或 plain_text。')
      }
      break

    case 'event':
      if (!String(config.eventType || '').trim()) errors.push('事件节点必须填写事件类型。')
      break

    case 'http':
      if (!String(config.url || '').trim()) errors.push('HTTP 节点必须填写请求地址。')
      break

    case 'delay':
      if (Number(config.durationMs || 0) < 0) errors.push('延迟时长不能小于 0。')
      break

    default:
      break
  }

  return {
    valid: errors.length === 0,
    errors
  }
}

export function validateEdgeFormDraft(
  edge: WorkflowDomainEdge | null,
  form: WorkflowEdgeFormModel | null
): WorkflowDraftValidationResult {
  if (!edge || !form) return { valid: true, errors: [] }
  const errors: string[] = []
  if (form.source === form.target) {
    errors.push('不允许自环连接。')
  }
  return {
    valid: errors.length === 0,
    errors
  }
}
