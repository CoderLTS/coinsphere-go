<template>
  <div class="account-console">
    <header class="console-head">
      <div>
        <div class="eyebrow">交易管理</div>
        <h1>交易账户</h1>
      </div>
      <div class="head-actions">
        <ElButton :icon="Refresh" :loading="loading" @click="loadAccounts">刷新</ElButton>
        <ElButton type="primary" :icon="Plus" @click="openCreate">新增账户</ElButton>
      </div>
    </header>

    <section class="account-summary">
      <div
        ><span>账户总数</span><strong>{{ accounts.length }}</strong></div
      >
      <div
        ><span>运行中</span><strong class="is-success">{{ activeCount }}</strong></div
      >
      <div
        ><span>已暂停</span><strong class="is-warning">{{ pausedCount }}</strong></div
      >
      <div
        ><span>自动化</span><strong>{{ automationCount }}</strong></div
      >
    </section>

    <ElCard shadow="never" class="account-table">
      <ElTable v-loading="loading" :data="accounts" row-key="id" @row-click="openDetail">
        <ElTableColumn label="账户" min-width="220">
          <template #default="{ row }">
            <div class="account-name"
              ><span class="account-mark"><ArtSvgIcon icon="ri:wallet-3-line" /></span
              ><div
                ><strong>{{ row.name }}</strong
                ><small>{{ row.id }}</small></div
              ></div
            >
          </template>
        </ElTableColumn>
        <ElTableColumn label="市场 / 环境" width="170">
          <template #default="{ row }"
            ><span
              >{{ marketLabel(row.market) }} · {{ environmentLabel(row.environment) }}</span
            ></template
          >
        </ElTableColumn>
        <ElTableColumn label="状态" width="120">
          <template #default="{ row }"
            ><ElTag :type="row.status === 'active' ? 'success' : 'warning'" effect="plain">{{
              row.status === 'active' ? '运行中' : '已暂停'
            }}</ElTag></template
          >
        </ElTableColumn>
        <ElTableColumn label="自动化" width="120">
          <template #default="{ row }"
            ><ElTag :type="row.automationEnabled ? 'success' : 'info'" effect="plain">{{
              row.automationEnabled ? '已开启' : '已关闭'
            }}</ElTag></template
          >
        </ElTableColumn>
        <ElTableColumn label="凭据" width="130">
          <template #default="{ row }"
            ><span :class="row.credentialsConfigured ? 'is-success' : 'muted'">{{
              row.credentialsConfigured ? '已配置' : '未配置'
            }}</span></template
          >
        </ElTableColumn>
        <ElTableColumn label="风险" width="120">
          <template #default="{ row }"
            ><ElTag :type="row.risk.complete ? 'success' : 'danger'" effect="plain">{{
              row.risk.complete ? '完整' : '待完善'
            }}</ElTag></template
          >
        </ElTableColumn>
        <ElTableColumn label="更新时间" min-width="170" prop="updatedAt" />
        <ElTableColumn label="操作" width="130" fixed="right">
          <template #default="{ row }"
            ><ElButton link type="primary" @click.stop="openDetail(row)"
              >查看详情</ElButton
            ></template
          >
        </ElTableColumn>
      </ElTable>
      <div v-if="!loading && !accounts.length" class="empty-state"
        ><ArtSvgIcon icon="ri:wallet-3-line" /><strong>还没有交易账户</strong
        ><span>创建账户后，工作流中的策略节点才能选择运行绑定。</span
        ><ElButton type="primary" @click="openCreate">新增账户</ElButton></div
      >
    </ElCard>

    <ElDrawer v-model="detailVisible" :title="selected?.name || '账户详情'" size="min(760px, 94vw)">
      <template v-if="detail">
        <div class="detail-head"
          ><div
            ><span class="muted"
              >{{ marketLabel(detail.account.market) }} ·
              {{ environmentLabel(detail.account.environment) }}</span
            ><h2>{{ detail.account.name }}</h2></div
          ><ElTag
            :type="detail.account.status === 'active' ? 'success' : 'warning'"
            effect="plain"
            >{{ detail.account.status === 'active' ? '运行中' : '已暂停' }}</ElTag
          ></div
        >
        <ElTabs v-model="detailTab">
          <ElTabPane label="概览" name="overview"
            ><div class="fact-grid"
              ><div
                ><span>自动化</span
                ><strong>{{ detail.account.automationEnabled ? '已开启' : '已关闭' }}</strong></div
              ><div
                ><span>凭据</span
                ><strong>{{
                  detail.account.credentialsConfigured ? '已配置' : '未配置'
                }}</strong></div
              ><div
                ><span>风险配置</span
                ><strong>{{ detail.account.risk.complete ? '完整' : '待完善' }}</strong></div
              ><div
                ><span>策略信号</span><strong>{{ detail.intents.length }}</strong></div
              ></div
            ><ElDescriptions :column="1" border size="small"
              ><ElDescriptionsItem label="账户 ID">{{ detail.account.id }}</ElDescriptionsItem
              ><ElDescriptionsItem label="创建时间">{{
                detail.account.createdAt
              }}</ElDescriptionsItem
              ><ElDescriptionsItem label="最近更新">{{
                detail.account.updatedAt
              }}</ElDescriptionsItem
              ><ElDescriptionsItem label="暂停原因">{{
                detail.account.pauseReason || '—'
              }}</ElDescriptionsItem></ElDescriptions
            ></ElTabPane
          >
          <ElTabPane label="风险" name="risk"
            ><ElDescriptions :column="1" border size="small"
              ><ElDescriptionsItem label="总额度">{{
                detail.account.risk.maxTotalNotional || '—'
              }}</ElDescriptionsItem
              ><ElDescriptionsItem label="单标的额度">{{
                detail.account.risk.maxSymbolNotional || '—'
              }}</ElDescriptionsItem
              ><ElDescriptionsItem label="单笔额度">{{
                detail.account.risk.maxOrderNotional || '—'
              }}</ElDescriptionsItem
              ><ElDescriptionsItem label="日亏损上限">{{
                detail.account.risk.maxDailyLoss || '—'
              }}</ElDescriptionsItem
              ><ElDescriptionsItem label="最大回撤">{{
                detail.account.risk.maxDrawdown || '—'
              }}</ElDescriptionsItem></ElDescriptions
            ></ElTabPane
          >
          <ElTabPane label="余额 / 持仓" name="balance"
            ><ElTable :data="detail.balances" size="small"
              ><ElTableColumn prop="cashBalance" label="现金余额" /><ElTableColumn
                prop="equity"
                label="权益" /><ElTableColumn prop="unrealizedPnl" label="未实现盈亏" /></ElTable
            ><ElTable :data="detail.positions" size="small" class="sub-table"
              ><ElTableColumn prop="symbol" label="标的" /><ElTableColumn
                prop="quantity"
                label="数量" /><ElTableColumn
                prop="averageEntryPrice"
                label="开仓均价" /><ElTableColumn
                prop="unrealizedPnl"
                label="未实现盈亏" /></ElTable
          ></ElTabPane>
          <ElTabPane label="订单 / 事实" name="facts"
            ><ElTable :data="detail.orders" size="small"
              ><ElTableColumn prop="symbol" label="标的" /><ElTableColumn
                prop="side"
                label="方向" /><ElTableColumn prop="status" label="状态" /><ElTableColumn
                prop="filledQuantity"
                label="成交数量" /><ElTableColumn prop="updatedAt" label="更新时间" /></ElTable
          ></ElTabPane>
        </ElTabs>
        <div class="drawer-actions"
          ><ElButton @click="openRename">修改名称</ElButton
          ><ElButton
            v-if="detail.account.status === 'paused'"
            type="success"
            plain
            @click="resumeAccount"
            >恢复账户</ElButton
          ><ElButton type="danger" plain @click="archiveAccount">归档账户</ElButton></div
        >
      </template>
    </ElDrawer>

    <ElDialog v-model="createVisible" title="新增交易账户" width="520px"
      ><ElForm :model="createForm" label-position="top"
        ><ElFormItem label="账户名称" required
          ><ElInput v-model.trim="createForm.name" maxlength="120" /></ElFormItem
        ><div class="form-row"
          ><ElFormItem label="市场"
            ><ElSelect v-model="createForm.market"
              ><ElOption label="现货" value="spot" /><ElOption
                label="USD-M 合约"
                value="usd_m" /></ElSelect></ElFormItem
          ><ElFormItem label="环境"
            ><ElSelect v-model="createForm.environment"
              ><ElOption label="Paper" value="paper" /><ElOption
                label="Testnet"
                value="testnet" /><ElOption
                label="Live"
                value="live" /></ElSelect></ElFormItem></div
        ><ElFormItem v-if="createForm.environment === 'paper'" label="初始余额 USDT"
          ><ElInput v-model="createForm.initialBalance" /></ElFormItem></ElForm
      ><template #footer
        ><ElButton @click="createVisible = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="createAccount"
          >创建账户</ElButton
        ></template
      ></ElDialog
    >
    <ElDialog v-model="renameVisible" title="修改账户名称" width="420px"
      ><ElInput v-model.trim="renameValue" maxlength="120" /><template #footer
        ><ElButton @click="renameVisible = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="renameAccount">保存</ElButton></template
      ></ElDialog
    >
  </div>
</template>

<script setup lang="ts">
  import { Plus, Refresh } from '@element-plus/icons-vue'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { fetchReauth } from '@/api/auth'
  import {
    fetchArchiveTradingAccount,
    fetchCreateTradingAccount,
    fetchResumeTradingAccount,
    fetchTradingAccountDetail,
    fetchTradingAccounts,
    fetchUpdateTradingAccount,
    type TradingAccount,
    type TradingAccountDetail
  } from '@/api/trading'

  defineOptions({ name: 'TradingAccountsPage' })
  const loading = ref(false)
  const saving = ref(false)
  const accounts = ref<TradingAccount[]>([])
  const selected = ref<TradingAccount | null>(null)
  const detail = ref<TradingAccountDetail | null>(null)
  const detailVisible = ref(false)
  const createVisible = ref(false)
  const renameVisible = ref(false)
  const renameValue = ref('')
  const detailTab = ref('overview')
  const createForm = reactive({
    name: '',
    market: 'spot' as 'spot' | 'usd_m',
    environment: 'paper' as 'paper' | 'testnet' | 'live',
    initialBalance: '10000'
  })
  const activeCount = computed(
    () => accounts.value.filter((item) => item.status === 'active').length
  )
  const pausedCount = computed(
    () => accounts.value.filter((item) => item.status === 'paused').length
  )
  const automationCount = computed(
    () => accounts.value.filter((item) => item.automationEnabled).length
  )
  const marketLabel = (value: string) => (value === 'usd_m' ? 'USD-M' : 'Spot')
  const environmentLabel = (value: string) =>
    ({ paper: 'Paper', testnet: 'Testnet', live: 'Live' })[value] || value
  const loadAccounts = async () => {
    loading.value = true
    try {
      accounts.value = await fetchTradingAccounts()
    } finally {
      loading.value = false
    }
  }
  const openCreate = () => {
    Object.assign(createForm, {
      name: '',
      market: 'spot',
      environment: 'paper',
      initialBalance: '10000'
    })
    createVisible.value = true
  }
  const openDetail = async (row: TradingAccount) => {
    selected.value = row
    detailVisible.value = true
    detailTab.value = 'overview'
    detail.value = await fetchTradingAccountDetail(row.id)
  }
  const createAccount = async () => {
    if (!createForm.name) return ElMessage.warning('请输入账户名称')
    saving.value = true
    try {
      await fetchCreateTradingAccount(
        {
          name: createForm.name,
          market: createForm.market,
          environment: createForm.environment,
          initialBalance: createForm.initialBalance,
          paperFeeRate: '0.001',
          risk: {
            instrumentIds: [],
            maxTotalNotional: '0',
            maxSymbolNotional: '0',
            maxOrderNotional: '0',
            maxDailyLoss: '0',
            maxDrawdown: '0',
            maxQuoteAgeSeconds: 30
          }
        },
        crypto.randomUUID()
      )
      createVisible.value = false
      await loadAccounts()
    } finally {
      saving.value = false
    }
  }
  const openRename = () => {
    renameValue.value = detail.value?.account.name || ''
    renameVisible.value = true
  }
  const renameAccount = async () => {
    if (!detail.value || !renameValue.value) return
    saving.value = true
    try {
      await fetchUpdateTradingAccount(detail.value.account.id, { name: renameValue.value })
      renameVisible.value = false
      await loadAccounts()
      await openDetail(
        accounts.value.find((item) => item.id === detail.value?.account.id) || detail.value.account
      )
    } finally {
      saving.value = false
    }
  }
  const requestReauth = async (title: string) => {
    try {
      const result = await ElMessageBox.prompt('请输入当前密码完成身份复验', title, {
        inputType: 'password',
        inputPlaceholder: '当前密码',
        inputValidator: (value) => Boolean(value?.trim()) || '请输入当前密码'
      })
      return (await fetchReauth(result.value)).reauthToken
    } catch (error) {
      if (error === 'cancel' || error === 'close') return null
      throw error
    }
  }
  const resumeAccount = async () => {
    if (!detail.value) return
    const token = await requestReauth('恢复账户')
    if (!token) return
    await fetchResumeTradingAccount(detail.value.account.id, crypto.randomUUID(), token)
    await loadAccounts()
    await openDetail(
      accounts.value.find((item) => item.id === detail.value?.account.id) || detail.value.account
    )
  }
  const archiveAccount = async () => {
    if (!detail.value) return
    await ElMessageBox.confirm(
      '归档前请确认账户已暂停、关闭自动化，且没有未完成事实。',
      '归档账户',
      { type: 'warning' }
    )
    const token = await requestReauth('归档账户')
    if (!token) return
    await fetchArchiveTradingAccount(detail.value.account.id, crypto.randomUUID(), token)
    detailVisible.value = false
    await loadAccounts()
  }
  onMounted(() => void loadAccounts())
</script>

<style scoped lang="scss">
  .account-console {
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding-bottom: 24px;
  }

  .console-head,
  .head-actions,
  .account-name,
  .detail-head,
  .drawer-actions {
    display: flex;
    align-items: center;
  }

  .console-head,
  .detail-head {
    gap: 16px;
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

  h2 {
    margin: 4px 0 0;
    font-size: 20px;
  }

  .head-actions,
  .drawer-actions {
    gap: 8px;
  }

  .account-summary {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 12px;
  }

  .account-summary > div {
    padding: 14px 16px;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color-light);
  }

  .account-summary span,
  .fact-grid span {
    display: block;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .account-summary strong {
    display: block;
    margin-top: 6px;
    font-size: 24px;
  }

  .is-success {
    color: var(--el-color-success);
  }

  .is-warning {
    color: var(--el-color-warning);
  }

  .muted {
    color: var(--el-text-color-secondary);
  }

  .account-name {
    gap: 10px;
  }

  .account-name small {
    display: block;
    margin-top: 3px;
    font-size: 11px;
    color: var(--el-text-color-secondary);
  }

  .account-mark {
    display: inline-grid;
    place-items: center;
    width: 34px;
    height: 34px;
    color: var(--el-color-primary);
    background: var(--el-color-primary-light-9);
    border-radius: 8px;
  }

  .empty-state {
    display: flex;
    flex-direction: column;
    gap: 8px;
    align-items: center;
    padding: 56px 20px;
    color: var(--el-text-color-secondary);
  }

  .empty-state svg {
    font-size: 34px;
    color: var(--el-color-primary);
  }

  .detail-head {
    padding-bottom: 14px;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .detail-head h2 {
    font-size: 22px;
  }

  .fact-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 10px;
    margin-bottom: 16px;
  }

  .fact-grid > div {
    padding: 12px;
    background: var(--el-fill-color-light);
  }

  .fact-grid strong {
    display: block;
    margin-top: 5px;
  }

  .sub-table {
    margin-top: 14px;
  }

  .drawer-actions {
    justify-content: flex-end;
    padding-top: 18px;
    border-top: 1px solid var(--el-border-color-lighter);
  }

  .form-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  .form-row :deep(.el-select) {
    width: 100%;
  }

  @media (max-width: 720px) {
    .account-summary {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .console-head {
      flex-direction: column;
      align-items: flex-start;
    }

    .fact-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .drawer-actions {
      flex-wrap: wrap;
    }
  }
</style>
