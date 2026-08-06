/** 前端接口封装：scheduler。 */
import request from '@/utils/http'

export type WorkflowStartType = 'manual' | 'schedule' | 'event' | 'webhook'
export type WorkflowTriggerType = WorkflowStartType
export type WorkflowScheduleType = 'cron' | 'interval' | 'once'
export type WorkflowExecutionStatus = 'queued' | 'running' | 'retry_waiting' | 'success' | 'failed'

export interface TaskDefinitionItem {
  code: string
  label: string
  description: string
  parameterSchema: Record<string, any>
  supportedScheduleTypes: WorkflowScheduleType[]
}

export interface TaskDefinitionManagementItem {
  code: string
  label: string
  description: string
  parameterSchema: Record<string, any>
  schemaDefaultParams: Record<string, any>
  configuredOverrides: Record<string, any>
  effectiveDefaultParams: Record<string, any>
  updatedAt: string
  updatedBy?: number | null
}

export type TaskDefinitionManagementList =
  Api.Common.PaginatedResponse<TaskDefinitionManagementItem>

export interface TaskDefinitionQueryParams {
  cursor?: string
  limit?: number
  keyword?: string
}

export interface TaskDefinitionDefaultParamsPayload {
  params: Record<string, any>
}

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
  configSchema: Record<string, any>
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
  nodeType: string
  status: WorkflowExecutionStatus | string
  startedAt: string
  finishedAt: string
  durationMs: number
  inputSnapshotJson: string
  outputSnapshotJson: string
  errorMessage: string
}

export interface WorkflowExecutionTransitionLog {
  id: number
  edgeId: string
  sourceNodeId: string
  targetNodeId: string
  traversalIndex: number
  iterationIndex?: number | null
  branchKey: string
  payloadSnapshotJson: string
  createdAt: string
}

export interface WorkflowExecutionItem {
  id: number
  workflowDefinitionId: number
  workflowDefinitionCode: string
  workflowDefinitionVersion: number
  workflowDefinitionName: string
  startEntryKey: string
  startNodeId: string
  startNodeType: string
  triggerType: WorkflowTriggerType | string
  triggeredBy?: number | null
  triggerKey?: string | null
  idempotencyKey?: string | null
  concurrencyKey?: string | null
  triggerOutboxId?: number | null
  status: WorkflowExecutionStatus | string
  queuedAt: string
  claimedAt: string
  startedAt: string
  finishedAt: string
  lastHeartbeatAt: string
  workerId?: string | null
  attemptCount: number
  maxAttempts: number
  nextRetryAt: string
  failureCategory: string
  brokerMessageId: string
  durationMs: number
  errorMessage: string
  inputSnapshotJson: string
  contextSnapshotJson: string
  resultSnapshotJson: string
}

export interface WorkflowExecutionAttemptItem {
  id: number
  attempt: number
  workerId: string
  brokerMessageId: string
  leaseId: string
  startedAt: string
  finishedAt: string
  failureCategory: string
  errorSummary: string
  status: WorkflowExecutionStatus | string
}

export interface WorkflowExecutionDetail extends WorkflowExecutionItem {
  graph: WorkflowGraph
  nodeLogs: WorkflowExecutionNodeLog[]
  attempts: WorkflowExecutionAttemptItem[]
  transitionLogs: WorkflowExecutionTransitionLog[]
}

export type WorkflowExecutionList = Api.Common.PaginatedResponse<WorkflowExecutionItem>

export interface WorkflowExecutionQueryParams {
  cursor?: string
  limit?: number
  workflowDefinitionCode?: string
  keyword?: string
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

export function fetchSchedulerOverview() {
  return request.get<WorkflowOverview>({
    url: '/api/v1/workflows/overview'
  })
}

export function fetchTaskDefinitions() {
  return request.get<TaskDefinitionItem[]>({
    url: '/api/v1/workflows/task-definitions'
  })
}

export function fetchTaskDefinitionPage(params: TaskDefinitionQueryParams) {
  return request.get<TaskDefinitionManagementList>({
    url: '/api/v1/workflows/task-definitions/page',
    params
  })
}

export function fetchUpdateTaskDefinitionDefaultParams(
  taskCode: string,
  params: TaskDefinitionDefaultParamsPayload
) {
  return request.put<TaskDefinitionManagementItem>({
    url: `/api/v1/workflows/task-definitions/${taskCode}/default-params`,
    params,
    showSuccessMessage: true
  })
}

export function fetchNodeDefinitions() {
  return request.get<WorkflowNodeDefinitionItem[]>({
    url: '/api/v1/workflows/node-definitions'
  })
}

/** 工作流编辑器里 assistant.agent 节点的智能体下拉选项。 */
export function fetchWorkflowAgentOptions() {
  return request.get<WorkflowAgentOption[]>({
    url: '/api/v1/workflows/agent-options'
  })
}

export function fetchWorkflowDefinitionList() {
  return request.get<WorkflowDefinitionItem[]>({
    url: '/api/v1/workflows'
  })
}

export function fetchWorkflowDefinitionDetail(definitionId: number) {
  return request.get<WorkflowDefinitionItem>({
    url: `/api/v1/workflows/${definitionId}`
  })
}

export function fetchCreateWorkflowDefinition(params: WorkflowDefinitionUpsertPayload) {
  return request.post<WorkflowDefinitionItem>({
    url: '/api/v1/workflows',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateWorkflowDefinition(
  definitionId: number,
  params: WorkflowDefinitionUpsertPayload
) {
  return request.put<WorkflowDefinitionItem>({
    url: `/api/v1/workflows/${definitionId}`,
    params,
    showSuccessMessage: false
  })
}

export function fetchDeleteWorkflowDefinition(definitionId: number) {
  return request.del<void>({
    url: `/api/v1/workflows/${definitionId}`,
    showSuccessMessage: true
  })
}

export function fetchValidateWorkflowDefinition(params: WorkflowDefinitionUpsertPayload) {
  return request.post<WorkflowDefinitionValidationResult>({
    url: '/api/v1/workflows/validate',
    params
  })
}

export function fetchActivateWorkflowDefinition(definitionId: number) {
  return request.post<WorkflowRuntimeStateItem>({
    url: `/api/v1/workflows/${definitionId}/activate`,
    showSuccessMessage: true
  })
}

export function fetchDeactivateWorkflowDefinition(definitionId: number) {
  return request.post<WorkflowRuntimeStateItem>({
    url: `/api/v1/workflows/${definitionId}/deactivate`,
    showSuccessMessage: true
  })
}

export function fetchWorkflowRuntime(definitionId: number) {
  return request.get<WorkflowRuntimeStateItem>({
    url: `/api/v1/workflows/${definitionId}/runtime`
  })
}

export function fetchUpdateWorkflowRuntimeEntryStatus(
  definitionId: number,
  entryKey: string,
  isEnabled: boolean
) {
  return request.request<WorkflowRuntimeStateItem>({
    url: `/api/v1/workflows/${definitionId}/runtime/entries/${encodeURIComponent(entryKey)}`,
    method: 'PATCH',
    data: { isEnabled },
    showSuccessMessage: true
  })
}

export function fetchRotateWorkflowRuntimeEntrySecret(definitionId: number, entryKey: string) {
  return request.post<WorkflowRuntimeSecretRotationResult>({
    url: `/api/v1/workflows/${definitionId}/runtime/entries/${encodeURIComponent(entryKey)}/rotate-secret`,
    showSuccessMessage: true
  })
}

export function fetchRunWorkflowDefinition(definitionId: number, params: WorkflowManualRunPayload) {
  const idempotencyKey = crypto.randomUUID()

  return request.post<RunWorkflowDefinitionResponse>({
    url: `/api/v1/workflows/${definitionId}/executions`,
    params,
    headers: { 'Idempotency-Key': idempotencyKey },
    showSuccessMessage: true
  })
}

export function fetchWorkflowDefinitionExecutions(
  definitionId: number,
  params: WorkflowExecutionQueryParams
) {
  return request.get<WorkflowExecutionList>({
    url: `/api/v1/workflows/${definitionId}/executions`,
    params
  })
}

export function fetchWorkflowExecutionList(params: WorkflowExecutionQueryParams) {
  return request.get<WorkflowExecutionList>({
    url: '/api/v1/workflows/executions',
    params
  })
}

export function fetchWorkflowExecutionDetail(executionId: number) {
  return request.get<WorkflowExecutionDetail>({
    url: `/api/v1/workflows/executions/${executionId}`
  })
}
