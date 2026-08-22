/** 工作流编辑器辅助模块：workflow-editor.validator。 */
import type { WorkflowAgentOption } from '@/api/scheduler'
import type {
  WorkflowDomainEdge,
  WorkflowDomainGraphModel,
  WorkflowDomainNode,
  WorkflowDraftValidationResult,
  WorkflowEditorIssue,
  WorkflowEdgeFormModel,
  WorkflowNodeFormModel
} from './types'
import { getNodeBranches, getNodeGraphKind } from './node-registry'

const createIssue = (
  partial: Omit<WorkflowEditorIssue, 'id' | 'source'> & { id?: string }
): WorkflowEditorIssue => ({
  id: partial.id || `client-${Math.random().toString(36).slice(2, 10)}`,
  source: 'client',
  ...partial
})

const dedupeIssues = (issues: WorkflowEditorIssue[]) => {
  const seen = new Set<string>()
  return issues.filter((issue) => {
    const key = [
      issue.scope,
      issue.level,
      issue.nodeId || '',
      issue.edgeId || '',
      issue.field || '',
      issue.message
    ].join(':')
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

const isStartNode = (node: WorkflowDomainNode) => node.data.typeCode.startsWith('start.')

/** foreach 的 NEXT 连线：循环全部跑完后才走的后继边（BODY 连的才是循环体）。 */
const isForeachNextEdge = (edge: WorkflowDomainEdge) =>
  edge.data.branch === 'next' || edge.sourcePort === 'next'

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
    issues.push(
      createIssue({ scope: 'graph', level: 'error', message: '工作流至少需要一个开始节点。' })
    )
  }
  if (!endNodes.length) {
    issues.push(
      createIssue({ scope: 'graph', level: 'error', message: '工作流至少需要一个结束节点。' })
    )
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
          message: '开始节点的入口配置无效，请重新添加该节点。'
        })
      )
    } else if (entryKeyMap.has(entryKey)) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: node.id,
          field: 'entryKey',
          message: '开始节点的入口配置冲突，请重新添加其中一个节点。'
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

    // 多入边（汇聚 join）是被支持的：引擎会等所有活跃分支到齐再把该节点跑一次。
    // 这里不再限制入边来源。

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

    // 分支节点：出边必须恰好覆盖它声明的分支。分支清单来自后端注册表，
    // 多路 switch 的分支还会随节点配置变化，所以要把 config 一起传进去。
    if (getNodeGraphKind(node.data.typeCode) === 'branch') {
      const declared = getNodeBranches(node.data.typeCode, node.data.config)
      const seen = new Set(nodeOutgoing.map((edge) => edge.data.branch || edge.sourcePort || ''))
      const missing = declared.filter((branch) => !seen.has(branch))
      const unknown = Array.from(seen).filter((branch) => !declared.includes(branch))
      if (declared.length < 2) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: node.id,
            message: '分支节点至少需要配置两个分支。'
          })
        )
      } else if (missing.length || unknown.length || nodeOutgoing.length !== declared.length) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: node.id,
            message: `分支节点必须为每个分支各连一条线：${declared.join(' / ')}`
          })
        )
      }
    }

    if (node.data.typeCode === 'foreach') {
      const bodyEdges = nodeOutgoing.filter((edge) => !isForeachNextEdge(edge))
      const nextEdges = nodeOutgoing.filter(isForeachNextEdge)
      if (bodyEdges.length !== 1) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: node.id,
            message: 'foreach 节点必须且只能有一条 BODY 循环体连线。'
          })
        )
      }
      if (nextEdges.length > 1) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: node.id,
            message: 'foreach 节点最多只能有一条 NEXT 循环后继连线。'
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
      issues.push(
        createIssue({
          scope: 'graph',
          level: 'error',
          message: '当前版本仅支持 DAG，不允许出现环。'
        })
      )
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

  // foreach 循环体必须是「封闭」的一段子图：外面的连线进不来、里面的连线出不去。
  // 引擎把循环体当独立子图按元素反复跑：外部连进来会让汇聚(join)语义和循环语义打架；
  // 内部连出去则会让那个体外节点永远等不到入边结论，主流程会安静地少跑一段。
  // 想在「循环跑完之后」继续，用 foreach 节点的 NEXT 连线。
  const ensureForeachBody = (foreachNodeId: string) => {
    const nodeOutgoing = outgoing.get(foreachNodeId) || []
    const bodyEntry = nodeOutgoing.find((edge) => !isForeachNextEdge(edge))?.target
    if (!bodyEntry) return

    const collect = (entries: string[]) => {
      const seen = new Set<string>()
      const stack = [...entries]
      while (stack.length) {
        const currentId = stack.pop() as string
        if (!currentId || seen.has(currentId)) continue
        seen.add(currentId)
        ;(adjacency.get(currentId) || []).forEach((nextId) => stack.push(nextId))
      }
      return seen
    }

    // 循环体 = 从 BODY 入口可达 − 从 NEXT 后继可达（两边汇聚到同一收尾节点时，该节点归主流程）。
    const bodySet = collect([bodyEntry])
    collect(nodeOutgoing.filter(isForeachNextEdge).map((edge) => edge.target)).forEach((id) =>
      bodySet.delete(id)
    )

    if (!bodySet.size) {
      issues.push(
        createIssue({
          scope: 'node',
          level: 'error',
          nodeId: foreachNodeId,
          message: 'foreach 循环体不能为空。'
        })
      )
      return
    }

    for (const nodeId of bodySet) {
      if (nodeMap.get(nodeId)?.data.typeCode === 'foreach') {
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
    }

    for (const edge of edges) {
      const sourceInBody = bodySet.has(edge.source)
      const targetInBody = bodySet.has(edge.target)
      if (!sourceInBody && targetInBody && edge.source !== foreachNodeId) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: foreachNodeId,
            message: 'foreach 循环体不允许被循环外的节点连入。'
          })
        )
        return
      }
      if (sourceInBody && !targetInBody) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: foreachNodeId,
            message: 'foreach 循环体不能连回循环外；要在遍历之后继续，请改用 foreach 的 NEXT 连线。'
          })
        )
        return
      }
    }
  }

  nodes
    .filter((node) => node.data.typeCode === 'foreach')
    .forEach((node) => ensureForeachBody(node.id))

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

  nodes
    .filter((node) => node.data.typeCode === 'strategy.evaluate')
    .forEach((strategyNode) => {
      const instrumentId = String(strategyNode.data.config?.instrumentId || '').trim()
      const interval = String(strategyNode.data.config?.interval || '').trim()
      const matchingEntry = startNodes.find((startNode) => {
        if (
          startNode.data.typeCode !== 'start.event' ||
          String(startNode.data.config?.eventType || '').trim() !== 'market.candle.closed'
        ) {
          return false
        }
        const filters = Array.isArray(startNode.data.config?.filters)
          ? startNode.data.config.filters
          : []
        const matchesInstrument = filters.some(
          (filter: Record<string, any>) =>
            filter?.path === 'instrumentId' && String(filter?.equals || '') === instrumentId
        )
        const matchesInterval = filters.some(
          (filter: Record<string, any>) =>
            filter?.path === 'interval' && String(filter?.equals || '') === interval
        )
        if (!matchesInstrument || !matchesInterval) return false

        const queue = [startNode.id]
        const visited = new Set<string>()
        while (queue.length) {
          const current = queue.shift() as string
          if (current === strategyNode.id) return true
          if (visited.has(current)) continue
          visited.add(current)
          ;(adjacency.get(current) || []).forEach((next) => queue.push(next))
        }
        return false
      })
      if (!matchingEntry) {
        issues.push(
          createIssue({
            scope: 'node',
            level: 'error',
            nodeId: strategyNode.id,
            message: '策略节点必须连接匹配币种和周期的 K 线事件入口。'
          })
        )
      }
    })

  return dedupeIssues(issues)
}

export function validateNodeFormDraft(
  node: WorkflowDomainNode | null,
  form: WorkflowNodeFormModel | null,
  agentOptions: WorkflowAgentOption[] = []
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
      if (!entryKey) errors.push('开始节点的入口配置无效，请重新添加该节点。')
      if (!/^[a-z0-9._-]{1,64}$/i.test(entryKey)) {
        errors.push('开始节点的入口配置无效，请重新添加该节点。')
      }
      if (form.typeCode === 'start.event' && !String(config.eventType || '').trim()) {
        errors.push('事件开始节点必须填写事件类型。')
      }
      if (form.typeCode === 'start.schedule') {
        const scheduleType = String(config.scheduleType || '').trim()
        if (!scheduleType) {
          errors.push('定时开始节点必须选择计划类型。')
        } else if (scheduleType === 'cron' && !String(config.cronExpression || '').trim()) {
          errors.push('Cron 计划必须填写表达式。')
        } else if (scheduleType === 'interval') {
          if (Number(config.value || 0) <= 0) errors.push('间隔数值必须大于 0。')
          if (!['seconds', 'minutes', 'hours', 'days'].includes(String(config.unit || '').trim())) {
            errors.push('间隔计划的 unit 不正确。')
          }
        } else if (scheduleType === 'once' && !String(config.runAt || '').trim()) {
          errors.push('单次计划必须填写 runAt。')
        }
      }
      break
    }

    case 'agent': {
      const agentCode = String(config.agentCode || '').trim()
      if (!agentCode) errors.push('请选择智能体。')
      const agent = agentOptions.find((item) => item.code === agentCode)
      if (agentCode && !agent) errors.push('所选智能体不存在或已停用。')
      const analyze = Boolean(config.analyze)
      if (analyze && agent && !agent.supportsAnalyze) {
        errors.push('该智能体的数据源不支持结构化分析，请改用自定义提示词。')
      }
      if (!analyze && !String(config.promptTemplate || '').trim()) {
        errors.push('请填写提示词，或改用数据源的结构化分析模板。')
      }
      if (agent?.requiresRefId && !String(config.refIdPath || '').trim()) {
        errors.push('该智能体需要关联数据，请填写关联数据 id 路径。')
      }
      break
    }

    case 'condition': {
      // 多条件模式下顶层的单条件不生效，两套校验分开做。
      const clauses = Array.isArray(config.conditions) ? config.conditions : []
      if (clauses.length) {
        clauses.forEach((clause: Record<string, any>, index: number) => {
          if (!String(clause?.path || '').trim()) {
            errors.push(`条件 ${index + 1} 必须填写字段路径。`)
          }
          if (!String(clause?.operator || '').trim()) {
            errors.push(`条件 ${index + 1} 必须选择比较运算。`)
          }
          if (
            String(clause?.operator || '') !== 'truthy' &&
            !String(clause?.value ?? '').trim() &&
            !String(clause?.valuePath ?? '').trim()
          ) {
            errors.push(`条件 ${index + 1} 必须填写比较值或比较值路径。`)
          }
        })
        break
      }
      if (!String(config.path || '').trim())
        errors.push('条件节点必须填写字段路径，或改用多条件配置。')
      if (!String(config.operator || '').trim()) errors.push('条件节点必须选择比较运算。')
      // truthy 不需要比较值；比较值也可以改成从共享状态取（valuePath），二选一填一个即可。
      if (
        String(config.operator || '') !== 'truthy' &&
        !String(config.value ?? '').trim() &&
        !String(config.valuePath ?? '').trim()
      ) {
        errors.push('条件节点必须填写比较值或比较值路径。')
      }
      break
    }

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
      if (form.typeCode === 'strategy.evaluate') {
        if (!String(config.strategyVersionId || '').trim()) errors.push('请选择已发布策略版本。')
        if (!String(config.instrumentId || '').trim()) errors.push('请选择币种。')
        if (!['1m', '5m', '15m', '1h', '4h', '1d'].includes(String(config.interval || ''))) {
          errors.push('请选择有效的 K 线周期。')
        }
        if (!['paper', 'testnet', 'live'].includes(String(config.environment || 'paper'))) {
          errors.push('策略运行环境无效。')
        }
      }
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
