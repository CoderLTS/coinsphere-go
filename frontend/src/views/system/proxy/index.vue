<!-- 系统代理池：仅供显式选择代理的 Binance 公共行情节点使用。 -->
<template>
  <div class="art-full-height">
    <ElCard class="art-table-card">
      <ArtTableHeader
        v-model:columns="columnChecks"
        :show-zebra="false"
        :loading="loading"
        @refresh="loadProxies"
      >
        <template #left>
          <ElButton type="primary" :icon="Plus" @click="openCreate">新增代理</ElButton>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :columns="columns" :data="proxies" :stripe="false" />
    </ElCard>

    <ElDialog
      v-model="dialogVisible"
      :title="editingProxy ? '编辑代理' : '新增代理'"
      width="min(640px, 94vw)"
      align-center
      destroy-on-close
    >
      <ElForm ref="formRef" :model="form" :rules="rules" label-width="92px">
        <ElFormItem label="名称" prop="name">
          <ElInput v-model.trim="form.name" maxlength="120" placeholder="例如：新加坡节点" />
        </ElFormItem>

        <ElFormItem label="协议">
          <ElSegmented v-model="form.protocol" :options="protocolOptions" />
        </ElFormItem>

        <ElRow :gutter="16">
          <ElCol :xs="24" :sm="16">
            <ElFormItem label="主机" prop="host">
              <ElInput v-model.trim="form.host" placeholder="127.0.0.1 或 proxy.example.com" />
            </ElFormItem>
          </ElCol>
          <ElCol :xs="24" :sm="8">
            <ElFormItem label="端口" prop="port">
              <ElInputNumber v-model="form.port" :min="1" :max="65535" controls-position="right" />
            </ElFormItem>
          </ElCol>
        </ElRow>

        <ElRow :gutter="16">
          <ElCol :xs="24" :sm="12">
            <ElFormItem label="用户名">
              <ElInput v-model.trim="form.username" maxlength="255" placeholder="可选" />
            </ElFormItem>
          </ElCol>
          <ElCol :xs="24" :sm="12">
            <ElFormItem label="密码">
              <ElInput
                v-model="form.password"
                type="password"
                show-password
                :disabled="form.clearPassword"
                :placeholder="passwordPlaceholder"
              />
            </ElFormItem>
          </ElCol>
        </ElRow>

        <ElFormItem v-if="editingProxy?.passwordConfigured" label="认证信息">
          <ElCheckbox v-model="form.clearPassword" @change="form.password = ''">
            清除已保存密码
          </ElCheckbox>
        </ElFormItem>

        <ElFormItem label="启用">
          <ElSwitch v-model="form.isEnabled" />
        </ElFormItem>
      </ElForm>

      <template #footer>
        <ElButton @click="dialogVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="submitting" @click="submitProxy">保存</ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import { CircleCheck, Delete, Edit, Plus } from '@element-plus/icons-vue'
  import {
    ElButton,
    ElMessage,
    ElMessageBox,
    ElSwitch,
    ElTag,
    type FormInstance,
    type FormRules
  } from 'element-plus'
  import type { Component } from 'vue'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import { formatDateTime } from '@/utils/date'
  import {
    fetchCreateOutboundProxy,
    fetchDeleteOutboundProxy,
    fetchGetOutboundProxies,
    fetchSetOutboundProxyEnabled,
    fetchUpdateOutboundProxy,
    fetchValidateOutboundProxy
  } from '@/api/system'

  defineOptions({ name: 'OutboundProxyPage' })

  type ProxyItem = Api.System.OutboundProxyItem
  type ProxyPayload = Api.System.OutboundProxyUpsertPayload

  const loading = ref(false)
  const submitting = ref(false)
  const validatingId = ref<number | null>(null)
  const proxies = ref<ProxyItem[]>([])
  const dialogVisible = ref(false)
  const editingProxy = ref<ProxyItem | null>(null)
  const formRef = ref<FormInstance>()
  const protocolOptions = [
    { label: 'HTTP', value: 'http' },
    { label: 'SOCKS5', value: 'socks5' }
  ]

  const emptyForm = () => ({
    name: '',
    protocol: 'http' as Api.System.OutboundProxyProtocol,
    host: '',
    port: 7890,
    username: '',
    password: '',
    clearPassword: false,
    isEnabled: true
  })
  const form = reactive(emptyForm())
  const rules: FormRules = {
    name: [{ required: true, message: '请输入代理名称', trigger: 'blur' }],
    host: [{ required: true, message: '请输入代理主机', trigger: 'blur' }],
    port: [{ required: true, message: '请输入代理端口', trigger: 'change' }]
  }
  const passwordPlaceholder = computed(() =>
    editingProxy.value?.passwordConfigured ? '留空保留当前密码' : '可选'
  )

  const endpoint = (row: ProxyItem) => {
    const host = row.host.includes(':') ? `[${row.host}]` : row.host
    return `${row.protocol}://${host}:${row.port}`
  }

  const renderIconAction = (options: {
    icon: Component
    title: string
    onClick: () => void
    type?: '' | 'primary' | 'danger'
    loading?: boolean
  }) =>
    h(ElButton, {
      size: 'small',
      circle: true,
      plain: true,
      icon: options.icon,
      type: options.type,
      loading: options.loading,
      title: options.title,
      onClick: options.onClick
    })

  const renderActions = (row: ProxyItem) =>
    h('div', { class: 'proxy-actions' }, [
      renderIconAction({ icon: Edit, title: '编辑', onClick: () => openEdit(row) }),
      renderIconAction({
        icon: CircleCheck,
        title: '检测 Binance 连接',
        type: 'primary',
        loading: validatingId.value === row.id,
        onClick: () => validateProxy(row)
      }),
      renderIconAction({
        icon: Delete,
        title: '删除',
        type: 'danger',
        onClick: () => deleteProxy(row)
      })
    ])

  const statusTag = (row: ProxyItem) => {
    const status = {
      healthy: { type: 'success' as const, label: '正常' },
      failed: { type: 'danger' as const, label: '失败' },
      unchecked: { type: 'info' as const, label: '未检测' }
    }[row.lastCheckStatus]
    const latency =
      row.lastLatencyMs === null || row.lastLatencyMs === undefined
        ? ''
        : ` · ${row.lastLatencyMs} ms`
    return h(ElTag, { type: status.type, effect: 'plain' }, () => status.label + latency)
  }

  const { columns, columnChecks } = useTableColumns<ProxyItem>(() => [
    { prop: 'name', label: '名称', minWidth: 140, showOverflowTooltip: true },
    {
      prop: 'endpoint',
      label: '代理地址',
      minWidth: 230,
      showOverflowTooltip: true,
      formatter: (row) => h('code', { class: 'proxy-endpoint' }, endpoint(row))
    },
    {
      prop: 'username',
      label: '认证',
      minWidth: 120,
      formatter: (row) => row.username || (row.passwordConfigured ? '仅密码' : '无')
    },
    { prop: 'lastCheckStatus', label: '连通性', minWidth: 130, formatter: statusTag },
    {
      prop: 'lastCheckedAt',
      label: '检测时间',
      minWidth: 165,
      formatter: (row) => (row.lastCheckedAt ? formatDateTime(row.lastCheckedAt) : '—')
    },
    {
      prop: 'isEnabled',
      label: '启用',
      width: 90,
      align: 'center',
      formatter: (row) =>
        h(ElSwitch, {
          modelValue: row.isEnabled,
          'onUpdate:modelValue': (value: boolean | string | number) =>
            setEnabled(row, Boolean(value))
        })
    },
    {
      prop: 'updatedAt',
      label: '更新时间',
      minWidth: 165,
      formatter: (row) => formatDateTime(row.updatedAt)
    },
    { prop: 'operation', label: '操作', width: 150, fixed: 'right', formatter: renderActions }
  ])

  const loadProxies = async () => {
    loading.value = true
    try {
      proxies.value = await fetchGetOutboundProxies()
    } finally {
      loading.value = false
    }
  }

  const openCreate = () => {
    editingProxy.value = null
    Object.assign(form, emptyForm())
    dialogVisible.value = true
  }

  const openEdit = (row: ProxyItem) => {
    editingProxy.value = row
    Object.assign(form, {
      name: row.name,
      protocol: row.protocol,
      host: row.host,
      port: row.port,
      username: row.username,
      password: '',
      clearPassword: false,
      isEnabled: row.isEnabled
    })
    dialogVisible.value = true
  }

  const submitProxy = async () => {
    if (!formRef.value || submitting.value) return
    await formRef.value.validate()
    const payload: ProxyPayload = {
      name: form.name.trim(),
      protocol: form.protocol,
      host: form.host.trim(),
      port: form.port,
      username: form.username.trim(),
      password: form.password || undefined,
      clearPassword: form.clearPassword,
      isEnabled: form.isEnabled
    }
    submitting.value = true
    try {
      if (editingProxy.value) await fetchUpdateOutboundProxy(editingProxy.value.id, payload)
      else await fetchCreateOutboundProxy(payload)
      dialogVisible.value = false
      await loadProxies()
    } finally {
      submitting.value = false
    }
  }

  const setEnabled = async (row: ProxyItem, enabled: boolean) => {
    try {
      await fetchSetOutboundProxyEnabled(row.id, enabled)
    } finally {
      await loadProxies()
    }
  }

  const validateProxy = async (row: ProxyItem) => {
    if (validatingId.value !== null) return
    validatingId.value = row.id
    try {
      const result = await fetchValidateOutboundProxy(row.id)
      if (result.success) ElMessage.success(result.message)
      else ElMessage.error(result.message)
      await loadProxies()
    } finally {
      validatingId.value = null
    }
  }

  const deleteProxy = async (row: ProxyItem) => {
    await ElMessageBox.confirm(`确认删除代理“${row.name}”吗？`, '删除代理', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消'
    })
    await fetchDeleteOutboundProxy(row.id)
    await loadProxies()
  }

  onMounted(loadProxies)
</script>

<style scoped lang="scss">
  .proxy-actions {
    display: flex;
    gap: 8px;
    align-items: center;
    justify-content: center;
  }

  .proxy-endpoint {
    font-size: 12px;
    color: var(--el-text-color-regular);
  }

  :deep(.el-input-number),
  :deep(.el-segmented) {
    width: 100%;
  }
</style>
