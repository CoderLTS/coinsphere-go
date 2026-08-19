<template>
  <div class="market-metadata-page">
    <header class="page-head">
      <div>
        <div class="page-context">
          <ArtSvgIcon icon="ri:database-2-line" />
          行情数据
        </div>
        <h1>币种元数据</h1>
        <p>管理 Binance 同步范围，检查交易标的状态与交易精度。</p>
      </div>
      <div class="head-actions">
        <ElButton v-if="canManage" :icon="Refresh" :loading="syncStarting" @click="runSync">
          立即同步
        </ElButton>
        <ElButton
          v-if="canManage"
          type="primary"
          :icon="Check"
          :loading="settingsSaving"
          @click="saveSettings"
        >
          保存范围
        </ElButton>
      </div>
    </header>

    <section class="metric-grid" aria-label="同步摘要">
      <article class="metric-card art-card">
        <span class="metric-icon" :class="isSyncing ? 'is-warning' : 'is-success'">
          <ArtSvgIcon :icon="isSyncing ? 'ri:loader-4-line' : 'ri:checkbox-circle-line'" />
        </span>
        <div>
          <span>同步状态</span>
          <strong>{{
            isSyncing ? '同步中' : executionStatusLabel(syncStatus.lastExecution?.status || '')
          }}</strong>
          <small>{{
            syncStatus.lastExecution ? `执行 #${syncStatus.lastExecution.id}` : '等待首次同步'
          }}</small>
        </div>
      </article>
      <article class="metric-card art-card">
        <span class="metric-icon is-primary"><ArtSvgIcon icon="ri:time-line" /></span>
        <div>
          <span>上次同步</span>
          <strong class="metric-time">{{ formatTime(syncStatus.lastSyncAt) }}</strong>
          <small>每小时自动同步</small>
        </div>
      </article>
      <article class="metric-card art-card">
        <span class="metric-icon is-market"><ArtSvgIcon icon="ri:exchange-line" /></span>
        <div>
          <span>同步范围</span>
          <strong>{{ selectedScopeText }}</strong>
          <small>配置只影响后续同步</small>
        </div>
      </article>
      <article class="metric-card art-card">
        <span class="metric-icon is-symbol"><ArtSvgIcon icon="ri:coins-line" /></span>
        <div>
          <span>交易标的</span>
          <strong>{{ symbolTotal.toLocaleString() }}</strong>
          <small>当前筛选结果</small>
        </div>
      </article>
    </section>

    <section class="sync-grid">
      <article class="sync-card art-card">
        <header class="section-head">
          <div class="section-title">
            <span class="section-icon"><ArtSvgIcon icon="ri:refresh-line" /></span>
            <div><h2>同步任务</h2><p>后台每小时自动拉取 Binance 元数据</p></div>
          </div>
          <ElTag :type="syncStatusType" effect="light">
            {{
              isSyncing ? '正在运行' : executionStatusLabel(syncStatus.lastExecution?.status || '')
            }}
          </ElTag>
        </header>
        <dl class="sync-timeline">
          <div>
            <dt><i class="is-complete"></i>最近完成</dt>
            <dd>{{ formatTime(syncStatus.lastSyncAt) }}</dd>
          </div>
          <div>
            <dt><i :class="{ 'is-running': isSyncing }"></i>下次计划</dt>
            <dd>{{ formatTime(syncStatus.nextSyncAt) }}</dd>
          </div>
          <div>
            <dt><i></i>同步来源</dt>
            <dd>Binance Public API</dd>
          </div>
        </dl>
        <p class="sync-note">
          <ArtSvgIcon icon="ri:information-line" />
          修改范围不会删除历史元数据，未选中的报价资产仅停止后续更新。
        </p>
      </article>

      <article class="scope-card art-card">
        <header class="section-head">
          <div class="section-title">
            <span class="section-icon is-scope"><ArtSvgIcon icon="ri:equalizer-2-line" /></span>
            <div><h2>同步范围</h2><p>选择需要持续维护的市场与报价资产</p></div>
          </div>
          <span v-if="!canManage" class="read-only">只读</span>
        </header>
        <div class="scope-fields">
          <div>
            <label>市场类型</label>
            <ElCheckboxGroup v-model="settings.marketTypes" :disabled="!canManage">
              <ElCheckbox label="spot" border>Spot</ElCheckbox>
              <ElCheckbox label="usd_m" border>USD-M</ElCheckbox>
            </ElCheckboxGroup>
          </div>
          <div>
            <label>报价资产</label>
            <ElCheckboxGroup v-model="settings.quoteAssets" :disabled="!canManage">
              <ElCheckbox label="USDT" border>USDT</ElCheckbox>
              <ElCheckbox label="USDC" border>USDC</ElCheckbox>
              <ElCheckbox label="FDUSD" border>FDUSD</ElCheckbox>
            </ElCheckboxGroup>
          </div>
        </div>
      </article>
    </section>

    <section class="catalog-card art-card">
      <header class="section-head catalog-head">
        <div class="section-title">
          <span class="section-icon is-catalog"><ArtSvgIcon icon="ri:list-check-3" /></span>
          <div><h2>交易标的目录</h2><p>查看状态、价格精度、数量步长与最小名义价值</p></div>
        </div>
        <span class="result-count">{{ symbolTotal.toLocaleString() }} 个结果</span>
      </header>

      <div class="filters" aria-label="标的筛选">
        <ElInput
          v-model="filters.keyword"
          clearable
          :prefix-icon="Search"
          placeholder="搜索交易对或基础资产"
          @keyup.enter="reloadSymbols"
          @clear="reloadSymbols"
        />
        <ElSelect v-model="filters.market" placeholder="全部市场" @change="reloadSymbols">
          <ElOption label="全部市场" value="" />
          <ElOption label="Spot" value="spot" />
          <ElOption label="USD-M" value="usd_m" />
        </ElSelect>
        <ElSelect v-model="filters.quoteAsset" placeholder="全部报价" @change="reloadSymbols">
          <ElOption label="全部报价" value="" />
          <ElOption label="USDT" value="USDT" />
          <ElOption label="USDC" value="USDC" />
          <ElOption label="FDUSD" value="FDUSD" />
        </ElSelect>
        <ElRadioGroup v-model="filters.status" @change="reloadSymbols">
          <ElRadioButton label="trading">交易中</ElRadioButton>
          <ElRadioButton label="suspended">已暂停</ElRadioButton>
          <ElRadioButton label="all">全部</ElRadioButton>
        </ElRadioGroup>
        <ElButton :icon="Search" @click="reloadSymbols">筛选</ElButton>
      </div>

      <div v-loading="symbolsLoading" class="symbol-table-wrap">
        <ElTable :data="symbols" row-key="id" table-layout="fixed">
          <ElTableColumn label="交易对" min-width="180">
            <template #default="{ row }">
              <div class="symbol-cell">
                <span class="symbol-avatar">{{ row.baseAsset.slice(0, 1) }}</span>
                <div
                  ><strong>{{ row.nativeSymbol }}</strong
                  ><span>{{ row.baseAsset }} / {{ row.quoteAsset }}</span></div
                >
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="市场" width="110">
            <template #default="{ row }">
              <ElTag type="info" effect="plain" size="small">
                {{ row.market === 'usd_m' ? 'USD-M' : 'SPOT' }}
              </ElTag>
            </template>
          </ElTableColumn>
          <ElTableColumn label="状态" width="120">
            <template #default="{ row }">
              <span class="status-cell" :class="{ 'is-paused': row.status !== 'trading' }">
                <i></i>{{ row.status === 'trading' ? '交易中' : '已暂停' }}
              </span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="价格精度" min-width="140">
            <template #default="{ row }"
              ><span class="decimal-value">{{ row.priceTick }}</span></template
            >
          </ElTableColumn>
          <ElTableColumn label="数量步长" min-width="140">
            <template #default="{ row }"
              ><span class="decimal-value">{{ row.quantityStep }}</span></template
            >
          </ElTableColumn>
          <ElTableColumn label="最小名义价值" min-width="150">
            <template #default="{ row }"
              ><span class="decimal-value">{{ row.minNotional }}</span></template
            >
          </ElTableColumn>
          <ElTableColumn label="更新时间" min-width="170">
            <template #default="{ row }"
              ><span class="table-time">{{ formatTime(row.updatedAt) }}</span></template
            >
          </ElTableColumn>
        </ElTable>
        <div v-if="!symbolsLoading && !symbols.length" class="empty-state">
          <ArtSvgIcon icon="ri:database-2-line" />
          <strong>没有匹配的标的</strong>
          <span>调整筛选条件或先运行一次元数据同步。</span>
        </div>
      </div>
      <div v-if="hasMore" class="load-more">
        <ElButton text :loading="symbolsLoading" @click="loadMore">加载更多标的</ElButton>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
  import { Check, Refresh, Search } from '@element-plus/icons-vue'
  import { ElMessage, type TagProps } from 'element-plus'
  import {
    fetchMarketSymbols,
    fetchMarketSyncSettings,
    fetchMarketSyncStatus,
    fetchRunMarketSync,
    fetchUpdateMarketSyncSettings,
    type MarketStatus,
    type MarketSymbol,
    type MarketSyncSettings,
    type MarketSyncStatus,
    type MarketType,
    type QuoteAsset
  } from '@/api/market'
  import { useAuth } from '@/hooks/core/useAuth'

  defineOptions({ name: 'MarketMetadataPage' })

  const { hasAuth } = useAuth()
  const canManage = computed(() => hasAuth('data.market.manage'))
  const settingsSaving = ref(false)
  const syncStarting = ref(false)
  const symbolsLoading = ref(false)
  const symbols = ref<MarketSymbol[]>([])
  const nextCursor = ref('')
  const hasMore = ref(false)
  const symbolTotal = ref(0)
  const pollTimer = ref<ReturnType<typeof setInterval> | null>(null)
  const settings = reactive<Pick<MarketSyncSettings, 'marketTypes' | 'quoteAssets'>>({
    marketTypes: ['spot', 'usd_m'],
    quoteAssets: ['USDT', 'USDC']
  })
  const syncStatus = reactive<MarketSyncStatus>({
    lastSyncAt: null,
    nextSyncAt: null,
    lastExecution: null
  })
  const filters = reactive<{
    keyword: string
    market: MarketType | ''
    quoteAsset: QuoteAsset | ''
    status: MarketStatus
  }>({ keyword: '', market: '', quoteAsset: '', status: 'trading' })

  const isSyncing = computed(() => {
    const status = syncStatus.lastExecution?.status
    return status === 'queued' || status === 'running' || status === 'retry_waiting'
  })
  const selectedScopeText = computed(
    () => `${settings.marketTypes.length} 个市场 · ${settings.quoteAssets.length} 个报价`
  )
  const syncStatusType = computed<TagProps['type']>(() => {
    if (syncStatus.lastExecution?.status === 'failed') return 'danger'
    if (isSyncing.value) return 'warning'
    return syncStatus.lastSyncAt ? 'success' : 'info'
  })

  const executionStatusLabel = (status: string) =>
    ({
      queued: '排队中',
      running: '运行中',
      retry_waiting: '等待重试',
      success: '同步成功',
      failed: '同步失败'
    })[status] ||
    status ||
    '等待同步'

  const formatTime = (value?: string | null) => {
    if (!value) return '--'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return new Intl.DateTimeFormat('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      timeZone: 'UTC'
    }).format(date)
  }

  const loadSettings = async () => {
    const [nextSettings, nextStatus] = await Promise.all([
      fetchMarketSyncSettings(),
      fetchMarketSyncStatus()
    ])
    settings.marketTypes = [...nextSettings.marketTypes]
    settings.quoteAssets = [...nextSettings.quoteAssets]
    Object.assign(syncStatus, nextStatus)
  }

  const loadSymbols = async (append = false) => {
    symbolsLoading.value = true
    try {
      const result = await fetchMarketSymbols({
        cursor: append ? nextCursor.value : undefined,
        limit: 100,
        market: filters.market,
        quoteAsset: filters.quoteAsset,
        status: filters.status,
        keyword: filters.keyword.trim()
      })
      symbols.value = append ? [...symbols.value, ...result.records] : result.records
      nextCursor.value = result.nextCursor
      hasMore.value = result.hasMore
      symbolTotal.value = result.total
    } finally {
      symbolsLoading.value = false
    }
  }

  const reloadSymbols = () => {
    nextCursor.value = ''
    void loadSymbols()
  }

  const loadMore = () => void loadSymbols(true)

  const saveSettings = async () => {
    if (!settings.marketTypes.length || !settings.quoteAssets.length) {
      ElMessage.warning('至少选择一个市场和一个报价资产')
      return
    }
    settingsSaving.value = true
    try {
      const nextSettings = await fetchUpdateMarketSyncSettings({
        marketTypes: settings.marketTypes,
        quoteAssets: settings.quoteAssets
      })
      settings.marketTypes = [...nextSettings.marketTypes]
      settings.quoteAssets = [...nextSettings.quoteAssets]
      await loadSymbols()
    } finally {
      settingsSaving.value = false
    }
  }

  const stopPolling = () => {
    if (pollTimer.value) clearInterval(pollTimer.value)
    pollTimer.value = null
  }

  const refreshStatus = async () => {
    const nextStatus = await fetchMarketSyncStatus()
    Object.assign(syncStatus, nextStatus)
    if (!isSyncing.value) {
      stopPolling()
      if (nextStatus.lastExecution?.status === 'success') void loadSymbols()
    }
  }

  const runSync = async () => {
    if (!canManage.value) {
      ElMessage.warning('当前账号没有管理同步范围的权限')
      return
    }
    syncStarting.value = true
    try {
      syncStatus.lastExecution = await fetchRunMarketSync()
      stopPolling()
      pollTimer.value = setInterval(() => void refreshStatus(), 2000)
    } finally {
      syncStarting.value = false
    }
  }

  onMounted(async () => {
    try {
      await Promise.all([loadSettings(), loadSymbols()])
    } catch {
      // 请求层已显示错误；页面保留可操作的空状态。
    }
  })

  onBeforeUnmount(stopPolling)
</script>

<style scoped lang="scss">
  .market-metadata-page {
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
  .section-head,
  .section-title,
  .symbol-cell,
  .status-cell,
  .sync-note {
    display: flex;
    align-items: center;
  }

  .page-head,
  .section-head {
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

  .page-context {
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
  .section-title p {
    margin-top: 4px;
    color: var(--art-gray-600);
  }

  .head-actions {
    gap: 10px;
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

  .metric-icon,
  .section-icon,
  .symbol-avatar {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    border-radius: 8px;
  }

  .metric-icon {
    width: 40px;
    height: 40px;
    font-size: 20px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .metric-icon.is-success {
    color: var(--el-color-success);
    background: color-mix(in srgb, var(--el-color-success) 10%, transparent);
  }

  .metric-icon.is-warning {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .metric-icon.is-market {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .metric-icon.is-symbol {
    color: var(--el-color-success);
    background: color-mix(in srgb, var(--el-color-success) 10%, transparent);
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
    margin-top: 5px;
    overflow: hidden;
    font-size: 17px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .metric-card small {
    margin-top: 4px;
    font-size: 10px;
  }

  .metric-card .metric-time {
    font-size: 14px;
  }

  .sync-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 16px;
  }

  .sync-card,
  .scope-card,
  .catalog-card {
    min-width: 0;
    padding: 18px;
    background: var(--default-box-color);
  }

  .section-title {
    gap: 11px;
    min-width: 0;
  }

  .section-icon {
    width: 36px;
    height: 36px;
    font-size: 18px;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .section-icon.is-scope {
    color: var(--el-color-warning);
    background: color-mix(in srgb, var(--el-color-warning) 11%, transparent);
  }

  .section-icon.is-catalog {
    color: var(--el-color-success);
    background: color-mix(in srgb, var(--el-color-success) 10%, transparent);
  }

  .section-title h2 {
    font-size: 15px;
  }

  .section-title p {
    font-size: 10px;
  }

  .read-only,
  .result-count {
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .sync-timeline {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    margin: 18px 0 14px;
    border-top: 1px solid var(--art-card-border);
    border-bottom: 1px solid var(--art-card-border);
  }

  .sync-timeline > div {
    min-width: 0;
    padding: 13px 12px;
    border-right: 1px solid var(--art-card-border);
  }

  .sync-timeline > div:first-child {
    padding-left: 0;
  }

  .sync-timeline > div:last-child {
    padding-right: 0;
    border-right: 0;
  }

  .sync-timeline dt {
    display: flex;
    gap: 6px;
    align-items: center;
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .sync-timeline i,
  .status-cell i {
    width: 7px;
    height: 7px;
    background: var(--art-gray-400);
    border-radius: 50%;
  }

  .sync-timeline i.is-complete {
    background: var(--el-color-success);
  }

  .sync-timeline i.is-running {
    background: var(--el-color-warning);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--el-color-warning) 12%, transparent);
  }

  .sync-timeline dd {
    margin: 6px 0 0;
    overflow: hidden;
    font-size: 12px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sync-note {
    gap: 7px;
    font-size: 11px;
    line-height: 1.5;
    color: var(--art-gray-600);
  }

  .scope-fields {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 24px;
    padding-top: 18px;
    margin-top: 14px;
    border-top: 1px solid var(--art-card-border);
  }

  .scope-fields label {
    display: block;
    margin-bottom: 10px;
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .scope-fields :deep(.el-checkbox-group) {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .scope-fields :deep(.el-checkbox) {
    margin-right: 0;
  }

  .catalog-head {
    padding-bottom: 16px;
    border-bottom: 1px solid var(--art-card-border);
  }

  .filters {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    padding: 14px;
    margin: 16px 0 4px;
    background: var(--el-fill-color-lighter);
    border-radius: 6px;
  }

  .filters :deep(.el-input) {
    width: min(260px, 100%);
  }

  .filters :deep(.el-select) {
    width: 140px;
  }

  .symbol-table-wrap {
    position: relative;
    min-height: 280px;
  }

  .symbol-cell {
    gap: 10px;
  }

  .symbol-avatar {
    width: 30px;
    height: 30px;
    font-size: 11px;
    font-weight: 700;
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .symbol-cell strong,
  .symbol-cell div > span {
    display: block;
  }

  .symbol-cell strong {
    font-size: 12px;
  }

  .symbol-cell div > span,
  .table-time {
    margin-top: 3px;
    font-size: 10px;
    color: var(--art-gray-600);
  }

  .status-cell {
    gap: 7px;
    font-size: 11px;
    color: var(--el-color-success);
  }

  .status-cell i {
    background: currentcolor;
  }

  .status-cell.is-paused {
    color: var(--el-color-warning);
  }

  .decimal-value {
    font-family: 'Cascadia Code', Consolas, monospace;
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }

  .empty-state {
    display: grid;
    gap: 7px;
    place-items: center;
    min-height: 280px;
    color: var(--art-gray-600);
    text-align: center;
  }

  .empty-state :deep(.art-svg-icon) {
    font-size: 30px;
    color: var(--art-gray-400);
  }

  .empty-state strong {
    font-size: 13px;
    color: var(--art-gray-900);
  }

  .empty-state span {
    font-size: 11px;
  }

  .load-more {
    padding-top: 10px;
    text-align: center;
    border-top: 1px solid var(--art-card-border);
  }

  @media (max-width: 1000px) {
    .metric-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .sync-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 760px) {
    .market-metadata-page {
      padding: 14px 12px 20px;
    }

    .page-head,
    .head-actions {
      flex-direction: column;
      align-items: stretch;
    }

    .head-actions .el-button {
      width: 100%;
      margin: 0;
    }

    .sync-timeline,
    .scope-fields {
      grid-template-columns: 1fr;
    }

    .sync-timeline > div,
    .sync-timeline > div:first-child,
    .sync-timeline > div:last-child {
      padding: 10px 0;
      border-right: 0;
      border-bottom: 1px solid var(--art-card-border);
    }

    .sync-timeline > div:last-child {
      border-bottom: 0;
    }

    .filters :deep(.el-input),
    .filters :deep(.el-select),
    .filters > .el-button,
    .filters :deep(.el-radio-group) {
      width: 100%;
    }

    .filters :deep(.el-radio-button) {
      flex: 1;
    }

    .filters :deep(.el-radio-button__inner) {
      width: 100%;
    }
  }

  @media (max-width: 480px) {
    .metric-grid {
      grid-template-columns: 1fr;
    }

    .section-head {
      align-items: flex-start;
    }
  }
</style>
