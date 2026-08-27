<template>
  <div class="strategy-console">
    <header class="console-head"
      ><div><div class="eyebrow">交易管理</div><h1>策略管理</h1></div
      ><div class="head-actions"
        ><ElButton :icon="Refresh" :loading="loading" @click="loadData">刷新</ElButton
        ><ElButton type="primary" :icon="Plus" @click="newDraft">新建策略</ElButton></div
      ></header
    >
    <div class="strategy-layout">
      <ElCard shadow="never" class="strategy-list"
        ><template #header
          ><div class="card-head"
            ><strong>策略草稿</strong><ElTag effect="plain">{{ drafts.total }} 条</ElTag></div
          ></template
        ><ElTable :data="drafts.records" v-loading="loading" row-key="id" @row-click="selectDraft"
          ><ElTableColumn
            prop="name"
            label="名称"
            min-width="170"
            show-overflow-tooltip
          /><ElTableColumn prop="runtimeVersion" label="运行时" width="110" /><ElTableColumn
            prop="lookbackBars"
            label="回看 K 线"
            width="110"
          /><ElTableColumn label="状态" width="90"
            ><template #default
              ><ElTag type="warning" effect="plain">草稿</ElTag></template
            ></ElTableColumn
          ><ElTableColumn label="操作" width="130"
            ><template #default="{ row }"
              ><ElButton link type="primary" @click.stop="editDraft(row)">编辑</ElButton
              ><ElButton link type="success" @click.stop="publish(row)">发布</ElButton></template
            ></ElTableColumn
          ></ElTable
        ></ElCard
      >
      <ElCard shadow="never" class="editor-card"
        ><template #header
          ><div class="card-head"
            ><strong>{{ editingId ? '编辑策略草稿' : '新建策略' }}</strong
            ><ElTag :type="editingId ? 'warning' : 'info'" effect="plain">{{
              editingId ? '未发布' : '草稿'
            }}</ElTag></div
          ></template
        ><ElForm :model="form" label-position="top"
          ><ElFormItem label="策略名称" required
            ><ElInput v-model.trim="form.name" maxlength="120" /></ElFormItem
          ><div class="editor-meta"
            ><ElFormItem label="回看 K 线" required
              ><ElInputNumber
                v-model="form.lookbackBars"
                :min="1"
                :max="10000"
                controls-position="right" /></ElFormItem
            ><div class="runtime-note"
              ><span>运行时</span><strong>Python {{ selectedRuntime }}</strong
              ><small>发布版本不可修改</small></div
            ></div
          ><ElFormItem label="Python 源码" required
            ><div ref="editorHost" class="monaco-host"></div></ElFormItem
          ><ElFormItem label="参数 Schema（JSON）" required
            ><ElInput
              v-model="form.parameterSchemaText"
              type="textarea"
              :rows="7"
              spellcheck="false" /></ElFormItem></ElForm
        ><div class="editor-footer"
          ><span class="muted">{{
            editingUpdatedAt ? `最近保存 ${editingUpdatedAt}` : '未保存草稿'
          }}</span
          ><div
            ><ElButton @click="newDraft">清空</ElButton
            ><ElButton type="primary" :loading="saving" @click="saveDraft">保存草稿</ElButton></div
          ></div
        ></ElCard
      >
    </div>
    <ElCard shadow="never" class="secondary-card"
      ><template #header
        ><ElTabs v-model="activeTab"
          ><ElTabPane label="发布版本" name="versions" /><ElTabPane
            label="工作流绑定 / 实例"
            name="instances" /><ElTabPane label="信号" name="signals" /><ElTabPane
            label="回测"
            name="backtests" /></ElTabs></template
      ><ElTable v-if="activeTab === 'versions'" :data="published.records" size="small"
        ><ElTableColumn prop="name" label="策略" min-width="180" /><ElTableColumn
          label="版本"
          width="90"
          ><template #default="{ row }">v{{ row.versionNumber }}</template></ElTableColumn
        ><ElTableColumn prop="runtimeVersion" label="运行时" width="120" /><ElTableColumn
          label="发布时间"
          min-width="180"
          ><template #default="{ row }">{{
            formatDateTime(row.publishedAt)
          }}</template></ElTableColumn
        ><ElTableColumn label="操作" width="110"
          ><template #default="{ row }"
            ><ElButton link type="primary" @click="openBacktest(row)">回测</ElButton></template
          ></ElTableColumn
        ></ElTable
      ><ElTable v-else-if="activeTab === 'instances'" :data="instances" size="small"
        ><ElTableColumn prop="strategyName" label="策略" min-width="160" /><ElTableColumn
          prop="symbol"
          label="币种"
          width="130"
        /><ElTableColumn prop="interval" label="周期" width="90" /><ElTableColumn
          prop="environment"
          label="环境"
          width="100"
        /><ElTableColumn prop="workflowNodeId" label="工作流节点" min-width="150" /><ElTableColumn
          label="状态"
          width="90"
          ><template #default="{ row }"
            ><ElTag :type="row.isEnabled ? 'success' : 'info'" effect="plain">{{
              row.isEnabled ? '运行中' : '已停用'
            }}</ElTag></template
          ></ElTableColumn
        ></ElTable
      ><ElTable v-else-if="activeTab === 'signals'" :data="signals" size="small"
        ><ElTableColumn label="K 线时间" min-width="180"
          ><template #default="{ row }">{{
            formatDateTime(row.candleOpenTime)
          }}</template></ElTableColumn
        ><ElTableColumn prop="action" label="动作" width="90" /><ElTableColumn
          prop="target"
          label="目标仓位"
          width="110"
        /><ElTableColumn prop="status" label="状态" width="100" /><ElTableColumn
          label="生成时间"
          min-width="180"
          ><template #default="{ row }">{{
            formatDateTime(row.createdAt)
          }}</template></ElTableColumn
        ></ElTable
      ><ElTable v-else :data="backtests.records" size="small"
        ><ElTableColumn prop="symbol" label="币种" width="130" /><ElTableColumn
          prop="interval"
          label="周期"
          width="90"
        /><ElTableColumn prop="status" label="状态" width="110" /><ElTableColumn
          label="开始"
          min-width="170"
          ><template #default="{ row }">{{
            formatDateTime(row.startTime)
          }}</template></ElTableColumn
        ><ElTableColumn label="结束" min-width="170"
          ><template #default="{ row }">{{ formatDateTime(row.endTime) }}</template></ElTableColumn
        ></ElTable
      ></ElCard
    >
    <ElDialog v-model="backtestVisible" title="创建回测" width="520px"
      ><ElForm :model="backtestForm" label-position="top"
        ><ElFormItem label="币种"
          ><ElSelect v-model="backtestForm.instrumentId" filterable
            ><ElOption
              v-for="item in symbols"
              :key="item.id"
              :label="item.nativeSymbol"
              :value="item.id" /></ElSelect></ElFormItem
        ><ElFormItem label="周期"
          ><ElSelect v-model="backtestForm.interval"
            ><ElOption
              v-for="item in intervals"
              :key="item"
              :label="item"
              :value="item" /></ElSelect></ElFormItem
        ><div class="form-row"
          ><ElFormItem label="开始时间"
            ><ElInput
              v-model="backtestForm.startTime"
              placeholder="2025-01-01T00:00:00Z" /></ElFormItem
          ><ElFormItem label="结束时间"
            ><ElInput
              v-model="backtestForm.endTime"
              placeholder="2025-02-01T00:00:00Z" /></ElFormItem></div></ElForm
      ><template #footer
        ><ElButton @click="backtestVisible = false">取消</ElButton
        ><ElButton type="primary" @click="createBacktest">提交回测</ElButton></template
      ></ElDialog
    >
  </div>
</template>

<script setup lang="ts">
  import { Plus, Refresh } from '@element-plus/icons-vue'
  import * as monaco from 'monaco-editor/editor/editor.api.js'
  import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker'
  import 'monaco-editor/languages/definitions/python/register.js'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { fetchMarketSymbols, type MarketSymbol } from '@/api/market'
  import {
    fetchCreateStrategyBacktest,
    fetchCreateStrategyDraft,
    fetchPublishStrategy,
    fetchPublishedStrategies,
    fetchStrategyBacktests,
    fetchStrategyDrafts,
    fetchUpdateStrategyDraft,
    type StrategyBacktestItem,
    type StrategyDraftItem,
    type StrategyDraftPayload,
    type StrategyVersionItem
  } from '@/api/strategy'
  import {
    fetchStrategyInstances,
    fetchStrategySignals,
    type StrategyInstanceItem,
    type StrategySignalItem
  } from '@/api/signals'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'StrategyManagementPage' })
  globalThis.MonacoEnvironment = { getWorker: () => new EditorWorker() }
  const intervals = ['1m', '5m', '15m', '1h', '4h', '1d']
  const loading = ref(false)
  const saving = ref(false)
  const editingId = ref('')
  const editingUpdatedAt = ref('')
  const editorHost = ref<HTMLElement>()
  let editor: monaco.editor.IStandaloneCodeEditor | null = null
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
  const instances = ref<StrategyInstanceItem[]>([])
  const signals = ref<StrategySignalItem[]>([])
  const backtests = ref<Api.Common.PaginatedResponse<StrategyBacktestItem>>({
    records: [],
    nextCursor: '',
    hasMore: false,
    total: 0
  })
  const symbols = ref<MarketSymbol[]>([])
  const activeTab = ref('versions')
  const backtestVisible = ref(false)
  const selectedVersion = ref<StrategyVersionItem | null>(null)
  const form = reactive({
    name: '',
    sourceCode: 'def on_bar(candles, params):\n    return Decimal("0")\n',
    lookbackBars: 50,
    parameterSchemaText: '{}'
  })
  const backtestForm = reactive({ instrumentId: '', interval: '1h', startTime: '', endTime: '' })
  const selectedRuntime = computed(() => '3.12')
  const syncEditor = () => {
    if (editor && editor.getValue() !== form.sourceCode) editor.setValue(form.sourceCode)
  }
  const newDraft = () => {
    editingId.value = ''
    editingUpdatedAt.value = ''
    Object.assign(form, {
      name: '',
      sourceCode: 'def on_bar(candles, params):\n    return Decimal("0")\n',
      lookbackBars: 50,
      parameterSchemaText: '{}'
    })
    syncEditor()
  }
  const editDraft = (row: StrategyDraftItem) => {
    editingId.value = row.id
    editingUpdatedAt.value = row.updatedAt
    Object.assign(form, {
      name: row.name,
      sourceCode: row.sourceCode,
      lookbackBars: row.lookbackBars,
      parameterSchemaText: JSON.stringify(row.parameterSchema || {}, null, 2)
    })
    syncEditor()
  }
  const selectDraft = (row: StrategyDraftItem) => editDraft(row)
  const loadData = async () => {
    loading.value = true
    try {
      const [
        draftsResult,
        publishedResult,
        instanceResult,
        signalResult,
        backtestResult,
        symbolResult
      ] = await Promise.all([
        fetchStrategyDrafts({ limit: 100 }),
        fetchPublishedStrategies({ limit: 100 }),
        fetchStrategyInstances({ limit: 100 }),
        fetchStrategySignals({ limit: 100 }),
        fetchStrategyBacktests({ limit: 100 }),
        fetchMarketSymbols({ market: '', status: 'trading', limit: 200 })
      ])
      drafts.value = draftsResult
      published.value = publishedResult
      instances.value = instanceResult.records
      signals.value = signalResult.records
      backtests.value = backtestResult
      symbols.value = symbolResult.records
    } finally {
      loading.value = false
    }
  }
  const saveDraft = async () => {
    if (!form.name.trim() || !form.sourceCode.trim())
      return ElMessage.warning('请填写策略名称和源码')
    let schema: Record<string, unknown>
    try {
      const parsed = JSON.parse(form.parameterSchemaText)
      if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error()
      schema = parsed
    } catch {
      return ElMessage.error('参数 Schema 必须是 JSON 对象')
    }
    const payload: StrategyDraftPayload = {
      name: form.name.trim(),
      sourceCode: form.sourceCode,
      lookbackBars: form.lookbackBars,
      parameterSchema: schema
    }
    saving.value = true
    try {
      if (editingId.value) await fetchUpdateStrategyDraft(editingId.value, payload)
      else await fetchCreateStrategyDraft(payload)
      await loadData()
      newDraft()
    } finally {
      saving.value = false
    }
  }
  const publish = async (row: StrategyDraftItem) => {
    await ElMessageBox.confirm(`发布“${row.name}”后版本不可修改，确认继续？`, '发布策略', {
      type: 'warning'
    })
    await fetchPublishStrategy(row.id)
    await loadData()
  }
  const openBacktest = (row: StrategyVersionItem) => {
    selectedVersion.value = row
    backtestForm.instrumentId = symbols.value[0]?.id || ''
    backtestVisible.value = true
  }
  const createBacktest = async () => {
    if (
      !selectedVersion.value ||
      !backtestForm.instrumentId ||
      !backtestForm.startTime ||
      !backtestForm.endTime
    )
      return ElMessage.warning('请完整填写回测输入')
    await fetchCreateStrategyBacktest({
      strategyVersionId: selectedVersion.value.id,
      instrumentId: backtestForm.instrumentId,
      interval: backtestForm.interval,
      parameters: {},
      startTime: backtestForm.startTime,
      endTime: backtestForm.endTime,
      allocationUsdt: '1000',
      initialEquity: '1000',
      feeRate: '0.001',
      slippageRate: '0',
      fundingRates: [],
      stopLossRatio: '',
      maintenanceMarginRatio: ''
    })
    backtestVisible.value = false
    activeTab.value = 'backtests'
    await loadData()
  }
  onMounted(() => {
    editor = monaco.editor.create(editorHost.value!, {
      value: form.sourceCode,
      language: 'python',
      theme: 'vs',
      minimap: { enabled: false },
      automaticLayout: true,
      fontSize: 13,
      tabSize: 4
    })
    editor.onDidChangeModelContent(() => {
      form.sourceCode = editor?.getValue() || ''
    })
    void loadData()
  })
  onBeforeUnmount(() => {
    editor?.dispose()
    editor = null
  })
</script>

<style scoped lang="scss">
  .strategy-console {
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding-bottom: 24px;
  }

  .console-head,
  .head-actions,
  .card-head,
  .editor-footer,
  .editor-meta {
    display: flex;
    align-items: center;
  }

  .console-head,
  .card-head,
  .editor-footer {
    gap: 12px;
    justify-content: space-between;
  }

  .head-actions {
    gap: 8px;
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

  .strategy-layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(420px, 0.9fr);
    gap: 16px;
    align-items: start;
  }

  .editor-card {
    position: sticky;
    top: 16px;
  }

  .editor-meta {
    gap: 18px;
  }

  .runtime-note {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 10px 12px;
    background: var(--el-fill-color-light);
  }

  .runtime-note span,
  .runtime-note small,
  .muted {
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .monaco-host {
    height: 360px;
    overflow: hidden;
    border: 1px solid var(--el-border-color);
  }

  .editor-footer {
    padding-top: 8px;
    border-top: 1px solid var(--el-border-color-lighter);
  }

  .form-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  .form-row :deep(.el-form-item) {
    min-width: 0;
  }

  .secondary-card :deep(.el-tabs__header) {
    margin-bottom: 0;
  }

  @media (max-width: 900px) {
    .strategy-layout {
      grid-template-columns: 1fr;
    }

    .editor-card {
      position: static;
    }

    .console-head {
      flex-direction: column;
      align-items: flex-start;
    }
  }
</style>
