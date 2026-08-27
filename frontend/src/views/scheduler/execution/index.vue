<!-- 执行记录列表页 -->
<template>
  <div class="workflow-execution-page art-full-height">
    <ArtSearchBar
      v-model="formFilters"
      :items="formItems"
      :span="5"
      :show-expand="false"
      label-width="86px"
      @search="handleSearch"
      @reset="handleReset"
    />

    <ElCard class="art-table-card">
      <ArtTableHeader
        v-model:columns="columnChecks"
        :show-zebra="false"
        :loading="loading"
        @refresh="loadPageData"
      >
        <template #left>
          <ElSpace wrap>
            <strong>{{ workflowName || '工作流' }} · 执行记录</strong>
            <ElButton @click="router.push('/scheduler/definition')">返回工作流定义</ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :columns="columns"
        :data="executionList.records"
        :pagination="{
          current: pagination.current,
          size: pagination.size,
          total: executionList.total
        }"
        :pagination-options="{
          pageSizes: [10, 20, 50],
          layout: 'total, prev, next, sizes',
          align: 'center'
        }"
        :stripe="false"
        @pagination:size-change="handleSizeChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { View } from '@element-plus/icons-vue'
  import { ElButton, ElTag } from 'element-plus'
  import { useCursorPagination } from '@/hooks/core/useCursorPagination'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import {
    fetchWorkflowExecutionList,
    type WorkflowExecutionItem,
    type WorkflowExecutionList,
    type WorkflowExecutionQueryParams,
    type WorkflowExecutionStatus,
    type WorkflowTriggerType
  } from '@/api/scheduler'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'SchedulerWorkflowExecutionsPage' })

  const router = useRouter()
  const route = useRoute()
  const loading = ref(false)
  const executionList = ref<WorkflowExecutionList>({
    records: [],
    nextCursor: '',
    hasMore: false,
    total: 0
  })
  let pollTimer: number | null = null

  const { pagination, requestParams, applyPage, reset, moveTo } = useCursorPagination(10)

  const initialFilters = {
    triggerType: '' as WorkflowTriggerType | '',
    status: '' as WorkflowExecutionStatus | ''
  }

  const formFilters = reactive({ ...initialFilters })

  const formItems = computed(() => [
    {
      label: '触发方式',
      key: 'triggerType',
      type: 'select',
      span: 4,
      labelWidth: 72,
      props: {
        clearable: true,
        placeholder: '全部触发方式',
        options: [
          { value: 'manual', label: '手动触发' },
          { value: 'schedule', label: '定时触发' },
          { value: 'event', label: '事件触发' },
          { value: 'webhook', label: 'Webhook 触发' }
        ]
      }
    },
    {
      label: '执行状态',
      key: 'status',
      type: 'select',
      span: 4,
      labelWidth: 72,
      props: {
        clearable: true,
        placeholder: '全部执行状态',
        options: [
          { value: 'queued', label: '排队中' },
          { value: 'running', label: '运行中' },
          { value: 'retry_waiting', label: '等待重试' },
          { value: 'success', label: '成功' },
          { value: 'failed', label: '失败' },
          { value: 'canceled', label: '已取消' }
        ]
      }
    }
  ])

  const workflowId = computed(() => String(route.query.workflowId || ''))
  const workflowName = computed(() => String(route.query.workflowName || ''))

  const triggerTypeLabel = (value: string) =>
    (
      ({
        manual: '手动触发',
        schedule: '定时触发',
        event: '事件触发',
        webhook: 'Webhook 触发'
      }) as Record<string, string>
    )[value] ||
    value ||
    '--'

  const statusTagType = (status: string) => {
    if (status === 'failed') return 'danger'
    if (status === 'success') return 'success'
    if (status === 'running' || status === 'retry_waiting') return 'warning'
    return 'info'
  }

  const formatStatusLabel = (status: string) =>
    (
      ({
        queued: '排队中',
        running: '运行中',
        retry_waiting: '等待重试',
        success: '成功',
        failed: '失败',
        canceled: '已取消'
      }) as Record<string, string>
    )[status] ||
    status ||
    '--'

  const renderViewButton = (row: WorkflowExecutionItem) =>
    h(
      ElButton,
      {
        circle: true,
        plain: true,
        size: 'small',
        icon: View,
        title: '查看详情',
        onClick: () =>
          router.push({
            path: `/scheduler/execution/${row.id}/detail`,
            query: { workflowId: workflowId.value, workflowName: workflowName.value }
          })
      },
      {}
    )

  const { columns, columnChecks } = useTableColumns<WorkflowExecutionItem>(() => [
    {
      prop: 'workflowDefinitionName',
      label: '工作流定义',
      minWidth: 180,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'workflowDefinitionVersion',
      label: '定义版本',
      width: 100,
      align: 'center',
      formatter: (row) => `v${row.workflowDefinitionVersion}`
    },
    {
      prop: 'entryName',
      label: '开始入口',
      minWidth: 160,
      align: 'center',
      formatter: (row) => row.entryName || '--'
    },
    {
      prop: 'triggerType',
      label: '触发方式',
      minWidth: 120,
      align: 'center',
      formatter: (row) => row.triggerLabel || triggerTypeLabel(row.triggerType)
    },
    {
      prop: 'status',
      label: '执行状态',
      minWidth: 100,
      align: 'center',
      formatter: (row) =>
        h(
          ElTag,
          { type: statusTagType(row.status), effect: 'plain' },
          () => row.statusLabel || formatStatusLabel(row.status)
        )
    },
    {
      prop: 'startedAt',
      label: '开始/入队时间',
      minWidth: 170,
      align: 'center',
      formatter: (row) => formatDateTime(row.startedAt || row.queuedAt)
    },
    {
      prop: 'finishedAt',
      label: '结束时间',
      minWidth: 170,
      align: 'center',
      formatter: (row) => formatDateTime(row.finishedAt)
    },
    {
      prop: 'durationMs',
      label: '耗时(ms)',
      width: 110,
      align: 'center',
      formatter: (row) => (row.durationMs ? row.durationMs : '--')
    },
    {
      prop: 'error',
      label: '错误信息',
      minWidth: 220,
      align: 'center',
      showOverflowTooltip: true,
      formatter: (row) => row.error?.summary || '--'
    },
    {
      prop: 'operation',
      label: '操作',
      width: 90,
      align: 'center',
      formatter: (row) => renderViewButton(row)
    }
  ])

  const hasInflightExecutions = computed(() =>
    executionList.value.records.some(
      (item) =>
        item.status === 'queued' || item.status === 'running' || item.status === 'retry_waiting'
    )
  )

  const clearPollTimer = () => {
    if (pollTimer !== null) {
      window.clearTimeout(pollTimer)
      pollTimer = null
    }
  }

  const schedulePoll = () => {
    clearPollTimer()
    if (!hasInflightExecutions.value) return
    pollTimer = window.setTimeout(() => {
      void loadPageData({ silent: true })
    }, 2000)
  }

  const loadPageData = async (options: { silent?: boolean } = {}) => {
    if (!Number.isSafeInteger(Number(workflowId.value)) || Number(workflowId.value) <= 0) {
      await router.replace('/scheduler/definition')
      return
    }
    clearPollTimer()
    if (!options.silent) {
      loading.value = true
    }
    try {
      executionList.value = await fetchWorkflowExecutionList({
        ...requestParams(),
        workflowDefinitionCode: workflowId.value,
        triggerType: formFilters.triggerType || undefined,
        status: formFilters.status || undefined
      } satisfies WorkflowExecutionQueryParams)
      applyPage(executionList.value)
    } catch (error) {
      if (!options.silent) {
        console.error(error)
      }
    } finally {
      if (!options.silent) {
        loading.value = false
      }
      schedulePoll()
    }
  }

  const handleSearch = () => {
    reset()
    void loadPageData()
  }

  const handleReset = () => {
    Object.assign(formFilters, { ...initialFilters })
    reset()
    void loadPageData()
  }

  const handleCurrentChange = (current: number) => {
    if (moveTo(current)) void loadPageData()
  }

  const handleSizeChange = (size: number) => {
    reset(size)
    void loadPageData()
  }

  watch(
    workflowId,
    () => {
      reset()
      void loadPageData()
    },
    { immediate: true }
  )

  onBeforeUnmount(() => {
    clearPollTimer()
  })
</script>

<style scoped lang="scss">
  .workflow-execution-page {
    :deep(.art-search-bar .el-form-item__label) {
      white-space: nowrap;
    }
  }
</style>
