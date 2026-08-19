<template>
  <div class="operations-home" v-loading="loading && !refreshedAt">
    <template v-if="isGuest">
      <section class="guest-panel art-card">
        <span class="guest-panel__icon"><ArtSvgIcon icon="ri:server-line" /></span>
        <div>
          <span class="section-kicker">COINSPHERE OPERATIONS</span>
          <h1>系统运行中心</h1>
          <p>登录后查看服务、行情、调度与交易风控状态。</p>
        </div>
        <ElButton type="primary" @click="goLogin">登录系统</ElButton>
      </section>
    </template>

    <template v-else>
      <header class="page-head">
        <div>
          <div class="page-context">
            <ArtSvgIcon icon="ri:dashboard-3-line" />
            运行中心
          </div>
          <h1>系统运维总览</h1>
          <p>集中查看核心服务状态、运行负载与需要处理的异常。</p>
        </div>
        <div class="head-actions">
          <span>更新于 {{ formatTime(refreshedAt, true) }}</span>
          <ElButton type="primary" :icon="Refresh" :loading="loading" @click="loadDashboard">
            刷新状态
          </ElButton>
        </div>
      </header>

      <section class="health-banner art-card" :class="`health-banner--${overallTone}`">
        <div class="health-state">
          <span class="health-state__icon"><ArtSvgIcon :icon="overallIcon" /></span>
          <div>
            <span>系统状态</span>
            <strong>{{ overallLabel }}</strong>
            <small>{{ overallDescription }}</small>
          </div>
        </div>
        <div class="health-meta">
          <div
            ><span>服务</span><strong>{{ serviceMeta.service || 'CoinSphere' }}</strong></div
          >
          <div
            ><span>版本</span><strong>{{ serviceMeta.version || '--' }}</strong></div
          >
          <div
            ><span>运行域</span><strong>{{ availableDomainCount }} / 4</strong></div
          >
        </div>
      </section>

      <section class="metric-grid" aria-label="运行指标">
        <article v-for="item in metrics" :key="item.label" class="metric-card art-card">
          <span class="metric-card__icon" :class="`is-${item.tone}`">
            <ArtSvgIcon :icon="item.icon" />
          </span>
          <div>
            <span>{{ item.label }}</span>
            <strong>{{ item.value }}</strong>
            <small>{{ item.caption }}</small>
          </div>
        </article>
      </section>

      <section class="domain-grid" aria-label="核心运行域">
        <article class="domain-card art-card">
          <header>
            <div class="domain-title">
              <span class="domain-icon is-primary"><ArtSvgIcon icon="ri:server-line" /></span>
              <div><h2>应用服务</h2><p>Go App 与前端服务</p></div>
            </div>
            <ElTag type="success" effect="light">在线</ElTag>
          </header>
          <dl class="domain-facts">
            <div><dt>API 状态</dt><dd class="is-success">可用</dd></div>
            <div
              ><dt>当前版本</dt><dd>{{ serviceMeta.version || '--' }}</dd></div
            >
            <div><dt>认证状态</dt><dd>已连接</dd></div>
          </dl>
        </article>

        <article class="domain-card art-card">
          <header>
            <div class="domain-title">
              <span class="domain-icon is-workflow"><ArtSvgIcon icon="ri:flow-chart" /></span>
              <div><h2>调度引擎</h2><p>队列与执行器运行状态</p></div>
            </div>
            <ElTag :type="workflowTone" effect="light">{{ workflowStatus }}</ElTag>
          </header>
          <dl class="domain-facts">
            <div
              ><dt>运行中</dt><dd>{{ workflowOverview?.stats.runningCount ?? '--' }}</dd></div
            >
            <div
              ><dt>等待执行</dt><dd>{{ workflowQueueCount }}</dd></div
            >
            <div
              ><dt>异常运行</dt
              ><dd :class="{ 'is-danger': workflowStaleCount > 0 }">{{
                workflowStaleCount
              }}</dd></div
            >
          </dl>
          <ElButton v-if="canViewWorkflow" text @click="router.push('/scheduler/definition')">
            进入调度管理 <ArtSvgIcon icon="ri:arrow-right-line" />
          </ElButton>
        </article>

        <article class="domain-card art-card">
          <header>
            <div class="domain-title">
              <span class="domain-icon is-market"><ArtSvgIcon icon="ri:database-2-line" /></span>
              <div><h2>行情数据</h2><p>Binance 元数据同步</p></div>
            </div>
            <ElTag :type="marketTone" effect="light">{{ marketStatusLabel }}</ElTag>
          </header>
          <dl class="domain-facts">
            <div
              ><dt>标的总数</dt><dd>{{ marketSymbolTotal ?? '--' }}</dd></div
            >
            <div
              ><dt>上次同步</dt><dd>{{ formatTime(marketSync?.lastSyncAt) }}</dd></div
            >
            <div
              ><dt>下次同步</dt><dd>{{ formatTime(marketSync?.nextSyncAt) }}</dd></div
            >
          </dl>
          <ElButton v-if="canViewMarket" text @click="router.push('/data/market-metadata')">
            查看币种元数据 <ArtSvgIcon icon="ri:arrow-right-line" />
          </ElButton>
        </article>

        <article class="domain-card art-card">
          <header>
            <div class="domain-title">
              <span class="domain-icon is-trading"><ArtSvgIcon icon="ri:shield-check-line" /></span>
              <div><h2>交易风控</h2><p>账户、持仓与全局控制</p></div>
            </div>
            <ElTag :type="tradingTone" effect="light">{{ tradingStatusLabel }}</ElTag>
          </header>
          <dl class="domain-facts">
            <div
              ><dt>启用账户</dt><dd>{{ activeAccountCount }}</dd></div
            >
            <div
              ><dt>持仓数量</dt><dd>{{ tradingOverview?.positions.length ?? '--' }}</dd></div
            >
            <div
              ><dt>未完成订单</dt><dd>{{ openOrderCount }}</dd></div
            >
          </dl>
          <ElButton text @click="router.push('/trading/overview')">
            进入交易管理 <ArtSvgIcon icon="ri:arrow-right-line" />
          </ElButton>
        </article>
      </section>

      <section class="operations-grid">
        <article class="activity-panel art-card">
          <header class="panel-head">
            <div><h2>最近运行状态</h2><p>各运行域最后一次可观测状态</p></div>
            <span class="status-legend"><i></i>UTC</span>
          </header>
          <div class="activity-list">
            <div v-for="item in activities" :key="item.label" class="activity-row">
              <span class="activity-dot" :class="`is-${item.tone}`"></span>
              <div
                ><strong>{{ item.label }}</strong
                ><span>{{ item.description }}</span></div
              >
              <time>{{ formatTime(item.time) }}</time>
            </div>
          </div>
        </article>

        <aside class="attention-panel art-card">
          <header class="panel-head">
            <div><h2>待处理项</h2><p>影响运行质量的当前状态</p></div>
            <span class="attention-count">{{ attentionItems.length }}</span>
          </header>
          <div v-if="attentionItems.length" class="attention-list">
            <button
              v-for="item in attentionItems"
              :key="item.title"
              type="button"
              @click="item.path && router.push(item.path)"
            >
              <span :class="`is-${item.tone}`"><ArtSvgIcon :icon="item.icon" /></span>
              <div
                ><strong>{{ item.title }}</strong
                ><small>{{ item.description }}</small></div
              >
              <ArtSvgIcon v-if="item.path" icon="ri:arrow-right-s-line" />
            </button>
          </div>
          <div v-else class="attention-empty">
            <ArtSvgIcon icon="ri:checkbox-circle-line" />
            <strong>当前没有异常</strong>
            <span>核心运行域均处于可用状态。</span>
          </div>
        </aside>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
  import { fetchHomeMeta } from '@/api/home'
  import { fetchMarketSymbols, fetchMarketSyncStatus, type MarketSyncStatus } from '@/api/market'
  import { fetchSchedulerOverview, type WorkflowOverview } from '@/api/scheduler'
  import { fetchTradingOverview, type TradingOverview } from '@/api/trading'
  import { useUserStore } from '@/store/modules/user'

  defineOptions({ name: 'HomePage' })

  type Tone = 'primary' | 'success' | 'warning' | 'danger' | 'info'

  const router = useRouter()
  const userStore = useUserStore()
  const isGuest = computed(() => userStore.accessMode === 'guest')
  const canViewWorkflow = computed(() =>
    userStore.info.permissions.includes('scheduler.workflow_definitions.view')
  )
  const canViewMarket = computed(() => userStore.info.permissions.includes('data.market.view'))
  const loading = ref(false)
  const refreshedAt = ref('')
  const serviceMeta = reactive({ service: '', version: '' })
  const workflowOverview = ref<WorkflowOverview | null>(null)
  const marketSync = ref<MarketSyncStatus | null>(null)
  const marketSymbolTotal = ref<number | null>(null)
  const tradingOverview = ref<TradingOverview | null>(null)

  const workflowQueueCount = computed(() => {
    const stats = workflowOverview.value?.stats
    return stats ? stats.pendingCount + stats.queuedCount + stats.retryWaitingCount : '--'
  })
  const workflowStaleCount = computed(() => workflowOverview.value?.stats.staleRunningCount || 0)
  const activeAccountCount = computed(
    () => tradingOverview.value?.accounts.filter((item) => item.status === 'active').length ?? '--'
  )
  const openOrderCount = computed(() => {
    const overview = tradingOverview.value
    if (!overview) return '--'
    return [...overview.orders, ...overview.testnetOpenOrders].filter(
      (item) => !['filled', 'canceled', 'rejected', 'expired'].includes(item.status)
    ).length
  })
  const availableDomainCount = computed(
    () => 2 + Number(Boolean(workflowOverview.value)) + Number(Boolean(marketSync.value))
  )

  const workflowStatus = computed(() => {
    if (!canViewWorkflow.value) return '未授权'
    if (!workflowOverview.value) return '未加载'
    if (workflowStaleCount.value > 0) return '存在异常'
    if ((workflowOverview.value.stats.runningCount || 0) > 0) return '运行中'
    return '待命'
  })
  const workflowTone = computed(() =>
    !canViewWorkflow.value
      ? 'info'
      : workflowStaleCount.value > 0
        ? 'danger'
        : (workflowOverview.value?.stats.runningCount || 0) > 0
          ? 'warning'
          : 'success'
  )
  const marketStatusLabel = computed(() => {
    if (!canViewMarket.value) return '未授权'
    const status = marketSync.value?.lastExecution?.status
    if (['queued', 'running', 'retry_waiting'].includes(status || '')) return '同步中'
    if (status === 'failed') return '同步失败'
    return marketSync.value?.lastSyncAt ? '正常' : '等待同步'
  })
  const marketTone = computed(() =>
    marketSync.value?.lastExecution?.status === 'failed'
      ? 'danger'
      : ['queued', 'running', 'retry_waiting'].includes(
            marketSync.value?.lastExecution?.status || ''
          )
        ? 'warning'
        : canViewMarket.value
          ? 'success'
          : 'info'
  )
  const tradingStatusLabel = computed(() =>
    !tradingOverview.value
      ? '未加载'
      : tradingOverview.value.control.emergencyStopped
        ? '已急停'
        : '运行中'
  )
  const tradingTone = computed(() =>
    !tradingOverview.value
      ? 'info'
      : tradingOverview.value.control.emergencyStopped
        ? 'danger'
        : 'success'
  )

  const attentionItems = computed(() => {
    const items: Array<{
      title: string
      description: string
      icon: string
      tone: Tone
      path?: string
    }> = []
    if (tradingOverview.value?.control.emergencyStopped) {
      items.push({
        title: '交易全局急停已开启',
        description: tradingOverview.value.control.stopReason || '交易账户不会继续执行新指令。',
        icon: 'ri:stop-circle-line',
        tone: 'danger',
        path: '/trading/overview'
      })
    }
    if (workflowStaleCount.value > 0) {
      items.push({
        title: '存在超时运行的调度任务',
        description: `${workflowStaleCount.value} 个执行需要检查。`,
        icon: 'ri:time-line',
        tone: 'warning',
        path: '/scheduler/execution'
      })
    }
    if (marketSync.value?.lastExecution?.status === 'failed') {
      items.push({
        title: '最近一次行情同步失败',
        description: '检查执行详情后重新同步币种元数据。',
        icon: 'ri:database-2-line',
        tone: 'danger',
        path: '/data/market-metadata'
      })
    }
    const pausedAccounts =
      tradingOverview.value?.accounts.filter((item) => item.status === 'paused') || []
    if (pausedAccounts.length) {
      items.push({
        title: `${pausedAccounts.length} 个交易账户已暂停`,
        description: '检查账户风控、凭据或对账状态。',
        icon: 'ri:pause-circle-line',
        tone: 'warning',
        path: '/trading/overview'
      })
    }
    return items
  })

  const overallTone = computed<Tone>(() => {
    if (attentionItems.value.some((item) => item.tone === 'danger')) return 'danger'
    if (attentionItems.value.length) return 'warning'
    return 'success'
  })
  const overallLabel = computed(() =>
    overallTone.value === 'danger'
      ? '存在需要处理的异常'
      : overallTone.value === 'warning'
        ? '系统可用，部分状态需关注'
        : '核心服务运行正常'
  )
  const overallDescription = computed(() =>
    attentionItems.value.length
      ? `${attentionItems.value.length} 项状态需要检查，未影响运维页面访问。`
      : '应用、行情、调度与交易运行域均已响应。'
  )
  const overallIcon = computed(() =>
    overallTone.value === 'danger'
      ? 'ri:error-warning-line'
      : overallTone.value === 'warning'
        ? 'ri:alert-line'
        : 'ri:checkbox-circle-line'
  )

  const metrics = computed(() => [
    {
      label: '激活调度',
      value: workflowOverview.value?.stats.activeDefinitionCount ?? '--',
      caption: `共 ${workflowOverview.value?.stats.definitionCount ?? '--'} 个定义`,
      icon: 'ri:flow-chart',
      tone: 'primary'
    },
    {
      label: '执行总数',
      value: workflowOverview.value?.stats.executionCount ?? '--',
      caption: `${workflowOverview.value?.stats.runningCount ?? '--'} 个运行中`,
      icon: 'ri:play-list-2-line',
      tone: 'success'
    },
    {
      label: '行情标的',
      value: marketSymbolTotal.value ?? '--',
      caption: 'Binance 元数据',
      icon: 'ri:coins-line',
      tone: 'primary'
    },
    {
      label: '交易账户',
      value: tradingOverview.value?.accounts.length ?? '--',
      caption: `${activeAccountCount.value} 个启用`,
      icon: 'ri:wallet-3-line',
      tone: tradingOverview.value?.control.emergencyStopped ? 'danger' : 'success'
    }
  ])

  const activities = computed(() => [
    {
      label: '应用服务',
      description: `版本 ${serviceMeta.version || '--'} 已响应`,
      time: refreshedAt.value,
      tone: 'success'
    },
    {
      label: '调度引擎',
      description: `${workflowOverview.value?.stats.executionCount ?? '--'} 次累计执行`,
      time: workflowOverview.value?.stats.latestExecutedAt || '',
      tone: workflowStaleCount.value > 0 ? 'danger' : 'success'
    },
    {
      label: '行情数据',
      description: marketStatusLabel.value,
      time: marketSync.value?.lastSyncAt || '',
      tone: marketTone.value
    },
    {
      label: '交易风控',
      description: tradingStatusLabel.value,
      time: tradingOverview.value?.control.updatedAt || '',
      tone: tradingTone.value
    }
  ])

  const formatTime = (value?: string | null, timeOnly = false) => {
    if (!value) return '--'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return new Intl.DateTimeFormat('zh-CN', {
      month: timeOnly ? undefined : '2-digit',
      day: timeOnly ? undefined : '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
      timeZone: 'UTC'
    }).format(date)
  }

  const loadDashboard = async () => {
    loading.value = true
    try {
      const [meta, trading, workflow, sync, symbols] = await Promise.all([
        fetchHomeMeta(),
        fetchTradingOverview(),
        canViewWorkflow.value ? fetchSchedulerOverview() : Promise.resolve(null),
        canViewMarket.value ? fetchMarketSyncStatus() : Promise.resolve(null),
        canViewMarket.value
          ? fetchMarketSymbols({ limit: 1, status: 'all' })
          : Promise.resolve(null)
      ])
      Object.assign(serviceMeta, meta)
      tradingOverview.value = trading
      workflowOverview.value = workflow
      marketSync.value = sync
      marketSymbolTotal.value = symbols?.total ?? null
      refreshedAt.value = new Date().toISOString()
    } finally {
      loading.value = false
    }
  }

  const goLogin = () => router.push({ name: 'Login', query: { redirect: '/home' } })

  let refreshTimer: ReturnType<typeof setInterval> | null = null
  onMounted(() => {
    if (isGuest.value) return
    void loadDashboard()
    refreshTimer = setInterval(() => void loadDashboard(), 30000)
  })
  onBeforeUnmount(() => {
    if (refreshTimer) clearInterval(refreshTimer)
  })
</script>

<style scoped lang="scss">
  .operations-home {
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
  .head-actions,
  .health-banner,
  .health-state,
  .health-meta,
  .domain-card header,
  .domain-title,
  .panel-head,
  .status-legend {
    display: flex;
    align-items: center;
  }

  .page-head,
  .health-banner,
  .domain-card header,
  .panel-head {
    gap: 16px;
    justify-content: space-between;
  }

  h1,
  h2,
  p {
    margin: 0;
  }

  .page-head {
    min-height: 72px;
  }

  .page-context,
  .section-kicker {
    display: flex;
    gap: 6px;
    align-items: center;
    font-size: 11px;
    font-weight: 600;
    color: var(--theme-color);
  }

  .page-context :deep(.art-svg-icon) {
    font-size: 15px;
  }

  h1 {
    margin-top: 6px;
    font-size: 28px;
    line-height: 1.3;
  }

  .page-head p,
  .panel-head p,
  .domain-title p {
    margin-top: 4px;
    color: var(--art-gray-600);
  }

  .head-actions {
    gap: 12px;
  }

  .head-actions > span {
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .health-banner {
    min-height: 108px;
    padding: 18px 20px;
    overflow: hidden;
    background: var(--default-box-color);
    border-left: 3px solid var(--el-color-success);
  }

  .health-banner--warning {
    border-left-color: var(--el-color-warning);
  }

  .health-banner--danger {
    border-left-color: var(--el-color-danger);
  }

  .health-state {
    gap: 14px;
    min-width: 0;
  }

  .health-state__icon,
  .metric-card__icon,
  .domain-icon,
  .guest-panel__icon {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    border-radius: 8px;
  }

  .health-state__icon {
    width: 48px;
    height: 48px;
    font-size: 24px;
    color: var(--el-color-success);
    background: color-mix(in srgb, var(--el-color-success) 11%, transparent);
  }

  .health-banner--warning .health-state__icon {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .health-banner--danger .health-state__icon {
    color: var(--el-color-danger);
    background: color-mix(in srgb, var(--el-color-danger) 11%, transparent);
  }

  .health-state span,
  .health-state strong,
  .health-state small,
  .health-meta span,
  .health-meta strong {
    display: block;
  }

  .health-state span,
  .health-state small,
  .health-meta span {
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .health-state strong {
    margin: 4px 0;
    font-size: 17px;
  }

  .health-meta {
    flex: 0 0 auto;
  }

  .health-meta > div {
    min-width: 118px;
    padding: 4px 20px;
    border-left: 1px solid var(--art-card-border);
  }

  .health-meta strong {
    max-width: 160px;
    margin-top: 5px;
    overflow: hidden;
    font-size: 13px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .metric-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 16px;
  }

  .metric-card {
    display: flex;
    gap: 13px;
    align-items: center;
    min-width: 0;
    min-height: 104px;
    padding: 16px;
    background: var(--default-box-color);
  }

  .metric-card__icon,
  .domain-icon {
    width: 40px;
    height: 40px;
    font-size: 20px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .metric-card__icon.is-success,
  .domain-icon.is-trading {
    color: var(--el-color-success);
    background: color-mix(in srgb, var(--el-color-success) 10%, transparent);
  }

  .metric-card__icon.is-warning,
  .domain-icon.is-workflow {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .metric-card__icon.is-danger {
    color: var(--el-color-danger);
    background: color-mix(in srgb, var(--el-color-danger) 10%, transparent);
  }

  .metric-card > div {
    min-width: 0;
  }

  .metric-card span,
  .metric-card strong,
  .metric-card small {
    display: block;
  }

  .metric-card span,
  .metric-card small {
    color: var(--art-gray-600);
  }

  .metric-card span {
    font-size: 11px;
  }

  .metric-card strong {
    margin-top: 4px;
    overflow: hidden;
    font-size: 20px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .metric-card small {
    margin-top: 3px;
    font-size: 10px;
  }

  .domain-grid,
  .operations-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
  }

  .domain-card,
  .activity-panel,
  .attention-panel {
    min-width: 0;
    padding: 18px;
    background: var(--default-box-color);
  }

  .domain-title {
    gap: 11px;
    min-width: 0;
  }

  .domain-title h2,
  .panel-head h2 {
    font-size: 15px;
  }

  .domain-title p,
  .panel-head p {
    font-size: 10px;
  }

  .domain-facts {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    margin: 16px 0 10px;
    border-top: 1px solid var(--art-card-border);
    border-bottom: 1px solid var(--art-card-border);
  }

  .domain-facts > div {
    min-width: 0;
    padding: 13px 12px;
    border-right: 1px solid var(--art-card-border);
  }

  .domain-facts > div:first-child {
    padding-left: 0;
  }

  .domain-facts > div:last-child {
    padding-right: 0;
    border-right: 0;
  }

  .domain-facts dt {
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .domain-facts dd {
    margin: 5px 0 0;
    overflow: hidden;
    font-size: 12px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .is-success {
    color: var(--el-color-success);
  }

  .is-danger {
    color: var(--el-color-danger);
  }

  .panel-head {
    min-height: 42px;
    padding-bottom: 14px;
    border-bottom: 1px solid var(--art-card-border);
  }

  .status-legend {
    gap: 6px;
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .status-legend i {
    width: 6px;
    height: 6px;
    background: var(--el-color-success);
    border-radius: 50%;
  }

  .activity-row {
    display: grid;
    grid-template-columns: 10px minmax(0, 1fr) auto;
    gap: 12px;
    align-items: center;
    min-height: 66px;
    border-bottom: 1px solid var(--art-card-border);
  }

  .activity-row:last-child {
    border-bottom: 0;
  }

  .activity-dot {
    width: 7px;
    height: 7px;
    background: var(--art-gray-400);
    border-radius: 50%;
  }

  .activity-dot.is-success {
    background: var(--el-color-success);
  }

  .activity-dot.is-warning {
    background: var(--el-color-warning);
  }

  .activity-dot.is-danger {
    background: var(--el-color-danger);
  }

  .activity-row strong,
  .activity-row span {
    display: block;
  }

  .activity-row strong {
    font-size: 12px;
  }

  .activity-row span,
  .activity-row time {
    margin-top: 4px;
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .attention-count {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    font-size: 11px;
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
    border-radius: 8px;
  }

  .attention-list button {
    display: grid;
    grid-template-columns: 34px minmax(0, 1fr) 18px;
    gap: 11px;
    align-items: center;
    width: 100%;
    min-height: 66px;
    padding: 10px 8px;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
    border-bottom: 1px solid var(--art-card-border);
    border-radius: 6px;
  }

  .attention-list button:hover,
  .attention-list button:focus-visible {
    background: var(--art-hover-color);
  }

  .attention-list button:focus-visible {
    outline: 2px solid var(--theme-color);
  }

  .attention-list button > span {
    display: grid;
    place-items: center;
    width: 34px;
    height: 34px;
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
    border-radius: 8px;
  }

  .attention-list button > span.is-danger {
    color: var(--el-color-danger);
    background: color-mix(in srgb, var(--el-color-danger) 10%, transparent);
  }

  .attention-list strong,
  .attention-list small {
    display: block;
  }

  .attention-list strong {
    font-size: 12px;
  }

  .attention-list small {
    margin-top: 4px;
    font-size: 10px;
    line-height: 1.5;
    color: var(--art-gray-600);
  }

  .attention-empty {
    display: grid;
    gap: 7px;
    place-items: center;
    min-height: 220px;
    color: var(--art-gray-600);
    text-align: center;
  }

  .attention-empty :deep(.art-svg-icon) {
    font-size: 28px;
    color: var(--el-color-success);
  }

  .attention-empty strong {
    font-size: 13px;
    color: var(--art-gray-900);
  }

  .attention-empty span {
    font-size: 11px;
  }

  .guest-panel {
    display: grid;
    grid-template-columns: 56px minmax(0, 1fr) auto;
    gap: 18px;
    align-items: center;
    min-height: 180px;
    padding: 28px;
    background: var(--default-box-color);
  }

  .guest-panel__icon {
    width: 56px;
    height: 56px;
    font-size: 28px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .guest-panel h1 {
    margin-top: 5px;
  }

  .guest-panel p {
    margin-top: 5px;
    color: var(--art-gray-600);
  }

  @media (max-width: 1000px) {
    .metric-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .health-banner {
      align-items: flex-start;
    }

    .health-meta > div {
      min-width: 96px;
      padding: 4px 12px;
    }
  }

  @media (max-width: 760px) {
    .operations-home {
      padding: 14px 12px 20px;
    }

    .page-head,
    .health-banner,
    .head-actions {
      flex-direction: column;
      align-items: stretch;
    }

    .head-actions .el-button {
      width: 100%;
      margin: 0;
    }

    .health-meta {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      border-top: 1px solid var(--art-card-border);
    }

    .health-meta > div {
      min-width: 0;
      padding: 12px 8px 0;
      border-right: 1px solid var(--art-card-border);
      border-left: 0;
    }

    .health-meta > div:first-child {
      padding-left: 0;
    }

    .health-meta > div:last-child {
      padding-right: 0;
      border-right: 0;
    }

    .domain-grid,
    .operations-grid {
      grid-template-columns: 1fr;
    }

    .activity-row {
      grid-template-columns: 10px minmax(0, 1fr);
    }

    .activity-row time {
      grid-column: 2;
      margin: 0;
    }

    .guest-panel {
      grid-template-columns: 1fr;
    }

    .guest-panel .el-button {
      width: 100%;
    }
  }

  @media (max-width: 480px) {
    .metric-grid {
      grid-template-columns: 1fr;
    }

    .domain-facts {
      grid-template-columns: 1fr;
    }

    .domain-facts > div,
    .domain-facts > div:first-child,
    .domain-facts > div:last-child {
      padding: 10px 0;
      border-right: 0;
      border-bottom: 1px solid var(--art-card-border);
    }

    .domain-facts > div:last-child {
      border-bottom: 0;
    }
  }
</style>
