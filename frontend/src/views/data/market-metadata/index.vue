<template>
  <div class="market-metadata-page">
    <header class="page-head">
      <div>
        <div class="page-context">
          <ArtSvgIcon icon="ri:database-2-line" />
          行情数据
        </div>
        <h1>币种元数据</h1>
      </div>
      <div class="head-actions">
        <ElButton :icon="Refresh" :loading="symbolsLoading" @click="reloadSymbols">刷新</ElButton>
        <ElButton type="primary" :icon="View" @click="openWorkflows">查看采集工作流</ElButton>
      </div>
    </header>

    <section class="catalog-section">
      <header class="section-head">
        <div>
          <h2>交易标的目录</h2>
          <span>{{ symbolTotal.toLocaleString() }} 个结果</span>
        </div>
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
        <ElSelect
          v-model="filters.quoteAsset"
          clearable
          filterable
          allow-create
          placeholder="全部报价资产"
          @change="reloadSymbols"
        >
          <ElOption label="USDT" value="USDT" />
          <ElOption label="USDC" value="USDC" />
        </ElSelect>
        <ElRadioGroup v-model="filters.status" @change="reloadSymbols">
          <ElRadioButton value="trading">交易中</ElRadioButton>
          <ElRadioButton value="suspended">已暂停</ElRadioButton>
          <ElRadioButton value="all">全部</ElRadioButton>
        </ElRadioGroup>
        <ElButton :icon="Search" @click="reloadSymbols">筛选</ElButton>
      </div>

      <div v-loading="symbolsLoading" class="symbol-table-wrap">
        <ElTable :data="symbols" row-key="id" table-layout="fixed" @row-click="openSymbol">
          <ElTableColumn label="交易对" min-width="180">
            <template #default="{ row }">
              <div class="symbol-cell">
                <span class="symbol-avatar">{{ row.baseAsset.slice(0, 1) }}</span>
                <div>
                  <strong>{{ row.nativeSymbol }}</strong>
                  <span>{{ row.baseAsset }} / {{ row.quoteAsset }}</span>
                </div>
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
                <i></i>{{ row.status === 'trading' ? '交易中' : row.status }}
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
          <ElTableColumn label="最小数量" min-width="140">
            <template #default="{ row }"
              ><span class="decimal-value">{{ row.minQuantity }}</span></template
            >
          </ElTableColumn>
          <ElTableColumn label="更新时间" min-width="180">
            <template #default="{ row }"
              ><span class="table-time">{{ formatTime(row.updatedAt) }}</span></template
            >
          </ElTableColumn>
        </ElTable>
        <div v-if="!symbolsLoading && !symbols.length" class="empty-state">
          <ArtSvgIcon icon="ri:database-2-line" />
          <strong>没有匹配的标的</strong>
        </div>
      </div>
      <div v-if="hasMore" class="load-more">
        <ElButton text :loading="symbolsLoading" @click="loadMore">加载更多</ElButton>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
  import { Refresh, Search, View } from '@element-plus/icons-vue'
  import {
    fetchMarketSymbols,
    type MarketStatus,
    type MarketSymbol,
    type MarketType
  } from '@/api/market'
  import { formatDateTime as formatTime } from '@/utils/date'

  defineOptions({ name: 'MarketMetadataPage' })

  const router = useRouter()
  const symbolsLoading = ref(false)
  const symbols = ref<MarketSymbol[]>([])
  const nextCursor = ref('')
  const hasMore = ref(false)
  const symbolTotal = ref(0)
  const filters = reactive<{
    keyword: string
    market: MarketType | ''
    quoteAsset: string
    status: MarketStatus
  }>({ keyword: '', market: '', quoteAsset: '', status: 'all' })

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
  const openWorkflows = () => void router.push('/scheduler/definition')
  const openSymbol = (row: MarketSymbol) => {
    void router.push({
      path: '/quant-data/candles',
      query: { instrumentId: row.id, market: row.market, interval: '1h' }
    })
  }

  onMounted(() => void loadSymbols())
</script>

<style scoped lang="scss">
  .market-metadata-page {
    display: flex;
    flex-direction: column;
    gap: 20px;
    min-width: 0;
    min-height: 100%;
    padding: 20px;
    color: var(--art-gray-900);
    background: var(--default-bg-color);
  }

  .page-head,
  .head-actions,
  .section-head,
  .symbol-cell,
  .status-cell {
    display: flex;
    align-items: center;
  }

  .page-head {
    gap: 16px;
    justify-content: space-between;

    h1 {
      margin: 4px 0 0;
      font-size: 24px;
      letter-spacing: 0;
    }
  }

  .page-context {
    display: flex;
    gap: 6px;
    align-items: center;
    font-size: 13px;
    color: var(--art-gray-500);
  }

  .head-actions {
    gap: 8px;
  }

  .catalog-section {
    min-width: 0;
  }

  .section-head {
    justify-content: space-between;
    margin-bottom: 14px;

    h2 {
      margin: 0 0 4px;
      font-size: 18px;
      letter-spacing: 0;
    }

    span {
      font-size: 13px;
      color: var(--art-gray-500);
    }
  }

  .filters {
    display: grid;
    grid-template-columns: minmax(220px, 1fr) 140px 160px auto auto;
    gap: 10px;
    align-items: center;
    margin-bottom: 14px;
  }

  .symbol-table-wrap {
    min-height: 280px;
    overflow: hidden;
    border: 1px solid var(--art-gray-200);
    border-radius: 6px;
  }

  .symbol-cell {
    gap: 10px;

    > div {
      display: flex;
      flex-direction: column;
      min-width: 0;
    }

    strong,
    span {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    span {
      font-size: 12px;
      color: var(--art-gray-500);
    }
  }

  .symbol-avatar {
    display: grid;
    flex: 0 0 32px;
    place-items: center;
    width: 32px;
    height: 32px;
    font-weight: 700;
    color: #0e7490 !important;
    background: #ecfeff;
    border-radius: 6px;
  }

  .status-cell {
    gap: 7px;

    i {
      width: 7px;
      height: 7px;
      background: #16a34a;
      border-radius: 50%;
    }

    &.is-paused i {
      background: #94a3b8;
    }
  }

  .decimal-value,
  .table-time {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 12px;
  }

  .empty-state {
    display: grid;
    gap: 10px;
    place-content: center;
    justify-items: center;
    min-height: 280px;
    color: var(--art-gray-500);

    svg {
      font-size: 34px;
    }
  }

  .load-more {
    display: flex;
    justify-content: center;
    padding-top: 12px;
  }

  @media (width <= 960px) {
    .filters {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (width <= 640px) {
    .market-metadata-page {
      padding: 14px;
    }

    .page-head {
      flex-direction: column;
      align-items: flex-start;
    }

    .head-actions {
      width: 100%;

      :deep(.el-button) {
        flex: 1;
        min-width: 0;
      }
    }

    .filters {
      grid-template-columns: 1fr;
    }
  }
</style>
