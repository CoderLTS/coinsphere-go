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
export type WorkflowBindingKind = 'field' | 'literal' | 'cel' | 'input'

export interface WorkflowInputBinding {
  kind: WorkflowBindingKind
  nodeInstanceId?: string
  fieldPath?: string[]
  value?: unknown
  expression?: string
}

export interface WorkflowGraphNode {
  nodeInstanceId: string
  nodeType: string
  nodeVersion: string
  config: Record<string, unknown>
  inputBindings?: Record<string, WorkflowInputBinding>
  position: { x: number; y: number }
}

export interface WorkflowGraphEdge {
  edgeId: string
  sourceNodeInstanceId: string
  sourcePort: string
  targetNodeInstanceId: string
  targetPort: string
  condition?: string
}

export interface WorkflowGraph {
  schemaVersion: 3
  entryPoints: Record<string, string>
  nodes: WorkflowGraphNode[]
  edges: WorkflowGraphEdge[]
}

export interface WorkflowItem {
  id: number
  name: string
  description: string
  groupId: number | null
  mode: 'batch' | 'event' | 'stream'
  status: WorkflowStatus
  draftRevisionId: number
  publishedRevisionId: number
  ownerUserId: number
  mainTriggerNodeId: string
  retentionDays: number
  createdBy: number
  createdAt: string
  updatedAt: string
}

export interface WorkflowDetail extends WorkflowItem {
  permissions: string[]
  runtime: {
    maxConcurrentRuns: number
    backlogLimit: number
    nextScheduledAt?: string
    lastScheduledAt?: string
    updatedAt: string
  }
  stateNodeInstanceIds: string[]
}

export interface WorkflowTemplate {
  key: string
  name: string
  mode: 'batch' | 'event' | 'stream'
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
  mainTriggerNodeId: string
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
  pluginId?: string
  editorKey?: string
  type: string
  version: string
  title: string
  description: string
  kind: 'action' | 'trigger'
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
  }
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
  entryPoint: string
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

export interface WorkflowSummary extends WorkflowItem {
  latestRunId?: number
  latestRunStatus?: WorkflowRun['status']
  maxConcurrentRuns: number
  backlogLimit: number
}
export const fetchWorkflows = (
  params: {
    cursor?: string
    limit?: number
    status?: string
    keyword?: string
    groupId?: number
  } = {}
) =>
  request.get<Api.Common.PaginatedResponse<WorkflowSummary>>({ url: '/api/v1/workflows', params })

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
  graph?: WorkflowGraph
  secretChanges?: WorkflowSecretChange[]
}) => request.post<WorkflowDetail>({ url: '/api/v1/workflows', params })

export const saveWorkflowRevision = (
  workflowId: number,
  params: {
    expectedDraftRevisionId: number
    metadata?: { name: string; description: string }
    graph: WorkflowGraph
    secretChanges: WorkflowSecretChange[]
  }
) => request.post<WorkflowRevision>({ url: `/api/v1/workflows/${workflowId}/revisions`, params })

export const applyWorkflowLifecycle = (workflowId: number, action: 'activate' | 'deactivate') =>
  request.post<WorkflowDetail>({
    url: `/api/v1/workflows/${workflowId}/lifecycle`,
    params: { action }
  })

export interface WorkflowRunCreatePayload {
  entryPoint?: string
  revisionId?: number
  input?: Record<string, unknown>
}

export const createWorkflowRun = (
  workflowId: number,
  params: WorkflowRunCreatePayload = {},
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

export const publishWorkflow = (
  workflowId: number,
  revisionId: number,
  expectedPublishedRevisionId: number
) =>
  request.post<WorkflowDetail>({
    url: `/api/v1/workflows/${workflowId}/publish`,
    params: { revisionId, expectedPublishedRevisionId }
  })
export interface WorkflowGrant {
  userId?: number
  roleId?: number
  permissions: string[]
}
export const fetchWorkflowGrants = (workflowId: number) =>
  request.get<ItemList<WorkflowGrant>>({ url: `/api/v1/workflows/${workflowId}/grants` })
export const replaceWorkflowGrants = (workflowId: number, grants: WorkflowGrant[]) =>
  request.request({
    url: `/api/v1/workflows/${workflowId}/grants`,
    method: 'PUT',
    data: { grants }
  })
export const fetchWorkbench = () =>
  request.get<{
    workflows?: Api.Common.PaginatedResponse<WorkflowSummary>
    tasks?: WorkflowHumanTask[]
    resultViews?: unknown[]
  }>({ url: '/api/v1/workbench' })
