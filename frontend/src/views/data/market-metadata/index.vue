<template>
  <div class="market-metadata-page">
    <header class="page-head">
      <div>
        <div class="eyebrow">MARKET DATA / BINANCE</div>
        <h1>币种元数据</h1>
        <p>管理后续同步范围，查看交易所标的、精度和当前交易状态。</p>
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

    <section class="sync-strip" aria-label="同步状态">
      <div class="sync-mark" :class="{ 'sync-mark--active': isSyncing }">
        <span class="sync-mark__dot"></span>
        <div>
          <strong>{{ isSyncing ? '同步工作流运行中' : '同步服务待命' }}</strong>
          <span>{{
            syncStatus.lastSyncAt ? `上次完成 ${syncStatus.lastSyncAt}` : '尚未完成过同步'
          }}</span>
        </div>
      </div>
      <dl class="sync-facts">
        <div>
          <dt>下次自动同步</dt>
          <dd>{{ syncStatus.nextSyncAt || '--' }}</dd>
        </div>
        <div>
          <dt>当前范围</dt>
          <dd>{{ selectedScopeText }}</dd>
        </div>
        <div>
          <dt>已加载标的</dt>
          <dd>{{ symbolTotal.toLocaleString() }}</dd>
        </div>
        <div v-if="syncStatus.lastExecution">
          <dt>最近执行</dt>
          <dd>{{ executionStatusLabel(syncStatus.lastExecution.status) }}</dd>
        </div>
      </dl>
    </section>

    <section class="control-band">
      <div class="control-block">
        <div class="control-label">同步市场</div>
        <ElCheckboxGroup v-model="settings.marketTypes" :disabled="!canManage">
          <ElCheckbox label="spot">Spot</ElCheckbox>
          <ElCheckbox label="usd_m">USD-M</ElCheckbox>
        </ElCheckboxGroup>
      </div>
      <div class="control-block">
        <div class="control-label">报价资产</div>
        <ElCheckboxGroup v-model="settings.quoteAssets" :disabled="!canManage">
          <ElCheckbox label="USDT">USDT</ElCheckbox>
          <ElCheckbox label="USDC">USDC</ElCheckbox>
          <ElCheckbox label="FDUSD">FDUSD</ElCheckbox>
        </ElCheckboxGroup>
      </div>
      <div class="control-note">
        <ArtSvgIcon icon="ri:information-line" />
        <span>范围只影响后续同步，不会删除历史元数据。</span>
      </div>
    </section>

    <section class="table-band">
      <div class="band-head">
        <div>
          <div class="eyebrow">INSTRUMENT CATALOG</div>
          <h2>交易标的</h2>
        </div>
        <div class="result-count">{{ symbolTotal.toLocaleString() }} 个结果</div>
      </div>

      <div class="filters">
        <ElInput
          v-model="filters.keyword"
          clearable
          :prefix-icon="Search"
          placeholder="搜索交易对 / 基础资产"
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
                <strong>{{ row.nativeSymbol }}</strong>
                <span>{{ row.baseAsset }} / {{ row.quoteAsset }}</span>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="市场" width="110">
            <template #default="{ row }">
              <span class="mono">{{ row.market === 'usd_m' ? 'USD-M' : 'SPOT' }}</span>
            </template>
          </ElTableColumn>
          <ElTableColumn label="状态" width="120">
            <template #default="{ row }">
              <ElTag :type="row.status === 'trading' ? 'success' : 'warning'" effect="plain">
                {{ row.status === 'trading' ? '交易中' : '已暂停' }}
              </ElTag>
            </template>
          </ElTableColumn>
          <ElTableColumn label="价格精度" min-width="140">
            <template #default="{ row }"
              ><span class="mono">{{ row.priceTick }}</span></template
            >
          </ElTableColumn>
          <ElTableColumn label="数量步长" min-width="140">
            <template #default="{ row }"
              ><span class="mono">{{ row.quantityStep }}</span></template
            >
          </ElTableColumn>
          <ElTableColumn label="最小名义价值" min-width="150">
            <template #default="{ row }"
              ><span class="mono">{{ row.minNotional }}</span></template
            >
          </ElTableColumn>
          <ElTableColumn label="更新时间" min-width="180">
            <template #default="{ row }"
              ><span class="mono muted">{{ row.updatedAt }}</span></template
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
  import { ElMessage } from 'element-plus'
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
    () => `${settings.marketTypes.length} 市场 · ${settings.quoteAssets.join(' / ')}`
  )

  const executionStatusLabel = (status: string) =>
    ({
      queued: '排队中',
      running: '运行中',
      retry_waiting: '等待重试',
      success: '成功',
      failed: '失败'
    })[status] ||
    status ||
    '--'

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
    --ink: #17191b;
    --muted: #70777b;
    --paper: #e8e7e2;
    --panel: #f4f3ee;
    --line: #c9c9c2;
    --strong-line: #17191b;
    --panel-soft: rgb(244 243 238 / 0.65);
    --lift-shadow: rgb(23 25 27 / 0.12);
    --acid: #c7f46b;

    display: flex;
    flex-direction: column;
    gap: 18px;
    min-width: 0;
    padding: 24px 28px 32px;
    font-family: 'Space Grotesk', 'PingFang SC', 'Microsoft YaHei', sans-serif;
    color: var(--ink);
    background: var(--paper);
  }

  :global(html.dark .market-metadata-page) {
    --ink: #eff4f1;
    --muted: #9da6aa;
    --paper: #0d0f10;
    --panel: #181b1e;
    --line: #343a3d;
    --strong-line: #697276;
    --panel-soft: rgb(24 27 30 / 0.72);
    --lift-shadow: rgb(0 0 0 / 0.38);
  }

  .page-head,
  .band-head,
  .head-actions,
  .filters,
  .sync-strip,
  .sync-facts,
  .sync-mark,
  .control-band,
  .control-block,
  .control-note {
    display: flex;
    align-items: center;
  }

  .page-head,
  .band-head {
    gap: 20px;
    justify-content: space-between;
  }

  .eyebrow,
  .control-label,
  .sync-facts dt,
  .mono {
    font-family: 'IBM Plex Mono', 'Cascadia Code', Consolas, monospace;
  }

  .eyebrow {
    font-size: 10px;
    color: var(--muted);
    letter-spacing: 0;
  }

  h1,
  h2,
  p {
    margin: 0;
  }

  h1 {
    margin-top: 7px;
    font-size: 32px;
    font-weight: 600;
    letter-spacing: 0;
  }

  h2 {
    margin-top: 5px;
    font-size: 18px;
    font-weight: 600;
  }

  .page-head p {
    margin-top: 7px;
    font-size: 13px;
    color: var(--muted);
  }

  .head-actions {
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .sync-strip {
    gap: 24px;
    justify-content: space-between;
    padding: 16px 18px;
    background: var(--panel);
    border: 1px solid var(--strong-line);
    border-radius: 2px;
    box-shadow: 5px 5px 0 var(--lift-shadow);
  }

  .sync-mark {
    gap: 11px;
    min-width: 210px;
  }

  .sync-mark__dot {
    width: 9px;
    height: 9px;
    background: #5eaa74;
    border: 2px solid var(--strong-line);
    border-radius: 50%;
  }

  .sync-mark--active .sync-mark__dot {
    background: #eab24d;
    box-shadow: 0 0 0 4px rgb(234 178 77 / 0.18);
  }

  .sync-mark strong,
  .sync-mark span {
    display: block;
  }

  .sync-mark strong {
    font-size: 13px;
  }

  .sync-mark span {
    margin-top: 3px;
    font-size: 11px;
    color: var(--muted);
  }

  .sync-facts {
    flex-wrap: wrap;
    gap: 0;
    margin: 0;
  }

  .sync-facts div {
    min-width: 150px;
    padding: 0 18px;
    border-left: 1px solid var(--line);
  }

  .sync-facts dt {
    font-size: 10px;
    color: var(--muted);
  }

  .sync-facts dd {
    margin: 5px 0 0;
    font-size: 12px;
  }

  .control-band {
    flex-wrap: wrap;
    gap: 24px;
    padding: 15px 18px;
    background: var(--panel-soft);
    border-top: 2px solid var(--strong-line);
    border-bottom: 1px solid var(--line);
  }

  .control-block {
    gap: 13px;
  }

  .control-label {
    font-size: 10px;
    color: var(--muted);
    letter-spacing: 0;
  }

  .control-note {
    gap: 7px;
    margin-left: auto;
    font-size: 11px;
    color: var(--muted);
  }

  .table-band {
    min-width: 0;
    padding-top: 14px;
    border-top: 2px solid var(--strong-line);
  }

  .result-count,
  .muted {
    color: var(--muted);
  }

  .filters {
    flex-wrap: wrap;
    gap: 9px;
    margin: 18px 0 13px;
  }

  .filters :deep(.el-input) {
    width: min(250px, 100%);
  }

  .filters :deep(.el-select) {
    width: 140px;
  }

  .symbol-table-wrap {
    position: relative;
    min-height: 260px;
  }

  .symbol-cell strong,
  .symbol-cell span {
    display: block;
  }

  .symbol-cell strong {
    font-family: 'IBM Plex Mono', Consolas, monospace;
    font-size: 13px;
  }

  .symbol-cell span {
    margin-top: 4px;
    font-size: 11px;
    color: var(--muted);
  }

  .empty-state {
    display: grid;
    gap: 8px;
    place-items: center;
    min-height: 220px;
    color: var(--muted);
    text-align: center;
  }

  .empty-state :deep(svg) {
    font-size: 30px;
  }

  .empty-state strong {
    font-size: 13px;
    color: var(--ink);
  }

  .load-more {
    display: flex;
    justify-content: center;
    padding: 14px 0 0;
  }

  @media (max-width: 900px) {
    .market-metadata-page {
      padding: 18px 16px 24px;
    }

    .sync-strip {
      flex-direction: column;
      align-items: flex-start;
    }

    .sync-facts div:first-child {
      padding-left: 0;
      border-left: 0;
    }
  }

  @media (max-width: 620px) {
    h1 {
      font-size: 26px;
    }

    .page-head {
      flex-direction: column;
      align-items: flex-start;
    }

    .head-actions {
      justify-content: flex-start;
    }

    .control-block {
      flex-direction: column;
      gap: 7px;
      align-items: flex-start;
    }

    .control-note {
      width: 100%;
      margin-left: 0;
    }

    .filters :deep(.el-select),
    .filters :deep(.el-radio-group),
    .filters :deep(.el-button) {
      width: 100%;
    }

    .filters :deep(.el-radio-group) {
      display: flex;
    }

    .filters :deep(.el-radio-button) {
      flex: 1;
    }
  }
</style>
