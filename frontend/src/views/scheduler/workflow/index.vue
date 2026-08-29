<!-- 工作流定义列表。 -->
<template>
  <div class="workflow-definition-page art-full-height">
    <ArtSearchBar
      v-model="formFilters"
      :items="formItems"
      :show-expand="false"
      @search="handleSearch"
      @reset="handleReset"
    />

    <ElCard class="art-table-card">
      <ArtTableHeader
        :loading="loading"
        :show-zebra="false"
        layout="refresh,size,fullscreen,settings"
        @refresh="loadPageData"
      >
        <template #left>
          <ElButton
            v-if="hasAuth('scheduler.workflow_definitions.create')"
            type="primary"
            @click="router.push('/scheduler/workflow/create')"
          >
            新增工作流定义
          </ElButton>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="filteredDefinitions"
        :stripe="false"
        table-layout="auto"
        empty-height="320px"
      >
        <ElTableColumn
          prop="displayName"
          label="工作流名称"
          min-width="240"
          show-overflow-tooltip
        />
        <ElTableColumn label="版本" width="100" align="center">
          <template #default="{ row }">v{{ row.version }}</template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="110" align="center">
          <template #default="{ row }">
            <ElTag :type="statusTagType(row.workflowStatus)" effect="plain">
              {{ statusLabel(row.workflowStatus) }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="创建时间" min-width="180">
          <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="220" align="center">
          <template #default="{ row }">
            <ElSpace size="small">
              <ElTooltip content="工作流日志" placement="top">
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="Clock"
                  @click="openLogs(row)"
                />
              </ElTooltip>
              <ElTooltip content="编辑" placement="top">
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="Edit"
                  @click="router.push(`/scheduler/workflow/${row.id}/edit`)"
                />
              </ElTooltip>
              <ElTooltip content="版本记录" placement="top">
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="Collection"
                  @click="openVersionDialog(row)"
                />
              </ElTooltip>
              <ElTooltip :content="lifecycleLabel(row.workflowStatus)" placement="top">
                <ElButton
                  circle
                  plain
                  size="small"
                  :type="row.workflowStatus === 'inactive' ? 'success' : 'warning'"
                  :icon="SwitchButton"
                  :loading="actingId === row.id"
                  @click="toggleLifecycle(row)"
                />
              </ElTooltip>
              <ElTooltip content="手动运行" placement="top">
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="VideoPlay"
                  :disabled="row.workflowStatus !== 'active'"
                  :loading="runningId === row.id"
                  @click="runWorkflow(row)"
                />
              </ElTooltip>
            </ElSpace>
          </template>
        </ElTableColumn>
      </ArtTable>
    </ElCard>

    <ElDialog
      v-model="versionDialogVisible"
      title="版本记录"
      width="min(920px, calc(100vw - 32px))"
    >
      <div>
        <div v-if="versionDetail" class="version-header">
          <div>
            <div class="version-header__title">{{ versionDetail.displayName }}</div>
            <div v-if="versionDetail.description" class="version-header__description">
              {{ versionDetail.description }}
            </div>
          </div>
          <ElTag effect="plain" type="info">{{ versionRows.length }} 个版本</ElTag>
        </div>

        <ElTable :data="versionRows" empty-text="暂无版本记录">
          <ElTableColumn label="版本" width="100" align="center">
            <template #default="{ row }">
              <ElTag :type="row.isLatest ? 'primary' : 'info'" effect="plain">
                v{{ row.version }}
              </ElTag>
            </template>
          </ElTableColumn>
          <ElTableColumn label="状态" min-width="220">
            <template #default="{ row }">
              <ElSpace size="small" wrap>
                <ElTag v-if="row.isLatest" type="primary" effect="plain">最新版本</ElTag>
                <ElTag v-if="row.isActive" type="success" effect="plain">当前激活</ElTag>
                <ElTag v-if="!row.isLatest && !row.isActive" type="info" effect="plain">
                  历史版本
                </ElTag>
              </ElSpace>
            </template>
          </ElTableColumn>
          <ElTableColumn prop="executionCount" label="执行数" width="100" align="center" />
          <ElTableColumn label="创建时间" min-width="180">
            <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
          </ElTableColumn>
          <ElTableColumn
            v-if="hasAuth('scheduler.workflow_definitions.update')"
            label="操作"
            width="90"
            align="center"
          >
            <template #default="{ row }">
              <ElTooltip content="打开版本" placement="top">
                <ElButton
                  circle
                  plain
                  size="small"
                  type="primary"
                  :icon="Edit"
                  @click="openVersionEditor(row)"
                />
              </ElTooltip>
            </template>
          </ElTableColumn>
        </ElTable>
      </div>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import { Clock, Collection, Edit, SwitchButton, VideoPlay } from '@element-plus/icons-vue'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import {
    fetchActivateWorkflowDefinition,
    fetchDeactivateWorkflowDefinition,
    fetchRunWorkflowDefinition,
    fetchWorkflowDefinitionList,
    type WorkflowDefinitionItem,
    type WorkflowDefinitionVersionItem
  } from '@/api/scheduler'
  import { fetchWorkflowRuns } from '@/api/workflows'
  import type { WorkflowStatus } from '@/api/workflows'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'SchedulerWorkflowDefinitionsPage' })

  const router = useRouter()
  const { hasAuth } = useAuth()
  const loading = ref(false)
  const actingId = ref<number>()
  const runningId = ref<number>()
  const definitions = ref<WorkflowDefinitionItem[]>([])
  const versionDialogVisible = ref(false)
  const versionDetail = ref<WorkflowDefinitionItem | null>(null)
  const versionRows = computed(() => versionDetail.value?.versions || [])
  const initialFilters = { keyword: '', status: '' }
  const formFilters = reactive({ ...initialFilters })
  const appliedFilters = reactive({ ...initialFilters })

  const formItems = computed(() => [
    {
      label: '名称',
      key: 'keyword',
      type: 'input',
      props: { clearable: true, placeholder: '搜索工作流名称' }
    },
    {
      label: '状态',
      key: 'status',
      type: 'select',
      props: {
        clearable: true,
        options: [
          { label: '已激活', value: 'active' },
          { label: '未激活', value: 'inactive' },
          { label: '异常', value: 'error' }
        ]
      }
    }
  ])

  const filteredDefinitions = computed(() => {
    const keyword = appliedFilters.keyword.trim().toLowerCase()
    return definitions.value.filter(
      (item) =>
        (!keyword || item.displayName.toLowerCase().includes(keyword)) &&
        (!appliedFilters.status || item.workflowStatus === appliedFilters.status)
    )
  })

  const statusLabel = (status: WorkflowStatus) =>
    ({ inactive: '未激活', active: '已激活', error: '异常' })[status]

  const statusTagType = (status: WorkflowStatus) => {
    if (status === 'active') return 'success'
    if (status === 'error') return 'danger'
    return 'info'
  }

  const lifecycleLabel = (status: WorkflowStatus) =>
    status === 'inactive' ? '激活' : status === 'error' ? '恢复为未激活' : '停用'

  const loadPageData = async () => {
    loading.value = true
    try {
      definitions.value = await fetchWorkflowDefinitionList()
    } finally {
      loading.value = false
    }
  }

  const openLogs = async (row: WorkflowDefinitionItem) => {
    try {
      const result = await fetchWorkflowRuns(row.id, { limit: 1 })
      const run = result.records[0]
      if (!run) {
        ElMessage.info('该工作流暂无运行记录')
        return
      }
      await router.push({
        path: `/scheduler/execution/${run.id}/detail`,
        query: {
          workflowId: row.code,
          workflowName: row.displayName,
          ...(run.triggerType === 'stream' ? { followLatest: '1' } : {})
        }
      })
    } catch (error: any) {
      ElMessage.error(error?.message || '加载工作流运行日志失败')
    }
  }

  const openVersionDialog = (row: WorkflowDefinitionItem) => {
    versionDetail.value = row
    versionDialogVisible.value = true
  }

  const openVersionEditor = async (row: WorkflowDefinitionVersionItem) => {
    versionDialogVisible.value = false
    await router.push(`/scheduler/workflow/${row.id}/edit`)
  }

  const toggleLifecycle = async (row: WorkflowDefinitionItem) => {
    const activate = row.workflowStatus === 'inactive'
    await ElMessageBox.confirm(
      `${activate ? '激活' : '停用'}工作流“${row.displayName}”？`,
      lifecycleLabel(row.workflowStatus),
      { type: activate ? 'info' : 'warning' }
    )
    actingId.value = row.id
    try {
      if (activate) await fetchActivateWorkflowDefinition(row.id)
      else await fetchDeactivateWorkflowDefinition(row.id)
      await loadPageData()
    } finally {
      actingId.value = undefined
    }
  }

  const runWorkflow = async (row: WorkflowDefinitionItem) => {
    runningId.value = row.id
    try {
      const result = await fetchRunWorkflowDefinition(row.id, { startEntryKeys: [] })
      const run = result.executions[0]
      ElMessage.success('运行已加入队列')
      if (run) {
        await router.push({
          path: `/scheduler/execution/${run.id}/detail`,
          query: { workflowId: row.code, workflowName: row.displayName }
        })
      }
    } finally {
      runningId.value = undefined
    }
  }

  const handleSearch = () => Object.assign(appliedFilters, formFilters)
  const handleReset = () => {
    Object.assign(formFilters, initialFilters)
    Object.assign(appliedFilters, initialFilters)
  }

  onMounted(loadPageData)
</script>

<style scoped lang="scss">
  .version-header {
    display: flex;
    gap: 16px;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 16px;
  }

  .version-header__title {
    font-size: 16px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  .version-header__description {
    margin-top: 6px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }
</style>
