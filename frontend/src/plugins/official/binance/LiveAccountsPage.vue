<template>
  <main class="live-accounts-page">
    <header class="page-head">
      <div>
        <span>Binance / 安全门禁</span>
        <h1>真实交易账户放行</h1>
      </div>
      <div class="page-actions">
        <ElButton :icon="Refresh" :loading="loading" @click="loadAccounts">刷新</ElButton>
        <ElButton type="primary" :icon="Unlock" @click="openRelease">放行账户</ElButton>
      </div>
    </header>

    <ElAlert
      type="warning"
      :closable="false"
      title="账户放行不会自动激活工作流；真实下单仍需节点开关、全部风险上限和无提现权限密钥。"
    />

    <section class="release-table" aria-label="真实交易账户放行记录">
      <ElTable v-loading="loading" :data="accounts" :row-key="releaseKey">
        <ElTableColumn prop="account" label="账户" min-width="180" />
        <ElTableColumn label="市场" width="110">
          <template #default="{ row }">{{ row.market === 'usdm' ? 'USD-M' : 'SPOT' }}</template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="110">
          <template #default="{ row }">
            <ElTag :type="row.enabled ? 'danger' : 'info'" effect="plain">
              {{ row.enabled ? '已放行' : '已停用' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="确认人" width="110" prop="confirmedBy" />
        <ElTableColumn label="更新时间" min-width="180">
          <template #default="{ row }">{{ formatTime(row.updatedAt) }}</template>
        </ElTableColumn>
        <ElTableColumn label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <ElButton
              v-if="row.enabled"
              text
              type="danger"
              :icon="Lock"
              @click="disableAccount(row)"
            >
              停用
            </ElButton>
          </template>
        </ElTableColumn>
      </ElTable>
    </section>

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
  import { Lock, Refresh, Unlock } from '@element-plus/icons-vue'
  import {
    fetchBinanceLiveAccounts,
    updateBinanceLiveAccount,
    type BinanceLiveAccountRelease
  } from './api'
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
    gap: 16px;
    min-width: 0;
    min-height: 100%;
    padding: 20px;
    color: var(--art-gray-900);
    background: var(--default-bg-color);
  }

  .page-head,
  .page-actions {
    display: flex;
    gap: 12px;
    align-items: center;
    justify-content: space-between;
  }

  .page-head span {
    font-size: 13px;
    color: var(--art-gray-500);
  }

  .page-head h1 {
    margin: 4px 0 0;
    font-size: 24px;
    letter-spacing: 0;
  }

  .release-table {
    min-width: 0;
    overflow: hidden;
    background: var(--default-box-color);
    border: 1px solid var(--art-gray-200);
    border-radius: 6px;
  }

  @media (width <= 640px) {
    .page-head {
      align-items: flex-start;
      flex-direction: column;
    }

    .page-actions {
      width: 100%;
    }
  }
</style>
