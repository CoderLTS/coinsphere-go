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
  </div>
</template>

<script setup lang="ts">
  import { Clock, Edit, SwitchButton, VideoPlay } from '@element-plus/icons-vue'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import {
    fetchActivateWorkflowDefinition,
    fetchDeactivateWorkflowDefinition,
    fetchRunWorkflowDefinition,
    fetchWorkflowDefinitionList,
    type WorkflowDefinitionItem
  } from '@/api/scheduler'
  import type { WorkflowStatus } from '@/api/workflows'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'SchedulerWorkflowDefinitionsPage' })

  const router = useRouter()
  const { hasAuth } = useAuth()
  const loading = ref(false)
  const actingId = ref<number>()
  const runningId = ref<number>()
  const definitions = ref<WorkflowDefinitionItem[]>([])
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

  const openLogs = (row: WorkflowDefinitionItem) =>
    router.push({
      path: '/scheduler/execution',
      query: { workflowId: row.code, workflowName: row.displayName }
    })

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
