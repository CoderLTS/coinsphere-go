<template>
  <main class="workbench" v-loading="loading && !workbench">
    <header class="workbench__header">
      <div>
        <span class="workbench__eyebrow">COINSPHERE / WORKFLOWS</span>
        <h1>工作流工作台</h1>
        <p>设计、运行和处理业务流程</p>
      </div>
      <div class="workbench__actions">
        <ElTooltip content="系统首页" placement="bottom">
          <ElButton :icon="House" circle @click="router.push('/home')" />
        </ElTooltip>
        <ElTooltip content="刷新" placement="bottom">
          <ElButton :icon="Refresh" circle :loading="loading" @click="loadWorkbench" />
        </ElTooltip>
        <ElButton
          v-if="hasPermission('scheduler.workflow_definitions.create')"
          type="primary"
          :icon="Plus"
          @click="router.push('/workflows/new')"
        >
          新建工作流
        </ElButton>
      </div>
    </header>

    <section class="workbench__summary">
      <div class="summary-cell">
        <span>工作流</span><strong>{{ workflows.length }}</strong>
        <small>{{ activeWorkflowCount }} 个已激活</small>
      </div>
      <div class="summary-cell summary-cell--teal">
        <span>正在执行</span><strong>{{ executions.length }}</strong>
        <small>{{ runningCount }} 个运行中</small>
      </div>
      <button class="summary-cell summary-cell--amber" type="button" @click="openActions()">
        <span>人工待办</span><strong>{{ actions.length }}</strong>
        <small>{{ actions.length ? '需要处理' : '当前已清空' }}</small>
      </button>
      <div class="summary-cell summary-cell--risk">
        <span>队列关注</span><strong>{{ attentionCount }}</strong>
        <small>等待或取消中的执行</small>
      </div>
    </section>

    <section class="workbench__grid">
      <article class="workbench-panel workbench-panel--workflows">
        <header class="panel-head">
          <div><h2>工作流与模板</h2><span>最新定义与运行状态</span></div>
          <ElInput
            v-model="keyword"
            class="panel-search"
            clearable
            :prefix-icon="Search"
            placeholder="搜索工作流"
          />
        </header>

        <div v-if="filteredWorkflows.length" class="workflow-list">
          <div v-for="workflow in filteredWorkflows" :key="workflow.id" class="workflow-row">
            <button type="button" class="workflow-row__main" @click="openWorkflow(workflow.id)">
              <span class="workflow-row__mark" :class="{ 'is-active': workflow.isWorkflowActive }">
                <ArtSvgIcon icon="ri:node-tree" />
              </span>
              <span class="row-copy">
                <strong>{{ workflow.displayName }}</strong>
                <small>{{ workflow.description || workflow.code }}</small>
              </span>
            </button>
            <div class="workflow-row__facts">
              <span>v{{ workflow.version }}</span>
              <span>{{ workflow.executionCount }} 次执行</span>
              <ElTag
                :type="
                  workflow.isWorkflowActive ? 'success' : workflow.isBuiltin ? 'warning' : 'info'
                "
                effect="plain"
              >
                {{ workflow.isBuiltin ? '模板' : workflow.isWorkflowActive ? '已激活' : '草稿' }}
              </ElTag>
            </div>
            <div class="workflow-row__buttons">
              <ElTooltip
                :content="workflow.isBuiltin ? '先从模板创建副本' : '运行'"
                placement="top"
              >
                <ElButton
                  :icon="VideoPlay"
                  circle
                  :disabled="
                    !hasPermission('scheduler.workflow_definitions.run') ||
                    workflow.isBuiltin ||
                    !manualEntries(workflow).length
                  "
                  @click="openRun(workflow)"
                />
              </ElTooltip>
              <ElTooltip :content="workflow.isBuiltin ? '使用模板' : '编辑'" placement="top">
                <ElButton :icon="Edit" circle @click="openWorkflow(workflow.id)" />
              </ElTooltip>
            </div>
          </div>
        </div>
        <ElEmpty v-else description="还没有工作流">
          <ElButton
            v-if="hasPermission('scheduler.workflow_definitions.create')"
            type="primary"
            @click="router.push('/workflows/new')"
          >
            创建第一个工作流
          </ElButton>
        </ElEmpty>
      </article>

      <aside class="workbench__rail">
        <article class="workbench-panel">
          <header class="panel-head">
            <div><h2>执行队列</h2><span>非终态执行</span></div>
          </header>
          <div v-if="executions.length" class="execution-list">
            <button
              v-for="execution in executions"
              :key="execution.id"
              type="button"
              class="execution-row"
              @click="router.push(`/runs/${execution.id}`)"
            >
              <span :class="['execution-state', `is-${statusTone(execution.status)}`]"></span>
              <span class="row-copy">
                <strong>{{ execution.workflowDefinitionName }}</strong>
                <small>#{{ execution.id }} · {{ formatTime(execution.queuedAt) }}</small>
              </span>
              <span>{{ execution.statusLabel || statusLabel(execution.status) }}</span>
            </button>
          </div>
          <ElEmpty v-else :image-size="52" description="没有进行中的执行" />
        </article>

        <article class="workbench-panel workbench-panel--actions">
          <header class="panel-head">
            <div><h2>待处理动作</h2><span>审批与失败处置</span></div>
            <ElButton v-if="actions.length" link type="primary" @click="openActions()"
              >全部</ElButton
            >
          </header>
          <div v-if="actions.length" class="pending-list">
            <button
              v-for="action in actions.slice(0, 5)"
              :key="action.id"
              type="button"
              @click="openActions(action.id)"
            >
              <span class="row-copy">
                <strong>{{ action.title }}</strong
                ><small>{{ formatTime(action.createdAt) }}</small>
              </span>
              <ArtSvgIcon icon="ri:arrow-right-s-line" />
            </button>
          </div>
          <ElEmpty v-else :image-size="52" description="没有待处理动作" />
        </article>
      </aside>
    </section>

    <WorkflowRunDialog v-model="runVisible" :workflow="runWorkflow" @started="handleRunStarted" />

    <WorkflowActionDrawer
      v-model="actionsVisible"
      :actions="actions"
      :initial-action-id="selectedActionId"
      @decided="handleActionDecided"
    />
  </main>
</template>

<script setup lang="ts">
  import { Edit, House, Plus, Refresh, Search, VideoPlay } from '@element-plus/icons-vue'
  import {
    fetchWorkflowWorkbench,
    type RunWorkflowDefinitionResponse,
    type WorkflowDefinitionItem,
    type WorkflowWorkbench
  } from '@/api/scheduler'
  import { useUserStore } from '@/store/modules/user'
  import WorkflowActionDrawer from './components/WorkflowActionDrawer.vue'
  import WorkflowRunDialog from './components/WorkflowRunDialog.vue'

  defineOptions({ name: 'WorkflowWorkbenchPage' })

  const router = useRouter()
  const userStore = useUserStore()
  const loading = ref(false)
  const workbench = ref<WorkflowWorkbench | null>(null)
  const keyword = ref('')
  const actionsVisible = ref(false)
  const selectedActionId = ref('')
  const runVisible = ref(false)
  const runWorkflow = ref<WorkflowDefinitionItem | null>(null)
  let pollTimer: number | null = null

  const workflows = computed(() => workbench.value?.workflows || [])
  const executions = computed(() => workbench.value?.executions || [])
  const actions = computed(() => workbench.value?.actions || [])
  const activeWorkflowCount = computed(
    () => workflows.value.filter((item) => item.isWorkflowActive).length
  )
  const runningCount = computed(
    () => executions.value.filter((item) => item.status === 'running').length
  )
  const attentionCount = computed(
    () =>
      executions.value.filter((item) => !['queued', 'running'].includes(String(item.status))).length
  )
  const filteredWorkflows = computed(() => {
    const search = keyword.value.trim().toLowerCase()
    if (!search) return workflows.value
    return workflows.value.filter((item) =>
      [item.displayName, item.description, item.code].some((value) =>
        String(value || '')
          .toLowerCase()
          .includes(search)
      )
    )
  })
  const hasPermission = (permission: string) => userStore.info.permissions.includes(permission)

  const manualEntries = (workflow: WorkflowDefinitionItem) =>
    workflow.graph.nodes
      .filter((node) => node.type === 'start.manual')
      .map((node) => ({
        value: String(node.config?.entryKey || '').trim(),
        label: String(node.config?.displayName || node.label || '手动开始').trim()
      }))
      .filter((item) => item.value)

  const statusLabel = (status: string) =>
    ({
      queued: '排队中',
      running: '运行中',
      retry_waiting: '等待重试',
      waiting_job: '等待任务',
      waiting_action: '等待处理',
      cancel_requested: '取消中'
    })[status] || status
  const statusTone = (status: string) =>
    status === 'waiting_action' || status === 'retry_waiting'
      ? 'amber'
      : status === 'cancel_requested'
        ? 'risk'
        : 'teal'
  const formatTime = (value: string) =>
    value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '--'

  const loadWorkbench = async () => {
    loading.value = true
    try {
      workbench.value = await fetchWorkflowWorkbench()
    } finally {
      loading.value = false
    }
  }
  const openWorkflow = (id: number) => router.push(`/workflows/${id}`)
  const openActions = (id = '') => {
    selectedActionId.value = id
    actionsVisible.value = true
  }
  const handleActionDecided = () => void loadWorkbench()
  const openRun = (workflow: WorkflowDefinitionItem) => {
    if (!hasPermission('scheduler.workflow_definitions.run') || workflow.isBuiltin) return
    const entries = manualEntries(workflow)
    if (!entries.length) return
    runWorkflow.value = workflow
    runVisible.value = true
  }
  const handleRunStarted = async (result: RunWorkflowDefinitionResponse) => {
    await loadWorkbench()
    if (result.executions[0]) await router.push(`/runs/${result.executions[0].id}`)
  }

  onMounted(() => {
    void loadWorkbench()
    pollTimer = window.setInterval(() => {
      if (!document.hidden) void loadWorkbench()
    }, 10_000)
  })
  onBeforeUnmount(() => {
    if (pollTimer) window.clearInterval(pollTimer)
  })
</script>

<style scoped lang="scss">
  .workbench {
    --wb-teal: #10a6a6;
    --wb-amber: #d69a2d;
    --wb-risk: #d94b55;
    min-height: 100%;
    padding: 24px;
    color: var(--art-gray-900);
    letter-spacing: 0;
  }

  .workbench__header,
  .workbench__actions,
  .panel-head,
  .workflow-row,
  .workflow-row__facts,
  .workflow-row__buttons {
    display: flex;
    align-items: center;
  }

  .workbench__header {
    justify-content: space-between;
    gap: 24px;
    padding-bottom: 20px;
    border-bottom: 1px solid var(--art-card-border);

    h1 {
      margin: 2px 0 3px;
      font-size: 26px;
      line-height: 34px;
      letter-spacing: 0;
    }
    p,
    .workbench__eyebrow {
      margin: 0;
      color: var(--art-gray-600);
    }
  }

  .workbench__eyebrow {
    font-family: 'Cascadia Code', Consolas, monospace;
    font-size: 11px;
  }
  .workbench__actions {
    gap: 8px;
  }

  .workbench__summary {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    border-bottom: 1px solid var(--art-card-border);
  }

  .summary-cell {
    position: relative;
    display: flex;
    min-height: 112px;
    padding: 18px 20px 16px;
    color: inherit;
    text-align: left;
    flex-direction: column;
    background: transparent;
    border: 0;
    border-right: 1px solid var(--art-card-border);

    &:last-child {
      border-right: 0;
    }
    &::before {
      position: absolute;
      top: 0;
      right: 20px;
      left: 20px;
      height: 2px;
      content: '';
      background: var(--art-gray-400);
    }
    span,
    small {
      color: var(--art-gray-600);
    }
    strong {
      margin: 5px 0 2px;
      font-size: 28px;
      line-height: 34px;
    }
  }

  button.summary-cell {
    cursor: pointer;
  }
  .summary-cell--teal::before {
    background: var(--wb-teal);
  }
  .summary-cell--amber::before {
    background: var(--wb-amber);
  }
  .summary-cell--risk::before {
    background: var(--wb-risk);
  }

  .workbench__grid {
    display: grid;
    grid-template-columns: minmax(0, 1.65fr) minmax(340px, 0.75fr);
    min-height: 520px;
    margin-top: 20px;
    border: 1px solid var(--art-card-border);
    border-radius: 8px;
    overflow: hidden;
  }

  .workbench__rail {
    border-left: 1px solid var(--art-card-border);
    .workbench-panel + .workbench-panel {
      border-top: 1px solid var(--art-card-border);
    }
  }

  .workbench-panel {
    min-width: 0;
    padding: 18px;
    background: var(--el-bg-color);
  }
  .workbench-panel--workflows {
    min-height: 100%;
  }

  .panel-head {
    justify-content: space-between;
    gap: 16px;
    min-height: 42px;
    margin-bottom: 12px;

    h2 {
      margin: 0 0 2px;
      font-size: 16px;
      letter-spacing: 0;
    }
    span {
      font-size: 12px;
      color: var(--art-gray-600);
    }
  }

  .panel-search {
    width: 210px;
  }
  .workflow-list,
  .execution-list,
  .pending-list {
    display: flex;
    flex-direction: column;
  }

  .workflow-row {
    min-height: 72px;
    padding: 10px 2px;
    border-top: 1px solid var(--art-card-border);
  }

  .workflow-row__main {
    display: flex;
    min-width: 0;
    flex: 1;
    gap: 12px;
    align-items: center;
    padding: 0;
    color: inherit;
    text-align: left;
    background: none;
    border: 0;
    cursor: pointer;
  }

  .workflow-row__mark {
    display: grid;
    width: 38px;
    height: 38px;
    color: var(--art-gray-700);
    background: var(--art-gray-200);
    border-radius: 6px;
    place-items: center;

    &.is-active {
      color: var(--wb-teal);
      background: color-mix(in srgb, var(--wb-teal) 12%, var(--el-bg-color));
    }
  }

  .row-copy {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 4px;

    strong,
    small {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    small {
      color: var(--art-gray-600);
    }
  }

  .workflow-row__facts {
    gap: 14px;
    margin: 0 18px;
    font-size: 12px;
    color: var(--art-gray-600);
  }
  .workflow-row__buttons {
    gap: 6px;
  }

  .execution-row,
  .pending-list button {
    display: grid;
    grid-template-columns: 7px minmax(0, 1fr) auto;
    gap: 10px;
    align-items: center;
    min-height: 58px;
    padding: 9px 0;
    color: inherit;
    text-align: left;
    background: none;
    border: 0;
    border-top: 1px solid var(--art-card-border);
    cursor: pointer;

    > span:last-child {
      font-size: 12px;
      color: var(--art-gray-600);
    }
  }

  .execution-state {
    width: 7px;
    height: 28px;
    background: var(--art-gray-400);
    border-radius: 2px;
  }
  .execution-state.is-teal {
    background: var(--wb-teal);
  }
  .execution-state.is-amber {
    background: var(--wb-amber);
  }
  .execution-state.is-risk {
    background: var(--wb-risk);
  }

  .pending-list button {
    grid-template-columns: minmax(0, 1fr) auto;
    &:hover strong {
      color: var(--wb-amber);
    }
  }

  @media (max-width: 1100px) {
    .workbench__grid {
      grid-template-columns: 1fr;
    }
    .workbench__rail {
      display: grid;
      grid-template-columns: 1fr 1fr;
      border-top: 1px solid var(--art-card-border);
      border-left: 0;
    }
    .workbench__rail .workbench-panel + .workbench-panel {
      border-top: 0;
      border-left: 1px solid var(--art-card-border);
    }
  }

  @media (max-width: 768px) {
    .workbench {
      padding: 14px;
    }
    .workbench__header {
      align-items: flex-start;
    }
    .workbench__header h1 {
      font-size: 22px;
    }
    .workbench__summary {
      grid-template-columns: 1fr 1fr;
    }
    .summary-cell:nth-child(2) {
      border-right: 0;
    }
    .summary-cell:nth-child(n + 3) {
      border-top: 1px solid var(--art-card-border);
    }
    .workbench__rail {
      grid-template-columns: 1fr;
    }
    .workbench__rail .workbench-panel + .workbench-panel {
      border-top: 1px solid var(--art-card-border);
      border-left: 0;
    }
    .panel-search {
      width: 150px;
    }
    .workflow-row {
      align-items: flex-start;
      flex-wrap: wrap;
    }
    .workflow-row__main {
      flex-basis: calc(100% - 90px);
    }
    .workflow-row__facts {
      order: 3;
      width: 100%;
      margin: 8px 0 0 50px;
    }
  }
</style>
