<template>
  <div class="art-full-height market-metadata-page">
    <ArtSearchBar
      v-model="filters"
      :items="formItems"
      :show-expand="false"
      @search="reloadSymbols"
      @reset="resetFilters"
    />

    <ElCard class="art-table-card">
      <ArtTableHeader
        v-model:columns="columnChecks"
        :show-zebra="false"
        :loading="loading"
        @refresh="reloadSymbols"
      >
        <template #left>
          <ElSpace wrap>
            <div class="table-title">
              <h1>币种元数据</h1>
              <span>{{ pagination.total.toLocaleString() }} 个结果</span>
            </div>
            <ElButton type="primary" :icon="View" @click="openWorkflows"> 查看采集工作流 </ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="symbols"
        :columns="columns"
        :pagination="pagination"
        :stripe="false"
        @row-click="openSymbol"
        @pagination:size-change="handleSizeChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { View } from '@element-plus/icons-vue'
  import { ElTag } from 'element-plus'
  import {
    fetchMarketSymbols,
    type MarketStatus,
    type MarketSymbol,
    type MarketType
  } from './market-api'
  import { useCursorPagination } from '@/hooks/core/useCursorPagination'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import { formatDateTime as formatTime } from '@/utils/date'

  defineOptions({ name: 'MarketMetadataPage' })

  const router = useRouter()
  const loading = ref(false)
  const symbols = ref<MarketSymbol[]>([])
  const filters = reactive<{
    keyword: string
    market: MarketType | ''
    quoteAsset: string
    status: MarketStatus
  }>({ keyword: '', market: '', quoteAsset: '', status: 'all' })
  const { pagination, requestParams, applyPage, reset, moveTo } = useCursorPagination(20)

  const formItems = computed(() => [
    {
      label: '关键词',
      key: 'keyword',
      type: 'input',
      props: { clearable: true, placeholder: '搜索交易对或基础资产' }
    },
    {
      label: '市场',
      key: 'market',
      type: 'select',
      props: {
        clearable: true,
        placeholder: '全部市场',
        options: [
          { label: 'Spot', value: 'spot' },
          { label: 'USD-M', value: 'usd_m' }
        ]
      }
    },
    {
      label: '报价资产',
      key: 'quoteAsset',
      type: 'select',
      props: {
        clearable: true,
        filterable: true,
        allowCreate: true,
        placeholder: '全部报价资产',
        options: [
          { label: 'USDT', value: 'USDT' },
          { label: 'USDC', value: 'USDC' }
        ]
      }
    },
    {
      label: '状态',
      key: 'status',
      type: 'radiogroup',
      props: {
        options: [
          { label: '交易中', value: 'trading' },
          { label: '已暂停', value: 'suspended' },
          { label: '全部', value: 'all' }
        ]
      }
    }
  ])

  const renderStatus = (row: MarketSymbol) =>
    h(ElTag, { type: row.status === 'trading' ? 'success' : 'info', effect: 'plain' }, () =>
      row.status === 'trading' ? '交易中' : '已暂停'
    )

  const { columns, columnChecks } = useTableColumns<MarketSymbol>(() => [
    {
      prop: 'nativeSymbol',
      label: '交易对',
      minWidth: 180,
      formatter: (row) =>
        h('div', { class: 'symbol-cell' }, [
          h('span', { class: 'symbol-avatar' }, row.baseAsset.slice(0, 1)),
          h('div', [
            h('strong', row.nativeSymbol),
            h('span', `${row.baseAsset} / ${row.quoteAsset}`)
          ])
        ])
    },
    {
      prop: 'market',
      label: '市场',
      width: 110,
      formatter: (row) =>
        h(ElTag, { type: 'info', effect: 'plain', size: 'small' }, () =>
          row.market === 'usd_m' ? 'USD-M' : 'SPOT'
        )
    },
    { prop: 'status', label: '状态', width: 110, formatter: renderStatus },
    {
      prop: 'priceTick',
      label: '价格精度',
      minWidth: 140,
      formatter: (row) => h('span', { class: 'decimal-value' }, row.priceTick)
    },
    {
      prop: 'quantityStep',
      label: '数量步长',
      minWidth: 140,
      formatter: (row) => h('span', { class: 'decimal-value' }, row.quantityStep)
    },
    {
      prop: 'minQuantity',
      label: '最小数量',
      minWidth: 140,
      formatter: (row) => h('span', { class: 'decimal-value' }, row.minQuantity)
    },
    {
      prop: 'updatedAt',
      label: '更新时间',
      minWidth: 180,
      formatter: (row) => h('span', { class: 'table-time' }, formatTime(row.updatedAt))
    }
  ])

  const loadSymbols = async () => {
    loading.value = true
    try {
      const result = await fetchMarketSymbols({
        ...requestParams(),
        market: filters.market,
        quoteAsset: filters.quoteAsset,
        status: filters.status,
        keyword: filters.keyword.trim()
      })
      symbols.value = result.records
      applyPage(result)
    } finally {
      loading.value = false
    }
  }

  const reloadSymbols = () => {
    reset()
    void loadSymbols()
  }

  const resetFilters = () => {
    Object.assign(filters, { keyword: '', market: '', quoteAsset: '', status: 'all' })
    reloadSymbols()
  }

  const handleSizeChange = (size: number) => {
    reset(size)
    void loadSymbols()
  }

  const handleCurrentChange = (current: number) => {
    if (moveTo(current)) void loadSymbols()
  }

  const openWorkflows = () => void router.push('/scheduler/definition')
  const openSymbol = (row: MarketSymbol) => {
    void router.push({
      path: '/plugins/official.binance/candles',
      query: { instrumentId: row.id, market: row.market, interval: '1h' }
    })
  }

  onMounted(() => void loadSymbols())
</script>

<style scoped lang="scss">
  .market-metadata-page {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 100%;
    color: var(--art-gray-900);
  }

  .table-title {
    display: flex;
    gap: 10px;
    align-items: baseline;
    margin-right: 4px;

    h1 {
      margin: 0;
      font-size: 18px;
      font-weight: 600;
      color: var(--el-text-color-primary);
    }

    span {
      font-size: 13px;
      color: var(--art-gray-500);
    }
  }

  .symbol-cell {
    display: flex;
    gap: 10px;
    align-items: center;

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
    color: var(--theme-color) !important;
    background: var(--el-color-primary-light-9);
    border-radius: 6px;
  }

  .decimal-value,
  .table-time {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 12px;
  }

  :deep(.el-table__row) {
    cursor: pointer;
  }
</style>
