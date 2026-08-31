/** 前端类型定义：api.d。 */
/**
 * API 接口类型定义模块
 *
 * 提供所有后端接口的类型定义
 *
 * ## 主要功能
 *
 * - 通用类型（分页参数、响应结构等）
 * - 认证类型（登录、用户信息等）
 * - 系统管理类型（用户、角色等）
 * - 全局命名空间声明
 *
 * ## 使用场景
 *
 * - API 请求参数类型约束
 * - API 响应数据类型定义
 * - 接口文档类型同步
 *
 * ## 注意事项
 *
 * - 在 .vue 文件使用需要在 eslint.config.mjs 中配置 globals: { Api: 'readonly' }
 * - 使用全局命名空间，无需导入即可使用
 *
 * ## 使用方式
 *
 * ```typescript
 * const params: Api.Auth.LoginParams = {
 *   username: 'coinsphere',
 *   password: 'coinsphere',
 *   keepLoggedIn: false
 * }
 * const response: Api.Auth.UserInfo = await fetchUserInfo()
 * ```
 *
 * @module types/api/api
 * @author Art Design Pro Team
 */

declare namespace Api {
  /** 通用类型 */
  namespace Common {
    /** 分页参数 */
    interface PaginationParams {
      /** 当前页码 */
      current: number
      /** 每页条数 */
      size: number
      /** 总条数 */
      total: number
    }

    /** API 游标分页参数 */
    interface CursorParams {
      cursor?: string
      limit?: number
    }

    /** 通用搜索参数 */
    type CommonSearchParams = CursorParams

    /** 分页响应基础结构 */
    interface PaginatedResponse<T = any> {
      records: T[]
      nextCursor: string
      hasMore: boolean
      total: number
    }

    /** 启用状态 */
    type EnableStatus = '1' | '2'
  }

  /** 认证类型 */
  namespace Auth {
    /** 登录参数 */
    interface LoginParams {
      username: string
      password: string
      keepLoggedIn: boolean
    }

    /** 登录响应 */
    interface LoginResponse {
      accessToken: string
    }

    interface ReauthResponse {
      reauthToken: string
    }

    /** 用户信息 */
    interface UserInfo {
      permissions: string[]
      roleCodes: string[]
      userId: number
      username: string
      email: string
      avatar?: string
      accessMode: 'guest' | 'authenticated'
    }
  }

  /** 配置管理类型 */
  namespace Config {
    interface I18nTexts {
      zh: string
      en: string
    }

    interface MenuI18nDict {
      zh: Record<string, string>
      en: Record<string, string>
    }

    interface AiModelConfig {
      id: number
      displayName: string
      baseUrl: string
      modelName: string
      apiKeyMasked: string
      isEnabled: boolean
      priority: number
      timeoutMs: number
      sessionCount: number
      createdAt: string
      updatedAt: string
    }

    interface AiModelUpsertPayload {
      displayName: string
      baseUrl: string
      modelName: string
      apiKey?: string
      isEnabled: boolean
      priority: number
      timeoutMs: number
    }

    /** 用户列表 */
    type UserList = Api.Common.PaginatedResponse<UserListItem>

    /** 用户列表项 */
    interface UserListItem {
      id: number
      avatar: string
      isActive: boolean
      username: string
      gender: string
      nickname: string
      fullName?: string
      phone: string
      email: string
      roleCodes: string[]
      createdBy: string
      createdAt: string
      updatedBy: string
      updatedAt: string
    }

    /** 用户搜索参数 */
    type UserSearchParams = Partial<
      Pick<UserListItem, 'id' | 'username' | 'gender' | 'phone' | 'email' | 'isActive'> &
        Api.Common.CommonSearchParams
    >

    /** 角色列表 */
    type RoleList = Api.Common.PaginatedResponse<RoleListItem>

    /** 角色列表项 */
    interface RoleListItem {
      id: number
      displayName: string
      code: string
      description: string
      isEnabled: boolean
      isSystem: boolean
      createdAt: string
      updatedAt: string
    }

    /** 角色搜索参数 */
    type RoleSearchParams = Partial<
      Pick<RoleListItem, 'id' | 'displayName' | 'code' | 'description' | 'isEnabled'> &
        Api.Common.CommonSearchParams & {
          startTime: string | null
          endTime: string | null
        }
    >

    interface RoleUpsertPayload {
      displayName: string
      code: string
      description: string
      isEnabled: boolean
    }

    interface RolePermissionPayload {
      menuIds: number[]
      buttonIds: number[]
    }

    interface DataSourceDefinitionItem {
      code: string
      label: string
      description: string
      configSchema: Record<string, any>
      variableDefinitions: Array<{
        key: string
        label: string
      }>
    }

    interface DataSourceItem {
      id: number
      definitionCode: string
      definitionLabel: string
      displayName: string
      description: string
      configValues: Record<string, any>
      configSchema: Record<string, any>
      variableDefinitions: Array<{
        key: string
        label: string
      }>
      isEnabled: boolean
      createdAt: string
      updatedAt: string
    }

    interface DataSourceUpsertPayload {
      definitionCode: string
      displayName: string
      description: string
      configValues: Record<string, any>
      isEnabled: boolean
    }
  }

  namespace System {
    interface InstalledPlugin {
      id: string
      name: string
      version: string
      contributes: string[]
      status: 'loaded'
      nodes: Array<{
        type: string
        title: string
        version: string
        kind: 'action' | 'trigger'
        configSchema: Record<string, any>
      }>
      pages: Array<{
        pageKey: string
        title: string
        kind: 'page' | 'resultPage'
      }>
    }

    type I18nTexts = Api.Config.I18nTexts
    type MenuI18nDict = Api.Config.MenuI18nDict
    type UserList = Api.Config.UserList
    type UserListItem = Api.Config.UserListItem
    type UserSearchParams = Api.Config.UserSearchParams
    type RoleList = Api.Config.RoleList
    type RoleListItem = Api.Config.RoleListItem
    type RoleSearchParams = Api.Config.RoleSearchParams
    type RoleUpsertPayload = Api.Config.RoleUpsertPayload
    type RolePermissionPayload = Api.Config.RolePermissionPayload

    type SystemLogLevel = 'debug' | 'info' | 'warn' | 'error'

    interface SystemLogSearchParams extends Api.Common.CursorParams {
      startTime?: string
      endTime?: string
      level?: SystemLogLevel
      component?: string
      requestId?: string
      userId?: number
      method?: string
      route?: string
      statusCode?: number
      keyword?: string
    }

    interface SystemLogItem {
      id: number
      loggedAt: string
      level: SystemLogLevel
      component: string
      message: string
      requestId: string
      userId?: number | null
      userName?: string | null
      method: string
      route: string
      statusCode?: number | null
      durationMs?: number | null
      details: Record<string, string | number | boolean>
    }

    type SystemLogList = Api.Common.PaginatedResponse<SystemLogItem>

    interface SystemLogRuntimeStatus {
      level: SystemLogLevel
      retentionDays: number
      queueDepth: number
      queueCapacity: number
      written: number
      dropped: number
      failed: number
      startedAt: string
      updatedAt: string
      updatedBy?: number | null
    }

    interface SystemLogSettingsPayload {
      level: SystemLogLevel
      retentionDays: number
    }

    type OutboundProxyProtocol = 'http' | 'socks5'
    type OutboundProxyCheckStatus = 'unchecked' | 'healthy' | 'failed'

    interface OutboundProxyItem {
      id: number
      name: string
      protocol: OutboundProxyProtocol
      host: string
      port: number
      username: string
      passwordConfigured: boolean
      isEnabled: boolean
      lastCheckStatus: OutboundProxyCheckStatus
      lastCheckedAt: string
      lastLatencyMs?: number | null
      createdAt: string
      updatedAt: string
    }

    interface OutboundProxyUpsertPayload {
      name: string
      protocol: OutboundProxyProtocol
      host: string
      port: number
      username: string
      password?: string
      clearPassword?: boolean
      isEnabled: boolean
    }

    interface OutboundProxyValidationResult {
      success: boolean
      status: OutboundProxyCheckStatus
      message: string
      checkedAt: string
      latencyMs?: number | null
    }
  }

  namespace Notifications {
    interface InAppNoticeItem {
      id: number
      workflowExecutionId?: number | null
      workflowExecutionNodeId?: number | null
      workflowDefinitionId?: number | null
      workflowDefinitionCode: string
      workflowDefinitionName: string
      strategySignalId?: string | null
      strategySignalMode: string
      strategySignalStatus: string
      strategySignalExpiresAt: string
      targetType: string
      targetId?: number | null
      targetLabel: string
      recipientId?: number | null
      recipientLabel: string
      channelType: string
      channelTypeLabel: string
      channelDisplayName: string
      deliveryStatus: string
      deliveryStatusLabel: string
      messageTitle: string
      messageContent: string
      providerResponseText: string
      errorMessage: string
      isRead: boolean
      readAt: string
      sentAt: string
      createdAt: string
    }

    interface InAppNoticePage {
      records: InAppNoticeItem[]
      nextCursor: string
      total: number
      hasMore: boolean
      unreadCount: number
    }

    interface StrategySignalDecision {
      id: string
      mode: string
      environment: string
      status: string
      expiresAt?: string
      decidedAt?: string
    }
  }

  namespace Assistant {
    type MessageRole = 'user' | 'assistant'

    interface ModelOption {
      id: number
      displayName: string
      modelName: string
      priority: number
    }

    interface Session {
      id: number
      modelId: number
      modelName: string
      title: string
      messageCount: number
      latestPreview: string
      createdAt: string
      updatedAt: string
      lastMessageAt: string
    }

    interface WorkflowProposal {
      name: string
      description: string
      nodeCount: number
      edgeCount: number
      nodeTypes: string[]
      missingSecrets: string[]
      workflowId?: number
      editUrl?: string
    }

    interface Message {
      id: number
      role: MessageRole
      content: string
      proposal?: WorkflowProposal
      createdAt: string
    }

    interface SessionCreateRequest {
      modelId: number
      title?: string
    }

    interface StreamRequest {
      text: string
    }

    interface WorkflowCreateResult {
      workflowId: number
      status: 'inactive'
      editUrl: string
    }
  }

  namespace Home {
    interface DashboardStats {
      newsTotal: number
      newsToday: number
      activeTasks: number
    }

    interface DashboardNewsItem {
      sourceMessageId: number
      title: string
      summary: string
      publishedAt: string
    }

    interface DashboardTaskItem {
      taskId: number
      definitionCode: string
      taskName: string
      isEnabled: boolean
      runCount: number
      lastExecutedAt: string
      lastErrorMessage: string
      scheduleLabel: string
    }

    interface DashboardOverview {
      stats: DashboardStats
      recentNews: DashboardNewsItem[]
      tasks: DashboardTaskItem[]
    }
  }

  namespace Scheduler {
    type WorkflowStartType = 'manual' | 'schedule' | 'event' | 'webhook'
    type WorkflowTriggerType = WorkflowStartType
    type WorkflowScheduleType = 'cron' | 'interval' | 'once'
    type WorkflowExecutionStatus = 'queued' | 'running' | 'retry_waiting' | 'success' | 'failed'

    interface WorkflowNodeDefinitionItem {
      typeCode: string
      label: string
      configSchema: Record<string, any>
    }

    interface WorkflowNodeItem {
      id: string
      type: string
      label: string
      config: Record<string, any>
      position?: {
        x: number
        y: number
      }
    }

    interface WorkflowEdgeItem {
      id: string
      source: string
      target: string
      branch?: string
      label?: string
      condition?: string
    }

    interface WorkflowGraph {
      nodes: WorkflowNodeItem[]
      edges: WorkflowEdgeItem[]
    }

    interface WorkflowDefinitionVersionItem {
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

    interface WorkflowDefinitionItem {
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

    interface WorkflowDefinitionUpsertPayload {
      code?: string
      displayName: string
      description: string
      graph: WorkflowGraph
    }

    interface WorkflowDefinitionValidationIssue {
      scope: 'graph' | 'node' | 'edge'
      level: 'error' | 'warning'
      message: string
      nodeId?: string
      edgeId?: string
      field?: string
    }

    interface WorkflowDefinitionValidationResult {
      valid: boolean
      issues: WorkflowDefinitionValidationIssue[]
    }

    interface WorkflowRuntimeEntryItem {
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

    interface WorkflowRuntimeStateItem {
      workflowCode: string
      runtimeStateId: number | null
      activeDefinitionId: number | null
      activatedAt: string
      entries: WorkflowRuntimeEntryItem[]
    }

    interface WorkflowRuntimeSecretRotationResult {
      entryKey: string
      secret: string
      secretHint: string
    }

    interface WorkflowExecutionNodeLog {
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

    interface WorkflowExecutionItem {
      id: number
      workflowDefinitionId: number
      workflowDefinitionCode: string
      workflowDefinitionName: string
      workflowDefinitionVersion: number
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

    interface WorkflowExecutionAttemptItem {
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

    interface WorkflowExecutionDetail extends WorkflowExecutionItem {
      graph: Api.Scheduler.WorkflowGraph
      nodeLogs: WorkflowExecutionNodeLog[]
      attempts: WorkflowExecutionAttemptItem[]
      transitionLogs: Api.Scheduler.WorkflowExecutionTransitionLog[]
    }

    interface WorkflowManualRunPayload {
      startEntryKeys: string[]
      inputs?: Record<string, any>
    }

    interface RunWorkflowDefinitionResponse {
      executions: WorkflowExecutionItem[]
    }
  }

  namespace Data {
    type NewsList = Api.Common.PaginatedResponse<NewsListItem>

    interface NewsListItem {
      id: number
      sourceMessageId: number
      title: string
      content: string
      summary: string
      sourceUrl: string
      originalUrl: string
      imageUrl: string
      publishedAt: string
    }

    interface NewsSearchParams extends Api.Common.CommonSearchParams {
      keyword?: string
    }

    interface NewsUpsertPayload {
      title: string
      content: string
      sourceUrl: string
      originalUrl: string
      imageUrl: string
      publishedAt?: string
    }
  }
}
