import request from '@/utils/http'

export type WorkflowStatus = 'paused' | 'running' | 'needs_attention' | 'archived'
export type WorkflowBindingKind = 'field' | 'literal' | 'cel'

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
  schemaVersion: 1
  nodes: WorkflowGraphNode[]
  edges: WorkflowGraphEdge[]
}

export interface WorkflowItem {
  id: number
  name: string
  description: string
  mode: 'batch' | 'event' | 'stream'
  status: WorkflowStatus
  activeRevisionId: number
  mainTriggerNodeId: string
  retentionDays: number
  createdBy: number
  createdAt: string
  updatedAt: string
  archivedAt?: string
}

export interface WorkflowDetail extends WorkflowItem {
  runtime: {
    activityCursor: number
    healthSummary: string
    updatedAt: string
  }
  stateNodeInstanceIds: string[]
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
  type: string
  version: string
  title: string
  description: string
  kind: 'action' | 'trigger'
  configSchema: Record<string, unknown>
  uiSchema: Record<string, unknown>
  inputSchema: Record<string, unknown>
  outputSchema: Record<string, unknown>
  inputPorts: string[]
  outputPorts: string[]
  secretFields: WorkflowSecretField[]
  available: boolean
}

export interface WorkflowSecretChange {
  nodeInstanceId: string
  field: string
  value?: string
  remove?: boolean
}

export interface WorkflowBatch {
  id: number
  workflowId: number
  revisionId: number
  triggerType: 'manual' | 'schedule' | 'event' | 'stream' | 'webhook' | 'failure'
  status: 'queued' | 'running' | 'waiting' | 'retrying' | 'succeeded' | 'failed' | 'cancelled'
  currentNodeInstanceId?: string
  triggeredAt: string
  startedAt?: string
  completedAt?: string
  cancelRequestedAt?: string
  errorCategory?: string
  partitionKey?: string
  diagnostic: boolean
  originalBatchId?: number
}

export interface WorkflowNodeRun {
  id: number
  nodeInstanceId: string
  nodeType: string
  nodeVersion: string
  executionPool: 'stream' | 'compute'
  attempt: number
  loopIteration: number
  operationKey: string
  status: 'running' | 'waiting' | 'succeeded' | 'failed' | 'cancelled' | 'skipped'
  errorCategory?: string
  startedAt: string
  completedAt?: string
  durationMs?: number
}

export interface WorkflowActivity {
  cursor: number
  workflowId: number
  batchId?: number
  nodeRunId?: number
  eventType: string
  status?: string
  summary: string
  errorCategory?: string
  occurredAt: string
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

export interface WorkflowBatchDetail extends WorkflowBatch {
  nodeRuns: WorkflowNodeRun[]
  activities: WorkflowActivity[]
  artifacts: WorkflowArtifact[]
}

export interface WorkflowHumanTask {
  id: number
  workflowId: number
  batchId: number
  nodeInstanceId: string
  taskType: string
  businessKey: string
  prompt: string
  status: 'pending' | 'approved' | 'rejected' | 'expired' | 'superseded'
  expiresAt: string
  createdAt: string
  decidedAt?: string
}

interface WorkflowActivityPage extends ItemList<WorkflowActivity> {
  nextCursor: number
}

interface ItemList<T> {
  items: T[]
}

export interface WorkflowBatchQuery {
  cursor?: string
  limit?: number
  triggerType?: WorkflowBatch['triggerType'] | string
  status?: WorkflowBatch['status'] | 'success' | 'canceled' | 'retry_waiting' | string
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

export const fetchWorkflowRevision = (workflowId: number, revisionId: number) =>
  request.get<WorkflowRevision>({
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

export const validateWorkflowGraph = (graph: WorkflowGraph) =>
  request.post<{
    valid: boolean
    issues: { scope: 'graph'; level: 'error'; message: string }[]
  }>({ url: '/api/v1/workflows/validate', params: { graph } })

export const createWorkflow = (params: {
  name: string
  description: string
  templateKey:
    | 'blank'
    | 'scheduled'
    | 'event'
    | 'failure-handler'
    | 'connector-webhook'
    | 'connector-websocket'
    | 'quant-market-data'
    | 'quant-strategy'
    | 'quant-backtest'
    | 'quant-paper'
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

export const applyWorkflowLifecycle = (workflowId: number, action: 'start' | 'pause' | 'archive') =>
  request.post<WorkflowDetail>({
    url: `/api/v1/workflows/${workflowId}/lifecycle`,
    params: { action }
  })

export const createWorkflowBatch = (workflowId: number) =>
  request.post<WorkflowBatch>({ url: `/api/v1/workflows/${workflowId}/batches` })

export const fetchWorkflowBatches = (workflowId: number, params: WorkflowBatchQuery = {}) =>
  request.get<Api.Common.PaginatedResponse<WorkflowBatch>>({
    url: `/api/v1/workflows/${workflowId}/batches`,
    params
  })

export const applyWorkflowBatchAction = (batchId: number, action: 'cancel' | 'retry' | 'replay') =>
  request.post<WorkflowBatch>({ url: `/api/v1/batches/${batchId}`, params: { action } })

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

export const fetchWorkflowActivity = (workflowId: number, after = 0, limit = 100) =>
  request.get<WorkflowActivityPage>({
    url: `/api/v1/workflows/${workflowId}/activity`,
    params: { after, limit },
    showErrorMessage: after === 0
  })

export const fetchWorkflowBatch = (batchId: number) =>
  request.get<WorkflowBatchDetail>({ url: `/api/v1/batches/${batchId}` })

export const fetchWorkflowArtifactManifest = (sha256: string) =>
  request.get<WorkflowArtifact>({ url: `/api/v1/artifacts/${sha256}/manifest` })

export const downloadWorkflowArtifact = (url: string) =>
  request.request<Blob>({ url, method: 'GET', responseType: 'blob', rawResponse: true })
