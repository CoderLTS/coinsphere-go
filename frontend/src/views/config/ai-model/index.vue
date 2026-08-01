<!-- AI 模型配置页面或组件：index。 -->
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
            <ElButton v-if="hasAuth('config.ai_models.create')" @click="openAddDialog">
              {{ t('permissions.add') }}
            </ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :columns="columns" :data="modelList" :stripe="false" />
    </ElCard>

    <AiModelDialog
      v-model:visible="dialogVisible"
      :meta="providerMeta"
      :model-data="currentModel"
      :submitting="dialogSubmitting"
      @submit="handleDialogSubmit"
    />

    <ElDialog
      v-model="bindingDialogVisible"
      :title="t('aiConfig.agentDialog.title')"
      width="460px"
      destroy-on-close
    >
      <div class="bind-dialog">
        <p class="bind-dialog__tip">{{ t('aiConfig.agentDialog.description') }}</p>
        <ElForm label-width="92px">
          <ElFormItem :label="t('aiConfig.agentDialog.field')">
            <ElSelect
              v-model="bindingValue"
              multiple
              collapse-tags
              collapse-tags-tooltip
              :max-collapse-tags="2"
              :placeholder="t('aiConfig.agentDialog.placeholder')"
            >
              <ElOption
                v-for="item in agentOptions"
                :key="item.id"
                :label="item.displayName"
                :value="item.id"
                :disabled="!item.isEnabled && !bindingValue.includes(item.id)"
              >
                <div class="agent-option">
                  <span>{{ item.displayName }}</span>
                  <ElTag v-if="!item.isEnabled" size="small" type="info" effect="plain">
                    {{ t('aiConfig.agentDialog.disabled') }}
                  </ElTag>
                </div>
              </ElOption>
            </ElSelect>
          </ElFormItem>
        </ElForm>
      </div>
      <template #footer>
        <div class="dialog-footer">
          <ElButton @click="bindingDialogVisible = false">{{ t('common.cancel') }}</ElButton>
          <ElButton type="primary" @click="handleBindingSubmit">{{ t('common.confirm') }}</ElButton>
        </div>
      </template>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import { CircleCheck, Connection, Delete, Edit } from '@element-plus/icons-vue'
  import { ElButton, ElMessageBox, ElSwitch, ElTag } from 'element-plus'
  import type { Component } from 'vue'
  import { useI18n } from 'vue-i18n'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import {
    fetchAiModelList,
    fetchAiProviderMeta,
    fetchAssistantAgentMeta,
    fetchBindAiModelAgents,
    fetchCreateAiModel,
    fetchDeleteAiModel,
    fetchDisableAiModel,
    fetchEnableAiModel,
    fetchUpdateAiModel,
    fetchValidateAiModel,
    type AiModelConfigItem,
    type AiModelUpsertPayload,
    type AiProviderMeta,
    type AssistantAgentMeta
  } from '@/api/config'
  import { useAuth } from '@/hooks/core/useAuth'
  import AiModelDialog from './modules/ai-model-dialog.vue'

  defineOptions({ name: 'AiModelConfigPage' })

  const { t } = useI18n()
  const { hasAuth } = useAuth()

  const loading = ref(false)
  const dialogVisible = ref(false)
  const dialogSubmitting = ref(false)
  const bindingDialogVisible = ref(false)
  const modelList = ref<AiModelConfigItem[]>([])
  const providerMeta = ref<AiProviderMeta | null>(null)
  const agentMeta = ref<AssistantAgentMeta | null>(null)
  const currentModel = ref<AiModelConfigItem | null>(null)
  const bindingModel = ref<AiModelConfigItem | null>(null)
  const bindingValue = ref<number[]>([])
  const validatingModelId = ref<number | null>(null)

  const agentOptions = computed(() => agentMeta.value?.agentOptions || [])

  const getProviderTypeLabel = (provider: string) =>
    providerMeta.value?.providerOptions.find((item) => item.value === provider)?.label || provider

  const getValidationTagType = (status: string) => {
    if (status === 'success') return 'success'
    if (status === 'failed') return 'danger'
    return 'info'
  }

  const getValidationLabel = (status: string) => {
    if (status === 'success') return t('aiConfig.validation.statusSuccess')
    if (status === 'failed') return t('aiConfig.validation.statusFailed')
    return t('aiConfig.validation.statusUnknown')
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

  const renderActions = (row: AiModelConfigItem) =>
    h('div', { class: 'table-actions' }, [
      hasAuth('config.ai_models.update')
        ? renderIconAction({
            icon: Edit,
            title: t('permissions.edit'),
            onClick: () => openEditDialog(row)
          })
        : null,
      hasAuth('config.ai_models.bind_agents')
        ? renderIconAction({
            icon: Connection,
            title: t('aiConfig.actions.bindAgent'),
            onClick: () => openBindingDialog(row)
          })
        : null,
      hasAuth('config.ai_models.validate')
        ? renderIconAction({
            icon: CircleCheck,
            title: t('aiConfig.actions.validate'),
            type: 'primary',
            loading: validatingModelId.value === row.id,
            onClick: () => handleValidate(row)
          })
        : null,
      hasAuth('config.ai_models.delete')
        ? renderIconAction({
            icon: Delete,
            title: t('permissions.delete'),
            type: 'danger',
            onClick: () => handleDelete(row)
          })
        : null
    ])

  const { columns, columnChecks } = useTableColumns<AiModelConfigItem>(() => [
    {
      prop: 'displayName',
      label: t('aiConfig.table.displayName'),
      minWidth: 160,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'provider',
      label: t('aiConfig.table.provider'),
      minWidth: 150,
      align: 'center',
      formatter: (row) =>
        h('div', { class: 'provider-cell' }, [
          h('strong', row.providerName),
          h('small', getProviderTypeLabel(row.provider))
        ])
    },
    {
      prop: 'modelIdentifier',
      label: t('aiConfig.table.modelName'),
      minWidth: 160,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'baseUrl',
      label: t('aiConfig.table.baseUrl'),
      minWidth: 180,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'boundAgents',
      label: t('aiConfig.table.boundAgents'),
      minWidth: 180,
      align: 'center',
      formatter: (row) =>
        row.boundAgents.length
          ? h(
              'div',
              { class: 'tag-list' },
              row.boundAgents.map((agent) =>
                h(ElTag, { key: agent.id, size: 'small', effect: 'plain' }, () => agent.displayName)
              )
            )
          : h('span', { class: 'muted' }, t('aiConfig.table.noBoundAgents'))
    },
    {
      prop: 'isEnabled',
      label: t('aiConfig.table.status'),
      minWidth: 100,
      align: 'center',
      formatter: (row) =>
        h(ElSwitch, {
          modelValue: row.isEnabled,
          disabled: !hasAuth('config.ai_models.update'),
          'onUpdate:modelValue': (value: boolean | string | number) =>
            handleToggleEnabled(row, Boolean(value))
        })
    },
    {
      prop: 'sessionCount',
      label: t('aiConfig.table.sessionCount'),
      minWidth: 100,
      align: 'center'
    },
    {
      prop: 'lastValidationStatus',
      label: t('aiConfig.table.validation'),
      minWidth: 120,
      align: 'center',
      formatter: (row) =>
        h(ElTag, { type: getValidationTagType(row.lastValidationStatus), size: 'small' }, () =>
          getValidationLabel(row.lastValidationStatus)
        )
    },
    {
      prop: 'lastValidatedAt',
      label: t('aiConfig.table.validatedAt'),
      minWidth: 160,
      align: 'center'
    },
    {
      prop: 'updatedAt',
      label: t('aiConfig.table.updatedAt'),
      minWidth: 160,
      align: 'center'
    },
    {
      prop: 'operation',
      label: t('aiConfig.table.action'),
      width: 210,
      align: 'center',
      fixed: 'right',
      formatter: (row) => renderActions(row)
    }
  ])

  const loadPageData = async () => {
    loading.value = true
    try {
      const [meta, bindingsMeta, list] = await Promise.all([
        fetchAiProviderMeta(),
        fetchAssistantAgentMeta(),
        fetchAiModelList()
      ])
      providerMeta.value = meta
      agentMeta.value = bindingsMeta
      modelList.value = list
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

  const openBindingDialog = (row: AiModelConfigItem) => {
    bindingModel.value = { ...row }
    bindingValue.value = row.boundAgents.map((item) => item.id)
    bindingDialogVisible.value = true
  }

  const handleDialogSubmit = async (payload: AiModelUpsertPayload) => {
    if (dialogSubmitting.value) return
    dialogSubmitting.value = true
    try {
      if (currentModel.value?.id) {
        await fetchUpdateAiModel(currentModel.value.id, payload)
      } else {
        await fetchCreateAiModel(payload)
      }
      dialogVisible.value = false
      currentModel.value = null
      await loadPageData()
    } finally {
      dialogSubmitting.value = false
    }
  }

  const handleToggleEnabled = async (row: AiModelConfigItem, enabled: boolean) => {
    try {
      if (enabled) {
        await fetchEnableAiModel(row.id)
      } else {
        await fetchDisableAiModel(row.id)
      }
    } finally {
      await loadPageData()
    }
  }

  const handleBindingSubmit = async () => {
    if (!bindingModel.value?.id) return
    await fetchBindAiModelAgents(bindingModel.value.id, {
      agentIds: bindingValue.value
    })
    bindingDialogVisible.value = false
    bindingModel.value = null
    await loadPageData()
  }

  const handleValidate = async (row: AiModelConfigItem) => {
    if (validatingModelId.value === row.id) return
    validatingModelId.value = row.id
    try {
      await fetchValidateAiModel(row.id)
      await loadPageData()
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

  onMounted(() => {
    loadPageData()
  })
</script>

<style scoped lang="scss">
  .table-actions,
  .tag-list {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }

  .table-actions {
    justify-content: center;
  }

  .tag-list {
    justify-content: center;
  }

  .provider-cell {
    display: flex;
    flex-direction: column;
    gap: 6px;
    align-items: center;
  }

  .provider-cell strong {
    color: var(--el-text-color-primary);
  }

  .provider-cell small,
  .muted,
  .bind-dialog__tip {
    color: var(--el-text-color-secondary);
  }

  .bind-dialog__tip {
    margin: 0 0 16px;
    font-size: 13px;
    line-height: 1.7;
  }

  .agent-option {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
</style>
