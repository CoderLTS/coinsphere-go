import request from '@/utils/http'

export const WORKFLOW_RUNS_WS_PROTOCOL = 'coinsphere.workflow-runs.v1'

export function buildWorkflowRunsWsUrl(pageOrigin: string, workflowId: number) {
  const url = new URL(`/api/v1/ws/workflows/${workflowId}/runs`, pageOrigin)
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new Error('workflow websocket requires an HTTP page origin')
  }
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

export type WorkflowStatus = 'inactive' | 'active' | 'error'
export type WorkflowBindingKind = 'node' | 'event' | 'profile' | 'literal'

export interface WorkflowInputBinding {
  kind: WorkflowBindingKind
  nodeInstanceId?: string
  fieldPath?: string[]
  profileKey?: string
  value?: unknown
}

export interface WorkflowGraphNode {
  nodeInstanceId: string
  nodeType: string
  nodeVersion: string
  label?: string
  profileId?: string
  config: Record<string, unknown>
  profileBindings?: Record<
    string,
    { pluginId: string; profileId: string; version: string; type: string }
  >
  inputBindings?: Record<string, WorkflowInputBinding>
  position: { x: number; y: number }
}

export interface WorkflowGraphEdge {
  edgeId: string
  sourceNodeInstanceId: string
  sourcePort: string
  targetNodeInstanceId: string
  targetPort: string
  label?: string
}

export interface WorkflowGraph {
  schemaVersion: 3
  profileRefs: { pluginId: string; profileId: string; version: string; type: string }[]
  nodes: WorkflowGraphNode[]
  edges: WorkflowGraphEdge[]
}

export interface WorkflowItem {
  id: number
  name: string
  description: string
  groupId: number | null
  status: WorkflowStatus
  activeRevisionId: number
  retentionDays: number
  createdBy: number
  createdAt: string
  updatedAt: string
}

export interface WorkflowDetail extends WorkflowItem {
  runtime: {
    maxConcurrentRuns: number
    backlogLimit: number
    updatedAt: string
  }
  triggers: {
    nodeInstanceId: string
    status: string
    errorCategory?: string
    nextScheduledAt?: string
    lastScheduledAt?: string
  }[]
  stateNodeInstanceIds: string[]
}

export interface WorkflowTemplate {
  key: string
  name: string
  description: string
}

export interface WorkflowGroup {
  id: number
  name: string
  sortOrder: number
  createdAt: string
  updatedAt: string
}

export interface WorkflowRevision {
  id: number
  workflowId: number
  revisionNumber: number
  graph: WorkflowGraph
  nodeVersions: Record<string, { nodeType: string; nodeVersion: string }>
  createdBy: number
  createdAt: string
  secretFields: Record<string, Record<string, boolean>>
}

export interface WorkflowSecretField {
  name: string
  title: string
  description: string
  required: boolean
}

export interface WorkflowNodeDefinition {
  profileType?: string
  profileFields?: string[]
  type: string
  version: string
  title: string
  description: string
  kind: 'action' | 'trigger'
  role: string
  visibility: 'basic' | 'advanced'
  category: string
  aliases?: string[]
  tags?: string[]
  sortOrder: number
  color: string
  icon: string
  width: number
  height: number
  capabilities: {
    deterministic: boolean
    stateless: boolean
    frameDriver?: boolean
    frameSafe?: boolean
    frameResult?: boolean
    manualTrigger?: boolean
  }
  frameSourcePorts?: string[]
  profileSlots?: Array<{
    key: string
    title: string
    profileTypes: string[]
    required: boolean
  }>
  configGroups?: Array<{
    key: string
    title: string
    description?: string
    fields: string[]
    advanced: boolean
  }>
  configSchema: Record<string, unknown>
  uiSchema: Record<string, unknown>
  inputSchema: Record<string, unknown>
  outputSchema: Record<string, unknown>
  inputPorts: string[]
  outputPorts: string[]
  branches?: string[]
  secretFields: WorkflowSecretField[]
  available: boolean
}

export interface WorkflowSecretChange {
  nodeInstanceId: string
  field: string
  value?: string
  remove?: boolean
}

export interface WorkflowRun {
  id: number
  workflowId: number
  revisionId: number
  operationType?: string
  triggerNodeId: string
  entryNodeInstanceId: string
  triggerInstanceId: string
  triggerEventId?: string
  profileSnapshot: ProfileRef[]
  input: Record<string, unknown>
  triggerType: 'manual' | 'schedule' | 'event' | 'stream' | 'webhook' | 'failure'
  status: 'queued' | 'running' | 'waiting' | 'retrying' | 'succeeded' | 'failed' | 'cancelled'
  currentNodeInstanceId?: string
  triggeredAt: string
  startedAt?: string
  completedAt?: string
  cancelRequestedAt?: string
  errorCategory?: string
  errorMessage?: string
  resultSummary: Record<string, unknown>
  partitionKey?: string
  diagnostic: boolean
  originalRunId?: number
}

export interface ProfileRef {
  pluginId: string
  profileId: string
  version: string
  type: string
}

export interface ProfileSummary {
  pluginId: string
  id: string
  name: string
  summary: string
  type: string
  status: 'draft' | 'published' | 'disabled' | string
  version?: string
  latestPublishedVersion?: string
  config?: Record<string, unknown>
  configSchema?: Record<string, unknown>
  uiSchema?: Record<string, unknown>
  updatedAt?: string
}

export interface WorkflowNodeLog {
  id: number
  workflowId: number
  runId: number
  runNodeId: number
  loggedAt: string
  level: 'debug' | 'info' | 'warn' | 'error'
  message: string
  fields: Record<string, unknown>
}

export interface WorkflowRunNode {
  id: number
  nodeInstanceId: string
  nodeType: string
  nodeVersion: string
  executionPool: 'stream' | 'compute'
  attempt: number
  loopIteration: number
  operationKey: string
  status: 'running' | 'waiting' | 'succeeded' | 'failed' | 'cancelled' | 'skipped'
  inputSummary: Record<string, unknown>
  outputSummary: Record<string, unknown>
  errorCategory?: string
  errorMessage?: string
  startedAt: string
  completedAt?: string
  durationMs?: number
  logs: WorkflowNodeLog[]
}

export interface WorkflowArtifact {
  sha256: string
  nodeInstanceId?: string
  mediaType: string
  encoding: 'gzip'
  sizeBytes: number
  storedSizeBytes: number
  downloadUrl: string
  verified?: boolean
}

export interface WorkflowRunEvent {
  id: number
  source: string
  eventId: string
  type: string
  subject: string
  time: string
  partitionKey: string
}

export interface WorkflowRunDetail extends WorkflowRun {
  event?: WorkflowRunEvent
  runNodes: WorkflowRunNode[]
  logs: WorkflowNodeLog[]
  artifacts: WorkflowArtifact[]
}

export interface WorkflowHumanTask {
  id: number
  workflowId: number
  runId: number
  nodeInstanceId: string
  taskType: string
  businessKey: string
  prompt: string
  status: 'pending' | 'approved' | 'rejected' | 'expired' | 'superseded'
  expiresAt: string
  createdAt: string
  decidedAt?: string
}

interface ItemList<T> {
  items: T[]
}

export interface WorkflowRunQuery {
  cursor?: string
  limit?: number
  triggerType?: WorkflowRun['triggerType'] | string
  status?: WorkflowRun['status'] | 'success' | 'canceled' | 'retry_waiting' | string
  from?: string
  to?: string
  keyword?: string
}

export const fetchWorkflows = (status = '') =>
  request.get<ItemList<WorkflowItem>>({
    url: '/api/v1/workflows',
    params: status ? { status } : {}
  })

export const fetchWorkflow = (workflowId: number) =>
  request.get<WorkflowDetail>({ url: `/api/v1/workflows/${workflowId}` })

export const updateWorkflow = (
  workflowId: number,
  params: Pick<WorkflowItem, 'name' | 'description'>
) =>
  request.request<WorkflowDetail>({
    url: `/api/v1/workflows/${workflowId}`,
    method: 'PATCH',
    data: params
  })

export const deleteWorkflow = (workflowId: number) =>
  request.del<{ id: number }>({ url: `/api/v1/workflows/${workflowId}` })

export const fetchWorkflowRevision = (workflowId: number, revisionId: number) =>
  request.get<WorkflowRevision>({
    url: `/api/v1/workflows/${workflowId}/revisions/${revisionId}`
  })

export const deleteWorkflowRevision = (workflowId: number, revisionId: number) =>
  request.del<{ id: number }>({
    url: `/api/v1/workflows/${workflowId}/revisions/${revisionId}`
  })

export const fetchWorkflowRevisions = (workflowId: number) =>
  request.get<ItemList<WorkflowRevision>>({
    url: `/api/v1/workflows/${workflowId}/revisions`
  })

export const fetchWorkflowNodeDefinitions = () =>
  request.get<ItemList<WorkflowNodeDefinition>>({
    url: '/api/v1/workflows/node-definitions'
  })

export const fetchWorkflowTemplates = () =>
  request.get<ItemList<WorkflowTemplate>>({ url: '/api/v1/workflows/templates' })

export const fetchWorkflowGroups = () =>
  request.get<ItemList<WorkflowGroup>>({ url: '/api/v1/workflow-groups' })

export const createWorkflowGroup = (name: string) =>
  request.post<WorkflowGroup>({ url: '/api/v1/workflow-groups', params: { name } })

export const updateWorkflowGroup = (groupId: number, name: string) =>
  request.request<WorkflowGroup>({
    url: `/api/v1/workflow-groups/${groupId}`,
    method: 'PATCH',
    data: { name }
  })

export const deleteWorkflowGroup = (groupId: number) =>
  request.del<{ id: number }>({ url: `/api/v1/workflow-groups/${groupId}` })

export const updateWorkflowGroupOrder = (groupIds: number[]) =>
  request.request<ItemList<WorkflowGroup>>({
    url: '/api/v1/workflow-groups/order',
    method: 'PUT',
    data: { groupIds }
  })

export const assignWorkflowGroup = (workflowIds: number[], groupId: number | null) =>
  request.request<{ updated: number }>({
    url: '/api/v1/workflows/group-assignment',
    method: 'PATCH',
    data: { workflowIds, groupId }
  })

export const validateWorkflowGraph = (graph: WorkflowGraph) =>
  request.post<{
    valid: boolean
    issues: { scope: 'graph'; level: 'error'; message: string }[]
  }>({ url: '/api/v1/workflows/validate', params: { graph } })

export const createWorkflow = (params: {
  name: string
  description: string
  templateKey: string
  groupId?: number | null
}) => request.post<WorkflowDetail>({ url: '/api/v1/workflows', params })

export const saveWorkflowRevision = (
  workflowId: number,
  params: {
    expectedActiveRevisionId: number
    graph: WorkflowGraph
    secretChanges: WorkflowSecretChange[]
    resetStateNodeInstanceIds: string[]
  }
) => request.post<WorkflowRevision>({ url: `/api/v1/workflows/${workflowId}/revisions`, params })

export const applyWorkflowLifecycle = (workflowId: number, action: 'activate' | 'deactivate') =>
  request.post<WorkflowDetail>({
    url: `/api/v1/workflows/${workflowId}/lifecycle`,
    params: { action }
  })

export interface WorkflowRunCreatePayload {
  triggerNodeId: string
  revisionId?: number
  operationType?: string
  resultNodeIds?: string[]
  input?: Record<string, unknown>
}

export const createWorkflowRun = (
  workflowId: number,
  params: WorkflowRunCreatePayload,
  showErrorMessage = true
) =>
  request.post<WorkflowRun>({
    url: `/api/v1/workflows/${workflowId}/runs`,
    params,
    showErrorMessage
  })

export const fetchWorkflowRuns = (workflowId: number, params: WorkflowRunQuery = {}) =>
  request.get<Api.Common.PaginatedResponse<WorkflowRun>>({
    url: `/api/v1/workflows/${workflowId}/runs`,
    params
  })

export const applyWorkflowRunAction = (runId: number, action: 'cancel' | 'retry' | 'replay') =>
  request.post<WorkflowRun>({ url: `/api/v1/workflow-runs/${runId}`, params: { action } })

export const fetchWorkflowHumanTasks = (status = 'pending') =>
  request.get<ItemList<WorkflowHumanTask>>({
    url: '/api/v1/human-tasks',
    params: status ? { status } : {}
  })

export const decideWorkflowHumanTask = (taskId: number, action: 'approve' | 'reject') =>
  request.post<WorkflowHumanTask>({
    url: `/api/v1/human-tasks/${taskId}`,
    params: { action, data: {} }
  })

export const fetchWorkflowRun = (runId: number) =>
  request.get<WorkflowRunDetail>({ url: `/api/v1/workflow-runs/${runId}` })

export const fetchWorkflowArtifactManifest = (sha256: string) =>
  request.get<WorkflowArtifact>({ url: `/api/v1/artifacts/${sha256}/manifest` })

export const downloadWorkflowArtifact = (url: string) =>
  request.request<Blob>({ url, method: 'GET', responseType: 'blob', rawResponse: true })

// Workflow execution view projections live with the workflow API.

export type WorkflowTriggerType = WorkflowRun['triggerType']
export type WorkflowExecutionStatus =
  | 'queued'
  | 'running'
  | 'waiting'
  | 'retry_waiting'
  | 'success'
  | 'failed'
  | 'canceled'
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
  groupId: number | null
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
  triggerNodeId: string
  entryNodeInstanceId: string
  triggerInstanceId: string
  triggerEventId?: string
  profileSnapshot: WorkflowRun['profileSnapshot']
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

const runStatus = (status: WorkflowRun['status']): WorkflowExecutionStatus => {
  const mapped: Partial<Record<WorkflowRun['status'], WorkflowExecutionStatus>> = {
    succeeded: 'success',
    cancelled: 'canceled',
    retrying: 'retry_waiting',
    waiting: 'waiting'
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
    waiting: '等待数据',
    queued: '排队中',
    running: '运行中',
    retry_waiting: '等待重试',
    success: '成功',
    failed: '失败',
    canceled: '已取消'
  })[status] || status

const triggerLabel = (trigger: string) =>
  (
    ({
      manual: '手动',
      schedule: '定时',
      event: '事件',
      stream: '流式',
      webhook: 'Webhook',
      failure: '失败'
    }) as Record<string, string>
  )[trigger] || trigger

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
    triggerNodeId: run.triggerNodeId,
    entryNodeInstanceId: run.entryNodeInstanceId,
    triggerInstanceId: run.triggerInstanceId,
    triggerEventId: run.triggerEventId,
    profileSnapshot: run.profileSnapshot,
    entryName:
      revision?.graph.nodes.find((node) => node.nodeInstanceId === run.entryNodeInstanceId)
        ?.label || run.entryNodeInstanceId,
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
  const workflowID = definitionID
  const revisionID = 0
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
    groupId: workflow.groupId,
    graph: selected.graph,
    isLatest: selected.id === revisions[0]?.id,
    isBuiltin: false,
    isActive: workflow.status === 'active' && selected.id === workflow.activeRevisionId,
    isWorkflowActive: workflow.status === 'active',
    workflowStatus: workflow.status,
    activeDefinitionId: workflow.activeRevisionId,
    activeVersion: activeVersion || null,
    executionCount: runResult.total,
    createdBy: workflow.createdBy,
    createdAt: selected.createdAt,
    versions: revisions.map((revision) => ({
      id: revision.id,
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

export async function fetchWorkflowDefinitionList() {
  const { items } = await fetchWorkflows()
  return Promise.all(items.map((item) => loadDefinition(item.id)))
}
export const fetchWorkflowDefinitionDetail = loadDefinition
export const fetchActivateWorkflowDefinition = (id: number) =>
  applyWorkflowLifecycle(id, 'activate')
export const fetchDeactivateWorkflowDefinition = (id: number) =>
  applyWorkflowLifecycle(id, 'deactivate')
export const fetchDeleteWorkflowDefinition = deleteWorkflowRevision
export const fetchDeleteWorkflow = deleteWorkflow
export async function fetchRunWorkflowDefinition(id: number, params: WorkflowRunCreatePayload) {
  const run = await createWorkflowRun(id, params)
  const [workflow, revision] = await Promise.all([
    fetchWorkflow(id),
    fetchWorkflowRevision(id, run.revisionId)
  ])
  return { executions: [toExecution(run, workflow, revision)] }
}

export async function fetchWorkflowExecutionDetail(
  executionID: number
): Promise<WorkflowExecutionDetail> {
  const run = await fetchWorkflowRun(executionID)
  const [workflow, revision] = await Promise.all([
    fetchWorkflow(run.workflowId),
    fetchWorkflowRevision(run.workflowId, run.revisionId)
  ])
  const graph = revision.graph
  const names = new Map(
    graph.nodes.map((node) => [node.nodeInstanceId, node.label || node.nodeType])
  )
  const execution = toExecution(run, workflow, revision)
  return {
    ...execution,
    graph,
    startNodeId: run.entryNodeInstanceId,
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
