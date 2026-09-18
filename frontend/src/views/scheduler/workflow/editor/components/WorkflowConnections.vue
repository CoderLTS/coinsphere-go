<template>
  <ElDialog v-model="visible" title="连接" width="min(780px, 95vw)">
    <div class="connections__links"
      ><RouterLink to="/config/ai-models">模型连接</RouterLink
      ><RouterLink to="/config/proxies">代理连接</RouterLink
      ><ElButton @click="edit()">新建连接</ElButton></div
    >
    <ElTable :data="connections">
      <ElTableColumn prop="name" label="名称" /><ElTableColumn prop="type" label="类型" />
      <ElTableColumn label="状态"
        ><template #default="{ row }">{{
          row.enabled ? '可用' : '已停用'
        }}</template></ElTableColumn
      >
      <ElTableColumn label="操作"
        ><template #default="{ row }"
          ><ElButton v-if="!row.id.includes(':')" text @click="edit(row)">编辑</ElButton
          ><ElButton v-if="!row.id.includes(':')" text type="danger" @click="remove(row)"
            >删除</ElButton
          ></template
        ></ElTableColumn
      >
    </ElTable>
    <ElForm v-if="editing" label-position="top" class="connections__form">
      <ElFormItem label="名称"><ElInput v-model="form.name" /></ElFormItem>
      <ElFormItem label="类型"
        ><ElSelect v-model="form.type" :disabled="Boolean(form.id)" @change="changeType"
          ><ElOption
            v-for="type in editableTypes"
            :key="type.type"
            :value="type.type"
            :label="type.type" /></ElSelect
      ></ElFormItem>
      <WorkflowSchemaFields
        :schema="publicSchema"
        :config="form.config"
        @update="(key, value) => (form.config[key] = value)"
      />
      <ElFormItem v-for="(field, key) in secretProperties" :key="key" :label="field.title || key">
        <ElInput
          v-model="form.secrets[key]"
          type="password"
          show-password
          autocomplete="new-password"
          :placeholder="form.secretFields[key] ? '已配置，留空保留' : '请输入凭据'"
        />
      </ElFormItem>
      <ElFormItem label="启用"><ElSwitch v-model="form.enabled" /></ElFormItem>
      <ElButton type="primary" :loading="saving" @click="save">保存连接</ElButton
      ><ElButton @click="editing = false">取消</ElButton>
    </ElForm>
  </ElDialog>
</template>
<script setup lang="ts">
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { toRaw } from 'vue'
  import {
    fetchConnections,
    fetchConnectionTypes,
    saveConnection,
    deleteConnection,
    type Connection,
    type ConnectionType
  } from '@/api/connections'
  import WorkflowSchemaFields from './WorkflowSchemaFields.vue'
  import { schemaDefaults } from '../canvas'
  const visible = defineModel<boolean>({ required: true })
  const emit = defineEmits<{ (event: 'changed'): void }>()
  const connections = ref<Connection[]>([]),
    types = ref<ConnectionType[]>([])
  const editing = ref(false),
    saving = ref(false)
  const form = reactive({
    id: '',
    name: '',
    type: '',
    expectedVersion: 0,
    enabled: true,
    config: {} as Record<string, any>,
    secrets: {} as Record<string, string>,
    secretFields: {} as Record<string, boolean>
  })
  const editableTypes = computed(() => types.value.filter((t) => !['ai', 'proxy'].includes(t.type)))
  const schema = computed(() => types.value.find((t) => t.type === form.type)?.schema || {})
  const secretProperties = computed<Record<string, any>>(() =>
    Object.fromEntries(
      Object.entries(schema.value.properties || {}).filter(
        ([, p]) => (p as any)['x-coinsphere-secret']
      )
    )
  )
  const publicSchema = computed(() => ({
    ...schema.value,
    properties: Object.fromEntries(
      Object.entries(schema.value.properties || {}).filter(([key]) => !secretProperties.value[key])
    )
  }))
  async function load() {
    const [a, b] = await Promise.all([fetchConnections(), fetchConnectionTypes()])
    connections.value = a.items
    types.value = b.items
  }
  watch(visible, (value) => {
    if (value) load()
  })
  function edit(row?: Connection) {
    Object.assign(form, {
      id: row?.id || '',
      name: row?.name || '',
      type: row?.type || editableTypes.value[0]?.type || '',
      expectedVersion: row?.version || 0,
      enabled: row?.enabled ?? true,
      config: structuredClone(toRaw(row?.config || {})),
      secrets: {},
      secretFields: row?.secretFields || {}
    })
    if (!row) changeType()
    editing.value = true
  }
  function changeType() {
    form.config = schemaDefaults(publicSchema.value)
    form.secrets = {}
  }
  async function save() {
    saving.value = true
    try {
      await saveConnection(form.id, {
        ...form,
        secrets: Object.fromEntries(
          Object.entries(form.secrets).filter(([, value]) => value !== '')
        )
      })
      editing.value = false
      await load()
      emit('changed')
      ElMessage.success('连接已保存')
    } finally {
      saving.value = false
      form.secrets = {}
    }
  }
  async function remove(row: Connection) {
    await ElMessageBox.confirm(`删除连接“${row.name}”？引用中的连接不能删除。`, '删除连接')
    await deleteConnection(row.id)
    await load()
    emit('changed')
  }
</script>
<style scoped>
  .connections__links {
    display: flex;
    align-items: center;
    gap: 16px;
    margin-bottom: 16px;
  }
  .connections__form {
    margin-top: 20px;
  }
</style>
