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

interface ItemList<T> {
  items: T[]
}

export const fetchWorkflows = (status = '') =>
  request.get<ItemList<WorkflowItem>>({
    url: '/api/v1/workflows',
    params: status ? { status } : {}
  })

export const fetchWorkflow = (workflowId: number) =>
  request.get<WorkflowDetail>({ url: `/api/v1/workflows/${workflowId}` })

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

export const createWorkflow = (params: {
  name: string
  description: string
  templateKey: 'blank'
}) => request.post<WorkflowDetail>({ url: '/api/v1/workflows', params })

export const saveWorkflowRevision = (
  workflowId: number,
  params: {
    expectedActiveRevisionId: number
    graph: WorkflowGraph
    secretChanges: WorkflowSecretChange[]
  }
) => request.post<WorkflowRevision>({ url: `/api/v1/workflows/${workflowId}/revisions`, params })

export const applyWorkflowLifecycle = (workflowId: number, action: 'start' | 'pause' | 'archive') =>
  request.post<WorkflowDetail>({
    url: `/api/v1/workflows/${workflowId}/lifecycle`,
    params: { action }
  })
