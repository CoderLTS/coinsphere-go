<!-- 助手代理配置页面或组件：index。 -->
<template>
  <div class="art-full-height">
    <ElCard class="art-table-card">
      <ArtTableHeader
        :showZebra="false"
        :loading="loading"
        v-model:columns="columnChecks"
        @refresh="loadPageData"
      >
        <template #left>
          <ElSpace wrap>
            <ElButton v-if="hasAuth('config.assistant_agents.create')" @click="openAddDialog">
              {{ t('permissions.add') }}
            </ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :columns="columns" :data="agentList" :stripe="false" />
    </ElCard>

    <AssistantAgentDialog
      v-model:visible="dialogVisible"
      :meta="agentMeta"
      :agent-data="currentAgent"
      :submitting="dialogSubmitting"
      @submit="handleDialogSubmit"
    />
  </div>
</template>

<script setup lang="ts">
  import { Delete, Edit } from '@element-plus/icons-vue'
  import { ElButton, ElMessageBox, ElSwitch, ElTag } from 'element-plus'
  import type { Component } from 'vue'
  import { useI18n } from 'vue-i18n'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import {
    fetchAssistantAgentList,
    fetchAssistantAgentMeta,
    fetchCreateAssistantAgent,
    fetchDeleteAssistantAgent,
    fetchDisableAssistantAgent,
    fetchEnableAssistantAgent,
    fetchUpdateAssistantAgent,
    type AssistantAgentItem,
    type AssistantAgentMeta,
    type AssistantAgentUpsertPayload
  } from '@/api/config'
  import { useAuth } from '@/hooks/core/useAuth'
  import AssistantAgentDialog from './modules/assistant-agent-dialog.vue'

  defineOptions({ name: 'AssistantAgentConfigPage' })

  const { t } = useI18n()
  const { hasAuth } = useAuth()

  const loading = ref(false)
  const dialogVisible = ref(false)
  const dialogSubmitting = ref(false)
  const agentList = ref<AssistantAgentItem[]>([])
  const agentMeta = ref<AssistantAgentMeta | null>(null)
  const currentAgent = ref<AssistantAgentItem | null>(null)

  const builtinAgentCodes = computed(() => agentMeta.value?.builtinAgentCodes || [])

  const getDataSourceLabel = (value: string) =>
    agentMeta.value?.dataSourceOptions.find((item) => item.value === value)?.label || value

  const renderIconAction = (options: {
    icon: Component
    title: string
    onClick: () => void
    type?: '' | 'danger'
    disabled?: boolean
  }) =>
    h(ElButton, {
      class: 'icon-action',
      size: 'small',
      circle: true,
      plain: true,
      icon: options.icon,
      type: options.type,
      disabled: options.disabled,
      title: options.title,
      onClick: options.onClick
    })

  const renderActions = (row: AssistantAgentItem) =>
    h('div', { class: 'table-actions' }, [
      hasAuth('config.assistant_agents.update')
        ? renderIconAction({
            icon: Edit,
            title: t('permissions.edit'),
            onClick: () => openEditDialog(row)
          })
        : null,
      hasAuth('config.assistant_agents.delete')
        ? renderIconAction({
            icon: Delete,
            title: t('permissions.delete'),
            type: 'danger',
            disabled: builtinAgentCodes.value.includes(row.code),
            onClick: () => handleDelete(row)
          })
        : null
    ])

  const { columns, columnChecks } = useTableColumns<AssistantAgentItem>(() => [
    {
      prop: 'displayName',
      label: t('assistantAgent.table.displayName'),
      minWidth: 180,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'dataSourceType',
      label: t('assistantAgent.table.dataSourceType'),
      minWidth: 140,
      align: 'center',
      formatter: (row) =>
        h(ElTag, { effect: 'plain' }, () => getDataSourceLabel(row.dataSourceType))
    },
    {
      prop: 'bindingCount',
      label: t('assistantAgent.table.bindingCount'),
      minWidth: 100,
      align: 'center'
    },
    {
      prop: 'sessionCount',
      label: t('assistantAgent.table.sessionCount'),
      minWidth: 100,
      align: 'center'
    },
    {
      prop: 'isEnabled',
      label: t('assistantAgent.table.status'),
      minWidth: 100,
      align: 'center',
      formatter: (row) =>
        h(ElSwitch, {
          modelValue: row.isEnabled,
          disabled: !hasAuth('config.assistant_agents.update'),
          'onUpdate:modelValue': (value: boolean | string | number) =>
            handleToggleEnabled(row, Boolean(value))
        })
    },
    {
      prop: 'updatedAt',
      label: t('assistantAgent.table.updatedAt'),
      minWidth: 160,
      align: 'center'
    },
    {
      prop: 'operation',
      label: t('assistantAgent.table.action'),
      width: 140,
      fixed: 'right',
      align: 'center',
      formatter: (row) => renderActions(row)
    }
  ])

  const loadPageData = async () => {
    loading.value = true
    try {
      const [meta, list] = await Promise.all([fetchAssistantAgentMeta(), fetchAssistantAgentList()])
      agentMeta.value = meta
      agentList.value = list
    } finally {
      loading.value = false
    }
  }

  const openAddDialog = () => {
    currentAgent.value = null
    dialogVisible.value = true
  }

  const openEditDialog = (row: AssistantAgentItem) => {
    currentAgent.value = { ...row }
    dialogVisible.value = true
  }

  const handleDialogSubmit = async (payload: AssistantAgentUpsertPayload) => {
    if (dialogSubmitting.value) return
    dialogSubmitting.value = true
    try {
      if (currentAgent.value?.id) {
        await fetchUpdateAssistantAgent(currentAgent.value.id, payload)
      } else {
        await fetchCreateAssistantAgent(payload)
      }
      dialogVisible.value = false
      currentAgent.value = null
      await loadPageData()
    } finally {
      dialogSubmitting.value = false
    }
  }

  const handleToggleEnabled = async (row: AssistantAgentItem, enabled: boolean) => {
    try {
      if (enabled) {
        await fetchEnableAssistantAgent(row.id)
      } else {
        await fetchDisableAssistantAgent(row.id)
      }
    } finally {
      await loadPageData()
    }
  }

  const handleDelete = async (row: AssistantAgentItem) => {
    await ElMessageBox.confirm(
      t('assistantAgent.deleteConfirm', { name: row.displayName }),
      t('common.tips'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel')
      }
    )
    await fetchDeleteAssistantAgent(row.id)
    await loadPageData()
  }

  onMounted(() => {
    loadPageData()
  })
</script>

<style scoped lang="scss">
  .table-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    justify-content: center;
  }
</style>
