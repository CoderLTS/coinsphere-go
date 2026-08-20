<template>
  <div class="node-console">
    <header class="console-head"
      ><div><div class="eyebrow">工作流调度</div><h1>节点定义</h1></div
      ><ElButton :icon="Refresh" :loading="loading" @click="loadData">刷新</ElButton></header
    >
    <ElCard shadow="never"
      ><ElTabs v-model="activeTab"
        ><ElTabPane label="内置节点" name="builtin"
          ><ElTable :data="definitions" v-loading="loading"
            ><ElTableColumn label="节点" min-width="210"
              ><template #default="{ row }"
                ><div class="node-name"
                  ><span><ArtSvgIcon icon="ri:node-tree" /></span
                  ><strong>{{ row.label }}</strong></div
                ></template
              ></ElTableColumn
            ><ElTableColumn label="能力" min-width="220"
              ><template #default="{ row }">{{ capabilityLabel(row) }}</template></ElTableColumn
            ><ElTableColumn label="配置项" min-width="320"
              ><template #default="{ row }"
                ><div class="field-tags"
                  ><ElTag
                    v-for="field in configFields(row)"
                    :key="field"
                    effect="plain"
                    size="small"
                    >{{ field }}</ElTag
                  ><span v-if="!configFields(row).length" class="muted">无需配置</span></div
                ></template
              ></ElTableColumn
            ><ElTableColumn label="状态" width="100"
              ><template #default
                ><ElTag type="success" effect="plain">可用</ElTag></template
              ></ElTableColumn
            ></ElTable
          ></ElTabPane
        >
        <ElTabPane label="我的节点模板" name="templates"
          ><div class="tab-actions"
            ><ElButton type="primary" :icon="Plus" @click="openTemplate()">新增模板</ElButton></div
          ><ElTable :data="templates" v-loading="loading"
            ><ElTableColumn label="模板" min-width="220"
              ><template #default="{ row }"
                ><div class="node-name"
                  ><span><ArtSvgIcon :icon="row.icon" /></span
                  ><div
                    ><strong>{{ row.name }}</strong
                    ><small>{{ row.description || '未填写说明' }}</small></div
                  ></div
                ></template
              ></ElTableColumn
            ><ElTableColumn prop="baseNodeLabel" label="执行能力" min-width="180" /><ElTableColumn
              label="状态"
              width="100"
              ><template #default="{ row }"
                ><ElTag :type="row.isEnabled ? 'success' : 'info'" effect="plain">{{
                  row.isEnabled ? '启用' : '停用'
                }}</ElTag></template
              ></ElTableColumn
            ><ElTableColumn prop="updatedAt" label="更新时间" min-width="180" /><ElTableColumn
              label="操作"
              width="140"
              ><template #default="{ row }"
                ><ElButton link type="primary" @click="openTemplate(row)">编辑</ElButton
                ><ElButton link type="danger" @click="removeTemplate(row)">删除</ElButton></template
              ></ElTableColumn
            ></ElTable
          ></ElTabPane
        >
        <ElTabPane label="通知渠道" name="channels"
          ><div class="tab-actions"
            ><ElButton type="primary" :icon="Plus" @click="openChannel()">新增渠道</ElButton></div
          ><ElTable :data="channels" v-loading="loading"
            ><ElTableColumn prop="displayName" label="渠道" min-width="180" /><ElTableColumn
              prop="channelTypeLabel"
              label="类型"
              min-width="150"
            /><ElTableColumn prop="targetSummary" label="发送目标" min-width="180" /><ElTableColumn
              label="状态"
              width="100"
              ><template #default="{ row }"
                ><ElTag :type="row.isEnabled ? 'success' : 'info'" effect="plain">{{
                  row.isEnabled ? '启用' : '停用'
                }}</ElTag></template
              ></ElTableColumn
            ><ElTableColumn label="最近测试" min-width="160"
              ><template #default="{ row }"
                >{{ testStatusLabel(row.lastTestStatus) }} ·
                {{ row.lastTestedAt || '未测试' }}</template
              ></ElTableColumn
            ><ElTableColumn label="操作" width="170"
              ><template #default="{ row }"
                ><ElButton
                  link
                  type="primary"
                  :loading="testingChannelId === row.id"
                  @click="testChannel(row)"
                  >测试</ElButton
                ><ElButton v-if="!row.isBuiltin" link type="primary" @click="openChannel(row)"
                  >编辑</ElButton
                ><ElButton v-if="!row.isBuiltin" link type="danger" @click="removeChannel(row)"
                  >删除</ElButton
                ></template
              ></ElTableColumn
            ></ElTable
          ></ElTabPane
        ></ElTabs
      ></ElCard
    >
    <ElDialog
      v-model="templateVisible"
      :title="editingTemplate ? '编辑节点模板' : '新增节点模板'"
      width="540px"
      ><ElForm :model="templateForm" label-position="top"
        ><ElFormItem label="模板名称" required
          ><ElInput v-model.trim="templateForm.name" maxlength="120" /></ElFormItem
        ><ElFormItem label="内置执行能力" required
          ><ElSelect
            v-model="templateForm.baseNodeType"
            filterable
            :disabled="Boolean(editingTemplate)"
            ><ElOption
              v-for="item in definitions"
              :key="item.typeCode"
              :label="item.label"
              :value="item.typeCode" /></ElSelect></ElFormItem
        ><ElFormItem label="说明"
          ><ElInput
            v-model="templateForm.description"
            type="textarea"
            :rows="3"
            maxlength="500" /></ElFormItem
        ><ElFormItem label="图标"
          ><ElInput v-model.trim="templateForm.icon" placeholder="ri:node-tree" /></ElFormItem
        ><ElFormItem label="默认配置"
          ><div class="config-form" v-if="selectedBaseDefinition"
            ><template v-for="field in baseConfigFields" :key="field.key"
              ><label>{{ field.label }}</label
              ><ElInput v-model="templateConfig[field.key]" /></template
            ><span v-if="!baseConfigFields.length" class="muted">该节点无需默认配置</span></div
          ></ElFormItem
        ><ElFormItem label="启用"><ElSwitch v-model="templateForm.isEnabled" /></ElFormItem></ElForm
      ><template #footer
        ><ElButton @click="templateVisible = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="saveTemplate">保存</ElButton></template
      ></ElDialog
    >
    <NotifyChannelDialog
      v-model:visible="channelVisible"
      :meta="channelMeta"
      :channel-data="editingChannel"
      :submitting="saving"
      @submit="saveChannel"
    />
  </div>
</template>

<script setup lang="ts">
  import { Plus, Refresh } from '@element-plus/icons-vue'
  import { ElMessageBox } from 'element-plus'
  import { fetchTestInAppNotice } from '@/api/notifications'
  import NotifyChannelDialog from '@/views/config/notify-channel/modules/notify-channel-dialog.vue'
  import {
    fetchCreateNotifyChannel,
    fetchDeleteNotifyChannel,
    fetchNotifyChannelList,
    fetchNotifyChannelMeta,
    fetchTestNotifyChannel,
    fetchUpdateNotifyChannel,
    type NotifyChannelItem,
    type NotifyChannelMeta,
    type NotifyChannelUpsertPayload
  } from '@/api/config'
  import {
    fetchCreateWorkflowNodeTemplate,
    fetchDeleteWorkflowNodeTemplate,
    fetchNodeDefinitions,
    fetchUpdateWorkflowNodeTemplate,
    fetchWorkflowNodeTemplates,
    type WorkflowNodeDefinitionItem,
    type WorkflowNodeTemplateItem
  } from '@/api/scheduler'

  defineOptions({ name: 'NodeDefinitions' })
  const route = useRoute()
  const activeTab = ref(route.query.tab === 'channels' ? 'channels' : 'builtin')
  const loading = ref(false)
  const saving = ref(false)
  const definitions = ref<WorkflowNodeDefinitionItem[]>([])
  const templates = ref<WorkflowNodeTemplateItem[]>([])
  const channels = ref<NotifyChannelItem[]>([])
  const channelMeta = ref<NotifyChannelMeta | null>(null)
  const templateVisible = ref(false)
  const channelVisible = ref(false)
  const editingTemplate = ref<WorkflowNodeTemplateItem | null>(null)
  const editingChannel = ref<NotifyChannelItem | null>(null)
  const testingChannelId = ref<number | null>(null)
  const templateConfig = reactive<Record<string, string>>({})
  const templateForm = reactive({
    name: '',
    description: '',
    icon: 'ri:node-tree',
    baseNodeType: '',
    isEnabled: true
  })
  const selectedBaseDefinition = computed(() =>
    definitions.value.find((item) => item.typeCode === templateForm.baseNodeType)
  )
  const hiddenConfigFields = new Set(['entryKey', 'workflowCode'])
  const baseConfigFields = computed(() =>
    Object.entries(
      (selectedBaseDefinition.value?.configSchema?.properties || {}) as Record<string, any>
    )
      .filter(([key]) => !hiddenConfigFields.has(key))
      .map(([key, value]) => ({ key, label: String(value?.title || key) }))
  )
  const configFields = (row: WorkflowNodeDefinitionItem) =>
    Object.entries((row.configSchema?.properties || {}) as Record<string, any>)
      .filter(([key]) => !hiddenConfigFields.has(key))
      .map(([, item]) => String(item?.title || ''))
      .filter(Boolean)
  const capabilityLabel = (row: WorkflowNodeDefinitionItem) =>
    ({
      plain: '处理工作流数据',
      start: '启动工作流',
      branch: '条件分流',
      loop: '循环处理',
      terminal: '结束工作流'
    })[row.kind || 'plain'] ||
    (row.typeCode.includes('notify')
      ? '发送通知'
      : row.typeCode.includes('strategy')
        ? '运行策略'
        : '处理工作流数据')
  const loadData = async () => {
    loading.value = true
    try {
      const [builtin, mine, channelList, meta] = await Promise.all([
        fetchNodeDefinitions(),
        fetchWorkflowNodeTemplates(),
        fetchNotifyChannelList(),
        fetchNotifyChannelMeta()
      ])
      definitions.value = builtin
      templates.value = mine
      channels.value = channelList
      channelMeta.value = meta
    } finally {
      loading.value = false
    }
  }
  const openTemplate = (row?: WorkflowNodeTemplateItem) => {
    editingTemplate.value = row || null
    Object.keys(templateConfig).forEach((key) => delete templateConfig[key])
    Object.assign(
      templateForm,
      row
        ? {
            name: row.name,
            description: row.description,
            icon: row.icon,
            baseNodeType: row.baseNodeType,
            isEnabled: row.isEnabled
          }
        : {
            name: '',
            description: '',
            icon: 'ri:node-tree',
            baseNodeType: definitions.value[0]?.typeCode || '',
            isEnabled: true
          }
    )
    if (row)
      Object.entries(row.defaultConfig || {})
        .filter(([key]) => !hiddenConfigFields.has(key))
        .forEach(([key, value]) => {
          templateConfig[key] = typeof value === 'string' ? value : JSON.stringify(value)
        })
    templateVisible.value = true
  }
  const saveTemplate = async () => {
    if (!templateForm.name || !templateForm.baseNodeType) return
    const defaultConfig = Object.fromEntries(
      Object.entries(templateConfig)
        .filter(([, value]) => value !== '')
        .map(([key, value]) => {
          try {
            return [key, JSON.parse(value)]
          } catch {
            return [key, value]
          }
        })
    )
    saving.value = true
    try {
      const payload = { ...templateForm, defaultConfig }
      if (editingTemplate.value)
        await fetchUpdateWorkflowNodeTemplate(editingTemplate.value.id, payload)
      else await fetchCreateWorkflowNodeTemplate(payload)
      templateVisible.value = false
      await loadData()
    } finally {
      saving.value = false
    }
  }
  const removeTemplate = async (row: WorkflowNodeTemplateItem) => {
    await ElMessageBox.confirm(`删除模板“${row.name}”？已保存工作流不受影响。`, '删除节点模板', {
      type: 'warning'
    })
    await fetchDeleteWorkflowNodeTemplate(row.id)
    await loadData()
  }
  const openChannel = (row?: NotifyChannelItem) => {
    editingChannel.value = row ? { ...row } : null
    channelVisible.value = true
  }
  const saveChannel = async (payload: NotifyChannelUpsertPayload) => {
    saving.value = true
    try {
      if (editingChannel.value) await fetchUpdateNotifyChannel(editingChannel.value.id, payload)
      else await fetchCreateNotifyChannel(payload)
      channelVisible.value = false
      await loadData()
    } finally {
      saving.value = false
    }
  }
  const testChannel = async (row: NotifyChannelItem) => {
    testingChannelId.value = row.id
    try {
      if (row.channelType === 'in_app') await fetchTestInAppNotice()
      else await fetchTestNotifyChannel(row.id)
      await loadData()
    } finally {
      testingChannelId.value = null
    }
  }
  const removeChannel = async (row: NotifyChannelItem) => {
    await ElMessageBox.confirm(`删除通知渠道“${row.displayName}”？`, '删除通知渠道', {
      type: 'warning'
    })
    await fetchDeleteNotifyChannel(row.id)
    await loadData()
  }
  const testStatusLabel = (value: string) =>
    ({ success: '成功', failed: '失败', unknown: '未测试' })[value] || '未测试'
  onMounted(() => void loadData())
</script>

<style scoped lang="scss">
  .node-console {
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding-bottom: 24px;
  }

  .console-head,
  .node-name,
  .card-head {
    display: flex;
    align-items: center;
  }

  .console-head {
    gap: 12px;
    justify-content: space-between;
  }

  .eyebrow {
    font-size: 12px;
    font-weight: 600;
    color: var(--el-color-primary);
    letter-spacing: 0.08em;
  }

  h1 {
    margin: 6px 0 0;
    font-size: 24px;
  }

  .tab-actions {
    display: flex;
    justify-content: flex-end;
    margin-bottom: 12px;
  }

  .node-name {
    gap: 10px;
  }

  .node-name > span {
    display: grid;
    place-items: center;
    width: 34px;
    height: 34px;
    color: var(--el-color-primary);
    background: var(--el-color-primary-light-9);
    border-radius: 7px;
  }

  .node-name small {
    display: block;
    max-width: 280px;
    margin-top: 3px;
    overflow: hidden;
    color: var(--el-text-color-secondary);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .field-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }

  .muted {
    color: var(--el-text-color-secondary);
  }

  .config-form {
    display: grid;
    grid-template-columns: 120px minmax(0, 1fr);
    gap: 8px 12px;
    align-items: center;
    width: 100%;
  }

  :deep(.el-select) {
    width: 100%;
  }

  @media (max-width: 680px) {
    .console-head {
      flex-direction: column;
      align-items: flex-start;
    }

    .config-form {
      grid-template-columns: 1fr;
    }
  }
</style>
