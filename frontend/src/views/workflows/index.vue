<template>
  <div class="workflow-workbench art-full-height">
    <header class="revision-track">
      <div class="revision-track__identity">
        <span class="revision-track__mark"><ArtSvgIcon icon="ri:flow-chart" /></span>
        <div>
          <div class="revision-track__eyebrow">Workflow workbench</div>
          <h1>{{ selectedWorkflow?.name || '工作流工作台' }}</h1>
        </div>
      </div>
      <div v-if="selectedWorkflow" class="revision-track__facts">
        <ElTag :type="statusType" effect="plain">{{ statusLabel }}</ElTag>
        <span class="revision-track__revision">R{{ activeRevision?.revisionNumber || '-' }}</span>
        <span :class="['revision-track__dirty', { 'is-dirty': dirty }]">
          {{ dirty ? '未保存' : '已同步' }}
        </span>
      </div>
      <div class="revision-track__actions">
        <ElBadge :value="humanTasks.length" :hidden="!humanTasks.length">
          <ElButton title="人工任务" circle @click="openHumanTasks">
            <ArtSvgIcon icon="ri:task-line" />
          </ElButton>
        </ElBadge>
        <ElButton
          v-if="selectedWorkflow && manualTrigger"
          type="success"
          :loading="runningBatch"
          :disabled="selectedWorkflow.status !== 'running' || dirty || readOnly"
          @click="runNow"
        >
          <ArtSvgIcon icon="ri:rocket-2-line" />
          立即运行
        </ElButton>
        <ElSelect
          v-if="revisions.length"
          v-model="viewRevisionId"
          class="revision-track__select"
          aria-label="查看修订"
          @change="selectRevision"
        >
          <ElOption
            v-for="revision in revisions"
            :key="revision.id"
            :label="`R${revision.revisionNumber}`"
            :value="revision.id"
          />
        </ElSelect>
        <ElButton
          v-if="selectedWorkflow"
          :disabled="selectedWorkflow.status === 'archived' || dirty || readOnly"
          :title="
            selectedWorkflow.status === 'running'
              ? '暂停'
              : selectedWorkflow.status === 'needs_attention'
                ? '标记已处理'
                : '启动'
          "
          circle
          @click="toggleLifecycle"
        >
          <ArtSvgIcon
            :icon="
              selectedWorkflow.status === 'running'
                ? 'ri:pause-line'
                : selectedWorkflow.status === 'needs_attention'
                  ? 'ri:check-line'
                  : 'ri:play-line'
            "
          />
        </ElButton>
        <ElButton
          v-if="selectedWorkflow"
          title="归档"
          circle
          :disabled="
            selectedWorkflow.status === 'running' ||
            selectedWorkflow.status === 'archived' ||
            dirty ||
            readOnly
          "
          @click="archiveWorkflow"
        >
          <ArtSvgIcon icon="ri:archive-line" />
        </ElButton>
        <ElButton
          v-if="selectedWorkflow"
          type="primary"
          :loading="saving"
          :disabled="!dirty || readOnly"
          @click="save"
        >
          <ArtSvgIcon icon="ri:save-3-line" />
          保存修订
        </ElButton>
      </div>
    </header>

    <section class="mobile-activity">
      <div v-if="selectedWorkflow" class="mobile-activity__summary">
        <strong>{{ selectedWorkflow.name }}</strong>
        <ElTag :type="statusType" size="small" effect="plain">{{ statusLabel }}</ElTag>
        <span>R{{ activeRevision?.revisionNumber || '-' }}</span>
      </div>
      <div v-else class="mobile-activity__empty">暂无工作流</div>
      <ol v-if="recentActivities.length" class="mobile-activity__events">
        <li v-for="activity in recentActivities.slice(0, 20)" :key="activity.cursor">
          <button type="button" @click="activity.batchId && loadBatch(activity.batchId)">
            <span :data-status="activity.status"></span>
            <strong>{{ activity.summary }}</strong>
            <time>{{ formatTime(activity.occurredAt) }}</time>
          </button>
        </li>
      </ol>
      <div v-else-if="selectedWorkflow" class="mobile-activity__empty">暂无运行活动</div>
      <ol v-if="selectedBatch" class="mobile-activity__nodes">
        <li v-for="run in selectedBatch.nodeRuns" :key="run.id">
          <span :data-status="run.status"></span>
          <strong>{{ run.nodeInstanceId }}</strong>
          <small>尝试 {{ run.attempt }} · {{ formatDuration(run.durationMs) }}</small>
        </li>
      </ol>
    </section>

    <main v-loading="loading" class="workbench-grid">
      <aside class="workflow-rail">
        <div class="rail-section rail-section--workflows">
          <div class="rail-heading">
            <span>工作流</span>
            <ElButton title="创建工作流" circle size="small" @click="createDialogVisible = true">
              <ArtSvgIcon icon="ri:add-line" />
            </ElButton>
          </div>
          <ElInput v-model="workflowQuery" clearable placeholder="搜索工作流">
            <template #prefix><ArtSvgIcon icon="ri:search-line" /></template>
          </ElInput>
          <div class="workflow-list">
            <button
              v-for="workflow in filteredWorkflows"
              :key="workflow.id"
              type="button"
              :class="['workflow-row', { 'is-active': workflow.id === selectedWorkflow?.id }]"
              @click="selectWorkflow(workflow)"
            >
              <span class="workflow-row__status" :data-status="workflow.status"></span>
              <span class="workflow-row__copy">
                <strong>{{ workflow.name }}</strong>
                <small>{{ formatTime(workflow.updatedAt) }} · {{ workflow.status }}</small>
              </span>
              <ArtSvgIcon icon="ri:arrow-right-s-line" />
            </button>
            <div v-if="!filteredWorkflows.length" class="rail-empty">暂无工作流</div>
          </div>
        </div>

        <div class="rail-section rail-section--nodes">
          <div class="rail-heading"><span>节点目录</span></div>
          <div class="node-catalog">
            <button
              v-for="definition in availableDefinitions"
              :key="definition.type"
              type="button"
              :disabled="readOnly || definition.kind === 'trigger'"
              @click="addNode(definition)"
            >
              <span :class="['node-catalog__kind', `is-${definition.kind}`]">
                <ArtSvgIcon
                  :icon="definition.kind === 'trigger' ? 'ri:flashlight-line' : 'ri:box-3-line'"
                />
              </span>
              <span>
                <strong>{{ definition.title }}</strong>
                <small>{{ definition.type }}@{{ definition.version }}</small>
              </span>
              <ArtSvgIcon v-if="definition.kind !== 'trigger'" icon="ri:add-line" />
            </button>
          </div>
        </div>
      </aside>

      <section class="canvas-stage">
        <div class="canvas-stage__toolbar">
          <span>{{ graph.nodes.length }} 节点 · {{ graph.edges.length }} 连线</span>
          <div>
            <button type="button" title="缩小" @click="zoom(-0.1)"
              ><ArtSvgIcon icon="ri:subtract-line"
            /></button>
            <button type="button" title="适应画布" @click="fit"
              ><ArtSvgIcon icon="ri:focus-3-line"
            /></button>
            <button type="button" title="放大" @click="zoom(0.1)"
              ><ArtSvgIcon icon="ri:add-line"
            /></button>
          </div>
        </div>
        <div ref="canvasRef" class="canvas-stage__surface"></div>
        <div v-if="!selectedWorkflow" class="canvas-stage__empty">
          <ArtSvgIcon icon="ri:flow-chart" />
          <strong>创建第一个批处理工作流</strong>
        </div>
      </section>

      <aside class="inspector">
        <template v-if="selectedNode && selectedDefinition">
          <div class="inspector__heading">
            <div>
              <span>Node inspector</span>
              <h2>{{ selectedDefinition.title }}</h2>
              <code>{{ selectedDefinition.type }}@{{ selectedDefinition.version }}</code>
            </div>
            <ElButton
              v-if="selectedDefinition.kind !== 'trigger'"
              title="删除节点"
              type="danger"
              text
              circle
              :disabled="readOnly"
              @click="removeSelectedNode"
            >
              <ArtSvgIcon icon="ri:delete-bin-6-line" />
            </ElButton>
          </div>

          <section class="inspector__section">
            <h3>配置</h3>
            <WorkflowSchemaFields
              :schema="ordinaryConfigSchema"
              :ui-schema="selectedDefinition.uiSchema"
              :config="selectedNode.config"
              @update="updateConfig"
            />
            <p v-if="!schemaProperties(ordinaryConfigSchema).length" class="inspector__empty">
              此节点没有普通配置。
            </p>
          </section>

          <section v-if="inputFields.length" class="inspector__section">
            <h3>输入映射</h3>
            <div v-for="field in inputFields" :key="field.name" class="binding-field">
              <div class="binding-field__label">
                <span>{{ field.title }}</span>
                <small>{{ field.type }}</small>
              </div>
              <ElSegmented
                :model-value="bindingFor(field.name).kind"
                :options="bindingKinds"
                :disabled="readOnly"
                @change="changeBindingKind(field.name, String($event) as WorkflowBindingKind)"
              />
              <template v-if="bindingFor(field.name).kind === 'field'">
                <ElSelect
                  :model-value="fieldBindingValue(field.name)"
                  placeholder="选择上游字段"
                  :disabled="readOnly"
                  @change="setFieldBinding(field.name, String($event))"
                >
                  <ElOption
                    v-for="option in upstreamFieldOptions"
                    :key="option.value"
                    :label="option.label"
                    :value="option.value"
                  />
                </ElSelect>
              </template>
              <ElInput
                v-else-if="bindingFor(field.name).kind === 'cel'"
                :model-value="bindingFor(field.name).expression || ''"
                type="textarea"
                :rows="3"
                placeholder="event.type == 'manual'"
                :disabled="readOnly"
                @update:model-value="updateCelBinding(field.name, String($event))"
              />
              <ElSwitch
                v-else-if="field.type === 'boolean'"
                :model-value="Boolean(literalBindingValue(field.name))"
                :disabled="readOnly"
                @update:model-value="updateLiteralBinding(field.name, $event)"
              />
              <ElInputNumber
                v-else-if="field.type === 'integer' || field.type === 'number'"
                :model-value="literalNumberValue(field.name)"
                :step="field.type === 'integer' ? 1 : undefined"
                :disabled="readOnly"
                @update:model-value="updateLiteralBinding(field.name, $event)"
              />
              <ElInput
                v-else
                :model-value="String(literalBindingValue(field.name) ?? '')"
                :disabled="readOnly"
                @update:model-value="updateLiteralBinding(field.name, $event)"
              />
            </div>
          </section>

          <section v-if="selectedDefinition.secretFields.length" class="inspector__section">
            <h3>密钥</h3>
            <div
              v-for="field in selectedDefinition.secretFields"
              :key="field.name"
              class="secret-field"
            >
              <div>
                <span>{{ field.title }}</span>
                <small>{{ secretConfigured(field.name) ? '已配置' : '未配置' }}</small>
              </div>
              <ElInput
                type="password"
                show-password
                autocomplete="new-password"
                :model-value="secretDrafts[secretKey(field.name)] || ''"
                :placeholder="secretConfigured(field.name) ? '留空保持原值' : '输入密钥'"
                :disabled="readOnly"
                @update:model-value="setSecretDraft(field.name, String($event))"
              />
              <ElCheckbox
                v-if="secretConfigured(field.name) && !field.required"
                :model-value="Boolean(secretRemovals[secretKey(field.name)])"
                :disabled="readOnly"
                @change="setSecretRemoval(field.name, Boolean($event))"
              >
                保存时移除
              </ElCheckbox>
            </div>
          </section>
        </template>
        <template v-else-if="selectedEdge">
          <div class="inspector__heading">
            <div>
              <span>Edge inspector</span>
              <h2>连线条件</h2>
              <code
                >{{ selectedEdge.sourceNodeInstanceId }} →
                {{ selectedEdge.targetNodeInstanceId }}</code
              >
            </div>
            <ElButton
              title="删除连线"
              type="danger"
              text
              circle
              :disabled="readOnly"
              @click="removeSelectedEdge"
            >
              <ArtSvgIcon icon="ri:delete-bin-6-line" />
            </ElButton>
          </div>
          <section class="inspector__section">
            <h3>Boolean CEL</h3>
            <ElInput
              :model-value="selectedEdge.condition || ''"
              type="textarea"
              :rows="5"
              placeholder="event.type == 'manual'"
              :disabled="readOnly"
              @update:model-value="updateEdgeCondition(String($event))"
            />
          </section>
        </template>
        <div v-else class="inspector__placeholder">
          <ArtSvgIcon icon="ri:cursor-line" />
          <strong>选择节点以编辑</strong>
        </div>
      </aside>
    </main>

    <section class="activity-dock">
      <header class="activity-dock__header">
        <div>
          <ArtSvgIcon icon="ri:pulse-line" />
          <strong>运行活动</strong>
          <span v-if="selectedWorkflow">游标 {{ activityCursor }}</span>
        </div>
        <ElButton
          title="刷新活动"
          text
          circle
          :loading="activityLoading"
          :disabled="!selectedWorkflow"
          @click="refreshActivity(true)"
        >
          <ArtSvgIcon icon="ri:refresh-line" />
        </ElButton>
      </header>
      <div class="activity-dock__body">
        <nav class="activity-feed" aria-label="工作流活动">
          <button
            v-for="activity in recentActivities"
            :key="activity.cursor"
            type="button"
            :class="{ 'is-active': activity.batchId === selectedBatch?.id }"
            @click="activity.batchId && loadBatch(activity.batchId)"
          >
            <span class="activity-feed__rail" :data-status="activity.status"></span>
            <span class="activity-feed__copy">
              <strong>{{ activity.summary }}</strong>
              <small>
                #{{ activity.batchId || '-' }} · {{ formatTime(activity.occurredAt) }}
              </small>
            </span>
            <code>{{ activity.cursor }}</code>
          </button>
          <div v-if="!recentActivities.length" class="activity-dock__empty">暂无运行活动</div>
        </nav>

        <div class="batch-path">
          <template v-if="selectedBatch">
            <div class="batch-path__summary">
              <div>
                <strong>批次 #{{ selectedBatch.id }}</strong>
                <span :data-status="selectedBatch.status">{{ selectedBatch.status }}</span>
                <ElButton
                  v-if="['queued', 'running', 'waiting', 'retrying'].includes(selectedBatch.status)"
                  title="取消批次"
                  circle
                  size="small"
                  :loading="batchActionLoading"
                  @click="runBatchAction('cancel')"
                >
                  <ArtSvgIcon icon="ri:stop-circle-line" />
                </ElButton>
                <ElButton
                  v-if="selectedBatch.status === 'failed'"
                  title="重试批次"
                  circle
                  size="small"
                  :loading="batchActionLoading"
                  @click="runBatchAction('retry')"
                >
                  <ArtSvgIcon icon="ri:restart-line" />
                </ElButton>
                <ElButton
                  v-if="['succeeded', 'failed', 'cancelled'].includes(selectedBatch.status)"
                  title="诊断重放"
                  circle
                  size="small"
                  :loading="batchActionLoading"
                  @click="runBatchAction('replay')"
                >
                  <ArtSvgIcon icon="ri:history-line" />
                </ElButton>
              </div>
              <small>
                R{{ selectedBatch.revisionId }} · {{ selectedBatch.triggerType }} ·
                {{ formatDuration(batchDuration) }}
              </small>
            </div>
            <ol class="batch-path__nodes">
              <li v-for="run in selectedBatch.nodeRuns" :key="run.id">
                <span class="batch-path__state" :data-status="run.status"></span>
                <div>
                  <strong>{{ run.nodeInstanceId }}</strong>
                  <small>{{ run.nodeType }}@{{ run.nodeVersion }}</small>
                </div>
                <code>{{ run.executionPool }}</code>
                <span
                  >尝试 {{ run.attempt
                  }}<template v-if="run.loopIteration"> · L{{ run.loopIteration }}</template></span
                >
                <time>{{ formatDuration(run.durationMs) }}</time>
                <span class="batch-path__node-result">
                  <em v-if="run.errorCategory">{{ run.errorCategory }}</em>
                  <ElButton
                    v-if="resultPageForNode(run.nodeType)"
                    title="查看插件结果"
                    text
                    circle
                    size="small"
                    @click="openPluginResult(run)"
                  >
                    <ArtSvgIcon icon="ri:bar-chart-box-line" />
                  </ElButton>
                </span>
              </li>
            </ol>
            <div v-if="selectedBatch.artifacts.length" class="batch-path__artifacts">
              <button
                v-for="artifact in selectedBatch.artifacts"
                :key="`${artifact.nodeInstanceId}:${artifact.sha256}`"
                type="button"
                :title="`下载并校验 ${artifact.sha256}`"
                @click="downloadArtifact(artifact)"
              >
                <ArtSvgIcon icon="ri:download-2-line" />
                <span>{{ artifact.nodeInstanceId }} · {{ formatBytes(artifact.sizeBytes) }}</span>
                <code>{{ artifact.sha256.slice(0, 12) }}</code>
              </button>
            </div>
          </template>
          <div v-else class="activity-dock__empty">选择一条批次活动查看节点路径</div>
        </div>
      </div>
    </section>

    <ElDialog v-model="createDialogVisible" title="创建工作流" width="420px">
      <ElForm label-position="top">
        <ElFormItem label="名称">
          <ElInput v-model="createForm.name" maxlength="120" show-word-limit />
        </ElFormItem>
        <ElFormItem label="描述">
          <ElInput v-model="createForm.description" type="textarea" :rows="3" maxlength="500" />
        </ElFormItem>
        <ElFormItem label="触发方式">
          <ElSelect v-model="createForm.templateKey">
            <ElOption label="手工触发" value="blank" />
            <ElOption label="UTC 定时触发" value="scheduled" />
            <ElOption label="CloudEvent 事件触发" value="event" />
            <ElOption label="标准故障处理" value="failure-handler" />
            <ElOption label="Connector Webhook" value="connector-webhook" />
            <ElOption label="Connector WebSocket" value="connector-websocket" />
            <ElOption label="Binance 共享行情" value="quant-market-data" />
            <ElOption label="实时策略评估" value="quant-strategy" />
            <ElOption label="策略回测" value="quant-backtest" />
            <ElOption label="Paper 策略闭环" value="quant-paper" />
          </ElSelect>
        </ElFormItem>
      </ElForm>
      <template #footer>
        <ElButton @click="createDialogVisible = false">取消</ElButton>
        <ElButton
          type="primary"
          :loading="creating"
          :disabled="!createForm.name.trim()"
          @click="submitCreate"
        >
          创建
        </ElButton>
      </template>
    </ElDialog>

    <ElDialog v-model="humanTasksVisible" title="人工任务" width="min(620px, 92vw)">
      <div v-loading="humanTasksLoading" class="human-task-list">
        <article v-for="task in humanTasks" :key="task.id">
          <div>
            <strong>{{ task.prompt }}</strong>
            <small>{{ task.businessKey }} · {{ formatTime(task.expiresAt) }}</small>
          </div>
          <code>#{{ task.batchId }} / {{ task.nodeInstanceId }}</code>
          <div class="human-task-list__actions">
            <ElButton
              size="small"
              :loading="decidingTaskId === task.id"
              @click="decideTask(task.id, 'reject')"
              >拒绝</ElButton
            >
            <ElButton
              type="primary"
              size="small"
              :loading="decidingTaskId === task.id"
              @click="decideTask(task.id, 'approve')"
              >批准</ElButton
            >
          </div>
        </article>
        <div v-if="!humanTasks.length" class="activity-dock__empty">没有待处理任务</div>
      </div>
    </ElDialog>

    <ElDialog
      v-model="pluginResultVisible"
      title="插件结果"
      width="min(680px, 92vw)"
      destroy-on-close
    >
      <component
        :is="pluginResultComponent"
        v-if="pluginResultComponent && pluginResultContext"
        :result="pluginResultContext"
      />
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import { Graph, Shape } from '@antv/x6'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import type { Component } from 'vue'
  import { registeredFrontendPlugins } from '@/plugins'
  import WorkflowSchemaFields from '@/views/scheduler/workflow/editor/components/WorkflowSchemaFields.vue'
  import {
    applyWorkflowBatchAction,
    applyWorkflowLifecycle,
    createWorkflowBatch,
    createWorkflow,
    downloadWorkflowArtifact,
    decideWorkflowHumanTask,
    fetchWorkflow,
    fetchWorkflowActivity,
    fetchWorkflowArtifactManifest,
    fetchWorkflowBatch,
    fetchWorkflowHumanTasks,
    fetchWorkflowNodeDefinitions,
    fetchWorkflowRevision,
    fetchWorkflowRevisions,
    fetchWorkflows,
    saveWorkflowRevision,
    type WorkflowDetail,
    type WorkflowGraph,
    type WorkflowGraphNode,
    type WorkflowInputBinding,
    type WorkflowItem,
    type WorkflowHumanTask,
    type WorkflowNodeDefinition,
    type WorkflowBindingKind,
    type WorkflowActivity,
    type WorkflowArtifact,
    type WorkflowBatchDetail,
    type WorkflowNodeRun,
    type WorkflowRevision,
    type WorkflowSecretChange
  } from '@/api/workflows'
  import { useUserStore } from '@/store/modules/user'

  const canvasRef = ref<HTMLDivElement>()
  const graphInstance = shallowRef<Graph>()
  const workflows = ref<WorkflowItem[]>([])
  const definitions = ref<WorkflowNodeDefinition[]>([])
  const revisions = ref<WorkflowRevision[]>([])
  const selectedWorkflow = ref<WorkflowDetail>()
  const activeRevision = ref<WorkflowRevision>()
  const activities = ref<WorkflowActivity[]>([])
  const activityCursor = ref(0)
  const activityLoading = ref(false)
  const selectedBatch = ref<WorkflowBatchDetail>()
  const viewRevisionId = ref<number>()
  const selectedNodeId = ref('')
  const selectedEdgeId = ref('')
  const graph = ref<WorkflowGraph>({ schemaVersion: 1, nodes: [], edges: [] })
  const workflowQuery = ref('')
  const loading = ref(true)
  const saving = ref(false)
  const creating = ref(false)
  const runningBatch = ref(false)
  const batchActionLoading = ref(false)
  const humanTasks = ref<WorkflowHumanTask[]>([])
  const humanTasksVisible = ref(false)
  const humanTasksLoading = ref(false)
  const decidingTaskId = ref<number>()
  const pluginResultVisible = ref(false)
  const pluginResultComponent = shallowRef<Component>()
  const pluginResultContext = shallowRef<{
    batch: WorkflowBatchDetail
    nodeRun: WorkflowNodeRun
  }>()
  const dirty = ref(false)
  const applyingGraph = ref(false)
  const createDialogVisible = ref(false)
  const userStore = useUserStore()
  let activitySocket: WebSocket | undefined
  let activityReconnectTimer: ReturnType<typeof setTimeout> | undefined
  const createForm = reactive<{
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
  }>({ name: '', description: '', templateKey: 'blank' })
  const secretDrafts = reactive<Record<string, string>>({})
  const secretRemovals = reactive<Record<string, boolean>>({})
  const bindingKinds = [
    { label: '字段', value: 'field' },
    { label: '固定值', value: 'literal' },
    { label: 'CEL', value: 'cel' }
  ]

  const filteredWorkflows = computed(() => {
    const query = workflowQuery.value.trim().toLowerCase()
    return query
      ? workflows.value.filter((item) =>
          `${item.name} ${item.description}`.toLowerCase().includes(query)
        )
      : workflows.value
  })
  const availableDefinitions = computed(() => definitions.value.filter((item) => item.available))
  const recentActivities = computed(() => [...activities.value].reverse())
  const batchDuration = computed(() => {
    if (!selectedBatch.value?.startedAt) return undefined
    const end = selectedBatch.value.completedAt
      ? new Date(selectedBatch.value.completedAt).getTime()
      : Date.now()
    return Math.max(end - new Date(selectedBatch.value.startedAt).getTime(), 0)
  })
  const selectedNode = computed(() =>
    graph.value.nodes.find((node) => node.nodeInstanceId === selectedNodeId.value)
  )
  const selectedDefinition = computed(() =>
    definitions.value.find((item) => item.type === selectedNode.value?.nodeType)
  )
  const selectedEdge = computed(() =>
    graph.value.edges.find((edge) => edge.edgeId === selectedEdgeId.value)
  )
  const manualTrigger = computed(() =>
    activeRevision.value?.graph.nodes.some(
      (node) =>
        node.nodeInstanceId === activeRevision.value?.mainTriggerNodeId &&
        node.nodeType === 'core.manual'
    )
  )
  const readOnly = computed(
    () =>
      !selectedWorkflow.value ||
      selectedWorkflow.value.status === 'archived' ||
      viewRevisionId.value !== selectedWorkflow.value.activeRevisionId
  )
  const statusLabel = computed(
    () =>
      ({ paused: '已暂停', running: '运行中', needs_attention: '需处理', archived: '已归档' })[
        selectedWorkflow.value?.status || 'paused'
      ]
  )
  const statusType = computed(
    () =>
      ({ paused: 'info', running: 'success', needs_attention: 'warning', archived: 'info' })[
        selectedWorkflow.value?.status || 'paused'
      ] as 'info' | 'success' | 'warning'
  )
  const inputFields = computed(() => schemaProperties(selectedDefinition.value?.inputSchema || {}))
  const ordinaryConfigSchema = computed(() => {
    const schema = JSON.parse(
      JSON.stringify(selectedDefinition.value?.configSchema || {})
    ) as Record<string, any>
    const properties = (schema.properties || {}) as Record<string, Record<string, unknown>>
    const secretFields = new Set(
      Object.entries(properties)
        .filter(([, property]) => property['x-coinsphere-secret'])
        .map(([name]) => name)
    )
    Object.keys(properties).forEach((name) => {
      if (secretFields.has(name)) delete properties[name]
    })
    if (Array.isArray(schema.required))
      schema.required = schema.required.filter((name: string) => !secretFields.has(name))
    return schema
  })

  const cloneGraph = (value: WorkflowGraph): WorkflowGraph => JSON.parse(JSON.stringify(value))
  const formatTime = (value: string) =>
    new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(
      new Date(value)
    )
  const formatDuration = (value?: number) => {
    if (value === undefined) return '进行中'
    if (value < 1000) return `${value} ms`
    return `${(value / 1000).toFixed(value < 10_000 ? 1 : 0)} s`
  }
  const formatBytes = (value: number) => {
    if (value < 1024) return `${value} B`
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`
    return `${(value / (1024 * 1024)).toFixed(1)} MiB`
  }

  const resultPageForNode = (nodeType: string) => {
    if (nodeType.startsWith('official.connector.'))
      return { pluginId: 'official.connector', pageKey: 'connections' }
    if (nodeType === 'official.ai.model_call') return { pluginId: 'official.ai', pageKey: 'calls' }
    if (nodeType.startsWith('official.quant.'))
      return { pluginId: 'official.quant', pageKey: 'quant' }
  }

  const openPluginResult = async (nodeRun: WorkflowNodeRun) => {
    if (!selectedBatch.value) return
    const target = resultPageForNode(nodeRun.nodeType)
    const registration = registeredFrontendPlugins.find((plugin) => plugin.id === target?.pluginId)
    if (!target || !registration) return
    try {
      const module = await registration.load()
      const page = module.resultPages[target.pageKey]
      if (!page) throw new Error('result page is unavailable')
      pluginResultComponent.value = (await page()).default
      pluginResultContext.value = { batch: selectedBatch.value, nodeRun }
      pluginResultVisible.value = true
    } catch {
      ElMessage.error('插件结果页加载失败')
    }
  }
  const schemaProperties = (schema: Record<string, unknown>) => {
    const properties = (schema.properties || {}) as Record<string, Record<string, unknown>>
    return Object.entries(properties).map(([name, property]) => ({
      name,
      title: String(property.title || name),
      type: String(property.type || 'any')
    }))
  }
  const schemaLeafFields = (
    schema: Record<string, unknown>,
    prefix: string[] = []
  ): Array<{ path: string[]; title: string }> => {
    const properties = (schema.properties || {}) as Record<string, Record<string, unknown>>
    return Object.entries(properties).flatMap(([name, property]) => {
      const path = [...prefix, name]
      return property.type === 'object' && property.properties
        ? schemaLeafFields(property, path)
        : [{ path, title: String(property.title || path.join('.')) }]
    })
  }

  const initializeCanvas = () => {
    if (!canvasRef.value || graphInstance.value) return
    const instance = new Graph({
      container: canvasRef.value,
      background: { color: '#f7f8fa' },
      grid: { visible: true, size: 20, type: 'dot', args: { color: '#d6dbe1', thickness: 1 } },
      panning: true,
      mousewheel: { enabled: true, modifiers: ['ctrl', 'meta'], minScale: 0.4, maxScale: 1.8 },
      connecting: {
        allowBlank: false,
        allowLoop: false,
        allowEdge: false,
        snap: true,
        createEdge: () => new Shape.Edge({ attrs: edgeAttrs() })
      },
      interacting: () => !readOnly.value
    })
    instance.on('node:click', ({ node }) => {
      selectedNodeId.value = node.id
      selectedEdgeId.value = ''
    })
    instance.on('edge:click', ({ edge }) => {
      selectedEdgeId.value = edge.id
      selectedNodeId.value = ''
    })
    instance.on('blank:click', () => {
      selectedNodeId.value = ''
      selectedEdgeId.value = ''
    })
    instance.on('node:change:position', syncFromCanvas)
    instance.on('edge:connected', syncFromCanvas)
    instance.on('edge:removed', syncFromCanvas)
    graphInstance.value = instance
  }

  const edgeAttrs = () => ({
    line: {
      stroke: '#8b97a3',
      strokeWidth: 1.5,
      targetMarker: { name: 'block', width: 10, height: 7 }
    }
  })

  const renderGraph = () => {
    const instance = graphInstance.value
    if (!instance) return
    applyingGraph.value = true
    instance.fromJSON({
      nodes: graph.value.nodes.map((node) => {
        const definition = definitions.value.find((item) => item.type === node.nodeType)
        return {
          id: node.nodeInstanceId,
          shape: 'rect',
          x: node.position.x,
          y: node.position.y,
          width: 188,
          height: 64,
          attrs: {
            body: {
              fill: '#ffffff',
              stroke: node.nodeInstanceId === selectedNodeId.value ? '#208a6a' : '#aeb7c0',
              strokeWidth: node.nodeInstanceId === selectedNodeId.value ? 2 : 1,
              rx: 6,
              ry: 6
            },
            label: {
              text: `${definition?.title || node.nodeType}\n${node.nodeType}@${node.nodeVersion}`,
              fill: '#17202a',
              fontSize: 12,
              fontWeight: 600,
              lineHeight: 19
            }
          },
          ports: {
            groups: {
              in: {
                position: 'left',
                attrs: { circle: { r: 5, fill: '#ffffff', stroke: '#3976d6', magnet: 'passive' } }
              },
              out: {
                position: 'right',
                attrs: { circle: { r: 5, fill: '#ffffff', stroke: '#208a6a', magnet: true } }
              }
            },
            items: [
              ...(definition?.inputPorts || []).map((port) => ({ id: port, group: 'in' })),
              ...(definition?.outputPorts || []).map((port) => ({ id: port, group: 'out' }))
            ]
          },
          data: { nodeType: node.nodeType }
        }
      }),
      edges: graph.value.edges.map((edge) => ({
        id: edge.edgeId,
        shape: 'edge',
        source: { cell: edge.sourceNodeInstanceId, port: edge.sourcePort },
        target: { cell: edge.targetNodeInstanceId, port: edge.targetPort },
        attrs: edgeAttrs(),
        data: { condition: edge.condition || '' }
      }))
    })
    requestAnimationFrame(() => (applyingGraph.value = false))
  }

  const syncFromCanvas = () => {
    if (applyingGraph.value || readOnly.value || !graphInstance.value) return
    const positions = new Map(
      graphInstance.value.getNodes().map((node) => [node.id, node.position()])
    )
    graph.value.nodes.forEach((node) => {
      const position = positions.get(node.nodeInstanceId)
      if (position) node.position = { x: Math.round(position.x), y: Math.round(position.y) }
    })
    graph.value.edges = graphInstance.value.getEdges().map((edge) => ({
      edgeId: edge.id,
      sourceNodeInstanceId: edge.getSourceCellId() || '',
      sourcePort: edge.getSourcePortId() || 'out',
      targetNodeInstanceId: edge.getTargetCellId() || '',
      targetPort: edge.getTargetPortId() || 'in',
      condition: String(edge.getData()?.condition || '') || undefined
    }))
    dirty.value = true
  }

  const fit = () => graphInstance.value?.zoomToFit({ padding: 72, maxScale: 1 })
  const zoom = (delta: number) => graphInstance.value?.zoom(delta)

  const loadWorkflowRevision = async (workflowId: number, revisionId: number) => {
    const revision = await fetchWorkflowRevision(workflowId, revisionId)
    activeRevision.value = revision
    viewRevisionId.value = revision.id
    graph.value = cloneGraph(revision.graph)
    selectedNodeId.value = revision.mainTriggerNodeId
    selectedEdgeId.value = ''
    dirty.value = false
    Object.keys(secretDrafts).forEach((key) => delete secretDrafts[key])
    Object.keys(secretRemovals).forEach((key) => delete secretRemovals[key])
    renderGraph()
    requestAnimationFrame(fit)
  }

  const loadBatch = async (batchId: number) => {
    const batch = await fetchWorkflowBatch(batchId)
    if (batch.workflowId === selectedWorkflow.value?.id) selectedBatch.value = batch
  }

  const refreshActivity = async (reset = false) => {
    const workflowId = selectedWorkflow.value?.id
    if (!workflowId || activityLoading.value) return
    activityLoading.value = true
    try {
      const page = await fetchWorkflowActivity(workflowId, reset ? 0 : activityCursor.value)
      if (selectedWorkflow.value?.id !== workflowId) return
      activities.value = (
        reset
          ? page.items
          : [...activities.value, ...page.items].filter(
              (item, index, all) =>
                all.findIndex((candidate) => candidate.cursor === item.cursor) === index
            )
      ).slice(-200)
      activityCursor.value = page.nextCursor
      const latestBatchId = [...page.items].reverse().find((item) => item.batchId)?.batchId
      if (latestBatchId && (reset || !selectedBatch.value)) {
        await loadBatch(latestBatchId)
      } else if (
        selectedBatch.value &&
        page.items.some((item) => item.batchId === selectedBatch.value?.id)
      ) {
        await loadBatch(selectedBatch.value.id)
      }
    } finally {
      activityLoading.value = false
    }
  }

  const disconnectActivitySocket = () => {
    if (activityReconnectTimer) clearTimeout(activityReconnectTimer)
    activityReconnectTimer = undefined
    const socket = activitySocket
    activitySocket = undefined
    socket?.close(1000, 'workflow changed')
  }

  const connectActivitySocket = () => {
    disconnectActivitySocket()
    const workflowId = selectedWorkflow.value?.id
    if (!workflowId || !userStore.accessToken) return
    const url = new URL(`/api/v1/workflows/${workflowId}/activity/ws`, window.location.origin)
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    url.searchParams.set('after', String(activityCursor.value))
    const socket = new WebSocket(url, ['coinsphere.workflow-activity.v1', userStore.accessToken])
    activitySocket = socket
    socket.onmessage = (message) => {
      if (activitySocket !== socket || selectedWorkflow.value?.id !== workflowId) return
      try {
        const item = JSON.parse(message.data) as WorkflowActivity
        if (!Number.isSafeInteger(item.cursor) || item.cursor <= activityCursor.value) return
        activities.value.push(item)
        if (activities.value.length > 200) activities.value.splice(0, activities.value.length - 200)
        activityCursor.value = item.cursor
        if (item.status === 'waiting') void refreshHumanTasks()
      } catch {
        socket.close(1003, 'invalid activity payload')
      }
    }
    socket.onclose = () => {
      if (activitySocket !== socket || selectedWorkflow.value?.id !== workflowId) return
      activitySocket = undefined
      activityReconnectTimer = setTimeout(async () => {
        if (selectedWorkflow.value?.id !== workflowId) return
        await refreshActivity()
        connectActivitySocket()
      }, 1500)
    }
  }

  const selectWorkflow = async (workflow: WorkflowItem) => {
    if (dirty.value && !(await confirmDiscard())) return
    disconnectActivitySocket()
    loading.value = true
    activities.value = []
    activityCursor.value = 0
    selectedBatch.value = undefined
    try {
      const [detail, revisionList] = await Promise.all([
        fetchWorkflow(workflow.id),
        fetchWorkflowRevisions(workflow.id)
      ])
      selectedWorkflow.value = detail
      revisions.value = revisionList.items
      await loadWorkflowRevision(detail.id, detail.activeRevisionId)
      await refreshActivity(true)
      connectActivitySocket()
    } finally {
      loading.value = false
    }
  }

  const selectRevision = async (revisionId: number) => {
    if (!selectedWorkflow.value) return
    if (dirty.value && !(await confirmDiscard())) {
      viewRevisionId.value = activeRevision.value?.id
      return
    }
    loading.value = true
    try {
      await loadWorkflowRevision(selectedWorkflow.value.id, revisionId)
    } finally {
      loading.value = false
    }
  }

  const confirmDiscard = async () => {
    try {
      await ElMessageBox.confirm('当前修订有未保存修改，仍要离开吗？', '未保存修改', {
        type: 'warning',
        confirmButtonText: '离开',
        cancelButtonText: '继续编辑'
      })
      return true
    } catch {
      return false
    }
  }

  const addNode = (definition: WorkflowNodeDefinition) => {
    if (readOnly.value) return
    const base = definition.type.replace(/[^a-z0-9]+/gi, '-')
    let index = 1
    while (graph.value.nodes.some((node) => node.nodeInstanceId === `${base}-${index}`)) index++
    const properties = (definition.configSchema.properties || {}) as Record<
      string,
      Record<string, unknown>
    >
    const config: Record<string, unknown> = {}
    Object.entries(properties).forEach(([key, property]) => {
      if (property['x-coinsphere-secret']) return
      if (property.default !== undefined) config[key] = property.default
    })
    const node: WorkflowGraphNode = {
      nodeInstanceId: `${base}-${index}`,
      nodeType: definition.type,
      nodeVersion: definition.version,
      config,
      position: { x: 260 + graph.value.nodes.length * 36, y: 160 + graph.value.nodes.length * 28 }
    }
    graph.value.nodes.push(node)
    selectedNodeId.value = node.nodeInstanceId
    selectedEdgeId.value = ''
    dirty.value = true
    renderGraph()
  }

  const removeSelectedEdge = () => {
    graph.value.edges = graph.value.edges.filter((edge) => edge.edgeId !== selectedEdgeId.value)
    selectedEdgeId.value = ''
    dirty.value = true
    renderGraph()
  }

  const updateEdgeCondition = (condition: string) => {
    if (!selectedEdge.value || readOnly.value) return
    selectedEdge.value.condition = condition.trim() || undefined
    dirty.value = true
  }

  const removeSelectedNode = () => {
    const id = selectedNodeId.value
    graph.value.nodes = graph.value.nodes.filter((node) => node.nodeInstanceId !== id)
    graph.value.edges = graph.value.edges.filter(
      (edge) => edge.sourceNodeInstanceId !== id && edge.targetNodeInstanceId !== id
    )
    for (const key of Object.keys(secretDrafts))
      if (key.startsWith(`${id}\u0000`)) delete secretDrafts[key]
    for (const key of Object.keys(secretRemovals))
      if (key.startsWith(`${id}\u0000`)) delete secretRemovals[key]
    selectedNodeId.value = ''
    dirty.value = true
    renderGraph()
  }

  const updateConfig = (key: string, value: unknown) => {
    if (!selectedNode.value || readOnly.value) return
    selectedNode.value.config[key] = value
    dirty.value = true
  }

  const bindingFor = (field: string): WorkflowInputBinding =>
    selectedNode.value?.inputBindings?.[field] || { kind: 'literal', value: '' }
  const ensureBindings = () => {
    if (selectedNode.value && !selectedNode.value.inputBindings)
      selectedNode.value.inputBindings = {}
    return selectedNode.value?.inputBindings
  }
  const changeBindingKind = (field: string, kind: WorkflowBindingKind) => {
    const bindings = ensureBindings()
    if (!bindings) return
    bindings[field] =
      kind === 'field'
        ? { kind, fieldPath: [] }
        : kind === 'cel'
          ? { kind, expression: '' }
          : { kind, value: '' }
    dirty.value = true
  }
  const fieldBindingValue = (field: string) => {
    const binding = bindingFor(field)
    return binding.nodeInstanceId && binding.fieldPath?.length
      ? `${binding.nodeInstanceId}:${binding.fieldPath.join('.')}`
      : ''
  }
  const setFieldBinding = (field: string, value: string) => {
    const [nodeInstanceId, path = ''] = value.split(':', 2)
    const bindings = ensureBindings()
    if (!bindings) return
    bindings[field] = { kind: 'field', nodeInstanceId, fieldPath: path.split('.').filter(Boolean) }
    dirty.value = true
  }
  const literalBindingValue = (field: string) => bindingFor(field).value
  const literalNumberValue = (field: string) =>
    typeof literalBindingValue(field) === 'number'
      ? (literalBindingValue(field) as number)
      : undefined
  const updateLiteralBinding = (field: string, value: unknown) => {
    const bindings = ensureBindings()
    if (!bindings) return
    bindings[field] = { kind: 'literal', value }
    dirty.value = true
  }
  const updateCelBinding = (field: string, expression: string) => {
    const bindings = ensureBindings()
    if (!bindings) return
    bindings[field] = { kind: 'cel', expression }
    dirty.value = true
  }

  const upstreamFieldOptions = computed(() => {
    if (!selectedNode.value) return []
    const incoming = new Map<string, string[]>()
    graph.value.nodes.forEach((node) => incoming.set(node.nodeInstanceId, []))
    graph.value.edges.forEach((edge) =>
      incoming.get(edge.targetNodeInstanceId)?.push(edge.sourceNodeInstanceId)
    )
    const upstream = new Set<string>()
    const queue = [...(incoming.get(selectedNode.value.nodeInstanceId) || [])]
    while (queue.length) {
      const id = queue.shift() as string
      if (upstream.has(id)) continue
      upstream.add(id)
      queue.push(...(incoming.get(id) || []))
    }
    return graph.value.nodes.flatMap((node) => {
      if (!upstream.has(node.nodeInstanceId)) return []
      const definition = definitions.value.find((item) => item.type === node.nodeType)
      return schemaLeafFields(definition?.outputSchema || {}).map((field) => ({
        value: `${node.nodeInstanceId}:${field.path.join('.')}`,
        label: `${node.nodeInstanceId} / ${field.title}`
      }))
    })
  })

  const secretConfigured = (field: string) =>
    Boolean(activeRevision.value?.secretFields?.[selectedNodeId.value]?.[field])
  const secretKey = (field: string) => `${selectedNodeId.value}\u0000${field}`
  const setSecretDraft = (field: string, value: string) => {
    const key = secretKey(field)
    secretDrafts[key] = value
    secretRemovals[key] = false
    dirty.value = true
  }
  const setSecretRemoval = (field: string, remove: boolean) => {
    const key = secretKey(field)
    secretRemovals[key] = remove
    if (remove) secretDrafts[key] = ''
    dirty.value = true
  }

  const secretChanges = (): WorkflowSecretChange[] => {
    return [
      ...Object.entries(secretDrafts)
        .filter(([, value]) => value.trim())
        .map(([key, value]) => {
          const [nodeInstanceId, field] = key.split('\u0000', 2)
          return { nodeInstanceId, field, value }
        }),
      ...Object.entries(secretRemovals)
        .filter(([, remove]) => remove)
        .map(([key]) => {
          const [nodeInstanceId, field] = key.split('\u0000', 2)
          return { nodeInstanceId, field, remove: true }
        })
    ]
  }

  const save = async () => {
    if (!selectedWorkflow.value || !activeRevision.value || readOnly.value) return
    saving.value = true
    try {
      const revision = await saveWorkflowRevision(selectedWorkflow.value.id, {
        expectedActiveRevisionId: selectedWorkflow.value.activeRevisionId,
        graph: cloneGraph(graph.value),
        secretChanges: secretChanges()
      })
      selectedWorkflow.value.activeRevisionId = revision.id
      activeRevision.value = revision
      viewRevisionId.value = revision.id
      revisions.value.unshift(revision)
      dirty.value = false
      Object.keys(secretDrafts).forEach((key) => delete secretDrafts[key])
      Object.keys(secretRemovals).forEach((key) => delete secretRemovals[key])
      ElMessage.success(`修订 R${revision.revisionNumber} 已保存`)
    } finally {
      saving.value = false
    }
  }

  const toggleLifecycle = async () => {
    if (!selectedWorkflow.value) return
    const action =
      selectedWorkflow.value.status === 'running' ||
      selectedWorkflow.value.status === 'needs_attention'
        ? 'pause'
        : 'start'
    selectedWorkflow.value = await applyWorkflowLifecycle(selectedWorkflow.value.id, action)
    workflows.value = workflows.value.map((item) =>
      item.id === selectedWorkflow.value?.id ? { ...item, ...selectedWorkflow.value } : item
    )
  }

  const runNow = async () => {
    if (!selectedWorkflow.value) return
    runningBatch.value = true
    try {
      const batch = await createWorkflowBatch(selectedWorkflow.value.id)
      ElMessage.success(`批次 #${batch.id} 已进入队列`)
      await refreshActivity(true)
    } finally {
      runningBatch.value = false
    }
  }

  const runBatchAction = async (action: 'cancel' | 'retry' | 'replay') => {
    if (!selectedBatch.value) return
    batchActionLoading.value = true
    try {
      const batch = await applyWorkflowBatchAction(selectedBatch.value.id, action)
      await Promise.all([loadBatch(batch.id), refreshActivity()])
      ElMessage.success(
        action === 'cancel'
          ? '已请求取消'
          : action === 'retry'
            ? '批次已重新入队'
            : `诊断批次 #${batch.id} 已入队`
      )
    } finally {
      batchActionLoading.value = false
    }
  }

  const refreshHumanTasks = async () => {
    humanTasksLoading.value = true
    try {
      humanTasks.value = (await fetchWorkflowHumanTasks()).items
    } finally {
      humanTasksLoading.value = false
    }
  }

  const openHumanTasks = async () => {
    humanTasksVisible.value = true
    await refreshHumanTasks()
  }

  const decideTask = async (taskId: number, action: 'approve' | 'reject') => {
    decidingTaskId.value = taskId
    try {
      await decideWorkflowHumanTask(taskId, action)
      await Promise.all([refreshHumanTasks(), refreshActivity()])
      ElMessage.success(action === 'approve' ? '任务已批准' : '任务已拒绝')
    } finally {
      decidingTaskId.value = undefined
    }
  }

  const downloadArtifact = async (artifact: WorkflowArtifact) => {
    const manifest = await fetchWorkflowArtifactManifest(artifact.sha256)
    const blob = await downloadWorkflowArtifact(artifact.downloadUrl)
    const digest = Array.from(
      new Uint8Array(await crypto.subtle.digest('SHA-256', await blob.arrayBuffer()))
    )
      .map((value) => value.toString(16).padStart(2, '0'))
      .join('')
    if (!manifest.verified || digest !== artifact.sha256) {
      ElMessage.error('制品校验失败')
      return
    }
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `${artifact.sha256}.${artifact.mediaType === 'application/json' ? 'json' : 'bin'}`
    link.click()
    URL.revokeObjectURL(url)
  }

  const archiveWorkflow = async () => {
    if (!selectedWorkflow.value) return
    try {
      await ElMessageBox.confirm('归档后工作流及其修订将永久只读。', '归档工作流', {
        type: 'warning',
        confirmButtonText: '归档',
        cancelButtonText: '取消'
      })
      selectedWorkflow.value = await applyWorkflowLifecycle(selectedWorkflow.value.id, 'archive')
      workflows.value = workflows.value.map((item) =>
        item.id === selectedWorkflow.value?.id ? { ...item, ...selectedWorkflow.value } : item
      )
    } catch {
      // 用户取消归档。
    }
  }

  const submitCreate = async () => {
    creating.value = true
    try {
      const created = await createWorkflow({
        name: createForm.name.trim(),
        description: createForm.description.trim(),
        templateKey: createForm.templateKey
      })
      createDialogVisible.value = false
      createForm.name = ''
      createForm.description = ''
      createForm.templateKey = 'blank'
      const list = await fetchWorkflows()
      workflows.value = list.items
      await selectWorkflow(created)
    } finally {
      creating.value = false
    }
  }

  onMounted(async () => {
    if (!window.matchMedia('(max-width: 900px)').matches) initializeCanvas()
    try {
      const [workflowList, nodeList, taskList] = await Promise.all([
        fetchWorkflows(),
        fetchWorkflowNodeDefinitions(),
        fetchWorkflowHumanTasks()
      ])
      workflows.value = workflowList.items
      definitions.value = nodeList.items
      humanTasks.value = taskList.items
      if (workflows.value[0]) await selectWorkflow(workflows.value[0])
    } finally {
      loading.value = false
    }
  })

  onBeforeUnmount(() => {
    graphInstance.value?.dispose()
    disconnectActivitySocket()
  })
  onBeforeRouteLeave(async () => !dirty.value || (await confirmDiscard()))
</script>

<style scoped lang="scss">
  .workflow-workbench {
    --ink: #17202a;
    --canvas: #f7f8fa;
    --green: #208a6a;
    --amber: #d58a1f;
    --blue: #3976d6;
    --red: #c84f4f;

    min-width: 0;
    overflow: hidden;
    color: var(--ink);
    background: #eef1f4;
  }

  .revision-track {
    display: grid;
    grid-template-columns: minmax(220px, 1fr) auto minmax(260px, 1fr);
    gap: 18px;
    align-items: center;
    min-height: 72px;
    padding: 10px 18px;
    background: #fff;
    border-bottom: 1px solid #d8dde2;
  }

  .revision-track__identity,
  .revision-track__facts,
  .revision-track__actions,
  .revision-track__actions > *,
  .mobile-activity__summary {
    display: flex;
    align-items: center;
  }

  .revision-track__identity {
    gap: 11px;
    min-width: 0;
  }

  .revision-track__mark {
    display: grid;
    place-items: center;
    width: 38px;
    height: 38px;
    font-size: 20px;
    color: #fff;
    background: var(--ink);
    border-radius: 6px;
  }

  .revision-track__eyebrow {
    font-size: 10px;
    font-weight: 700;
    color: #71808e;
    text-transform: uppercase;
  }

  .revision-track h1 {
    margin: 1px 0 0;
    overflow: hidden;
    font-size: 18px;
    line-height: 24px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .revision-track__facts {
    gap: 10px;
    justify-content: center;
  }

  .revision-track__revision {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 13px;
    font-weight: 700;
  }

  .revision-track__dirty {
    font-size: 12px;
    color: #7d8994;
  }

  .revision-track__dirty.is-dirty {
    color: var(--amber);
  }

  .revision-track__actions {
    gap: 8px;
    justify-content: flex-end;
  }

  .revision-track__select {
    width: 92px;
  }

  .workbench-grid {
    display: grid;
    grid-template-columns: 260px minmax(420px, 1fr) 330px;
    height: calc(100% - 294px);
  }

  .activity-dock {
    height: 222px;
    background: #fff;
    border-top: 1px solid #cfd5db;
  }

  .activity-dock__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    min-height: 36px;
    padding: 0 12px;
    border-bottom: 1px solid #e1e5e9;
  }

  .activity-dock__header > div {
    display: flex;
    gap: 7px;
    align-items: center;
  }

  .activity-dock__header strong {
    font-size: 12px;
  }

  .activity-dock__header span {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 10px;
    color: #778490;
  }

  .activity-dock__body {
    display: grid;
    grid-template-columns: minmax(300px, 36%) minmax(0, 1fr);
    height: 185px;
    min-height: 0;
  }

  .activity-feed,
  .batch-path {
    min-width: 0;
    overflow: auto;
  }

  .activity-feed {
    border-right: 1px solid #d8dde2;
  }

  .activity-feed > button {
    display: grid;
    grid-template-columns: 4px minmax(0, 1fr) auto;
    gap: 9px;
    align-items: center;
    width: 100%;
    min-height: 46px;
    padding: 6px 10px 6px 0;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
    border-bottom: 1px solid #edf0f2;
  }

  .activity-feed > button:hover,
  .activity-feed > button.is-active {
    background: #f3f7f6;
  }

  .activity-feed__rail {
    width: 4px;
    height: 100%;
    background: #98a4ae;
  }

  [data-status='running'],
  [data-status='succeeded'] {
    background: var(--green);
  }

  [data-status='retrying'],
  [data-status='waiting'],
  [data-status='needs_attention'] {
    background: var(--amber);
  }

  [data-status='failed'],
  [data-status='cancelled'] {
    background: var(--red);
  }

  .activity-feed__copy {
    min-width: 0;
  }

  .activity-feed strong,
  .activity-feed small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .activity-feed strong {
    font-size: 12px;
  }

  .activity-feed small,
  .activity-feed code,
  .batch-path small,
  .batch-path code,
  .batch-path time {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 10px;
    color: #75828d;
  }

  .batch-path {
    padding: 9px 12px;
  }

  .batch-path__summary,
  .batch-path__summary > div {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .batch-path__summary > div {
    gap: 8px;
  }

  .batch-path__summary strong {
    font-size: 12px;
  }

  .batch-path__summary span {
    padding-left: 8px;
    font-size: 10px;
    color: #64717c;
    background: transparent;
    border-left: 3px solid #98a4ae;
  }

  .batch-path__summary span[data-status='succeeded'] {
    border-left-color: var(--green);
  }

  .batch-path__summary span[data-status='failed'],
  .batch-path__summary span[data-status='cancelled'] {
    border-left-color: var(--red);
  }

  .batch-path__nodes,
  .batch-path__artifacts {
    padding: 0;
    margin: 8px 0 0;
    list-style: none;
  }

  .batch-path__nodes li {
    display: grid;
    grid-template-columns: 7px minmax(150px, 1fr) 62px 62px 62px minmax(80px, auto);
    gap: 8px;
    align-items: center;
    min-height: 34px;
    border-top: 1px solid #edf0f2;
  }

  .human-task-list article {
    display: grid;
    grid-template-columns: minmax(180px, 1fr) minmax(120px, auto) auto;
    gap: 16px;
    align-items: center;
    min-height: 58px;
    border-top: 1px solid #e5e9ed;
  }

  .human-task-list article:first-child {
    border-top: 0;
  }

  .human-task-list strong,
  .human-task-list small {
    display: block;
  }

  .human-task-list small,
  .human-task-list code {
    margin-top: 4px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    color: #75828d;
  }

  .human-task-list__actions {
    display: flex;
    gap: 8px;
  }

  .batch-path__nodes strong,
  .batch-path__nodes small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .batch-path__nodes strong {
    font-size: 11px;
  }

  .batch-path__state {
    width: 7px;
    height: 7px;
    background: #98a4ae;
    border-radius: 50%;
  }

  .batch-path__nodes em {
    overflow: hidden;
    font-size: 10px;
    font-style: normal;
    color: var(--red);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .batch-path__node-result {
    display: flex;
    gap: 4px;
    align-items: center;
    justify-content: flex-end;
    min-width: 0;
  }

  .batch-path__artifacts {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .batch-path__artifacts button {
    display: grid;
    grid-template-columns: 16px auto auto;
    gap: 6px;
    align-items: center;
    min-height: 28px;
    padding: 3px 7px;
    color: inherit;
    cursor: pointer;
    background: #f4f6f8;
    border: 1px solid #d8dde2;
    border-radius: 4px;
  }

  .activity-dock__empty {
    display: grid;
    place-items: center;
    min-height: 100%;
    padding: 18px;
    font-size: 12px;
    color: #7d8994;
  }

  .workflow-rail,
  .inspector {
    min-width: 0;
    overflow: hidden;
    background: #fff;
  }

  .workflow-rail {
    display: grid;
    grid-template-rows: minmax(250px, 1fr) minmax(210px, 0.8fr);
    border-right: 1px solid #d8dde2;
  }

  .rail-section {
    min-height: 0;
    padding: 14px;
    overflow: hidden;
  }

  .rail-section--workflows {
    border-bottom: 1px solid #e1e5e9;
  }

  .rail-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    min-height: 28px;
    margin-bottom: 10px;
    font-size: 12px;
    font-weight: 750;
    text-transform: uppercase;
  }

  .workflow-list,
  .node-catalog {
    display: flex;
    flex-direction: column;
    gap: 4px;
    height: calc(100% - 76px);
    margin-top: 9px;
    overflow: auto;
  }

  .node-catalog {
    height: calc(100% - 36px);
    margin-top: 0;
  }

  .workflow-row,
  .node-catalog button {
    display: grid;
    width: 100%;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
  }

  .workflow-row {
    grid-template-columns: 8px minmax(0, 1fr) 16px;
    gap: 8px;
    align-items: center;
    min-height: 50px;
    padding: 7px 8px;
    border-left: 2px solid transparent;
  }

  .workflow-row:hover {
    background: #f4f6f8;
  }

  .workflow-row.is-active {
    background: #eef7f4;
    border-left-color: var(--green);
  }

  .workflow-row__status {
    width: 7px;
    height: 7px;
    background: #99a4ae;
    border-radius: 50%;
  }

  .workflow-row__status[data-status='running'] {
    background: var(--green);
  }

  .workflow-row__status[data-status='needs_attention'] {
    background: var(--amber);
  }

  .workflow-row__copy {
    min-width: 0;
  }

  .workflow-row strong,
  .workflow-row small,
  .node-catalog strong,
  .node-catalog small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .workflow-row strong,
  .node-catalog strong {
    font-size: 13px;
  }

  .workflow-row small,
  .node-catalog small {
    margin-top: 3px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 10px;
    color: #778490;
  }

  .rail-empty {
    padding: 24px 8px;
    font-size: 12px;
    color: #8895a0;
    text-align: center;
  }

  .node-catalog button {
    grid-template-columns: 32px minmax(0, 1fr) 18px;
    gap: 9px;
    align-items: center;
    min-height: 46px;
    padding: 6px;
    border: 1px solid transparent;
    border-radius: 5px;
  }

  .node-catalog button:hover:not(:disabled) {
    background: #f8fafb;
    border-color: #ccd3da;
  }

  .node-catalog button:disabled {
    cursor: default;
    opacity: 0.55;
  }

  .node-catalog__kind {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    color: var(--blue);
    background: #edf3fc;
    border-radius: 4px;
  }

  .node-catalog__kind.is-trigger {
    color: var(--amber);
    background: #fbf3e6;
  }

  .canvas-stage {
    position: relative;
    min-width: 0;
    overflow: hidden;
    background: var(--canvas);
  }

  .canvas-stage__toolbar {
    position: absolute;
    top: 12px;
    left: 14px;
    z-index: 2;
    display: flex;
    gap: 12px;
    align-items: center;
    padding: 5px 7px 5px 10px;
    font-size: 11px;
    color: #596875;
    background: rgb(255 255 255 / 0.94);
    border: 1px solid #d5dbe0;
    border-radius: 5px;
    box-shadow: 0 3px 10px rgb(23 32 42 / 0.06);
  }

  .canvas-stage__toolbar div {
    display: flex;
    border-left: 1px solid #dce1e5;
  }

  .canvas-stage__toolbar button {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    color: var(--ink);
    cursor: pointer;
    background: transparent;
    border: 0;
  }

  .canvas-stage__surface {
    width: 100%;
    height: 100%;
  }

  .canvas-stage__empty {
    position: absolute;
    inset: 0;
    display: grid;
    gap: 8px;
    place-content: center;
    justify-items: center;
    color: #7b8995;
    pointer-events: none;
  }

  .canvas-stage__empty :deep(svg) {
    font-size: 34px;
  }

  .inspector {
    overflow-y: auto;
    border-left: 1px solid #d8dde2;
  }

  .inspector__heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    padding: 18px;
    border-bottom: 1px solid #e1e5e9;
  }

  .inspector__heading span {
    font-size: 10px;
    font-weight: 700;
    color: #71808e;
    text-transform: uppercase;
  }

  .inspector__heading h2 {
    margin: 4px 0;
    font-size: 17px;
  }

  .inspector__heading code {
    font-size: 10px;
    color: #697784;
  }

  .inspector__section {
    padding: 16px 18px;
    border-bottom: 1px solid #e1e5e9;
  }

  .inspector__section h3 {
    margin: 0 0 13px;
    font-size: 12px;
    text-transform: uppercase;
  }

  .inspector__empty {
    margin: 0;
    font-size: 12px;
    color: #84909a;
  }

  .inspector__placeholder {
    display: grid;
    gap: 8px;
    place-content: center;
    justify-items: center;
    min-height: 300px;
    color: #89959f;
  }

  .inspector__placeholder :deep(svg) {
    font-size: 30px;
  }

  .binding-field,
  .secret-field {
    display: grid;
    gap: 8px;
    margin-bottom: 16px;
  }

  .binding-field:last-child,
  .secret-field:last-child {
    margin-bottom: 0;
  }

  .binding-field__label,
  .secret-field > div {
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 12px;
    font-weight: 650;
  }

  .binding-field__label small,
  .secret-field small {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 10px;
    font-weight: 500;
    color: #7c8994;
  }

  :deep(.el-form-item__label) {
    font-size: 12px;
  }

  :deep(.el-segmented) {
    width: 100%;
  }

  .mobile-activity {
    display: none;
  }

  @media (max-width: 900px) {
    .workflow-workbench {
      overflow: auto;
    }

    .revision-track {
      grid-template-columns: minmax(0, 1fr) auto;
      min-height: 64px;
      padding: 9px 12px;
    }

    .revision-track__facts {
      display: none;
    }

    .revision-track__actions {
      gap: 6px;
    }

    .revision-track__actions > .el-button,
    .revision-track__select {
      display: none;
    }

    .revision-track h1 {
      font-size: 16px;
    }

    .mobile-activity {
      display: block;
      padding: 18px 16px;
      background: #fff;
    }

    .mobile-activity__summary {
      gap: 9px;
    }

    .mobile-activity__summary strong {
      min-width: 0;
      overflow-wrap: anywhere;
    }

    .mobile-activity__summary > span:last-child {
      margin-left: auto;
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    }

    .mobile-activity__empty {
      color: #7d8994;
    }

    .mobile-activity__events,
    .mobile-activity__nodes {
      padding: 0;
      margin: 18px 0 0;
      list-style: none;
      border-top: 1px solid #e1e5e9;
    }

    .mobile-activity__events button,
    .mobile-activity__nodes li {
      display: grid;
      grid-template-columns: 7px minmax(0, 1fr) auto;
      gap: 9px;
      align-items: center;
      width: 100%;
      min-height: 44px;
      padding: 9px 0;
      font-size: 13px;
      color: inherit;
      text-align: left;
      background: transparent;
      border: 0;
      border-bottom: 1px solid #e8ebee;
    }

    .mobile-activity__events button > span,
    .mobile-activity__nodes li > span {
      width: 7px;
      height: 7px;
      background: #98a4ae;
      border-radius: 50%;
    }

    .mobile-activity__events span[data-status='running'],
    .mobile-activity__events span[data-status='succeeded'],
    .mobile-activity__nodes span[data-status='running'],
    .mobile-activity__nodes span[data-status='succeeded'] {
      background: var(--green);
    }

    .mobile-activity__events span[data-status='retrying'],
    .mobile-activity__nodes span[data-status='retrying'] {
      background: var(--amber);
    }

    .mobile-activity__events span[data-status='failed'],
    .mobile-activity__events span[data-status='cancelled'],
    .mobile-activity__nodes span[data-status='failed'],
    .mobile-activity__nodes span[data-status='cancelled'] {
      background: var(--red);
    }

    .mobile-activity__events time,
    .mobile-activity__nodes small {
      font-size: 10px;
      color: #73808c;
    }

    .mobile-activity__nodes strong {
      overflow-wrap: anywhere;
    }

    .workbench-grid,
    .activity-dock {
      display: none;
    }
  }

  @media (max-width: 620px) {
    .human-task-list article {
      grid-template-columns: 1fr;
      gap: 8px;
      padding: 12px 0;
    }
  }
</style>
