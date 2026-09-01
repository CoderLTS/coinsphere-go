<template>
  <div class="results-page art-full-height">
    <header class="results-header">
      <div class="results-header__identity">
        <span><ArtSvgIcon icon="ri:file-chart-line" /></span>
        <div>
          <p>Shared results</p>
          <h1>共享结果</h1>
        </div>
      </div>
      <div class="results-header__actions">
        <ElButton circle title="刷新" :loading="loading" @click="loadViews">
          <ArtSvgIcon icon="ri:refresh-line" />
        </ElButton>
        <ElButton v-if="isAdmin" type="primary" @click="openCreate">
          <ArtSvgIcon icon="ri:add-line" />
          创建视图
        </ElButton>
      </div>
    </header>

    <div class="results-mobile-select">
      <ElSelect :model-value="selectedView?.id" placeholder="选择结果视图" @change="selectById">
        <ElOption v-for="view in activeViews" :key="view.id" :label="view.name" :value="view.id" />
      </ElSelect>
    </div>

    <main v-loading="loading" class="results-layout">
      <aside class="results-rail" aria-label="共享结果视图">
        <div class="results-rail__heading">
          <span>结果视图</span>
          <small>{{ activeViews.length }} active</small>
        </div>
        <div class="results-rail__list">
          <button
            v-for="view in views"
            :key="view.id"
            type="button"
            :disabled="view.status !== 'active'"
            :class="['result-row', { 'is-active': selectedView?.id === view.id }]"
            @click="selectView(view)"
          >
            <span class="result-row__icon">
              <ArtSvgIcon icon="ri:funds-line" />
            </span>
            <span class="result-row__copy">
              <strong>{{ view.name }}</strong>
              <small>{{ pageLabel(view) }}</small>
            </span>
            <ElTag v-if="view.status === 'revoked'" type="info" effect="plain" size="small"
              >撤销</ElTag
            >
            <ArtSvgIcon v-else icon="ri:arrow-right-s-line" />
          </button>
          <div v-if="!views.length" class="results-rail__empty">暂无获授权结果</div>
        </div>
      </aside>

      <section class="results-stage">
        <div v-if="selectedView" class="results-stage__toolbar">
          <div>
            <span>{{ pageLabel(selectedView) }}</span>
            <small>{{ formatTime(selectedView.createdAt) }}</small>
          </div>
          <div v-if="isAdmin" class="results-stage__commands">
            <ElButton circle title="管理授权" @click="openGrants(selectedView)">
              <ArtSvgIcon icon="ri:user-shared-line" />
            </ElButton>
            <ElButton circle title="撤销视图" type="danger" plain @click="revoke(selectedView)">
              <ArtSvgIcon icon="ri:stop-circle-line" />
            </ElButton>
          </div>
        </div>
        <component
          :is="resultComponent"
          v-if="selectedView && resultComponent"
          :key="selectedView.id"
          :view="selectedView"
        />
        <div v-else class="results-stage__empty">
          <ArtSvgIcon icon="ri:file-chart-line" />
          <strong>{{ views.length ? '选择一个可用结果视图' : '暂无共享结果' }}</strong>
        </div>
      </section>
    </main>

    <ElDialog v-model="createVisible" title="创建共享结果视图" width="min(620px, 94vw)">
      <ElForm label-position="top">
        <div class="dialog-grid">
          <ElFormItem label="名称">
            <ElInput v-model="createForm.name" maxlength="120" show-word-limit />
          </ElFormItem>
          <ElFormItem label="工作流">
            <ElSelect v-model="createForm.workflowId" filterable @change="loadWorkflowNodes">
              <ElOption
                v-for="workflow in workflowOptions"
                :key="workflow.id"
                :label="workflow.name"
                :value="workflow.id"
              />
            </ElSelect>
          </ElFormItem>
        </div>
        <ElFormItem label="市场">
          <ElSegmented v-model="createForm.market" :options="marketOptions" />
        </ElFormItem>
        <div class="dialog-grid">
          <ElFormItem label="Paper 节点">
            <ElSelect v-model="createForm.paperNodeInstanceId" filterable allow-create>
              <ElOption v-for="node in paperNodes" :key="node" :label="node" :value="node" />
            </ElSelect>
          </ElFormItem>
          <ElFormItem label="品种过滤">
            <ElInput v-model="createForm.instrument" maxlength="32" />
          </ElFormItem>
          <ElFormItem label="状态过滤">
            <ElSelect v-model="createForm.status" clearable>
              <ElOption label="待成交" value="new" />
              <ElOption label="部分成交" value="partially_filled" />
              <ElOption label="已成交" value="filled" />
              <ElOption label="已撤单" value="canceled" />
              <ElOption label="已拒绝" value="rejected" />
              <ElOption label="已过期" value="expired" />
            </ElSelect>
          </ElFormItem>
        </div>
        <ElFormItem label="允许操作">
          <ElCheckboxGroup v-model="createForm.allowedActions">
            <ElCheckbox value="export">导出</ElCheckbox>
          </ElCheckboxGroup>
        </ElFormItem>
        <div class="dialog-grid">
          <ElFormItem label="授权用户">
            <ElSelect v-model="createForm.userIds" multiple filterable collapse-tags>
              <ElOption
                v-for="user in userOptions"
                :key="user.id"
                :label="user.nickname || user.username"
                :value="user.id"
              />
            </ElSelect>
          </ElFormItem>
          <ElFormItem label="授权角色">
            <ElSelect v-model="createForm.roleCodes" multiple filterable collapse-tags>
              <ElOption
                v-for="role in roleOptions"
                :key="role.code"
                :label="role.displayName || role.code"
                :value="role.code"
              />
            </ElSelect>
          </ElFormItem>
        </div>
      </ElForm>
      <template #footer>
        <ElButton @click="createVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="saving" :disabled="!canCreate" @click="submitCreate">
          创建视图
        </ElButton>
      </template>
    </ElDialog>

    <ElDialog v-model="grantsVisible" title="管理视图授权" width="min(520px, 94vw)">
      <ElForm label-position="top">
        <ElFormItem label="授权用户">
          <ElSelect v-model="grantForm.userIds" multiple filterable collapse-tags>
            <ElOption
              v-for="user in userOptions"
              :key="user.id"
              :label="user.nickname || user.username"
              :value="user.id"
            />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="授权角色">
          <ElSelect v-model="grantForm.roleCodes" multiple filterable collapse-tags>
            <ElOption
              v-for="role in roleOptions"
              :key="role.code"
              :label="role.displayName || role.code"
              :value="role.code"
            />
          </ElSelect>
        </ElFormItem>
      </ElForm>
      <template #footer>
        <ElButton @click="grantsVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="saving" @click="submitGrants">保存授权</ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import type { Component } from 'vue'
  import { ElMessageBox } from 'element-plus'
  import {
    createResultView,
    fetchResultViews,
    replaceResultViewGrants,
    revokeResultView,
    type ResultView
  } from '@/api/resultViews'
  import { fetchGetRoleList, fetchGetUserList } from '@/api/system'
  import {
    fetchWorkflowRevision,
    fetchWorkflows,
    type WorkflowGraphNode,
    type WorkflowItem
  } from '@/api/workflows'
  import { registeredFrontendPlugins } from '@/plugins'
  import { useUserStore } from '@/store/modules/user'
  import { formatDateTime as formatTime } from '@/utils/date'

  defineOptions({ name: 'Results' })

  const userStore = useUserStore()
  const isAdmin = computed(() => userStore.info.roleCodes.includes('R_SUPER'))
  const views = ref<ResultView[]>([])
  const selectedView = ref<ResultView>()
  const resultComponent = shallowRef<Component>()
  const userOptions = ref<Api.System.UserListItem[]>([])
  const roleOptions = ref<Api.System.RoleListItem[]>([])
  const workflowOptions = ref<WorkflowItem[]>([])
  const paperNodes = ref<string[]>([])
  const paperPluginId = ref('')
  const loading = ref(false)
  const saving = ref(false)
  const createVisible = ref(false)
  const grantsVisible = ref(false)
  const grantViewId = ref<number>()
  const marketOptions = [
    { label: 'Spot', value: 'spot' },
    { label: 'USD-M', value: 'usdm' }
  ]
  const createForm = reactive({
    name: '',
    workflowId: 1,
    market: 'spot' as 'spot' | 'usdm',
    instrument: 'BTCUSDT',
    status: '',
    paperNodeInstanceId: 'paper',
    allowedActions: ['export'],
    userIds: [] as number[],
    roleCodes: ['R_USER'] as string[]
  })
  const grantForm = reactive({ userIds: [] as number[], roleCodes: [] as string[] })
  const activeViews = computed(() => views.value.filter((view) => view.status === 'active'))
  const canCreate = computed(() =>
    Boolean(
      createForm.name.trim() &&
        workflowOptions.value.some((workflow) => workflow.id === createForm.workflowId) &&
        createForm.paperNodeInstanceId.trim() &&
        paperPluginId.value
    )
  )

  const pageLabel = (view: ResultView) => `${view.pluginId} / ${view.pageKey}`
  const loadComponent = async (view: ResultView) => {
    const registration = registeredFrontendPlugins.find((plugin) => plugin.id === view.pluginId)
    if (!registration) {
      resultComponent.value = undefined
      return
    }
    const module = await registration.load()
    const page = module.resultPages?.[view.pageKey]
    resultComponent.value = page ? (await page()).default : undefined
  }
  const selectView = async (view: ResultView) => {
    if (view.status !== 'active') return
    selectedView.value = view
    try {
      await loadComponent(view)
    } catch {
      resultComponent.value = undefined
      ElMessage.error('结果页加载失败')
    }
  }
  const selectById = (viewId: number) => {
    const view = views.value.find((item) => item.id === viewId)
    if (view) void selectView(view)
  }
  const loadViews = async () => {
    loading.value = true
    try {
      views.value = (await fetchResultViews()).items.filter((view) => view.pageKey === 'paper')
      const next =
        activeViews.value.find((view) => view.id === selectedView.value?.id) || activeViews.value[0]
      selectedView.value = undefined
      resultComponent.value = undefined
      if (next) await selectView(next)
    } finally {
      loading.value = false
    }
  }
  const loadGrantOptions = async () => {
    if (!isAdmin.value || workflowOptions.value.length) return
    const [users, roles, workflows] = await Promise.all([
      fetchGetUserList({ limit: 200 }),
      fetchGetRoleList({ limit: 200 }),
      fetchWorkflows()
    ])
    userOptions.value = users.records
    roleOptions.value = roles.records
    workflowOptions.value = workflows.items
    if (!workflowOptions.value.some((workflow) => workflow.id === createForm.workflowId)) {
      createForm.workflowId = workflowOptions.value[0]?.id || 1
    }
    await loadWorkflowNodes(createForm.workflowId)
  }
  const nodesOfType = (nodes: WorkflowGraphNode[], suffix: string) =>
    nodes
      .filter((node) => node.nodeType.split('.').at(-1) === suffix)
      .map((node) => node.nodeInstanceId)
  const pluginIDForNode = (nodes: WorkflowGraphNode[], nodeID: string) => {
    const type = nodes.find((node) => node.nodeInstanceId === nodeID)?.nodeType || ''
    const parts = type.split('.')
    return parts.length >= 2 ? parts.slice(0, 2).join('.') : ''
  }
  const loadWorkflowNodes = async (workflowId: number) => {
    const workflow = workflowOptions.value.find((item) => item.id === workflowId)
    if (!workflow) return
    const revision = await fetchWorkflowRevision(workflow.id, workflow.activeRevisionId)
    paperNodes.value = nodesOfType(revision.graph.nodes, 'paper_execute')
    createForm.paperNodeInstanceId = paperNodes.value[0] || createForm.paperNodeInstanceId
    paperPluginId.value = pluginIDForNode(revision.graph.nodes, createForm.paperNodeInstanceId)
  }
  const openCreate = async () => {
    await loadGrantOptions()
    createVisible.value = true
  }
  const submitCreate = async () => {
    const scope = {
      workflowId: createForm.workflowId,
      paperNodeInstanceId: createForm.paperNodeInstanceId.trim()
    }
    const filters: Record<string, string> = {}
    filters.market = createForm.market
    if (createForm.instrument.trim())
      filters.instrument = createForm.instrument.trim().toUpperCase()
    if (createForm.status) filters.status = createForm.status
    saving.value = true
    try {
      const created = await createResultView({
        name: createForm.name.trim(),
        pluginId: paperPluginId.value,
        pageKey: 'paper',
        scope,
        filters,
        allowedActions: [...createForm.allowedActions],
        userIds: [...createForm.userIds],
        roleCodes: [...createForm.roleCodes]
      })
      createVisible.value = false
      createForm.name = ''
      await loadViews()
      const view = views.value.find((item) => item.id === created.id)
      if (view) await selectView(view)
    } finally {
      saving.value = false
    }
  }
  const openGrants = async (view: ResultView) => {
    await loadGrantOptions()
    grantViewId.value = view.id
    grantForm.userIds = [...(view.userIds || [])]
    grantForm.roleCodes = [...(view.roleCodes || [])]
    grantsVisible.value = true
  }
  const submitGrants = async () => {
    if (!grantViewId.value) return
    saving.value = true
    try {
      await replaceResultViewGrants(grantViewId.value, {
        userIds: [...grantForm.userIds],
        roleCodes: [...grantForm.roleCodes]
      })
      grantsVisible.value = false
      await loadViews()
    } finally {
      saving.value = false
    }
  }
  const revoke = async (view: ResultView) => {
    await ElMessageBox.confirm('撤销后所有普通用户立即失去访问权限。', '撤销结果视图', {
      confirmButtonText: '撤销',
      cancelButtonText: '取消',
      type: 'warning'
    })
    await revokeResultView(view.id)
    await loadViews()
  }

  onMounted(loadViews)
</script>

<style scoped>
  .results-page {
    --results-ink: #18202a;
    --results-muted: #66717f;

    display: flex;
    flex-direction: column;
    min-height: 0;
    color: var(--results-ink);
    letter-spacing: 0;
    background: var(--el-bg-color);
  }

  .results-header,
  .results-header__identity,
  .results-header__actions,
  .results-stage__toolbar,
  .results-stage__commands,
  .results-rail__heading {
    display: flex;
    align-items: center;
  }

  .results-header {
    justify-content: space-between;
    min-height: 68px;
    padding: 10px 18px;
    border-bottom: 1px solid var(--el-border-color);
  }

  .results-header__identity,
  .results-header__actions,
  .results-stage__commands {
    gap: 8px;
  }

  .results-header__identity > span {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    color: #fff;
    background: #263746;
    border-radius: 6px;
  }

  .results-header p,
  .results-header h1 {
    margin: 0;
  }

  .results-header p {
    font-size: 11px;
    color: var(--results-muted);
    text-transform: uppercase;
  }

  .results-header h1 {
    margin-top: 2px;
    font-size: 19px;
    font-weight: 680;
  }

  .results-layout {
    display: grid;
    flex: 1;
    grid-template-columns: 270px minmax(0, 1fr);
    min-height: 0;
  }

  .results-rail {
    min-width: 0;
    overflow: auto;
    background: var(--el-fill-color-extra-light);
    border-right: 1px solid var(--el-border-color);
  }

  .results-rail__heading {
    justify-content: space-between;
    min-height: 44px;
    padding: 0 14px;
    font-size: 12px;
    font-weight: 650;
    color: var(--results-muted);
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .results-rail__heading small {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-weight: 500;
  }

  .results-rail__list {
    padding: 8px;
  }

  .result-row {
    display: grid;
    grid-template-columns: 32px minmax(0, 1fr) auto;
    gap: 9px;
    align-items: center;
    width: 100%;
    min-height: 52px;
    padding: 7px 8px;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 1px solid transparent;
    border-radius: 5px;
  }

  .result-row:hover:not(:disabled),
  .result-row:focus-visible {
    background: var(--el-bg-color);
    border-color: var(--el-border-color);
    outline: none;
  }

  .result-row.is-active {
    background: var(--el-bg-color);
    border-color: #8ba0b2;
    box-shadow: inset 3px 0 #263746;
  }

  .result-row:disabled {
    cursor: default;
    opacity: 0.6;
  }

  .result-row__icon {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    color: #314a5f;
    background: #e6edf2;
    border-radius: 5px;
  }

  .result-row__copy {
    min-width: 0;
  }

  .result-row__copy strong,
  .result-row__copy small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .result-row__copy strong {
    font-size: 13px;
    font-weight: 620;
  }

  .result-row__copy small,
  .results-stage__toolbar small,
  .results-stage__toolbar span {
    margin-top: 3px;
    font-size: 11px;
    color: var(--results-muted);
  }

  .results-stage {
    min-width: 0;
    padding: 0 20px 24px;
    overflow: auto;
  }

  .results-stage__toolbar {
    justify-content: space-between;
    min-height: 48px;
    margin-bottom: 12px;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .results-stage__toolbar span,
  .results-stage__toolbar small {
    display: block;
  }

  .results-stage__empty {
    display: grid;
    place-content: center;
    justify-items: center;
    min-height: 320px;
    color: var(--results-muted);
  }

  .results-stage__empty :deep(.art-svg-icon) {
    margin-bottom: 8px;
    font-size: 28px;
  }

  .results-rail__empty {
    padding: 28px 8px;
    font-size: 12px;
    color: var(--results-muted);
    text-align: center;
  }

  .results-mobile-select {
    display: none;
  }

  .dialog-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0 14px;
  }

  .dialog-grid :deep(.el-input-number),
  .dialog-grid :deep(.el-select),
  .dialog-grid :deep(.el-input) {
    width: 100%;
  }

  :global(.dark) .results-page {
    --results-ink: var(--el-text-color-primary);
    --results-muted: var(--el-text-color-secondary);
  }

  :global(.dark) .results-header__identity > span {
    background: #415767;
  }

  :global(.dark) .result-row__icon {
    color: #b9cbd8;
    background: #26333d;
  }

  @media (max-width: 760px) {
    .results-header {
      min-height: 58px;
      padding: 8px 12px;
    }

    .results-header__identity > span {
      display: none;
    }

    .results-header h1 {
      font-size: 17px;
    }

    .results-header__actions .el-button--primary span span {
      display: none;
    }

    .results-mobile-select {
      display: block;
      padding: 10px 12px;
      border-bottom: 1px solid var(--el-border-color);
    }

    .results-mobile-select :deep(.el-select) {
      width: 100%;
    }

    .results-layout {
      display: block;
    }

    .results-rail {
      display: none;
    }

    .results-stage {
      padding: 0 12px 20px;
    }

    .dialog-grid {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
