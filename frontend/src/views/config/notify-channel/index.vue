<!-- 通知渠道配置页面或组件：index。 -->
<template>
  <div class="art-full-height">
    <ElCard class="art-table-card">
      <ArtTableHeader :showZebra="false" :loading="loading" v-model:columns="columnChecks" @refresh="loadPageData">
        <template #left>
          <ElSpace wrap>
            <ElButton v-if="hasAuth('config.notification_channels.create')" @click="openAddDialog">
              {{ t('permissions.add') }}
            </ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :columns="columns" :data="channelList" :stripe="false" />
    </ElCard>

    <NotifyChannelDialog
      v-model:visible="dialogVisible"
      :meta="channelMeta"
      :channel-data="currentChannel"
      :submitting="dialogSubmitting"
      @submit="handleDialogSubmit"
    />
  </div>
</template>

<script setup lang="ts">
  import { CircleCheck, Delete, Edit } from '@element-plus/icons-vue'
  import { ElButton, ElMessageBox, ElSwitch, ElTag } from 'element-plus'
  import type { Component } from 'vue'
  import { useI18n } from 'vue-i18n'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import { useAuth } from '@/hooks/core/useAuth'
  import { useNotificationStore } from '@/store/modules/notification'
  import NotifyChannelDialog from './modules/notify-channel-dialog.vue'
  import {
    fetchCreateNotifyChannel,
    fetchDeleteNotifyChannel,
    fetchDisableNotifyChannel,
    fetchEnableNotifyChannel,
    fetchNotifyChannelList,
    fetchNotifyChannelMeta,
    fetchTestNotifyChannel,
    fetchUpdateNotifyChannel,
    type NotifyChannelItem,
    type NotifyChannelMeta,
    type NotifyChannelUpsertPayload
  } from '@/api/config'
  import { fetchTestInAppNotice } from '@/api/notifications'

  defineOptions({ name: 'NotifyChannelPage' })

  const { t } = useI18n()
  const { hasAuth } = useAuth()
  const notificationStore = useNotificationStore()

  const loading = ref(false)
  const dialogVisible = ref(false)
  const dialogSubmitting = ref(false)
  const channelList = ref<NotifyChannelItem[]>([])
  const channelMeta = ref<NotifyChannelMeta | null>(null)
  const currentChannel = ref<NotifyChannelItem | null>(null)
  const testingChannelId = ref<number | null>(null)
  const getTestStatusType = (status: string) => {
    if (status === 'success') return 'success'
    if (status === 'failed') return 'danger'
    return 'info'
  }

  const getTestStatusLabel = (status: string) => {
    if (status === 'success') return t('notifyChannel.status.success')
    if (status === 'failed') return t('notifyChannel.status.failed')
    return t('notifyChannel.status.unknown')
  }

  const renderIconAction = (options: {
    icon: Component
    title: string
    onClick: () => void
    type?: '' | 'primary' | 'danger'
    loading?: boolean
  }) =>
    h(ElButton, {
      class: 'icon-action',
      size: 'small',
      circle: true,
      plain: true,
      icon: options.icon,
      type: options.type,
      loading: options.loading,
      title: options.title,
      onClick: options.onClick
    })

  const renderActions = (row: NotifyChannelItem) =>
    h('div', { class: 'table-actions' }, [
      hasAuth('config.notification_channels.test') && (!row.isBuiltin || row.channelType === 'in_app')
        ? renderIconAction({
            icon: CircleCheck,
            title: t('notifyChannel.table.testStatus'),
            type: 'primary',
            loading: testingChannelId.value === row.id,
            onClick: () => handleTest(row)
          })
        : null,
      hasAuth('config.notification_channels.update') && !row.isBuiltin
        ? renderIconAction({
            icon: Edit,
            title: t('permissions.edit'),
            onClick: () => openEditDialog(row)
          })
        : null,
      hasAuth('config.notification_channels.delete') && !row.isBuiltin
        ? renderIconAction({
            icon: Delete,
            title: t('permissions.delete'),
            type: 'danger',
            onClick: () => handleDelete(row)
          })
        : null
    ])

  const { columns, columnChecks } = useTableColumns<NotifyChannelItem>(() => [
      {
        prop: 'displayName',
        label: t('notifyChannel.table.displayName'),
        minWidth: 140,
        align: 'center',
        showOverflowTooltip: true
      },
      {
        prop: 'channelTypeLabel',
        label: t('notifyChannel.table.channelType'),
        minWidth: 140,
        align: 'center'
      },
      {
        prop: 'ownerLabel',
        label: t('notifyChannel.table.owner'),
        minWidth: 140,
        align: 'center',
        showOverflowTooltip: true,
        formatter: (row: NotifyChannelItem) => row.ownerLabel || '--'
      },
      {
        prop: 'targetSummary',
        label: t('notifyChannel.table.targetSummary'),
        minWidth: 140,
        align: 'center',
        showOverflowTooltip: true
      },
      {
        prop: 'isEnabled',
        label: t('notifyChannel.table.status'),
        minWidth: 140,
        align: 'center',
        formatter: (row: NotifyChannelItem) =>
          h(ElSwitch, {
            modelValue: row.isEnabled,
            disabled: !hasAuth('config.notification_channels.update') || row.isBuiltin,
            'onUpdate:modelValue': (value: boolean | string | number) =>
              handleToggleEnabled(row, Boolean(value))
          })
      },
      {
        prop: 'lastTestStatus',
        label: t('notifyChannel.table.testStatus'),
        minWidth: 140,
        align: 'center',
        formatter: (row: NotifyChannelItem) =>
          h(
            ElTag,
            { type: getTestStatusType(row.lastTestStatus), size: 'small' },
            () => getTestStatusLabel(row.lastTestStatus)
          )
      },
      {
        prop: 'lastTestedAt',
        label: t('notifyChannel.table.lastTestedAt'),
        minWidth: 140,
        align: 'center'
      },
      {
        prop: 'updatedAt',
        label: t('notifyChannel.table.updatedAt'),
        minWidth: 140,
        align: 'center'
      },
      {
        prop: 'operation',
        label: t('notifyChannel.table.action'),
        width: 132,
        fixed: 'right',
        align: 'center',
        formatter: (row: NotifyChannelItem) => renderActions(row)
      }
    ])

  const loadPageData = async () => {
    loading.value = true
    try {
      const [meta, list] = await Promise.all([fetchNotifyChannelMeta(), fetchNotifyChannelList()])
      channelMeta.value = meta
      channelList.value = list
    } finally {
      loading.value = false
    }
  }

  const openAddDialog = () => {
    currentChannel.value = null
    dialogVisible.value = true
  }

  const openEditDialog = (row: NotifyChannelItem) => {
    currentChannel.value = { ...row }
    dialogVisible.value = true
  }

  const handleDialogSubmit = async (payload: NotifyChannelUpsertPayload) => {
    if (dialogSubmitting.value) {
      return
    }
    dialogSubmitting.value = true
    try {
      if (currentChannel.value?.id) {
        await fetchUpdateNotifyChannel(currentChannel.value.id, payload)
      } else {
        await fetchCreateNotifyChannel(payload)
      }
      dialogVisible.value = false
      currentChannel.value = null
      await loadPageData()
    } finally {
      dialogSubmitting.value = false
    }
  }

  const handleToggleEnabled = async (row: NotifyChannelItem, enabled: boolean) => {
    try {
      if (enabled) {
        await fetchEnableNotifyChannel(row.id)
      } else {
        await fetchDisableNotifyChannel(row.id)
      }
    } finally {
      await loadPageData()
    }
  }

  const handleTest = async (row: NotifyChannelItem) => {
    if (testingChannelId.value === row.id) {
      return
    }
    testingChannelId.value = row.id
    try {
      if (row.channelType === 'in_app') {
        await fetchTestInAppNotice()
        await notificationStore.loadNotices()
      } else {
        await fetchTestNotifyChannel(row.id)
      }
      await loadPageData()
    } finally {
      testingChannelId.value = null
    }
  }

  const handleDelete = async (row: NotifyChannelItem) => {
    await ElMessageBox.confirm(
      t('notifyChannel.deleteConfirm', { name: row.displayName }),
      t('common.tips'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel')
      }
    )
    await fetchDeleteNotifyChannel(row.id)
    await loadPageData()
  }

  onMounted(() => {
    loadPageData()
  })
</script>

<style scoped lang="scss">
  .table-actions {
    display: flex;
    justify-content: center;
    gap: 8px;
    flex-wrap: wrap;
  }
</style>
