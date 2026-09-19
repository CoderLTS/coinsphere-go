<template>
  <main class="art-full-height live-accounts-page">
    <ElAlert
      type="warning"
      :closable="false"
      title="账户放行不会自动激活工作流；真实下单仍需节点开关、全部风险上限和无提现权限密钥。"
    />

    <ElCard class="art-table-card release-table" aria-label="真实交易账户放行记录">
      <ArtTableHeader
        v-model:columns="columnChecks"
        :show-zebra="false"
        :loading="loading"
        @refresh="loadAccounts"
      >
        <template #left>
          <ElButton type="primary" :icon="Unlock" @click="openRelease">放行账户</ElButton>
        </template>
      </ArtTableHeader>
      <ArtTable
        :loading="loading"
        :columns="columns"
        :data="accounts"
        :row-key="releaseKey"
        :stripe="false"
      />
    </ElCard>

    <ElDialog v-model="dialogOpen" title="放行真实交易账户" width="min(480px, calc(100vw - 32px))">
      <ElForm label-position="top">
        <ElFormItem label="账户">
          <ElInput v-model="form.account" maxlength="64" />
        </ElFormItem>
        <ElFormItem label="市场">
          <ElSegmented v-model="form.market" :options="marketOptions" />
        </ElFormItem>
        <ElFormItem label="精确确认文本">
          <ElInput v-model="form.confirmation" :placeholder="expectedConfirmation" />
        </ElFormItem>
      </ElForm>
      <template #footer>
        <ElButton @click="dialogOpen = false">取消</ElButton>
        <ElButton
          type="danger"
          :loading="submitting"
          :disabled="!validAccount || form.confirmation !== expectedConfirmation"
          @click="submitRelease"
        >
          确认放行
        </ElButton>
      </template>
    </ElDialog>
  </main>
</template>

<script setup lang="ts">
  import { Lock, Unlock } from '@element-plus/icons-vue'
  import { ElButton, ElMessageBox, ElTag } from 'element-plus'
  import {
    fetchBinanceLiveAccounts,
    updateBinanceLiveAccount,
    type BinanceLiveAccountRelease
  } from './api'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import { formatDateTime as formatTime } from '@/utils/date'

  defineOptions({ name: 'BinanceLiveAccountsPage' })

  const accounts = ref<BinanceLiveAccountRelease[]>([])
  const loading = ref(false)
  const submitting = ref(false)
  const dialogOpen = ref(false)
  const form = reactive<{ account: string; market: 'spot' | 'usdm'; confirmation: string }>({
    account: '',
    market: 'spot',
    confirmation: ''
  })
  const marketOptions = [
    { label: 'Spot', value: 'spot' },
    { label: 'USD-M', value: 'usdm' }
  ]
  const expectedConfirmation = computed(() => `ENABLE LIVE ${form.account.trim()} ${form.market}`)
  const validAccount = computed(() => /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(form.account.trim()))
  const releaseKey = (row: BinanceLiveAccountRelease) => `${row.account}:${row.market}`

  const renderActions = (row: BinanceLiveAccountRelease) =>
    row.enabled
      ? h('div', { class: 'table-actions' }, [
          h(ElButton, {
            class: 'icon-action',
            size: 'small',
            circle: true,
            plain: true,
            type: 'danger',
            icon: Lock,
            title: '停用',
            onClick: () => disableAccount(row)
          })
        ])
      : null

  const { columns, columnChecks } = useTableColumns<BinanceLiveAccountRelease>(() => [
    { prop: 'account', label: '账户', minWidth: 180 },
    {
      prop: 'market',
      label: '市场',
      width: 110,
      formatter: (row) => (row.market === 'usdm' ? 'USD-M' : 'SPOT')
    },
    {
      prop: 'enabled',
      label: '状态',
      width: 110,
      formatter: (row) =>
        h(ElTag, { type: row.enabled ? 'danger' : 'info', effect: 'plain' }, () =>
          row.enabled ? '已放行' : '已停用'
        )
    },
    { prop: 'confirmedBy', label: '确认人', width: 110 },
    {
      prop: 'updatedAt',
      label: '更新时间',
      minWidth: 180,
      formatter: (row) => formatTime(row.updatedAt)
    },
    {
      prop: 'operation',
      label: '操作',
      width: 100,
      fixed: 'right',
      formatter: renderActions
    }
  ])

  const loadAccounts = async () => {
    loading.value = true
    try {
      accounts.value = (await fetchBinanceLiveAccounts()).items
    } finally {
      loading.value = false
    }
  }
  const openRelease = () => {
    form.account = ''
    form.market = 'spot'
    form.confirmation = ''
    dialogOpen.value = true
  }
  const submitRelease = async () => {
    const account = form.account.trim()
    if (!validAccount.value || form.confirmation !== expectedConfirmation.value) return
    submitting.value = true
    try {
      await updateBinanceLiveAccount(account, {
        market: form.market,
        enabled: true,
        confirmation: form.confirmation
      })
      dialogOpen.value = false
      await loadAccounts()
    } finally {
      submitting.value = false
    }
  }
  const disableAccount = async (row: BinanceLiveAccountRelease) => {
    await ElMessageBox.confirm(`停用 ${row.account} / ${row.market} 的真实交易放行？`, '停用账户', {
      type: 'warning',
      confirmButtonText: '停用',
      cancelButtonText: '取消'
    })
    await updateBinanceLiveAccount(row.account, {
      market: row.market,
      enabled: false,
      confirmation: ''
    })
    await loadAccounts()
  }

  onMounted(loadAccounts)
</script>

<style scoped>
  .live-accounts-page {
    display: flex;
    flex-direction: column;
    gap: 12px;
    min-width: 0;
    min-height: 100%;
    padding: 0;
    color: var(--art-gray-900);
    background: var(--default-bg-color);
  }

  .release-table {
    min-width: 0;
    overflow: hidden;
    margin-top: 0;
  }

  .table-actions {
    display: flex;
    gap: 8px;
    align-items: center;
    justify-content: center;
  }

  @media (width <= 640px) {
    .live-accounts-page :deep(.el-alert) {
      align-items: flex-start;
    }
  }
</style>
