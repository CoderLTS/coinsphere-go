/** 前端接口封装：scheduler。 */
import {
  applyWorkflowLifecycle,
  createWorkflow,
  createWorkflowBatch,
  fetchWorkflow,
  fetchWorkflowBatch,
  fetchWorkflowBatches,
  fetchWorkflowNodeDefinitions,
  fetchWorkflowRevision,
  fetchWorkflowRevisions,
  fetchWorkflows,
  saveWorkflowRevision,
  updateWorkflow,
  validateWorkflowGraph,
  type WorkflowBatch,
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

export interface WorkflowExecutionNodeLog {
  id: number
  nodeId: string
  nodeName: string
  status: WorkflowExecutionStatus | string
  statusLabel: string
  startedAt: string
  finishedAt: string
  durationMs: number
  error: { summary: string; category: string; retryable: boolean } | null
}

export interface WorkflowExecutionTransitionLog {
  id: number
  edgeId: string
  sourceNodeId: string
  targetNodeId: string
  traversalIndex: number
  iterationIndex?: number | null
  sourceNodeName: string
  targetNodeName: string
  branchLabel: string
  createdAt: string
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

export interface WorkflowExecutionAttemptItem {
  id: number
  attempt: number
  startedAt: string
  finishedAt: string
  statusLabel: string
  error: { summary: string; category: string; retryable: boolean } | null
  status: WorkflowExecutionStatus | string
}

export interface WorkflowExecutionDetail extends WorkflowExecutionItem {
  graph: WorkflowGraph
  startNodeId: string
  nodeLogs: WorkflowExecutionNodeLog[]
  attempts: WorkflowExecutionAttemptItem[]
  transitionLogs: WorkflowExecutionTransitionLog[]
}

export type WorkflowExecutionList = Api.Common.PaginatedResponse<WorkflowExecutionItem>

export interface WorkflowExecutionQueryParams {
  cursor?: string
  limit?: number
  workflowDefinitionCode?: string
  triggerType?: WorkflowTriggerType | string
  status?: WorkflowExecutionStatus | string
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
  'official.quant.binance_candles': 'Binance K 线采集',
  'official.quant.evaluate': '量化策略评估',
  'official.quant.backtest': '量化策略回测',
  'official.quant.signal': '量化信号',
  'official.quant.paper_execute': 'Paper 执行',
  'official.notification.in_app': '站内通知'
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

const legacyConfigSchema = (definition: WorkflowNodeDefinition) => {
  const config = definition.configSchema || {}
  const inputs = inputProperties(definition)
  return {
    ...config,
    properties: { ...((config.properties || {}) as object), ...inputs }
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

const batchStatus = (status: WorkflowBatch['status']): WorkflowExecutionStatus => {
  const mapped: Partial<Record<WorkflowBatch['status'], WorkflowExecutionStatus>> = {
    succeeded: 'success',
    cancelled: 'canceled',
    retrying: 'retry_waiting',
    waiting: 'queued'
  }
  return mapped[status] || (status as WorkflowExecutionStatus)
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
  batch: WorkflowBatch,
  workflow: WorkflowItem,
  revision?: WorkflowRevision
): WorkflowExecutionItem => {
  const status = batchStatus(batch.status)
  return {
    id: batch.id,
    workflowDefinitionId: workflow.id,
    workflowDefinitionVersion: revision?.revisionNumber || 1,
    workflowDefinitionName: workflow.name,
    entryName: workflow.mainTriggerNodeId,
    triggerType: batch.triggerType,
    status,
    statusLabel: statusLabel(status),
    triggerLabel: triggerLabel(batch.triggerType),
    queuedAt: batch.triggeredAt,
    claimedAt: batch.startedAt || '',
    startedAt: batch.startedAt || '',
    finishedAt: batch.completedAt || '',
    lastHeartbeatAt: batch.startedAt || '',
    attemptCount: 1,
    maxAttempts: 3,
    durationMs: elapsed(batch.startedAt, batch.completedAt),
    error: batch.errorCategory
      ? { summary: batch.errorCategory, category: batch.errorCategory, retryable: false }
      : null
  }
}

const loadDefinition = async (definitionID: number): Promise<WorkflowDefinitionItem> => {
  const { workflowID, revisionID } = decodeDefinitionID(definitionID)
  const [workflow, revisionResult, batchResult] = await Promise.all([
    fetchWorkflow(workflowID),
    fetchWorkflowRevisions(workflowID),
    fetchWorkflowBatches(workflowID)
  ])
  const revisions = [...revisionResult.items].sort((a, b) => b.revisionNumber - a.revisionNumber)
  const selected =
    revisions.find((item) => item.id === revisionID) ||
    revisions.find((item) => item.id === workflow.activeRevisionId) ||
    revisions[0]
  if (!selected) throw new Error('工作流没有可编辑的修订版本')
  const counts = new Map<number, number>()
  batchResult.records.forEach((batch) =>
    counts.set(batch.revisionId, (counts.get(batch.revisionId) || 0) + 1)
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
    isActive: workflow.status === 'running' && selected.id === workflow.activeRevisionId,
    isWorkflowActive: workflow.status === 'running',
    activeDefinitionId: encodeVersionID(workflow.id, workflow.activeRevisionId),
    activeVersion: activeVersion || null,
    executionCount: batchResult.total,
    createdBy: workflow.createdBy,
    createdAt: selected.createdAt,
    versions: revisions.map((revision) => ({
      id: encodeVersionID(workflow.id, revision.id),
      version: revision.revisionNumber,
      displayName: workflow.name,
      isLatest: revision.id === revisions[0]?.id,
      isBuiltin: false,
      isActive: workflow.status === 'running' && revision.id === workflow.activeRevisionId,
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
  await applyWorkflowLifecycle(workflowID, 'start')
  return fetchWorkflowRuntime(workflowID)
}

export async function fetchDeactivateWorkflowDefinition(definitionID: number) {
  const { workflowID } = decodeDefinitionID(definitionID)
  await applyWorkflowLifecycle(workflowID, 'pause')
  return fetchWorkflowRuntime(workflowID)
}

export async function fetchDeleteWorkflowDefinition(definitionID: number) {
  const { workflowID, revisionID } = decodeDefinitionID(definitionID)
  if (revisionID) throw new Error('V2 修订版本不可删除')
  await applyWorkflowLifecycle(workflowID, 'archive')
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
    activeDefinitionId: workflow.status === 'running' ? activeID : null,
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
            isEnabled: workflow.status === 'running',
            registrationStatus: workflow.status === 'running' ? 'registered' : 'disabled',
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
  await applyWorkflowLifecycle(workflowID, isEnabled ? 'start' : 'pause')
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
  const [batch, workflow, revisions] = await Promise.all([
    createWorkflowBatch(workflowID),
    fetchWorkflow(workflowID),
    fetchWorkflowRevisions(workflowID)
  ])
  return {
    executions: [
      toExecution(
        batch,
        workflow,
        revisions.items.find((revision) => revision.id === batch.revisionId)
      )
    ]
  }
}

const pageExecutions = async (
  workflowID: number,
  params: WorkflowExecutionQueryParams
): Promise<WorkflowExecutionList> => {
  const [workflow, revisions, batches] = await Promise.all([
    fetchWorkflow(workflowID),
    fetchWorkflowRevisions(workflowID),
    fetchWorkflowBatches(workflowID, {
      cursor: params.cursor,
      limit: params.limit,
      triggerType: params.triggerType,
      status: params.status
    })
  ])
  return {
    ...batches,
    records: batches.records.map((batch) =>
      toExecution(
        batch,
        workflow,
        revisions.items.find((revision) => revision.id === batch.revisionId)
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
    throw new Error('请选择要查看执行记录的工作流')
  }
  return pageExecutions(workflowID, params)
}

export async function fetchWorkflowExecutionDetail(
  executionID: number
): Promise<WorkflowExecutionDetail> {
  const batch = await fetchWorkflowBatch(executionID)
  const [workflow, revision] = await Promise.all([
    fetchWorkflow(batch.workflowId),
    fetchWorkflowRevision(batch.workflowId, batch.revisionId)
  ])
  const graph = toLegacyGraph(revision.graph)
  const names = new Map(graph.nodes.map((node) => [node.id, node.label]))
  const execution = toExecution(batch, workflow, revision)
  return {
    ...execution,
    graph,
    startNodeId: revision.mainTriggerNodeId,
    nodeLogs: batch.nodeRuns.map((run) => {
      const nodeStatus = batchStatus(
        run.status === 'succeeded'
          ? 'succeeded'
          : run.status === 'cancelled'
            ? 'cancelled'
            : (run.status as WorkflowBatch['status'])
      )
      return {
        id: run.id,
        nodeId: run.nodeInstanceId,
        nodeName: names.get(run.nodeInstanceId) || run.nodeType,
        status: nodeStatus,
        statusLabel: statusLabel(nodeStatus),
        startedAt: run.startedAt,
        finishedAt: run.completedAt || '',
        durationMs: run.durationMs || 0,
        error: run.errorCategory
          ? { summary: run.errorCategory, category: run.errorCategory, retryable: false }
          : null
      }
    }),
    attempts: [
      {
        id: batch.id,
        attempt: Math.max(1, ...batch.nodeRuns.map((run) => run.attempt)),
        startedAt: batch.startedAt || batch.triggeredAt,
        finishedAt: batch.completedAt || '',
        status: execution.status,
        statusLabel: statusLabel(execution.status),
        error: execution.error
      }
    ],
    transitionLogs: []
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
