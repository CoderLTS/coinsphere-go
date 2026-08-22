/** 前端接口封装：scheduler。 */
import request from '@/utils/http'

export type WorkflowStartType = 'manual' | 'schedule' | 'event' | 'webhook'
export type WorkflowTriggerType = WorkflowStartType
export type WorkflowScheduleType = 'cron' | 'interval' | 'once'
export type WorkflowExecutionStatus =
  | 'queued'
  | 'running'
  | 'retry_waiting'
  | 'waiting_job'
  | 'waiting_action'
  | 'cancel_requested'
  | 'success'
  | 'failed'
  | 'canceled'
export type WorkflowTerminalStatus = 'success' | 'failed' | 'canceled'
export type WorkflowEdgeKind = 'flow' | 'data'

export interface WorkflowNodePortDefinition {
  id: string
  label: string
  required: boolean
  schema: Record<string, any>
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
  inputPorts: WorkflowNodePortDefinition[]
  outputPorts: WorkflowNodePortDefinition[]
  executionMode: 'sync' | 'worker_job' | 'human_action' | string
  securityPolicy: 'standard' | 'automatic_restrictive' | 'human_reauth' | string
  requiredPermission: string
  permissionConfigKey?: string
  permissionByValue?: Record<string, string>
}

export interface WorkflowNodeTemplateItem {
  id: string
  name: string
  description: string
  icon: string
  baseNodeType: string
  baseNodeLabel: string
  defaultConfig: Record<string, any>
  isEnabled: boolean
  createdAt: string
  updatedAt: string
}

export interface WorkflowNodeTemplatePayload {
  name: string
  description: string
  icon?: string
  baseNodeType: string
  defaultConfig: Record<string, any>
  isEnabled?: boolean
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
  kind: WorkflowEdgeKind
  source: string
  target: string
  branch?: string
  label?: string
  sourcePort?: string
  targetPort?: string
  sourcePointer?: string
  targetPointer?: string
}

export interface WorkflowGraph {
  schemaVersion: 2
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
  input: Record<string, any>
  output: Record<string, any>
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
  cancelRequestedAt: string
  rerunOfExecutionId?: number | null
  canCancel: boolean
  canRerun: boolean
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
  input: Record<string, any>
  output: Record<string, any>
}

export type WorkflowExecutionList = Api.Common.PaginatedResponse<WorkflowExecutionItem>

export interface WorkflowExecutionQueryParams {
  cursor?: string
  limit?: number
  workflowDefinitionId?: number
  workflowDefinitionCode?: string
  keyword?: string
  triggerType?: WorkflowTriggerType | string
  status?: WorkflowExecutionStatus | string
}

export interface WorkflowManualRunPayload {
  startEntryKeys: string[]
  inputs?: Record<string, any>
}

export interface WorkflowExecutionCreatePayload extends WorkflowManualRunPayload {
  workflowDefinitionId: number
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

export interface WorkflowActionItem {
  id: string
  executionId: number
  actionType: string
  title: string
  targetType: string
  targetId: string
  status: string
  request: Record<string, any>
  result: Record<string, any>
  requiresReauth: boolean
  requiredPermission: string
  expiresAt: string
  formSchema: Record<string, any>
  resolvedAt: string
  createdAt: string
}

export interface WorkflowActionDecisionPayload {
  decision: 'approved' | 'rejected'
  formData: Record<string, any>
}

export interface WorkflowWorkbench {
  workflows: WorkflowDefinitionItem[]
  executions: WorkflowExecutionItem[]
  actions: WorkflowActionItem[]
  health: WorkflowOverview & { system?: Record<string, any> }
  nodeDefinitions: WorkflowNodeDefinitionItem[]
}

export function fetchWorkflowWorkbench() {
  return request.get<WorkflowWorkbench>({ url: '/api/v1/workbench' })
}

export function fetchNodeDefinitions() {
  return request.get<WorkflowNodeDefinitionItem[]>({
    url: '/api/v1/workflows/node-definitions'
  })
}

export function fetchWorkflowNodeTemplates() {
  return request.get<WorkflowNodeTemplateItem[]>({ url: '/api/v1/workflow-node-templates' })
}

export function fetchCreateWorkflowNodeTemplate(params: WorkflowNodeTemplatePayload) {
  return request.post<WorkflowNodeTemplateItem>({
    url: '/api/v1/workflow-node-templates',
    params,
    showSuccessMessage: true
  })
}

export function fetchUpdateWorkflowNodeTemplate(id: string, params: WorkflowNodeTemplatePayload) {
  return request.put<WorkflowNodeTemplateItem>({
    url: `/api/v1/workflow-node-templates/${encodeURIComponent(id)}`,
    params,
    showSuccessMessage: true
  })
}

export function fetchDeleteWorkflowNodeTemplate(id: string) {
  return request.del<{ deleted: boolean }>({
    url: `/api/v1/workflow-node-templates/${encodeURIComponent(id)}`,
    showSuccessMessage: true
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
  return fetchCreateWorkflowExecution({ workflowDefinitionId: definitionId, ...params })
}

export function fetchWorkflowDefinitionExecutions(
  definitionId: number,
  params: WorkflowExecutionQueryParams
) {
  return request.get<WorkflowExecutionList>({
    url: '/api/v1/workflow-executions',
    params: { ...params, workflowDefinitionId: definitionId }
  })
}

export function fetchWorkflowExecutionList(params: WorkflowExecutionQueryParams) {
  return request.get<WorkflowExecutionList>({
    url: '/api/v1/workflow-executions',
    params
  })
}

export function fetchWorkflowExecutionDetail(executionId: number) {
  return request.get<WorkflowExecutionDetail>({
    url: `/api/v1/workflow-executions/${executionId}`
  })
}

export function fetchCreateWorkflowExecution(params: WorkflowExecutionCreatePayload) {
  return request.post<RunWorkflowDefinitionResponse>({
    url: '/api/v1/workflow-executions',
    params,
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    showSuccessMessage: true
  })
}

export function fetchCancelWorkflowExecution(executionId: number) {
  return request.post<WorkflowExecutionItem>({
    url: `/api/v1/workflow-executions/${executionId}/cancel`,
    showSuccessMessage: true
  })
}

export function fetchRerunWorkflowExecution(executionId: number) {
  return request.post<{ execution: WorkflowExecutionItem; duplicate: boolean }>({
    url: `/api/v1/workflow-executions/${executionId}/rerun`,
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    showSuccessMessage: true
  })
}

export function fetchWorkflowActionList(status?: string) {
  return request.get<WorkflowActionItem[]>({
    url: '/api/v1/workflow-actions',
    params: status ? { status } : undefined
  })
}

export function fetchWorkflowActionDetail(actionId: string) {
  return request.get<WorkflowActionItem>({
    url: `/api/v1/workflow-actions/${encodeURIComponent(actionId)}`
  })
}

export function fetchDecideWorkflowAction(
  actionId: string,
  params: WorkflowActionDecisionPayload,
  reauthToken?: string
) {
  return request.post<WorkflowActionItem>({
    url: `/api/v1/workflow-actions/${encodeURIComponent(actionId)}/decisions`,
    params,
    headers: {
      'Idempotency-Key': crypto.randomUUID(),
      ...(reauthToken ? { 'X-Reauth-Token': reauthToken } : {})
    },
    showSuccessMessage: true
  })
}
