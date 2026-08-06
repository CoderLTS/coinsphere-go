<!-- 通知投递记录页面：index。 -->
<template>
  <div class="art-full-height">
    <ArtSearchBar
      v-model="formFilters"
      :items="formItems"
      :span="5"
      :showExpand="false"
      label-width="86px"
      @reset="handleReset"
      @search="handleSearch"
    />

    <ElCard class="art-table-card">
      <ArtTableHeader
        :showZebra="false"
        :loading="loading"
        v-model:columns="columnChecks"
        @refresh="loadRecords"
      />

      <ArtTable
        :loading="loading"
        :columns="columns"
        :data="records"
        :pagination="pagination"
        :pagination-options="{ pageSizes: [10, 20, 50], layout: 'total, prev, next, sizes' }"
        :stripe="false"
        @pagination:size-change="handleSizeChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { ElTag, ElTooltip } from 'element-plus'
  import { fetchPushDeliveryList, type NotifyDeliveryItem } from '@/api/data'
  import { fetchWorkflowDefinitionList } from '@/api/scheduler'
  import { useCursorPagination } from '@/hooks/core/useCursorPagination'
  import { useTableColumns } from '@/hooks/core/useTableColumns'

  defineOptions({ name: 'PushDataPage' })

  const loading = ref(false)
  const records = ref<NotifyDeliveryItem[]>([])
  const { pagination, requestParams, applyPage, reset, moveTo } = useCursorPagination(10)
  const definitionOptions = ref<Array<{ value: number; label: string }>>([])

  const initialFilters = {
    keyword: '',
    workflowDefinitionId: null as number | null,
    channelType: '' as Api.Config.NotifyChannelType | '',
    deliveryStatus: '' as '' | 'pending' | 'success' | 'failed' | 'skipped_offline'
  }

  const formFilters = reactive({ ...initialFilters })

  const formItems = computed(() => [
    {
      label: '关键词',
      key: 'keyword',
      type: 'input',
      span: 5,
      labelWidth: 60,
      props: {
        clearable: true,
        placeholder: '标题 / 内容 / 收件人'
      }
    },
    {
      label: '工作流定义',
      key: 'workflowDefinitionId',
      type: 'select',
      span: 5,
      labelWidth: 86,
      props: {
        clearable: true,
        placeholder: '全部定义',
        options: definitionOptions.value
      }
    },
    {
      label: '通知渠道',
      key: 'channelType',
      type: 'select',
      span: 4,
      labelWidth: 72,
      props: {
        clearable: true,
        placeholder: '全部渠道',
        options: [
          { value: 'in_app', label: '站内通知' },
          { value: 'dingtalk_webhook', label: '钉钉机器人' },
          { value: 'smtp_email', label: '邮件通知' }
        ]
      }
    },
    {
      label: '投递状态',
      key: 'deliveryStatus',
      type: 'select',
      span: 4,
      labelWidth: 72,
      props: {
        clearable: true,
        placeholder: '全部状态',
        options: [
          { value: 'success', label: '发送成功' },
          { value: 'failed', label: '发送失败' },
          { value: 'pending', label: '待发送' },
          { value: 'skipped_offline', label: '离线跳过' }
        ]
      }
    }
  ])

  const statusType = (status: string) => {
    if (status === 'success') return 'success'
    if (status === 'failed') return 'danger'
    if (status === 'skipped_offline') return 'warning'
    return 'info'
  }

  const previewText = (row: NotifyDeliveryItem) =>
    row.messageTitle || row.messageContent || row.errorMessage || '--'
  const clipText = (value: string, maxLength: number) =>
    value.length > maxLength ? `${value.slice(0, maxLength)}...` : value

  const { columns, columnChecks } = useTableColumns<NotifyDeliveryItem>(() => [
    {
      prop: 'messageTitle',
      label: '消息内容',
      minWidth: 240,
      formatter: (row) =>
        h(
          ElTooltip,
          {
            content: [row.messageTitle, row.messageContent].filter(Boolean).join('\n\n') || '--',
            placement: 'top'
          },
          {
            default: () => h('span', { class: 'content-preview' }, clipText(previewText(row), 28))
          }
        )
    },
    {
      prop: 'workflowDefinitionName',
      label: '工作流定义',
      minWidth: 180,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'targetLabel',
      label: '通知目标',
      minWidth: 140,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'recipientLabel',
      label: '接收用户',
      minWidth: 140,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'channelDisplayName',
      label: '渠道',
      minWidth: 150,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'deliveryStatus',
      label: '状态',
      minWidth: 120,
      align: 'center',
      formatter: (row) =>
        h(
          ElTag,
          { size: 'small', type: statusType(row.deliveryStatus), effect: 'plain' },
          () => row.deliveryStatusLabel
        )
    },
    {
      prop: 'sentAt',
      label: '发送时间',
      minWidth: 170,
      align: 'center'
    },
    {
      prop: 'createdAt',
      label: '创建时间',
      minWidth: 170,
      align: 'center'
    }
  ])

  const loadRecords = async () => {
    loading.value = true
    try {
      const data = await fetchPushDeliveryList({
        ...requestParams(),
        keyword: formFilters.keyword || undefined,
        workflowDefinitionId: formFilters.workflowDefinitionId || undefined,
        channelType: formFilters.channelType || undefined,
        deliveryStatus: formFilters.deliveryStatus || undefined
      })
      records.value = data.records
      applyPage(data)
    } finally {
      loading.value = false
    }
  }

  const handleSearch = () => {
    reset()
    void loadRecords()
  }

  const handleReset = () => {
    Object.assign(formFilters, { ...initialFilters })
    reset()
    void loadRecords()
  }

  const handleCurrentChange = (current: number) => {
    if (moveTo(current)) void loadRecords()
  }

  const handleSizeChange = (size: number) => {
    reset(size)
    void loadRecords()
  }

  onMounted(() => {
    void Promise.all([fetchWorkflowDefinitionList(), loadRecords()]).then(([definitions]) => {
      definitionOptions.value = definitions.map((item) => ({
        value: item.id,
        label: item.displayName
      }))
    })
  })
</script>

<style scoped lang="scss">
  .content-preview {
    display: inline-block;
    max-width: 100%;
    overflow: hidden;
    font-weight: 600;
    color: var(--el-text-color-primary);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
