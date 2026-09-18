import {
  applyWorkflowLifecycle, createWorkflowRun, deleteWorkflow, deleteWorkflowRevision,
  fetchWorkflow, fetchWorkflowRun, fetchWorkflowRuns, fetchWorkflowRevision,
  fetchWorkflowRevisions, fetchWorkflows,
  type WorkflowRun, type WorkflowNodeLog, type WorkflowArtifact, type WorkflowRunEvent,
  type WorkflowGraph, type WorkflowItem, type WorkflowRevision, type WorkflowRunCreatePayload
} from './workflows'

export type WorkflowTriggerType = WorkflowRun['triggerType']
export type WorkflowExecutionStatus = 'queued' | 'running' | 'waiting' | 'retry_waiting' | 'success' | 'failed' | 'canceled'
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
    entryName: revision?.graph.nodes.find(node => node.nodeInstanceId === run.entryNodeInstanceId)?.label || run.entryNodeInstanceId,
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
  return Promise.all(items.map(item => loadDefinition(item.id)))
}
export const fetchWorkflowDefinitionDetail = loadDefinition
export const fetchActivateWorkflowDefinition = (id: number) => applyWorkflowLifecycle(id, 'activate')
export const fetchDeactivateWorkflowDefinition = (id: number) => applyWorkflowLifecycle(id, 'deactivate')
export const fetchDeleteWorkflowDefinition = deleteWorkflowRevision
export const fetchDeleteWorkflow = deleteWorkflow
export async function fetchRunWorkflowDefinition(id: number, params: WorkflowRunCreatePayload) {
  const run = await createWorkflowRun(id, params)
  const [workflow, revision] = await Promise.all([fetchWorkflow(id), fetchWorkflowRevision(id, run.revisionId)])
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
  const names = new Map(graph.nodes.map((node) => [node.nodeInstanceId, node.label || node.nodeType]))
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
