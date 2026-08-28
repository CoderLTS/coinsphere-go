/** 前端接口封装：scheduler。 */
import {
  applyWorkflowLifecycle,
  createWorkflow,
  createWorkflowRun,
  fetchWorkflow,
  fetchWorkflowRun,
  fetchWorkflowRuns,
  fetchWorkflowNodeDefinitions,
  fetchWorkflowRevision,
  fetchWorkflowRevisions,
  fetchWorkflows,
  saveWorkflowRevision,
  updateWorkflow,
  validateWorkflowGraph,
  type WorkflowRun,
  type WorkflowNodeLog,
  type WorkflowArtifact,
  type WorkflowRunEvent,
  type WorkflowDetail,
  type WorkflowGraph as CurrentWorkflowGraph,
  type WorkflowInputBinding,
  type WorkflowItem,
  type WorkflowNodeDefinition,
  type WorkflowRevision,
  type WorkflowSecretChange
} from './workflows'

export type WorkflowStartType = 'manual' | 'schedule' | 'event' | 'webhook'
export type WorkflowTriggerType = WorkflowStartType
export type WorkflowScheduleType = 'cron' | 'interval' | 'once'
export type WorkflowExecutionStatus =
  | 'queued'
  | 'running'
  | 'retry_waiting'
  | 'success'
  | 'failed'
  | 'canceled'
export type WorkflowTerminalStatus = 'success' | 'failed' | 'canceled'

/** 工作流可编排的智能体选项。requiresRefId / supportsAnalyze 决定节点表单显示哪些输入项。 */
export interface WorkflowAgentOption {
  code: string
  label: string
  description: string
  dataSourceType: string
  dataSourceLabel?: string
  requiresRefId: boolean
  supportsAnalyze: boolean
}

/** 后端声明的节点"图语义"：决定这个节点在画布上怎么连线（端口、分支、校验）。 */
export type WorkflowNodeGraphKind = 'plain' | 'start' | 'branch' | 'loop' | 'terminal'

export interface WorkflowNodeDefinitionItem {
  typeCode: string
  label: string
  version?: string
  description?: string
  configSchema: Record<string, any>
  uiSchema?: Record<string, any>
  secretFields?: { name: string; title: string; required: boolean }[]
  /** 图语义分类，后端注册表下发；老后端没有这个字段时按 plain 处理。 */
  kind?: WorkflowNodeGraphKind
  /** 分支节点必须存在的分支键，如 ['true','false']。 */
  branches?: string[]
  /**
   * 非空表示分支不是固定的，而是从节点 config 的这个数组字段逐项取 key（多路 switch 用）。
   * 与 extraBranches 一起，决定这个节点实例该有几个出口。
   */
  branchesConfigKey?: string
  /** 动态分支之外总是存在的分支（如 switch 的 default）。 */
  extraBranches?: string[]
}

export interface WorkflowNodeItem {
  id: string
  type: string
  label: string
  config: Record<string, any>
  position?: {
    x: number
    y: number
  }
}

export interface WorkflowEdgeItem {
  id: string
  source: string
  target: string
  branch?: string
  label?: string
}

export interface WorkflowGraph {
  nodes: WorkflowNodeItem[]
  edges: WorkflowEdgeItem[]
}

export interface WorkflowDefinitionVersionItem {
  id: number
  version: number
  displayName: string
  isLatest: boolean
  isBuiltin: boolean
  isActive: boolean
  executionCount: number
  createdBy?: number | null
  createdAt: string
}

export interface WorkflowDefinitionItem {
  id: number
  code: string
  version: number
  displayName: string
  description: string
  graph: WorkflowGraph
  isLatest: boolean
  isBuiltin: boolean
  isActive: boolean
  isWorkflowActive?: boolean
  workflowStatus: WorkflowItem['status']
  activeDefinitionId?: number | null
  activeVersion?: number | null
  executionCount: number
  createdBy?: number | null
  createdAt: string
  versions?: WorkflowDefinitionVersionItem[]
}

export interface WorkflowDefinitionUpsertPayload {
  code?: string
  displayName: string
  description: string
  graph: WorkflowGraph
}

export interface WorkflowDefinitionValidationIssue {
  scope: 'graph' | 'node' | 'edge'
  level: 'error' | 'warning'
  message: string
  nodeId?: string
  edgeId?: string
  field?: string
}

export interface WorkflowDefinitionValidationResult {
  valid: boolean
  issues: WorkflowDefinitionValidationIssue[]
}

export interface WorkflowRuntimeEntryItem {
  id: number
  definitionId: number
  startNodeId: string
  entryKey: string
  entryName: string
  startType: WorkflowStartType
  isEnabled: boolean
  registrationStatus: 'ready' | 'registered' | 'failed' | 'disabled' | string
  nextRunAt: string
  lastTriggeredAt: string
  lastErrorMessage: string
  secretHint: string
  secretRotatedAt: string
}

export interface WorkflowRuntimeStateItem {
  workflowCode: string
  runtimeStateId: number | null
  activeDefinitionId: number | null
  activatedAt: string
  entries: WorkflowRuntimeEntryItem[]
}

export interface WorkflowRuntimeSecretRotationResult {
  entryKey: string
  secret: string
  secretHint: string
}

export interface WorkflowExecutionNodeAttempt {
  id: number
  nodeId: string
  nodeName: string
  attempt: number
  loopIteration: number
  status: WorkflowExecutionStatus | string
  statusLabel: string
  startedAt: string
  finishedAt: string
  durationMs: number
  inputSummary: Record<string, unknown>
  outputSummary: Record<string, unknown>
  logs: WorkflowNodeLog[]
  error: { summary: string; category: string; retryable: boolean } | null
}

export interface WorkflowExecutionItem {
  id: number
  workflowDefinitionId: number
  workflowDefinitionVersion: number
  workflowDefinitionName: string
  entryName: string
  triggerType: WorkflowTriggerType | string
  triggeredBy?: number | null
  status: WorkflowExecutionStatus | string
  statusLabel: string
  triggerLabel: string
  queuedAt: string
  claimedAt: string
  startedAt: string
  finishedAt: string
  lastHeartbeatAt: string
  attemptCount: number
  maxAttempts: number
  durationMs: number
  error: { summary: string; category: string; retryable: boolean } | null
}

export interface WorkflowExecutionDetail extends WorkflowExecutionItem {
  graph: WorkflowGraph
  startNodeId: string
  nodeAttempts: WorkflowExecutionNodeAttempt[]
  logs: WorkflowNodeLog[]
  event?: WorkflowRunEvent
  resultSummary: Record<string, unknown>
  artifacts: WorkflowArtifact[]
}

export type WorkflowExecutionList = Api.Common.PaginatedResponse<WorkflowExecutionItem>

export interface WorkflowExecutionQueryParams {
  cursor?: string
  limit?: number
  workflowDefinitionCode?: string
  triggerType?: WorkflowTriggerType | string
  status?: WorkflowExecutionStatus | string
  from?: string
  to?: string
  keyword?: string
}

export interface WorkflowManualRunPayload {
  startEntryKeys: string[]
  inputs?: Record<string, any>
}

export interface RunWorkflowDefinitionResponse {
  executions: WorkflowExecutionItem[]
}

export interface WorkflowOverviewDefinitionItem {
  workflowDefinitionId: number
  workflowDefinitionCode: string
  workflowDefinitionVersion: number
  workflowDefinitionName: string
  isActive: boolean
  executionCount: number
}

export interface WorkflowOverview {
  stats: {
    definitionCount: number
    activeDefinitionCount: number
    executionCount: number
    latestExecutedAt: string
    pendingCount: number
    queuedCount: number
    runningCount: number
    retryWaitingCount: number
    oldestPendingAgeMs: number
    staleRunningCount: number
  }
  definitions: WorkflowOverviewDefinitionItem[]
}

const versionIDFactor = 1_000_000_000
const currentNodeDefinitions = new Map<string, WorkflowNodeDefinition>()
const nodeLabels: Record<string, string> = {
  'core.manual': '手动开始',
  'core.schedule': '定时开始',
  'core.event': '事件开始',
  'core.constant': '常量',
  'core.end': '结束',
  'core.human_approval': '人工审批',
  'core.loop': '循环',
  'official.connector.http': 'HTTP 请求',
  'official.connector.webhook': 'Webhook 触发',
  'official.connector.websocket': 'WebSocket 触发',
  'official.ai.model_call': 'AI 模型调用',
  'official.quant.binance_candles': 'Binance K 线采集',
  'official.quant.evaluate': '量化策略评估',
  'official.quant.backtest': '量化策略回测',
  'official.quant.signal': '量化信号',
  'official.quant.paper_execute': 'Paper 执行',
  'official.notification.in_app': '站内通知'
}

const schemaTitleLabels: Record<string, string> = {
  Value: '值',
  'Interval (seconds)': '执行间隔（秒）',
  'Event types': '事件类型',
  Source: '事件来源',
  Subject: '事件主题',
  Result: '执行结果',
  'Decision mode': '决策方式',
  'Task type': '任务类型',
  Prompt: '提示内容',
  'Expires after (seconds)': '有效时间（秒）',
  'Business key': '业务标识',
  'Maximum iterations': '最大循环次数',
  'Absolute timeout (seconds)': '总超时时间（秒）',
  'Boolean exit condition': '退出条件',
  'Embedded DAG': '内嵌流程',
  URL: '请求地址',
  Method: '请求方法',
  'Timeout (seconds)': '超时时间（秒）',
  'Use Authorization secret': '使用访问凭据',
  Authorization: '访问凭据',
  'JSON body': '请求内容',
  'CloudEvent type': '事件类型',
  'Webhook secret': 'Webhook 密钥',
  'WebSocket URL': 'WebSocket 连接地址',
  'Event ID field': '事件编号字段',
  'Partition field': '分区字段',
  'OpenAI-compatible endpoint': 'OpenAI 兼容接口地址',
  Model: '模型',
  'API key': '接口密钥',
  'Structured data': '结构化数据',
  Title: '通知标题',
  Market: '市场类型',
  Instrument: '交易对',
  Interval: 'K 线周期',
  Strategy: '量化策略',
  Parameters: '策略参数',
  'Start (UTC)': '开始时间（UTC）',
  'End (UTC)': '结束时间（UTC）',
  'Initial capital': '初始资金',
  'Fee rate': '手续费率',
  'Slippage rate': '滑点率',
  'Initial balance': '初始余额',
  'Max total notional': '最大总名义金额',
  'Max instrument notional': '单交易对最大名义金额',
  'Max operation notional': '单次操作最大名义金额',
  'Max daily loss': '单日最大亏损',
  'Max drawdown ratio': '最大回撤比例',
  'Max quote age': '行情最大延迟（秒）'
}

const schemaFieldLabels: Record<string, string> = {
  eventTime: '事件时间',
  strategyId: '策略标识',
  strategyVersion: '策略版本',
  target: '目标仓位',
  evaluatedAt: '评估时间',
  businessKey: '业务标识',
  signalId: '信号编号',
  decisionTaskId: '审批任务编号',
  decisionStatus: '审批状态',
  subjectKey: '通知对象标识',
  message: '通知内容'
}

const schemaEnumLabels: Record<string, Record<string, string>> = {
  decisionMode: { human: '人工确认', auto: '自动执行' },
  market: { spot: '现货', usdm: 'U 本位合约' },
  decisionStatus: {
    approved: '已批准',
    rejected: '已拒绝',
    expired: '已过期',
    superseded: '已替代'
  }
}
const legacyNodeTypes: Record<string, string> = {
  'core.manual': 'start.manual',
  'core.schedule': 'start.schedule',
  'core.event': 'start.event',
  'official.connector.webhook': 'start.webhook',
  'core.end': 'end'
}

const encodeVersionID = (workflowID: number, revisionID: number) =>
  workflowID * versionIDFactor + revisionID

const decodeDefinitionID = (definitionID: number) =>
  definitionID >= versionIDFactor
    ? {
        workflowID: Math.floor(definitionID / versionIDFactor),
        revisionID: definitionID % versionIDFactor
      }
    : { workflowID: definitionID, revisionID: 0 }

const graphKind = (definition: WorkflowNodeDefinition): WorkflowNodeGraphKind => {
  if (definition.kind === 'trigger') return 'start'
  if (definition.type === 'core.end' || definition.type === 'core.loop_end') return 'terminal'
  return 'plain'
}

const inputProperties = (definition?: WorkflowNodeDefinition) =>
  (definition?.inputSchema?.properties || {}) as Record<string, Record<string, unknown>>

const localizeSchemaProperties = (properties: Record<string, Record<string, unknown>>) =>
  Object.fromEntries(
    Object.entries(properties).map(([key, schema]) => {
      const title = String(schema.title || '')
      const enumLabels = (schema.enum as unknown[] | undefined)?.map(
        (value) => schemaEnumLabels[key]?.[String(value)] || String(value)
      )
      return [
        key,
        {
          ...schema,
          title: schemaTitleLabels[title] || schemaFieldLabels[key] || title || key,
          ...(enumLabels ? { enumLabels } : {})
        }
      ]
    })
  )

const legacyConfigSchema = (definition: WorkflowNodeDefinition) => {
  const config = definition.configSchema || {}
  const inputs = inputProperties(definition)
  return {
    ...config,
    properties: localizeSchemaProperties({
      ...((config.properties || {}) as Record<string, Record<string, unknown>>),
      ...inputs
    })
  }
}

const toLegacyGraph = (graph: CurrentWorkflowGraph): WorkflowGraph => ({
  nodes: (graph.nodes || []).map((node) => {
    const definition = currentNodeDefinitions.get(node.nodeType)
    const bindings = node.inputBindings || {}
    const literalInputs = Object.fromEntries(
      Object.entries(bindings)
        .filter(([, binding]) => binding.kind === 'literal')
        .map(([name, binding]) => [name, binding.value])
    )
    const config: Record<string, unknown> = { ...node.config, ...literalInputs }
    if (definition?.kind === 'trigger') {
      config.entryKey = node.nodeInstanceId
      config.displayName = nodeLabels[node.nodeType] || node.nodeType
      config.inputBindings = {}
    }
    if (node.nodeType === 'core.schedule') {
      config.scheduleType = 'interval'
      config.value = Number(config.everySeconds || 60)
      config.unit = 'seconds'
    }
    if (node.nodeType === 'core.event') {
      config.eventType = Array.isArray(config.types) ? config.types[0] || '' : ''
    }
    return {
      id: node.nodeInstanceId,
      type: legacyNodeTypes[node.nodeType] || node.nodeType,
      label: nodeLabels[node.nodeType] || definition?.title || node.nodeType,
      config: {
        ...config,
        __nodeType: node.nodeType,
        __nodeVersion: node.nodeVersion,
        __inputBindings: bindings
      },
      position: node.position
    }
  }),
  edges: (graph.edges || []).map((edge) => ({
    id: edge.edgeId,
    source: edge.sourceNodeInstanceId,
    target: edge.targetNodeInstanceId,
    branch: edge.sourcePort === 'out' ? '' : edge.sourcePort,
    label: edge.condition || ''
  }))
})

const toCurrentRevision = (graph: WorkflowGraph) => {
  const secretChanges: WorkflowSecretChange[] = []
  const currentGraph: CurrentWorkflowGraph = {
    schemaVersion: 1,
    nodes: (graph.nodes || []).map((node) => {
      const rawConfig = { ...(node.config || {}) }
      const type = String(
        rawConfig.__nodeType ||
          (
            {
              'start.manual': 'core.manual',
              'start.schedule': 'core.schedule',
              'start.event': 'core.event',
              'start.webhook': 'official.connector.webhook',
              end: 'core.end'
            } as Record<string, string>
          )[node.type] ||
          node.type
      )
      const definition = currentNodeDefinitions.get(type)
      const nodeVersion = String(rawConfig.__nodeVersion || definition?.version || '1.0.0')
      const bindings = {
        ...((rawConfig.__inputBindings || {}) as Record<string, WorkflowInputBinding>)
      }
      delete rawConfig.__nodeType
      delete rawConfig.__nodeVersion
      delete rawConfig.__inputBindings
      definition?.secretFields.forEach((field) => {
        const value = rawConfig[field.name]
        delete rawConfig[field.name]
        if (typeof value === 'string' && value.trim()) {
          secretChanges.push({ nodeInstanceId: node.id, field: field.name, value })
        }
      })
      if (definition?.kind === 'trigger') {
        delete rawConfig.entryKey
        delete rawConfig.displayName
        delete rawConfig.inputBindings
      }
      if (type === 'core.schedule') {
        const multiplier =
          { seconds: 1, minutes: 60, hours: 3600, days: 86400 }[
            String(rawConfig.unit || 'seconds')
          ] || 1
        rawConfig.everySeconds =
          Math.max(1, Number(rawConfig.value || rawConfig.everySeconds || 60)) * multiplier
        for (const key of [
          'entryKey',
          'displayName',
          'inputBindings',
          'scheduleType',
          'value',
          'unit',
          'cronExpression',
          'runAt'
        ])
          delete rawConfig[key]
      }
      if (type === 'core.event') {
        const eventType = String(rawConfig.eventType || '').trim()
        if (eventType) rawConfig.types = [eventType]
        for (const key of ['entryKey', 'displayName', 'inputBindings', 'eventType', 'filters'])
          delete rawConfig[key]
      }
      Object.keys(inputProperties(definition)).forEach((name) => {
        if (!(name in rawConfig)) return
        bindings[name] = { kind: 'literal', value: rawConfig[name] }
        delete rawConfig[name]
      })
      return {
        nodeInstanceId: node.id,
        nodeType: type,
        nodeVersion,
        config: rawConfig,
        ...(Object.keys(bindings).length ? { inputBindings: bindings } : {}),
        position: node.position || { x: 0, y: 0 }
      }
    }),
    edges: (graph.edges || []).map((edge) => ({
      edgeId: edge.id,
      sourceNodeInstanceId: edge.source,
      sourcePort: edge.branch || 'out',
      targetNodeInstanceId: edge.target,
      targetPort: 'in',
      ...(edge.label ? { condition: edge.label } : {})
    }))
  }
  return { graph: currentGraph, secretChanges }
}

const runStatus = (status: WorkflowRun['status']): WorkflowExecutionStatus => {
  const mapped: Partial<Record<WorkflowRun['status'], WorkflowExecutionStatus>> = {
    succeeded: 'success',
    cancelled: 'canceled',
    retrying: 'retry_waiting',
    waiting: 'queued'
  }
  return mapped[status] || (status as WorkflowExecutionStatus)
}

const nodeAttemptStatus = (status: string): WorkflowExecutionStatus | string => {
  if (status === 'succeeded') return 'success'
  if (status === 'cancelled') return 'canceled'
  return status
}

const statusLabel = (status: WorkflowExecutionStatus | string) =>
  ({
    queued: '排队中',
    running: '运行中',
    retry_waiting: '等待重试',
    success: '成功',
    failed: '失败',
    canceled: '已取消'
  })[status] || status

const triggerLabel = (trigger: string) =>
  ({ manual: '手动', schedule: '定时', event: '事件', stream: '流式', webhook: 'Webhook' })[
    trigger
  ] || trigger

const elapsed = (startedAt?: string, completedAt?: string) => {
  if (!startedAt || !completedAt) return 0
  return Math.max(0, Date.parse(completedAt) - Date.parse(startedAt)) || 0
}

const toExecution = (
  run: WorkflowRun,
  workflow: WorkflowItem,
  revision?: WorkflowRevision
): WorkflowExecutionItem => {
  const status = runStatus(run.status)
  return {
    id: run.id,
    workflowDefinitionId: workflow.id,
    workflowDefinitionVersion: revision?.revisionNumber || 1,
    workflowDefinitionName: workflow.name,
    entryName: workflow.mainTriggerNodeId,
    triggerType: run.triggerType,
    status,
    statusLabel: statusLabel(status),
    triggerLabel: triggerLabel(run.triggerType),
    queuedAt: run.triggeredAt,
    claimedAt: run.startedAt || '',
    startedAt: run.startedAt || '',
    finishedAt: run.completedAt || '',
    lastHeartbeatAt: run.startedAt || '',
    attemptCount: 1,
    maxAttempts: 3,
    durationMs: elapsed(run.startedAt, run.completedAt),
    error: run.errorCategory
      ? {
          summary: run.errorMessage || run.errorCategory,
          category: run.errorCategory,
          retryable: false
        }
      : null
  }
}

const loadDefinition = async (definitionID: number): Promise<WorkflowDefinitionItem> => {
  const { workflowID, revisionID } = decodeDefinitionID(definitionID)
  const [workflow, revisionResult, runResult] = await Promise.all([
    fetchWorkflow(workflowID),
    fetchWorkflowRevisions(workflowID),
    fetchWorkflowRuns(workflowID)
  ])
  const revisions = [...revisionResult.items].sort((a, b) => b.revisionNumber - a.revisionNumber)
  const selected =
    revisions.find((item) => item.id === revisionID) ||
    revisions.find((item) => item.id === workflow.activeRevisionId) ||
    revisions[0]
  if (!selected) throw new Error('工作流没有可编辑的修订版本')
  const counts = new Map<number, number>()
  runResult.records.forEach((run) =>
    counts.set(run.revisionId, (counts.get(run.revisionId) || 0) + 1)
  )
  const activeVersion = revisions.find(
    (item) => item.id === workflow.activeRevisionId
  )?.revisionNumber
  return {
    id: definitionID,
    code: String(workflow.id),
    version: selected.revisionNumber,
    displayName: workflow.name,
    description: workflow.description,
    graph: toLegacyGraph(selected.graph),
    isLatest: selected.id === revisions[0]?.id,
    isBuiltin: false,
    isActive: workflow.status === 'active' && selected.id === workflow.activeRevisionId,
    isWorkflowActive: workflow.status === 'active',
    workflowStatus: workflow.status,
    activeDefinitionId: encodeVersionID(workflow.id, workflow.activeRevisionId),
    activeVersion: activeVersion || null,
    executionCount: runResult.total,
    createdBy: workflow.createdBy,
    createdAt: selected.createdAt,
    versions: revisions.map((revision) => ({
      id: encodeVersionID(workflow.id, revision.id),
      version: revision.revisionNumber,
      displayName: workflow.name,
      isLatest: revision.id === revisions[0]?.id,
      isBuiltin: false,
      isActive: workflow.status === 'active' && revision.id === workflow.activeRevisionId,
      executionCount: counts.get(revision.id) || 0,
      createdBy: revision.createdBy,
      createdAt: revision.createdAt
    }))
  }
}

export async function fetchNodeDefinitions() {
  const result = await fetchWorkflowNodeDefinitions()
  result.items.forEach((item) => currentNodeDefinitions.set(item.type, item))
  return result.items
    .filter((item) => item.available)
    .map((item) => ({
      typeCode: legacyNodeTypes[item.type] || item.type,
      label: nodeLabels[item.type] || item.title,
      version: item.version,
      description: item.description,
      configSchema: legacyConfigSchema(item),
      uiSchema: item.uiSchema,
      secretFields: item.secretFields,
      kind: graphKind(item)
    }))
}

export async function fetchWorkflowDefinitionList() {
  const { items } = await fetchWorkflows()
  return Promise.all(items.map((item) => loadDefinition(item.id)))
}

export const fetchWorkflowDefinitionDetail = (definitionID: number) => loadDefinition(definitionID)

export async function fetchCreateWorkflowDefinition(params: WorkflowDefinitionUpsertPayload) {
  const workflow = await createWorkflow({
    name: params.displayName,
    description: params.description,
    templateKey: 'blank'
  })
  const revision = toCurrentRevision(params.graph)
  await saveWorkflowRevision(workflow.id, {
    expectedActiveRevisionId: workflow.activeRevisionId,
    graph: revision.graph,
    secretChanges: revision.secretChanges,
    resetStateNodeInstanceIds: []
  })
  return loadDefinition(workflow.id)
}

export interface WorkflowDefinitionSaveContext {
  workflow: WorkflowDetail
  resetStateNodeInstanceIds: string[]
}

export async function fetchWorkflowDefinitionSaveContext(
  definitionID: number,
  params: WorkflowDefinitionUpsertPayload
): Promise<WorkflowDefinitionSaveContext> {
  const { workflowID } = decodeDefinitionID(definitionID)
  const workflow = await fetchWorkflow(workflowID)
  const activeRevision = await fetchWorkflowRevision(workflowID, workflow.activeRevisionId)
  const nextVersions = new Map(
    toCurrentRevision(params.graph).graph.nodes.map((node) => [
      node.nodeInstanceId,
      { nodeType: node.nodeType, nodeVersion: node.nodeVersion }
    ])
  )
  const resetStateNodeInstanceIds = workflow.stateNodeInstanceIds.filter((nodeID) => {
    const previous = activeRevision.nodeVersions[nodeID]
    const next = nextVersions.get(nodeID)
    return (
      !previous ||
      !next ||
      previous.nodeType !== next.nodeType ||
      previous.nodeVersion !== next.nodeVersion
    )
  })
  return { workflow, resetStateNodeInstanceIds }
}

export async function fetchUpdateWorkflowDefinition(
  definitionID: number,
  params: WorkflowDefinitionUpsertPayload,
  context: WorkflowDefinitionSaveContext
) {
  const { workflowID } = decodeDefinitionID(definitionID)
  const { workflow } = context
  if (workflow.name !== params.displayName || workflow.description !== params.description) {
    await updateWorkflow(workflowID, {
      name: params.displayName,
      description: params.description
    })
  }
  const revision = toCurrentRevision(params.graph)
  await saveWorkflowRevision(workflowID, {
    expectedActiveRevisionId: workflow.activeRevisionId,
    graph: revision.graph,
    secretChanges: revision.secretChanges,
    resetStateNodeInstanceIds: context.resetStateNodeInstanceIds
  })
  return loadDefinition(workflowID)
}

export const fetchValidateWorkflowDefinition = async (params: WorkflowDefinitionUpsertPayload) => {
  const result = await validateWorkflowGraph(toCurrentRevision(params.graph).graph)
  return result as WorkflowDefinitionValidationResult
}

export async function fetchActivateWorkflowDefinition(definitionID: number) {
  const { workflowID, revisionID } = decodeDefinitionID(definitionID)
  const workflow = await fetchWorkflow(workflowID)
  if (revisionID && revisionID !== workflow.activeRevisionId) {
    const revision = await fetchWorkflowRevision(workflowID, revisionID)
    await saveWorkflowRevision(workflowID, {
      expectedActiveRevisionId: workflow.activeRevisionId,
      graph: revision.graph,
      secretChanges: [],
      resetStateNodeInstanceIds: []
    })
  }
  await applyWorkflowLifecycle(workflowID, 'activate')
  return fetchWorkflowRuntime(workflowID)
}

export async function fetchDeactivateWorkflowDefinition(definitionID: number) {
  const { workflowID } = decodeDefinitionID(definitionID)
  await applyWorkflowLifecycle(workflowID, 'deactivate')
  return fetchWorkflowRuntime(workflowID)
}

export async function fetchDeleteWorkflowDefinition(definitionID: number) {
  void definitionID
  throw new Error('工作流不支持归档或删除')
}

const startType = (nodeType: string): WorkflowStartType => {
  if (nodeType === 'core.manual') return 'manual'
  if (nodeType === 'core.schedule') return 'schedule'
  return 'event'
}

export async function fetchWorkflowRuntime(
  definitionID: number
): Promise<WorkflowRuntimeStateItem> {
  const { workflowID } = decodeDefinitionID(definitionID)
  const [workflow, revision] = await Promise.all([
    fetchWorkflow(workflowID),
    fetchWorkflow(workflowID).then((item) =>
      fetchWorkflowRevision(workflowID, item.activeRevisionId)
    )
  ])
  const trigger = revision.graph.nodes.find(
    (node) => node.nodeInstanceId === workflow.mainTriggerNodeId
  )
  const activeID = encodeVersionID(workflowID, workflow.activeRevisionId)
  return {
    workflowCode: String(workflowID),
    runtimeStateId: workflowID,
    activeDefinitionId: workflow.status === 'active' ? activeID : null,
    activatedAt: workflow.updatedAt,
    entries: trigger
      ? [
          {
            id: workflowID,
            definitionId: activeID,
            startNodeId: trigger.nodeInstanceId,
            entryKey: trigger.nodeInstanceId,
            entryName: nodeLabels[trigger.nodeType] || trigger.nodeType,
            startType: startType(trigger.nodeType),
            isEnabled: workflow.status === 'active',
            registrationStatus: workflow.status === 'active' ? 'registered' : 'disabled',
            nextRunAt: '',
            lastTriggeredAt: '',
            lastErrorMessage: '',
            secretHint: '',
            secretRotatedAt: ''
          }
        ]
      : []
  }
}

export async function fetchUpdateWorkflowRuntimeEntryStatus(
  definitionID: number,
  _entryKey: string,
  isEnabled: boolean
) {
  const { workflowID } = decodeDefinitionID(definitionID)
  await applyWorkflowLifecycle(workflowID, isEnabled ? 'activate' : 'deactivate')
  return fetchWorkflowRuntime(workflowID)
}

export const fetchRotateWorkflowRuntimeEntrySecret = async (
  definitionID: number,
  entryKey: string
): Promise<WorkflowRuntimeSecretRotationResult> => {
  void definitionID
  void entryKey
  throw new Error('当前连接器的 Secret 在节点配置中管理')
}

export async function fetchRunWorkflowDefinition(
  definitionID: number,
  params: WorkflowManualRunPayload
): Promise<RunWorkflowDefinitionResponse> {
  void params
  const { workflowID } = decodeDefinitionID(definitionID)
  const [run, workflow, revisions] = await Promise.all([
    createWorkflowRun(workflowID),
    fetchWorkflow(workflowID),
    fetchWorkflowRevisions(workflowID)
  ])
  return {
    executions: [
      toExecution(
        run,
        workflow,
        revisions.items.find((revision) => revision.id === run.revisionId)
      )
    ]
  }
}

const pageExecutions = async (
  workflowID: number,
  params: WorkflowExecutionQueryParams
): Promise<WorkflowExecutionList> => {
  const [workflow, revisions, runs] = await Promise.all([
    fetchWorkflow(workflowID),
    fetchWorkflowRevisions(workflowID),
    fetchWorkflowRuns(workflowID, {
      cursor: params.cursor,
      limit: params.limit,
      triggerType: params.triggerType,
      status: params.status,
      from: params.from,
      to: params.to,
      keyword: params.keyword
    })
  ])
  return {
    ...runs,
    records: runs.records.map((run) =>
      toExecution(
        run,
        workflow,
        revisions.items.find((revision) => revision.id === run.revisionId)
      )
    )
  }
}

export async function fetchWorkflowDefinitionExecutions(
  definitionID: number,
  params: WorkflowExecutionQueryParams
) {
  const { workflowID } = decodeDefinitionID(definitionID)
  return pageExecutions(workflowID, params)
}

export const fetchWorkflowExecutionList = async (params: WorkflowExecutionQueryParams) => {
  const workflowID = Number(params.workflowDefinitionCode)
  if (!Number.isSafeInteger(workflowID) || workflowID <= 0) {
    throw new Error('请选择要查看日志的工作流')
  }
  return pageExecutions(workflowID, params)
}

export async function fetchWorkflowExecutionDetail(
  executionID: number
): Promise<WorkflowExecutionDetail> {
  const run = await fetchWorkflowRun(executionID)
  const [workflow, revision] = await Promise.all([
    fetchWorkflow(run.workflowId),
    fetchWorkflowRevision(run.workflowId, run.revisionId)
  ])
  const graph = toLegacyGraph(revision.graph)
  const names = new Map(graph.nodes.map((node) => [node.id, node.label]))
  const execution = toExecution(run, workflow, revision)
  return {
    ...execution,
    graph,
    startNodeId: revision.mainTriggerNodeId,
    logs: run.logs,
    event: run.event,
    resultSummary: run.resultSummary,
    artifacts: run.artifacts,
    nodeAttempts: run.runNodes.map((node) => {
      const nodeStatus = nodeAttemptStatus(node.status)
      return {
        id: node.id,
        nodeId: node.nodeInstanceId,
        nodeName: names.get(node.nodeInstanceId) || node.nodeType,
        attempt: node.attempt,
        loopIteration: node.loopIteration,
        status: nodeStatus,
        statusLabel: statusLabel(nodeStatus),
        startedAt: node.startedAt,
        finishedAt: node.completedAt || '',
        durationMs: node.durationMs || 0,
        inputSummary: node.inputSummary,
        outputSummary: node.outputSummary,
        logs: node.logs,
        error: node.errorCategory
          ? {
              summary: node.errorMessage || node.errorCategory,
              category: node.errorCategory,
              retryable: false
            }
          : null
      }
    })
  }
}

export async function fetchSchedulerOverview(): Promise<WorkflowOverview> {
  const definitions = await fetchWorkflowDefinitionList()
  const pages = await Promise.all(
    definitions.map((item) => fetchWorkflowDefinitionExecutions(item.id, { limit: 50 }))
  )
  const executions = pages
    .flatMap((page) => page.records)
    .sort((a, b) => Date.parse(b.queuedAt) - Date.parse(a.queuedAt))
  const pending = executions.filter((item) =>
    ['queued', 'running', 'retry_waiting'].includes(item.status)
  )
  return {
    stats: {
      definitionCount: definitions.length,
      activeDefinitionCount: definitions.filter((item) => item.isWorkflowActive).length,
      executionCount: pages.reduce((total, page) => total + page.total, 0),
      latestExecutedAt: executions[0]?.queuedAt || '',
      pendingCount: pending.length,
      queuedCount: pending.filter((item) => item.status === 'queued').length,
      runningCount: pending.filter((item) => item.status === 'running').length,
      retryWaitingCount: pending.filter((item) => item.status === 'retry_waiting').length,
      oldestPendingAgeMs: 0,
      staleRunningCount: 0
    },
    definitions: definitions.map((item) => ({
      workflowDefinitionId: item.id,
      workflowDefinitionCode: item.code,
      workflowDefinitionVersion: item.version,
      workflowDefinitionName: item.displayName,
      isActive: Boolean(item.isWorkflowActive),
      executionCount: item.executionCount
    }))
  }
}
