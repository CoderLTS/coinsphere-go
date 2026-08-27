<!-- 工作流定义页面或组件：index。 -->
<template>
  <div class="workflow-definition-page art-full-height">
    <ArtSearchBar
      v-model="formFilters"
      :items="formItems"
      :showExpand="false"
      @search="handleSearch"
      @reset="handleReset"
    />

    <ElCard class="art-table-card">
      <ArtTableHeader :loading="loading" :showZebra="false" @refresh="loadPageData">
        <template #left>
          <ElSpace wrap>
            <ElButton
              v-if="hasAuth('scheduler.workflow_definitions.create')"
              type="primary"
              @click="router.push('/scheduler/workflow/create')"
            >
              新增工作流定义
            </ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ElTable
        v-loading="loading"
        :data="filteredDefinitionList"
        stripe
        table-layout="auto"
        class="workflow-definition-table"
      >
        <ElTableColumn
          prop="displayName"
          label="工作流名称"
          min-width="220"
          show-overflow-tooltip
        />
        <ElTableColumn label="最新版本" width="100" align="center">
          <template #default="{ row }">
            <ElTag :type="row.isLatest ? 'primary' : 'info'" effect="plain"
              >v{{ row.version }}</ElTag
            >
          </template>
        </ElTableColumn>
        <ElTableColumn label="激活版本" width="110" align="center">
          <template #default="{ row }">
            <ElTag
              v-if="row.activeVersion"
              :type="row.isActive ? 'success' : 'warning'"
              effect="plain"
            >
              v{{ row.activeVersion }}
            </ElTag>
            <span v-else>--</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="激活状态" width="110" align="center">
          <template #default="{ row }">
            <ElTag :type="row.isWorkflowActive ? 'success' : 'info'" effect="plain">
              {{ row.isWorkflowActive ? '已激活' : '未激活' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="executionCount" label="执行数" width="100" align="center" />
        <ElTableColumn label="内置" width="90" align="center">
          <template #default="{ row }">
            <ElTag :type="row.isBuiltin ? 'warning' : 'info'" effect="plain">{{
              row.isBuiltin ? '是' : '否'
            }}</ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="创建时间" min-width="170">
          <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="220" align="center">
          <template #default="{ row }">
            <ElSpace wrap size="small" class="operation-actions">
              <ElTooltip
                v-if="hasAuth('scheduler.workflow_executions.view')"
                content="执行记录"
                placement="top"
              >
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="Clock"
                  @click="openExecutionList(row)"
                />
              </ElTooltip>
              <ElTooltip
                v-if="hasAuth('scheduler.workflow_definitions.update')"
                content="编辑"
                placement="top"
              >
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="Edit"
                  @click="router.push(`/scheduler/workflow/${row.id}/edit`)"
                />
              </ElTooltip>
              <ElTooltip content="版本管理" placement="top">
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="Collection"
                  @click="openVersionDialog(row)"
                />
              </ElTooltip>
              <ElTooltip
                v-if="hasAuth('scheduler.workflow_runtime.view')"
                content="查看运行态"
                placement="top"
              >
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="Operation"
                  @click="openRuntimeDrawer(row)"
                />
              </ElTooltip>
              <ElTooltip
                v-if="hasAuth('scheduler.workflow_definitions.run')"
                content="手动运行"
                placement="top"
              >
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="VideoPlay"
                  @click="openRunDialog(row)"
                />
              </ElTooltip>
            </ElSpace>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>

    <ElDialog v-model="versionDialogVisible" title="版本管理" width="960px">
      <div v-loading="versionDialogLoading">
        <template v-if="versionFamilyDetail">
          <div class="version-header">
            <div>
              <div class="version-header__title">{{ versionFamilyDetail.displayName }}</div>
              <div v-if="versionFamilyDetail.description" class="version-header__meta">
                {{ versionFamilyDetail.description }}
              </div>
            </div>
            <ElTag effect="plain" type="info">{{ versionDialogVersions.length }} 个版本</ElTag>
          </div>

          <ElTable :data="versionDialogVersions" stripe>
            <ElTableColumn label="版本" width="90" align="center">
              <template #default="{ row }">
                <ElTag effect="plain" :type="row.isLatest ? 'primary' : 'info'"
                  >v{{ row.version }}</ElTag
                >
              </template>
            </ElTableColumn>
            <ElTableColumn prop="displayName" label="名称" min-width="200" show-overflow-tooltip />
            <ElTableColumn label="状态" min-width="220">
              <template #default="{ row }">
                <ElSpace wrap size="small">
                  <ElTag v-if="row.isLatest" type="primary" effect="plain">最新版本</ElTag>
                  <ElTag v-if="row.isActive" type="success" effect="plain">当前激活</ElTag>
                  <ElTag v-if="row.executionCount > 0" type="info" effect="plain">有执行记录</ElTag>
                </ElSpace>
              </template>
            </ElTableColumn>
            <ElTableColumn prop="executionCount" label="执行数" width="90" align="center" />
            <ElTableColumn label="创建时间" min-width="170">
              <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
            </ElTableColumn>
            <ElTableColumn label="操作" width="180" align="center">
              <template #default="{ row }">
                <ElSpace wrap size="small" class="operation-actions">
                  <ElTooltip
                    v-if="hasAuth('scheduler.workflow_definitions.update')"
                    content="编辑"
                    placement="top"
                  >
                    <ElButton
                      circle
                      plain
                      size="small"
                      type="primary"
                      :icon="Edit"
                      :disabled="versionActionLoading"
                      @click="openVersionEditor(row)"
                    />
                  </ElTooltip>
                  <ElTooltip
                    v-if="hasAuth('scheduler.workflow_runtime.activate') && !row.isActive"
                    content="激活版本"
                    placement="top"
                  >
                    <ElButton
                      circle
                      plain
                      size="small"
                      type="success"
                      :icon="Select"
                      :loading="versionActionLoadingKey === `activate-${row.id}`"
                      :disabled="versionActionLoading"
                      @click="activateVersion(row)"
                    />
                  </ElTooltip>
                  <ElTooltip
                    v-if="hasAuth('scheduler.workflow_runtime.activate') && row.isActive"
                    content="取消激活"
                    placement="top"
                  >
                    <ElButton
                      circle
                      plain
                      size="small"
                      type="warning"
                      :icon="SwitchButton"
                      :loading="versionActionLoadingKey === `deactivate-${row.id}`"
                      :disabled="versionActionLoading"
                      @click="deactivateVersion(row)"
                    />
                  </ElTooltip>
                  <ElTooltip
                    v-if="hasAuth('scheduler.workflow_definitions.delete')"
                    :content="row.isActive ? '当前激活版本不可删除' : 'V2 修订版本不可删除'"
                    placement="top"
                  >
                    <ElButton
                      circle
                      plain
                      size="small"
                      type="danger"
                      :icon="Delete"
                      :loading="versionActionLoadingKey === `delete-${row.id}`"
                      :disabled="versionActionLoading || !canDeleteVersion(row)"
                      @click="deleteVersion(row)"
                    />
                  </ElTooltip>
                </ElSpace>
              </template>
            </ElTableColumn>
          </ElTable>
        </template>
      </div>
    </ElDialog>

    <ElDrawer v-model="runtimeDrawerVisible" title="运行状态" size="900px">
      <div v-loading="runtimeLoading">
        <template v-if="runtimeFamilyDetail && runtimeState">
          <div class="runtime-header">
            <div>
              <div class="runtime-header__title">{{ runtimeHeaderTitle }}</div>
              <div class="runtime-header__meta">
                {{ runtimeHeaderMeta }}
              </div>
            </div>
            <ElTag :type="runtimeActiveVersion ? 'success' : 'info'" effect="plain">
              {{ runtimeActiveVersion ? '当前激活版本' : '未激活' }}
            </ElTag>
          </div>

          <ElEmpty
            v-if="!runtimeState.activeDefinitionId"
            description="当前工作流尚未激活，没有可运行的入口。"
          />

          <ElEmpty
            v-else-if="!runtimeState.entries.length"
            description="当前激活版本没有已注册的运行态入口。"
          />

          <ElTable v-else :data="runtimeState.entries" stripe>
            <ElTableColumn prop="entryName" label="开始入口" min-width="180" />
            <ElTableColumn label="开始类型" width="120" align="center">
              <template #default="{ row }">
                {{ startTypeLabel(row.startType) }}
              </template>
            </ElTableColumn>
            <ElTableColumn label="启用" width="110" align="center">
              <template #default="{ row }">
                <ElSwitch
                  v-if="hasAuth('scheduler.workflow_runtime.update')"
                  :model-value="row.isEnabled"
                  @update:model-value="updateRuntimeEntryStatus(row.entryKey, Boolean($event))"
                />
                <ElTag v-else :type="row.isEnabled ? 'success' : 'info'" effect="plain">
                  {{ row.isEnabled ? '启用' : '停用' }}
                </ElTag>
              </template>
            </ElTableColumn>
            <ElTableColumn label="运行状态" width="130" align="center">
              <template #default="{ row }">
                {{ registrationStatusLabel(row.registrationStatus) }}
              </template>
            </ElTableColumn>
            <ElTableColumn label="下次运行" min-width="170">
              <template #default="{ row }">{{ formatDateTime(row.nextRunAt) }}</template>
            </ElTableColumn>
            <ElTableColumn label="最近触发" min-width="170">
              <template #default="{ row }">{{ formatDateTime(row.lastTriggeredAt) }}</template>
            </ElTableColumn>
            <ElTableColumn
              prop="lastErrorMessage"
              label="最近错误"
              min-width="180"
              show-overflow-tooltip
            />
            <ElTableColumn label="Secret" min-width="180">
              <template #default="{ row }">
                <div v-if="row.startType === 'webhook'" class="secret-cell">
                  <span>{{ row.secretHint || '--' }}</span>
                  <ElButton
                    v-if="hasAuth('scheduler.workflow_runtime.update')"
                    link
                    type="primary"
                    @click="rotateSecret(row.entryKey)"
                  >
                    轮换
                  </ElButton>
                </div>
                <span v-else>--</span>
              </template>
            </ElTableColumn>
          </ElTable>
        </template>
      </div>
    </ElDrawer>

    <ElDialog v-model="runDialogVisible" title="手动运行工作流" width="620px">
      <ElForm label-position="top">
        <ElFormItem label="工作流版本">
          <ElInput :model-value="runTargetLabel" disabled />
        </ElFormItem>
        <ElFormItem label="手动开始节点">
          <ElSelect
            v-model="selectedManualEntryKeys"
            multiple
            filterable
            clearable
            collapse-tags
            collapse-tags-tooltip
            placeholder="请选择一个或多个手动开始节点"
          >
            <ElOption
              v-for="item in manualEntryOptions"
              :key="item.entryKey"
              :label="item.label"
              :value="item.entryKey"
            />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="运行输入 JSON">
          <ElInput
            v-model="manualRunInputsJson"
            type="textarea"
            :rows="6"
            placeholder='例如：{"source":"manual"}'
          />
        </ElFormItem>
      </ElForm>

      <template #footer>
        <ElButton @click="runDialogVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="runSubmitting" @click="submitManualRun"
          >开始执行</ElButton
        >
      </template>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import {
    Clock,
    Collection,
    Delete,
    Edit,
    Operation,
    Select,
    SwitchButton,
    VideoPlay
  } from '@element-plus/icons-vue'
  import { ElMessage, ElMessageBox, ElTag, ElTooltip } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import {
    fetchActivateWorkflowDefinition,
    fetchDeactivateWorkflowDefinition,
    fetchDeleteWorkflowDefinition,
    fetchRotateWorkflowRuntimeEntrySecret,
    fetchRunWorkflowDefinition,
    fetchUpdateWorkflowRuntimeEntryStatus,
    fetchWorkflowDefinitionDetail,
    fetchWorkflowDefinitionList,
    fetchWorkflowRuntime,
    type WorkflowDefinitionItem,
    type WorkflowDefinitionVersionItem,
    type WorkflowRuntimeStateItem,
    type WorkflowStartType
  } from '@/api/scheduler'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'SchedulerWorkflowDefinitionsPage' })

  const router = useRouter()
  const { hasAuth } = useAuth()

  const loading = ref(false)
  const runtimeLoading = ref(false)
  const versionDialogLoading = ref(false)
  const runSubmitting = ref(false)
  const definitionList = ref<WorkflowDefinitionItem[]>([])
  const versionDialogVisible = ref(false)
  const versionFamilyDetail = ref<WorkflowDefinitionItem | null>(null)
  const versionActionLoadingKey = ref('')
  const runtimeDrawerVisible = ref(false)
  const runtimeFamilyDetail = ref<WorkflowDefinitionItem | null>(null)
  const runtimeState = ref<WorkflowRuntimeStateItem | null>(null)
  const runDialogVisible = ref(false)
  const runDefinition = ref<WorkflowDefinitionItem | null>(null)
  const selectedManualEntryKeys = ref<string[]>([])
  const manualRunInputsJson = ref('{}')

  const initialFilters = {
    keyword: '',
    activeStatus: ''
  }

  const formFilters = reactive({ ...initialFilters })
  const appliedFilters = reactive({ ...initialFilters })

  const formItems = computed(() => [
    {
      label: '名称',
      key: 'keyword',
      type: 'input',
      props: {
        clearable: true,
        placeholder: '搜索工作流名称'
      }
    },
    {
      label: '激活状态',
      key: 'activeStatus',
      type: 'select',
      props: {
        clearable: true,
        options: [
          { label: '已激活', value: 'active' },
          { label: '未激活', value: 'inactive' }
        ]
      }
    }
  ])

  const filteredDefinitionList = computed(() => {
    const keyword = appliedFilters.keyword.trim().toLowerCase()
    const activeStatus = appliedFilters.activeStatus

    return definitionList.value.filter((item) => {
      if (keyword && !item.displayName.toLowerCase().includes(keyword)) return false
      if (activeStatus === 'active' && !item.isWorkflowActive) return false
      if (activeStatus === 'inactive' && item.isWorkflowActive) return false
      return true
    })
  })

  const versionDialogVersions = computed(() => versionFamilyDetail.value?.versions || [])
  const versionActionLoading = computed(() => !!versionActionLoadingKey.value)
  const versionFamilyCode = computed(() => versionFamilyDetail.value?.code || '')
  const runtimeActiveDefinitionId = computed(() => runtimeState.value?.activeDefinitionId || null)
  const runtimeActiveVersion = computed<WorkflowDefinitionVersionItem | null>(() => {
    if (!runtimeFamilyDetail.value || !runtimeActiveDefinitionId.value) return null
    return (
      runtimeFamilyDetail.value.versions?.find(
        (item) => item.id === runtimeActiveDefinitionId.value
      ) || null
    )
  })
  const runtimeHeaderTitle = computed(() => {
    if (runtimeActiveVersion.value) return runtimeActiveVersion.value.displayName
    return runtimeFamilyDetail.value?.displayName || ''
  })
  const runtimeHeaderMeta = computed(() => {
    if (!runtimeFamilyDetail.value) return ''
    if (runtimeActiveVersion.value) {
      return `版本 v${runtimeActiveVersion.value.version}`
    }
    return '未激活'
  })

  const manualEntryOptions = computed(() => {
    const graph = runDefinition.value?.graph
    if (!graph) return []
    return (graph.nodes || [])
      .filter((node) => node.type === 'start.manual')
      .map((node) => {
        const config = node.config || {}
        const entryKey = String(config.entryKey || '').trim()
        const label = String(config.displayName || node.label || entryKey || node.id).trim()
        return { entryKey, label }
      })
      .filter((item) => !!item.entryKey)
  })

  const runTargetLabel = computed(() => {
    if (!runDefinition.value) return ''
    return `${runDefinition.value.displayName} / v${runDefinition.value.version}`
  })

  const loadPageData = async () => {
    loading.value = true
    try {
      definitionList.value = await fetchWorkflowDefinitionList()
    } finally {
      loading.value = false
    }
  }

  const startTypeLabel = (value: WorkflowStartType) =>
    ({
      manual: '手动',
      schedule: '定时',
      event: '事件',
      webhook: 'Webhook'
    })[value] || value

  const registrationStatusLabel = (value: string) =>
    ({
      ready: '就绪',
      registered: '已注册',
      failed: '注册失败',
      disabled: '已停用'
    })[value] || '未知状态'

  const findLatestDefinitionByCode = (workflowCode: string) =>
    definitionList.value.find((item) => item.code === workflowCode) || null

  const refreshVersionDialogByCode = async (workflowCode: string) => {
    if (!versionDialogVisible.value || versionFamilyDetail.value?.code !== workflowCode) return
    const latestDefinition = findLatestDefinitionByCode(workflowCode)
    if (!latestDefinition) {
      versionDialogVisible.value = false
      versionFamilyDetail.value = null
      return
    }

    versionDialogLoading.value = true
    try {
      versionFamilyDetail.value = await fetchWorkflowDefinitionDetail(latestDefinition.id)
    } finally {
      versionDialogLoading.value = false
    }
  }

  const refreshRuntimeDrawerByCode = async (workflowCode: string) => {
    if (!runtimeDrawerVisible.value || runtimeFamilyDetail.value?.code !== workflowCode) return
    const latestDefinition = findLatestDefinitionByCode(workflowCode)
    if (!latestDefinition) {
      runtimeDrawerVisible.value = false
      runtimeFamilyDetail.value = null
      runtimeState.value = null
      return
    }

    runtimeLoading.value = true
    try {
      const [detail, runtime] = await Promise.all([
        fetchWorkflowDefinitionDetail(latestDefinition.id),
        fetchWorkflowRuntime(latestDefinition.id)
      ])
      runtimeFamilyDetail.value = detail
      runtimeState.value = runtime
    } finally {
      runtimeLoading.value = false
    }
  }

  const refreshFamilyPanelsByCode = async (workflowCode: string) => {
    await Promise.all([
      refreshVersionDialogByCode(workflowCode),
      refreshRuntimeDrawerByCode(workflowCode)
    ])
  }

  const openVersionDialog = async (row: WorkflowDefinitionItem) => {
    versionFamilyDetail.value = row
    versionDialogVisible.value = true
    versionDialogLoading.value = true
    try {
      versionFamilyDetail.value = await fetchWorkflowDefinitionDetail(row.id)
    } finally {
      versionDialogLoading.value = false
    }
  }

  const openRuntimeDrawer = async (row: WorkflowDefinitionItem) => {
    runtimeFamilyDetail.value = row
    runtimeDrawerVisible.value = true
    runtimeLoading.value = true
    try {
      const [detail, runtime] = await Promise.all([
        fetchWorkflowDefinitionDetail(row.id),
        fetchWorkflowRuntime(row.id)
      ])
      runtimeFamilyDetail.value = detail
      runtimeState.value = runtime
    } finally {
      runtimeLoading.value = false
    }
  }

  const refreshRuntimeDrawer = async () => {
    if (!runtimeFamilyDetail.value) return
    await refreshRuntimeDrawerByCode(runtimeFamilyDetail.value.code)
  }

  const openVersionEditor = async (row: WorkflowDefinitionVersionItem) => {
    versionDialogVisible.value = false
    await router.push(`/scheduler/workflow/${row.id}/edit`)
  }

  const canDeleteVersion = (_row: WorkflowDefinitionVersionItem) => {
    void _row
    return false
  }

  const activateVersion = async (row: WorkflowDefinitionVersionItem) => {
    if (!versionFamilyCode.value) return
    versionActionLoadingKey.value = `activate-${row.id}`
    try {
      await fetchActivateWorkflowDefinition(row.id)
      await loadPageData()
      await refreshFamilyPanelsByCode(versionFamilyCode.value)
    } finally {
      versionActionLoadingKey.value = ''
    }
  }

  const deactivateVersion = async (row: WorkflowDefinitionVersionItem) => {
    if (!versionFamilyCode.value) return
    await ElMessageBox.confirm(
      `确认取消激活工作流定义“${row.displayName} / v${row.version}”吗？`,
      '提示',
      {
        type: 'warning',
        confirmButtonText: '确定',
        cancelButtonText: '取消'
      }
    )
    versionActionLoadingKey.value = `deactivate-${row.id}`
    try {
      await fetchDeactivateWorkflowDefinition(row.id)
      await loadPageData()
      await refreshFamilyPanelsByCode(versionFamilyCode.value)
    } finally {
      versionActionLoadingKey.value = ''
    }
  }

  const deleteVersion = async (row: WorkflowDefinitionVersionItem) => {
    if (!versionFamilyCode.value || !canDeleteVersion(row)) return
    await ElMessageBox.confirm(
      `确认删除工作流定义版本“${row.displayName} v${row.version}”吗？`,
      '提示',
      {
        type: 'warning',
        confirmButtonText: '确定',
        cancelButtonText: '取消'
      }
    )
    versionActionLoadingKey.value = `delete-${row.id}`
    try {
      await fetchDeleteWorkflowDefinition(row.id)
      await loadPageData()
      await refreshFamilyPanelsByCode(versionFamilyCode.value)
    } finally {
      versionActionLoadingKey.value = ''
    }
  }

  const updateRuntimeEntryStatus = async (entryKey: string, isEnabled: boolean) => {
    if (!runtimeActiveDefinitionId.value) return
    runtimeState.value = await fetchUpdateWorkflowRuntimeEntryStatus(
      runtimeActiveDefinitionId.value,
      entryKey,
      isEnabled
    )
  }

  const rotateSecret = async (entryKey: string) => {
    if (!runtimeActiveDefinitionId.value) return
    const result = await fetchRotateWorkflowRuntimeEntrySecret(
      runtimeActiveDefinitionId.value,
      entryKey
    )
    ElMessage.success(`新的 Webhook Secret：${result.secret}`)
    await refreshRuntimeDrawer()
  }

  const openRunDialog = (row: WorkflowDefinitionItem) => {
    runDefinition.value = row
    selectedManualEntryKeys.value = []
    manualRunInputsJson.value = '{}'
    runDialogVisible.value = true
  }

  const openExecutionList = (row: WorkflowDefinitionItem) =>
    router.push({
      path: '/scheduler/execution',
      query: { workflowId: row.code, workflowName: row.displayName }
    })

  const submitManualRun = async () => {
    if (!runDefinition.value) return
    if (!selectedManualEntryKeys.value.length) {
      ElMessage.warning('请至少选择一个手动开始节点')
      return
    }

    let inputs: Record<string, any> = {}
    try {
      const parsed = JSON.parse(manualRunInputsJson.value || '{}')
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        inputs = parsed
      } else {
        throw new Error('运行输入必须是 JSON 对象')
      }
    } catch (error) {
      ElMessage.error(error instanceof Error ? error.message : '运行输入 JSON 格式不正确')
      return
    }

    runSubmitting.value = true
    try {
      const result = await fetchRunWorkflowDefinition(runDefinition.value.id, {
        startEntryKeys: selectedManualEntryKeys.value,
        inputs
      })
      runDialogVisible.value = false
      ElMessage.success(`已加入执行队列，共 ${result.executions.length} 条执行记录`)
      await loadPageData()
      if (result.executions.length === 1 && result.executions[0]?.id) {
        await router.push({
          path: `/scheduler/execution/${result.executions[0].id}/detail`,
          query: {
            workflowId: runDefinition.value.code,
            workflowName: runDefinition.value.displayName
          }
        })
        return
      }
      await openExecutionList(runDefinition.value)
    } finally {
      runSubmitting.value = false
    }
  }

  const handleSearch = () => {
    Object.assign(appliedFilters, formFilters)
  }

  const handleReset = () => {
    Object.assign(formFilters, initialFilters)
    Object.assign(appliedFilters, initialFilters)
  }

  onMounted(() => {
    void loadPageData()
  })
</script>

<style scoped lang="scss">
  .version-header,
  .runtime-header {
    display: flex;
    gap: 16px;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 16px;
  }

  .version-header__title,
  .runtime-header__title {
    font-size: 16px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  .version-header__meta,
  .runtime-header__meta {
    margin-top: 6px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .secret-cell {
    display: flex;
    gap: 8px;
    align-items: center;
    justify-content: space-between;
  }

  .operation-actions {
    justify-content: center;
    width: 100%;
  }

  .workflow-definition-page {
    :deep(.workflow-definition-table .el-table__cell) {
      vertical-align: middle;
    }

    :deep(.workflow-definition-table .cell) {
      line-height: 1.4;
    }
  }
</style>
