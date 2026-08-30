<!-- 平台级 OpenAI-compatible 模型配置。 -->
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
          <ElButton @click="openAddDialog">{{ t('permissions.add') }}</ElButton>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :columns="columns" :data="modelList" :stripe="false" />
    </ElCard>

    <AiModelDialog
      v-model:visible="dialogVisible"
      :model-data="currentModel"
      :submitting="dialogSubmitting"
      @submit="handleDialogSubmit"
    />
  </div>
</template>

<script setup lang="ts">
  import { CircleCheck, Delete, Edit } from '@element-plus/icons-vue'
  import { ElButton, ElMessage, ElMessageBox, ElSwitch } from 'element-plus'
  import type { Component } from 'vue'
  import { useI18n } from 'vue-i18n'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import {
    fetchAiModelList,
    fetchCreateAiModel,
    fetchDeleteAiModel,
    fetchDisableAiModel,
    fetchEnableAiModel,
    fetchUpdateAiModel,
    fetchValidateAiModel,
    type AiModelConfigItem,
    type AiModelUpsertPayload
  } from '@/api/config'
  import { formatDateTime } from '@/utils/date'
  import AiModelDialog from './modules/ai-model-dialog.vue'

  defineOptions({ name: 'AiModelConfigPage' })

  const { t } = useI18n()
  const loading = ref(false)
  const dialogVisible = ref(false)
  const dialogSubmitting = ref(false)
  const modelList = ref<AiModelConfigItem[]>([])
  const currentModel = ref<AiModelConfigItem | null>(null)
  const validatingModelId = ref<number | null>(null)

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

  const renderActions = (row: AiModelConfigItem) =>
    h('div', { class: 'table-actions' }, [
      renderIconAction({
        icon: Edit,
        title: t('permissions.edit'),
        onClick: () => openEditDialog(row)
      }),
      renderIconAction({
        icon: CircleCheck,
        title: t('aiConfig.actions.validate'),
        type: 'primary',
        loading: validatingModelId.value === row.id,
        onClick: () => handleValidate(row)
      }),
      renderIconAction({
        icon: Delete,
        title: t('permissions.delete'),
        type: 'danger',
        onClick: () => handleDelete(row)
      })
    ])

  const { columns, columnChecks } = useTableColumns<AiModelConfigItem>(() => [
    {
      prop: 'displayName',
      label: t('aiConfig.table.displayName'),
      minWidth: 150,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'modelName',
      label: t('aiConfig.table.modelName'),
      minWidth: 160,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'baseUrl',
      label: t('aiConfig.table.baseUrl'),
      minWidth: 210,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'apiKeyMasked',
      label: t('aiConfig.table.apiKey'),
      minWidth: 120,
      align: 'center',
      formatter: (row) => row.apiKeyMasked || t('aiConfig.table.noApiKey')
    },
    {
      prop: 'isEnabled',
      label: t('aiConfig.table.status'),
      minWidth: 90,
      align: 'center',
      formatter: (row) =>
        h(ElSwitch, {
          modelValue: row.isEnabled,
          'onUpdate:modelValue': (value: boolean | string | number) =>
            handleToggleEnabled(row, Boolean(value))
        })
    },
    {
      prop: 'priority',
      label: t('aiConfig.table.priority'),
      minWidth: 90,
      align: 'center'
    },
    {
      prop: 'timeoutMs',
      label: t('aiConfig.table.timeout'),
      minWidth: 110,
      align: 'center'
    },
    {
      prop: 'sessionCount',
      label: t('aiConfig.table.sessionCount'),
      minWidth: 90,
      align: 'center'
    },
    {
      prop: 'updatedAt',
      label: t('aiConfig.table.updatedAt'),
      minWidth: 160,
      align: 'center',
      formatter: (row) => formatDateTime(row.updatedAt)
    },
    {
      prop: 'operation',
      label: t('aiConfig.table.action'),
      width: 150,
      align: 'center',
      fixed: 'right',
      formatter: renderActions
    }
  ])

  const loadPageData = async () => {
    loading.value = true
    try {
      modelList.value = await fetchAiModelList()
    } finally {
      loading.value = false
    }
  }

  const openAddDialog = () => {
    currentModel.value = null
    dialogVisible.value = true
  }

  const openEditDialog = (row: AiModelConfigItem) => {
    currentModel.value = { ...row }
    dialogVisible.value = true
  }

  const handleDialogSubmit = async (payload: AiModelUpsertPayload) => {
    if (dialogSubmitting.value) return
    dialogSubmitting.value = true
    try {
      if (currentModel.value) await fetchUpdateAiModel(currentModel.value.id, payload)
      else await fetchCreateAiModel(payload)
      dialogVisible.value = false
      await loadPageData()
    } finally {
      dialogSubmitting.value = false
    }
  }

  const handleToggleEnabled = async (row: AiModelConfigItem, enabled: boolean) => {
    try {
      if (enabled) await fetchEnableAiModel(row.id)
      else await fetchDisableAiModel(row.id)
    } finally {
      await loadPageData()
    }
  }

  const handleValidate = async (row: AiModelConfigItem) => {
    if (validatingModelId.value === row.id) return
    validatingModelId.value = row.id
    try {
      const result = await fetchValidateAiModel(row.id)
      if (result.success) ElMessage.success(result.message)
      else ElMessage.error(result.message)
    } finally {
      validatingModelId.value = null
    }
  }

  const handleDelete = async (row: AiModelConfigItem) => {
    await ElMessageBox.confirm(
      t('aiConfig.deleteConfirm', { name: row.displayName }),
      t('common.tips'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel')
      }
    )
    await fetchDeleteAiModel(row.id)
    await loadPageData()
  }

  onMounted(loadPageData)
</script>

<style scoped lang="scss">
  .table-actions {
    display: flex;
    gap: 8px;
    align-items: center;
    justify-content: center;
  }
</style>
