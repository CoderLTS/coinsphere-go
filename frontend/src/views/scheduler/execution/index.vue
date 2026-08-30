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
            <strong>{{ workflowName || '工作流' }} · 历史日志</strong>
            <ElButton @click="router.push('/scheduler/definition')">返回工作流定义</ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :columns="columns"
        :data="runList.records"
        :pagination="{
          current: pagination.current,
          size: pagination.size,
          total: runList.total
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
    fetchWorkflow,
    fetchWorkflowRuns,
    type WorkflowRun,
    type WorkflowRunQuery
  } from '@/api/workflows'

  defineOptions({ name: 'SchedulerWorkflowExecutionsPage' })

  const router = useRouter()
  const route = useRoute()
  const loading = ref(false)
  const runList = ref({ records: [] as WorkflowRun[], nextCursor: '', hasMore: false, total: 0 })
  const loadedWorkflowName = ref('')
  let pollTimer: number | null = null

  const { pagination, requestParams, applyPage, reset, moveTo } = useCursorPagination(10)
  const recentWindow = () => [new Date(Date.now() - 24 * 60 * 60 * 1000), new Date()]
  const initialFilters = {
    triggerType: '',
    status: '',
    keyword: '',
    timeRange: recentWindow()
  }
  const formFilters = reactive({ ...initialFilters })
  const defaultWindow = ref(true)

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
          { value: 'stream', label: '流式触发' },
          { value: 'webhook', label: 'Webhook 触发' },
          { value: 'failure', label: '失败触发' }
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
          { value: 'waiting', label: '等待中' },
          { value: 'retrying', label: '等待重试' },
          { value: 'succeeded', label: '成功' },
          { value: 'failed', label: '失败' },
          { value: 'cancelled', label: '已取消' }
        ]
      }
    },
    {
      label: 'UTC 时间',
      key: 'timeRange',
      type: 'datetimerange',
      span: 8,
      props: {
        type: 'datetimerange',
        rangeSeparator: '至',
        startPlaceholder: '开始时间',
        endPlaceholder: '结束时间',
        clearable: false
      }
    },
    {
      label: '关键词',
      key: 'keyword',
      type: 'input',
      span: 5,
      props: { clearable: true, maxlength: 200, placeholder: '节点日志或错误' }
    }
  ])

  const workflowId = computed(() => Number(route.query.workflowId || 0))
  const workflowName = computed(
    () => String(route.query.workflowName || '').trim() || loadedWorkflowName.value
  )

  const triggerTypeLabel = (value: string) =>
    (
      ({
        manual: '手动触发',
        schedule: '定时触发',
        event: '事件触发',
        stream: '流式触发',
        webhook: 'Webhook 触发',
        failure: '失败触发'
      }) as Record<string, string>
    )[value] || value || '--'

  const statusLabel = (value: string) =>
    (
      ({
        queued: '排队中',
        running: '运行中',
        waiting: '等待中',
        retrying: '等待重试',
        succeeded: '成功',
        failed: '失败',
        cancelled: '已取消'
      }) as Record<string, string>
    )[value] || value || '--'

  const statusTagType = (status: string) => {
    if (status === 'failed') return 'danger'
    if (status === 'succeeded') return 'success'
    if (['running', 'waiting', 'retrying'].includes(status)) return 'warning'
    return 'info'
  }

  const duration = (run: WorkflowRun) => {
    if (!run.startedAt || !run.completedAt) return '--'
    return `${Math.max(0, Date.parse(run.completedAt) - Date.parse(run.startedAt))} ms`
  }

  const formatUTC = (value?: string) => {
    if (!value) return '--'
    const date = new Date(value)
    return Number.isNaN(date.getTime())
      ? value
      : date.toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, ' UTC')
  }

  const openDetail = (run: WorkflowRun) =>
    router.push({
      path: `/scheduler/execution/${run.id}/detail`,
      query: {
        workflowId: String(workflowId.value),
        workflowName: workflowName.value,
        followLatest: '0'
      }
    })

  const { columns, columnChecks } = useTableColumns<WorkflowRun>(() => [
    { prop: 'id', label: 'Run ID', width: 100, align: 'center' },
    {
      prop: 'revisionId',
      label: '修订 ID',
      width: 100,
      align: 'center',
      formatter: (row) => String(row.revisionId)
    },
    {
      prop: 'triggerType',
      label: '触发方式',
      minWidth: 120,
      align: 'center',
      formatter: (row) => triggerTypeLabel(row.triggerType)
    },
    {
      prop: 'status',
      label: '执行状态',
      minWidth: 100,
      align: 'center',
      formatter: (row) =>
        h(ElTag, { type: statusTagType(row.status), effect: 'plain' }, () => statusLabel(row.status))
    },
    {
      prop: 'triggeredAt',
      label: '触发时间（UTC）',
      minWidth: 180,
      align: 'center',
      formatter: (row) => formatUTC(row.triggeredAt)
    },
    {
      prop: 'completedAt',
      label: '结束时间（UTC）',
      minWidth: 180,
      align: 'center',
      formatter: (row) => formatUTC(row.completedAt)
    },
    {
      prop: 'duration',
      label: '耗时',
      width: 120,
      align: 'center',
      formatter: duration
    },
    {
      prop: 'errorMessage',
      label: '错误信息',
      minWidth: 220,
      align: 'center',
      showOverflowTooltip: true,
      formatter: (row) => row.errorMessage || '--'
    },
    {
      prop: 'operation',
      label: '操作',
      width: 90,
      align: 'center',
      formatter: (row) =>
        h(ElButton, {
          circle: true,
          plain: true,
          size: 'small',
          icon: View,
          title: '查看详情',
          onClick: () => openDetail(row)
        })
    }
  ])

  const hasInflightRuns = computed(() =>
    runList.value.records.some((run) => ['queued', 'running', 'waiting', 'retrying'].includes(run.status))
  )

  const clearPollTimer = () => {
    if (pollTimer !== null) window.clearTimeout(pollTimer)
    pollTimer = null
  }

  const schedulePoll = () => {
    clearPollTimer()
    if (!hasInflightRuns.value) return
    pollTimer = window.setTimeout(() => void loadPageData({ silent: true }), 2000)
  }

  const loadPageData = async (options: { silent?: boolean } = {}) => {
    if (!Number.isSafeInteger(workflowId.value) || workflowId.value <= 0) {
      await router.replace('/scheduler/definition')
      return
    }
    clearPollTimer()
    if (!options.silent) loading.value = true
    try {
      if (defaultWindow.value) formFilters.timeRange = recentWindow()
      const [from, to] = formFilters.timeRange || []
      const [runs, workflow] = await Promise.all([
        fetchWorkflowRuns(workflowId.value, {
          ...requestParams(),
          triggerType: formFilters.triggerType || undefined,
          status: formFilters.status || undefined,
          from: from ? new Date(from).toISOString() : undefined,
          to: to ? new Date(to).toISOString() : undefined,
          keyword: formFilters.keyword.trim() || undefined
        } satisfies WorkflowRunQuery),
        workflowName.value ? null : fetchWorkflow(workflowId.value)
      ])
      runList.value = runs
      if (workflow) loadedWorkflowName.value = workflow.name
      applyPage(runs)
    } finally {
      if (!options.silent) loading.value = false
      schedulePoll()
    }
  }

  const handleSearch = () => {
    defaultWindow.value = false
    reset()
    void loadPageData()
  }

  const handleReset = () => {
    Object.assign(formFilters, { ...initialFilters, timeRange: recentWindow() })
    defaultWindow.value = true
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
      loadedWorkflowName.value = ''
      reset()
      void loadPageData()
    },
    { immediate: true }
  )

  onBeforeUnmount(clearPollTimer)
</script>

<style scoped lang="scss">
  .workflow-execution-page :deep(.art-search-bar .el-form-item__label) {
    white-space: nowrap;
  }
</style>
