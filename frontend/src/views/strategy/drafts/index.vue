<!-- 策略中心：管理单文件策略草稿并发布不可变版本。 -->
<template>
  <div class="strategy-page">
    <section class="strategy-hero">
      <div>
        <div class="eyebrow">策略研发工作台</div>
        <h1>创建并发布策略</h1>
      </div>
      <div class="hero-actions">
        <ElButton :icon="Refresh" :loading="loading" @click="loadPageData">刷新</ElButton>
        <ElButton type="primary" :icon="Plus" @click="resetForm">新建策略</ElButton>
      </div>
    </section>

    <section class="summary-grid" aria-label="策略统计">
      <article class="summary-item">
        <span>草稿</span>
        <strong>{{ drafts.total }}</strong>
      </article>
      <article class="summary-item">
        <span>已发布版本</span>
        <strong>{{ published.total }}</strong>
      </article>
      <article class="summary-item summary-item--accent">
        <span>当前运行时</span>
        <strong>python3.12</strong>
      </article>
    </section>

    <div class="workspace-grid">
      <ElCard class="draft-card" shadow="never">
        <template #header>
          <div class="card-heading">
            <div>
              <h2>策略草稿</h2>
            </div>
            <ElTag effect="plain">{{ drafts.total }} 条</ElTag>
          </div>
        </template>

        <ElTable
          :data="drafts.records"
          :loading="loading"
          stripe
          height="520"
          empty-text="暂无策略草稿"
        >
          <ElTableColumn prop="name" label="策略名称" min-width="150" show-overflow-tooltip />
          <ElTableColumn label="行情" width="126">
            <template #default="{ row }">
              <div class="market-cell">
                <ElTag size="small" effect="plain">{{ marketLabel(row.market) }}</ElTag>
                <span>{{ symbolLabel(row.instrumentId) }}</span>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn prop="interval" label="周期" width="78" />
          <ElTableColumn prop="lookbackBars" label="回看" width="78" />
          <ElTableColumn label="操作" width="132" fixed="right">
            <template #default="{ row }">
              <div class="table-actions">
                <ElButton link type="primary" :icon="Edit" @click="editDraft(row)">编辑</ElButton>
                <ElButton link type="success" :icon="Promotion" @click="publishDraft(row)"
                  >发布</ElButton
                >
              </div>
            </template>
          </ElTableColumn>
        </ElTable>
      </ElCard>

      <ElCard class="form-card" shadow="never">
        <template #header>
          <div class="card-heading">
            <div>
              <h2>{{ editingId ? '编辑策略草稿' : '新建策略' }}</h2>
            </div>
            <ElTag :type="editingId ? 'warning' : 'info'" effect="plain">
              {{ editingId ? '编辑中' : '草稿' }}
            </ElTag>
          </div>
        </template>

        <ElForm ref="formRef" :model="form" :rules="rules" label-position="top">
          <ElFormItem label="策略名称" prop="name">
            <ElInput
              v-model.trim="form.name"
              maxlength="120"
              show-word-limit
              placeholder="例如：BTC 趋势跟随"
            />
          </ElFormItem>

          <div class="form-row">
            <ElFormItem label="市场" prop="market">
              <ElSelect v-model="form.market" @change="handleMarketChange">
                <ElOption label="现货 Spot" value="spot" />
                <ElOption label="U 本位合约 USDⓈ-M" value="usd_m" />
              </ElSelect>
            </ElFormItem>
            <ElFormItem label="周期" prop="interval">
              <ElSelect v-model="form.interval">
                <ElOption v-for="item in intervals" :key="item" :label="item" :value="item" />
              </ElSelect>
            </ElFormItem>
            <ElFormItem label="回看 K 线" prop="lookbackBars">
              <ElInputNumber
                v-model="form.lookbackBars"
                :min="1"
                :max="10000"
                controls-position="right"
              />
            </ElFormItem>
          </div>

          <ElFormItem label="交易标的" prop="instrumentId">
            <ElSelect
              v-model="form.instrumentId"
              filterable
              :loading="symbolsLoading"
              placeholder="先同步行情元数据，再选择标的"
            >
              <ElOption
                v-for="item in marketSymbols"
                :key="item.id"
                :label="`${item.nativeSymbol} · ${item.quoteAsset}`"
                :value="item.id"
              />
            </ElSelect>
          </ElFormItem>

          <ElFormItem label="参数 schema" prop="parameterSchemaText">
            <ElInput
              v-model="form.parameterSchemaText"
              type="textarea"
              :rows="6"
              spellcheck="false"
              placeholder='例如：{"lookback":{"type":"integer","required":true,"minimum":1}}'
            />
          </ElFormItem>

          <ElFormItem label="策略源码" prop="sourceCode">
            <ElInput
              v-model="form.sourceCode"
              type="textarea"
              :rows="13"
              spellcheck="false"
              class="code-input"
              placeholder="定义 on_bar(candles, params)，返回目标仓位 Decimal。"
            />
          </ElFormItem>
        </ElForm>

        <div class="form-footer">
          <span class="form-status">{{
            editingId ? `最后更新：${editingUpdatedAt || '--'}` : '尚未保存'
          }}</span>
          <div>
            <ElButton :icon="Delete" @click="resetForm">清空</ElButton>
            <ElButton type="primary" :icon="Check" :loading="saving" @click="saveDraft"
              >保存草稿</ElButton
            >
          </div>
        </div>
      </ElCard>
    </div>

    <ElCard class="published-card" shadow="never">
      <template #header>
        <div class="card-heading">
          <div>
            <h2>已发布版本</h2>
          </div>
        </div>
      </template>
      <ElTable :data="published.records" :loading="loading" stripe empty-text="暂无发布版本">
        <ElTableColumn prop="name" label="策略名称" min-width="180" />
        <ElTableColumn label="版本" width="100">
          <template #default="{ row }">v{{ row.versionNumber }}</template>
        </ElTableColumn>
        <ElTableColumn prop="symbol" label="标的" min-width="140" />
        <ElTableColumn prop="interval" label="周期" width="90" />
        <ElTableColumn prop="publishedAt" label="发布时间" min-width="180" />
        <ElTableColumn label="状态" width="100">
          <template #default="{ row }">
            <ElTag type="success" effect="plain">{{
              row.status === 'published' ? '已发布' : row.status
            }}</ElTag>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { Check, Delete, Edit, Plus, Promotion, Refresh } from '@element-plus/icons-vue'
  import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
  import { fetchMarketSymbols, type MarketSymbol } from '@/api/market'
  import {
    fetchCreateStrategyDraft,
    fetchPublishStrategy,
    fetchPublishedStrategies,
    fetchStrategyDrafts,
    fetchUpdateStrategyDraft,
    parseStrategyParameterSchema,
    type StrategyDraftItem,
    type StrategyDraftPayload,
    type StrategyVersionItem
  } from '@/api/strategy'

  defineOptions({ name: 'StrategyDraftsPage' })

  const intervals = ['1m', '5m', '15m', '1h', '4h', '1d']
  const loading = ref(false)
  const saving = ref(false)
  const symbolsLoading = ref(false)
  const editingId = ref('')
  const editingUpdatedAt = ref('')
  const formRef = ref<FormInstance>()
  const marketSymbols = ref<MarketSymbol[]>([])
  const drafts = ref<Api.Common.PaginatedResponse<StrategyDraftItem>>({
    records: [],
    nextCursor: '',
    hasMore: false,
    total: 0
  })
  const published = ref<Api.Common.PaginatedResponse<StrategyVersionItem>>({
    records: [],
    nextCursor: '',
    hasMore: false,
    total: 0
  })

  const form = reactive({
    name: '',
    sourceCode: `def on_bar(candles, params):
    return Decimal("0")`,
    market: 'spot' as 'spot' | 'usd_m',
    instrumentId: '',
    interval: '1h',
    lookbackBars: 50,
    parameterSchemaText: '{}'
  })

  const rules = reactive<FormRules>({
    name: [{ required: true, message: '请输入策略名称', trigger: 'blur' }],
    market: [{ required: true, message: '请选择市场', trigger: 'change' }],
    instrumentId: [{ required: true, message: '请选择交易标的', trigger: 'change' }],
    interval: [{ required: true, message: '请选择周期', trigger: 'change' }],
    lookbackBars: [{ required: true, message: '请输入回看 K 线数量', trigger: 'change' }],
    parameterSchemaText: [
      { required: true, message: '请输入参数 schema，空 schema 请填写 {}', trigger: 'blur' }
    ],
    sourceCode: [{ required: true, message: '请输入策略源码', trigger: 'blur' }]
  })

  const marketLabel = (market: string) => (market === 'usd_m' ? 'USDⓈ-M' : '现货')
  const symbolLabel = (instrumentId: string) =>
    marketSymbols.value.find((item) => item.id === instrumentId)?.nativeSymbol ||
    instrumentId.slice(0, 8)

  const loadSymbols = async () => {
    symbolsLoading.value = true
    try {
      const result = await fetchMarketSymbols({
        market: form.market,
        status: 'trading',
        limit: 200
      })
      marketSymbols.value = result.records
      if (!marketSymbols.value.some((item) => item.id === form.instrumentId)) form.instrumentId = ''
    } finally {
      symbolsLoading.value = false
    }
  }

  const loadPageData = async () => {
    loading.value = true
    try {
      const [draftResult, publishedResult] = await Promise.all([
        fetchStrategyDrafts({ limit: 100 }),
        fetchPublishedStrategies({ limit: 100 })
      ])
      drafts.value = draftResult
      published.value = publishedResult
      await loadSymbols()
    } finally {
      loading.value = false
    }
  }

  const handleMarketChange = () => {
    form.instrumentId = ''
    void loadSymbols()
  }

  const resetForm = () => {
    editingId.value = ''
    editingUpdatedAt.value = ''
    Object.assign(form, {
      name: '',
      sourceCode: `def on_bar(candles, params):
    return Decimal("0")`,
      market: 'spot',
      instrumentId: '',
      interval: '1h',
      lookbackBars: 50,
      parameterSchemaText: '{}'
    })
    void loadSymbols()
    nextTick(() => formRef.value?.clearValidate())
  }

  const editDraft = (draft: StrategyDraftItem) => {
    editingId.value = draft.id
    editingUpdatedAt.value = draft.updatedAt
    Object.assign(form, {
      name: draft.name,
      sourceCode: draft.sourceCode,
      market: draft.market,
      instrumentId: draft.instrumentId,
      interval: draft.interval,
      lookbackBars: draft.lookbackBars,
      parameterSchemaText: JSON.stringify(draft.parameterSchema || {}, null, 2)
    })
    void loadSymbols()
    nextTick(() => formRef.value?.clearValidate())
  }

  const parseSchema = () => {
    const parsed = parseStrategyParameterSchema(form.parameterSchemaText)
    if (!parsed) ElMessage.error('参数 schema 必须是 JSON 对象')
    return parsed
  }

  const saveDraft = async () => {
    if (!formRef.value || saving.value) return
    await formRef.value.validate()
    const parameterSchema = parseSchema()
    if (!parameterSchema) return
    const payload: StrategyDraftPayload = {
      name: form.name.trim(),
      sourceCode: form.sourceCode,
      market: form.market,
      instrumentId: form.instrumentId,
      interval: form.interval,
      lookbackBars: form.lookbackBars,
      parameterSchema
    }
    saving.value = true
    try {
      if (editingId.value) {
        await fetchUpdateStrategyDraft(editingId.value, payload)
      } else {
        await fetchCreateStrategyDraft(payload)
      }
      await loadPageData()
      resetForm()
    } finally {
      saving.value = false
    }
  }

  const publishDraft = async (draft: StrategyDraftItem) => {
    await ElMessageBox.confirm(`发布“${draft.name}”后会生成不可变版本，确认继续吗？`, '发布策略', {
      type: 'warning',
      confirmButtonText: '确认发布',
      cancelButtonText: '取消'
    })
    await fetchPublishStrategy(draft.id)
    await loadPageData()
  }

  onMounted(() => {
    void loadPageData()
  })
</script>

<style scoped lang="scss">
  .strategy-page {
    display: flex;
    flex-direction: column;
    gap: 18px;
    padding-bottom: 24px;
  }

  .strategy-hero {
    display: flex;
    gap: 24px;
    align-items: flex-start;
    justify-content: space-between;
    padding: 26px 28px;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color-light);
    border-left: 4px solid var(--el-color-primary);
  }

  .eyebrow {
    font-size: 12px;
    font-weight: 600;
    color: var(--el-color-primary);
    letter-spacing: 0.08em;
  }

  h1,
  h2,
  p {
    margin: 0;
  }

  h1 {
    margin-top: 8px;
    font-size: 28px;
    line-height: 1.25;
  }

  .hero-actions,
  .table-actions,
  .form-footer,
  .card-heading {
    display: flex;
    align-items: center;
  }

  .hero-actions {
    flex-shrink: 0;
    gap: 10px;
  }

  .summary-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 14px;
  }

  .summary-item {
    padding: 16px 18px;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color-light);
  }

  .summary-item span {
    display: block;
    color: var(--el-text-color-secondary);
  }

  .summary-item strong {
    display: block;
    margin: 8px 0 4px;
    font-size: 24px;
    color: var(--el-text-color-primary);
  }

  .summary-item--accent strong {
    font-size: 20px;
    color: var(--el-color-primary);
  }

  .workspace-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(420px, 0.86fr);
    gap: 18px;
    align-items: start;
  }

  .form-card {
    position: sticky;
    top: 16px;
  }

  .card-heading {
    gap: 12px;
    justify-content: space-between;
  }

  .card-heading h2 {
    font-size: 17px;
  }

  .market-cell {
    display: flex;
    flex-direction: column;
    gap: 4px;
    align-items: flex-start;
  }

  .market-cell span {
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .table-actions {
    gap: 2px;
  }

  .form-row {
    display: grid;
    grid-template-columns: 1fr 1fr 1.1fr;
    gap: 12px;
  }

  .form-row :deep(.el-form-item) {
    min-width: 0;
  }

  .code-input :deep(textarea),
  .form-card :deep(textarea) {
    font-family: SFMono-Regular, Consolas, 'Liberation Mono', monospace;
    line-height: 1.55;
  }

  .form-footer {
    gap: 12px;
    justify-content: space-between;
    padding-top: 4px;
  }

  .form-status {
    min-width: 0;
    overflow: hidden;
    font-size: 12px;
    color: var(--el-text-color-secondary);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  @media (max-width: 1200px) {
    .workspace-grid {
      grid-template-columns: 1fr;
    }

    .form-card {
      position: static;
    }
  }

  @media (max-width: 680px) {
    .strategy-hero,
    .form-footer {
      flex-direction: column;
      align-items: stretch;
    }

    .hero-actions {
      justify-content: flex-end;
    }

    .summary-grid,
    .form-row {
      grid-template-columns: 1fr;
    }

    h1 {
      font-size: 24px;
    }
  }
</style>
