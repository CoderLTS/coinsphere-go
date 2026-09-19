<template>
  <div v-loading="loading" class="profile-manager">
    <header class="profile-manager__header">
      <div
        ><h2>{{ title }}</h2
        ><p>Profile 由插件维护；工作流只固定已发布版本。</p></div
      >
      <div class="profile-manager__actions"
        ><ElSelect v-model="type" clearable placeholder="全部类型" @change="load"
          ><ElOption v-for="item in types" :key="item" :value="item" :label="item" /></ElSelect
        ><ElButton type="primary" @click="openCreate">新建 Profile</ElButton></div
      >
    </header>
    <ElTable :data="profiles" stripe>
      <ElTableColumn prop="name" label="名称" min-width="180" />
      <ElTableColumn prop="summary" label="摘要" min-width="240" show-overflow-tooltip />
      <ElTableColumn prop="type" label="类型" min-width="150" />
      <ElTableColumn label="版本" width="130"
        ><template #default="{ row }">{{
          row.latestPublishedVersion || row.version || '草稿'
        }}</template></ElTableColumn
      >
      <ElTableColumn label="状态" width="100"
        ><template #default="{ row }"
          ><ElTag :type="statusType(row.status)">{{ statusLabel(row.status) }}</ElTag></template
        ></ElTableColumn
      >
      <ElTableColumn label="操作" width="300" fixed="right"
        ><template #default="{ row }"
          ><ElButton link @click="openEdit(row)">编辑</ElButton
          ><ElButton link @click="copy(row)">复制</ElButton
          ><ElButton v-if="row.status !== 'published'" link type="success" @click="publish(row)"
            >发布</ElButton
          ><ElButton v-if="row.status !== 'disabled'" link type="warning" @click="disable(row)"
            >停用</ElButton
          ></template
        ></ElTableColumn
      >
    </ElTable>
    <ElEmpty v-if="!loading && !profiles.length" description="暂无 Profile" />
    <ElDialog
      v-model="dialog"
      :title="editing ? '编辑 Profile' : '新建 Profile'"
      width="min(680px, 95vw)"
      destroy-on-close
    >
      <ElForm label-position="top"
        ><ElFormItem label="名称" required><ElInput v-model="form.name" /></ElFormItem
        ><ElFormItem label="摘要"><ElInput v-model="form.summary" type="textarea" /></ElFormItem
        ><WorkflowSchemaFields
          :schema="profileSchema"
          :ui-schema="profileUiSchema"
          :config="form.config"
          @update="(key, value) => (form.config[key] = value)"
      /></ElForm>
      <template #footer
        ><ElButton @click="dialog = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="save">保存草稿</ElButton></template
      >
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import { ElMessage, ElMessageBox } from 'element-plus'
  import {
    fetchProfiles,
    fetchProfileDescriptor,
    saveProfile,
    copyProfile,
    publishProfile,
    disableProfile,
    type ProfileRecord,
    type ProfileDescriptor
  } from '@/api/profiles'
  import WorkflowSchemaFields from '@/views/scheduler/workflow/editor/components/WorkflowSchemaFields.vue'
  import { schemaDefaults } from '@/views/scheduler/workflow/editor/canvas'
  import { loadPluginProfileTypes } from '@/plugins'
  const route = useRoute()
  const pluginId = computed(() =>
    String(
      route.query.pluginId || route.path.match(/\/plugins\/([^/]+)\//)?.[1] || 'official.binance'
    )
  )
  const title = computed(() => `${pluginId.value} Profiles`)
  const loading = ref(false),
    saving = ref(false),
    dialog = ref(false),
    editing = ref<ProfileRecord | null>(null)
  const profiles = ref<ProfileRecord[]>([]),
    descriptor = ref<ProfileDescriptor | null>(null),
    type = ref(String(route.query.type || ''))
  const form = reactive({ name: '', summary: '', type: '', config: {} as Record<string, unknown> })
  const declaredTypes = ref<readonly string[]>([])
  const types = computed(() =>
    declaredTypes.value.length
      ? declaredTypes.value
      : descriptor.value?.type
        ? [descriptor.value.type]
        : [...new Set(profiles.value.map((item) => item.type).filter(Boolean))]
  )
  const profileSchema = computed(
    () =>
      editing.value?.configSchema ||
      editing.value?.descriptor?.configSchema ||
      descriptor.value?.configSchema || { type: 'object', properties: {} }
  )
  const profileUiSchema = computed(
    () =>
      editing.value?.uiSchema ||
      editing.value?.descriptor?.uiSchema ||
      descriptor.value?.uiSchema ||
      {}
  )
  const statusLabel = (value: string) =>
    ({ active: '已发布', published: '已发布', disabled: '已停用', draft: '草稿' })[value] || value
  const statusType = (value: string) =>
    value === 'published' || value === 'active'
      ? 'success'
      : value === 'disabled'
        ? 'info'
        : 'warning'
  async function load() {
    declaredTypes.value = await loadPluginProfileTypes(pluginId.value)
    const selectedType = type.value || declaredTypes.value[0] || ''
    if (!selectedType) return
    type.value = selectedType
    loading.value = true
    try {
      const [metadata, list] = await Promise.all([
        fetchProfileDescriptor(pluginId.value, selectedType),
        fetchProfiles(pluginId.value, selectedType)
      ])
      descriptor.value = metadata.item
      profiles.value = list.items
    } finally {
      loading.value = false
    }
  }
  function openCreate() {
    editing.value = null
    Object.assign(form, {
      name: '',
      summary: '',
      type: descriptor.value?.type || type.value,
      config: schemaDefaults(profileSchema.value)
    })
    dialog.value = true
  }
  function openEdit(row: ProfileRecord) {
    editing.value = row
    Object.assign(form, {
      name: row.name,
      summary: row.summary,
      type: row.type,
      config: { ...(row.config || {}) }
    })
    dialog.value = true
  }
  async function save() {
    const ownedType = descriptor.value?.type || form.type
    if (!form.name.trim() || !ownedType) {
      ElMessage.error('请填写名称')
      return
    }
    saving.value = true
    try {
      await saveProfile(pluginId.value, editing.value?.id, {
        name: form.name.trim(),
        summary: form.summary.trim(),
        type: ownedType,
        config: form.config
      })
      dialog.value = false
      await load()
      ElMessage.success('Profile 草稿已保存')
    } finally {
      saving.value = false
    }
  }
  async function copy(row: ProfileRecord) {
    await copyProfile(pluginId.value, row.type, row.id, `${row.name} 副本`)
    await load()
  }
  async function publish(row: ProfileRecord) {
    const defaultVersion = row.latestPublishedVersion
      ? `v${Number(row.latestPublishedVersion.replace(/^v/, '')) + 1}`
      : 'v1'
    const result = await ElMessageBox.prompt('请输入要发布的版本号', '发布 Profile', {
      inputValue: defaultVersion,
      inputPattern: /^\S+$/,
      inputErrorMessage: '版本号不能为空'
    }).catch(() => null)
    if (!result) return
    await publishProfile(pluginId.value, row.type, row.id, result.value)
    await load()
  }
  async function disable(row: ProfileRecord) {
    await ElMessageBox.confirm(`确定停用“${row.name}”？`, '停用 Profile', { type: 'warning' })
    await disableProfile(pluginId.value, row.type, row.id)
    await load()
  }
  onMounted(load)
</script>

<style scoped>
  .profile-manager {
    padding: 24px;
    min-height: calc(100vh - 140px);
    background: var(--el-bg-color);
  }
  .profile-manager__header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 16px;
    margin-bottom: 20px;
  }
  .profile-manager__header h2 {
    margin: 0 0 6px;
  }
  .profile-manager__header p {
    margin: 0;
    color: var(--el-text-color-secondary);
  }
  .profile-manager__actions {
    display: flex;
    gap: 8px;
  }
</style>
