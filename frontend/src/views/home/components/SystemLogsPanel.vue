<template>
  <div class="system-log-panel">
    <div class="log-console">
      <header class="console-header">
        <div class="title-block">
          <span class="title-mark"><ArtSvgIcon icon="ri:file-search-line" /></span>
          <div>
            <h1>系统日志</h1>
            <p>运行事件与 HTTP 访问记录</p>
          </div>
        </div>

        <div class="status-rail" aria-label="日志写入状态">
          <div class="status-item is-queue">
            <ArtSvgIcon icon="ri:stack-line" />
            <span>队列</span>
            <strong>{{ runtime?.queueDepth ?? 0 }}/{{ runtime?.queueCapacity ?? 5000 }}</strong>
          </div>
          <div class="status-item is-written">
            <ArtSvgIcon icon="ri:database-2-line" />
            <span>已写入</span>
            <strong>{{ runtime?.written ?? 0 }}</strong>
          </div>
          <div class="status-item is-dropped">
            <ArtSvgIcon icon="ri:skip-forward-line" />
            <span>已丢弃</span>
            <strong>{{ runtime?.dropped ?? 0 }}</strong>
          </div>
          <div class="status-item is-failed">
            <ArtSvgIcon icon="ri:error-warning-line" />
            <span>写入失败</span>
            <strong>{{ runtime?.failed ?? 0 }}</strong>
          </div>
        </div>
      </header>

      <section class="runtime-band">
        <div class="section-heading">
          <div>
            <span class="section-icon"><ArtSvgIcon icon="ri:settings-4-line" /></span>
            <strong>运行时配置</strong>
            <ElTag size="small" type="success" effect="plain">立即生效</ElTag>
          </div>
          <span v-if="runtime" class="updated-at"
            >更新于 {{ formatDateTime(runtime.updatedAt) }}</span
          >
        </div>

        <div class="runtime-controls">
          <label class="field-control">
            <span>日志级别</span>
            <ElSelect
              v-model="settingsForm.level"
              :disabled="!hasAuth('system.logs.configure')"
              aria-label="日志级别"
            >
              <ElOption v-for="option in levelOptions" :key="option.value" v-bind="option" />
            </ElSelect>
          </label>
          <label class="field-control">
            <span>保留天数</span>
            <ElInputNumber
              v-model="settingsForm.retentionDays"
              :min="1"
              :max="365"
              controls-position="right"
              :disabled="!hasAuth('system.logs.configure')"
              aria-label="日志保留天数"
            />
          </label>
          <div v-if="hasAuth('system.logs.configure')" class="runtime-actions">
            <ElTooltip content="重置为 info / 30 天" placement="top">
              <ElButton circle aria-label="重置日志默认配置" @click="resetSettings">
                <ArtSvgIcon icon="ri:restart-line" />
              </ElButton>
            </ElTooltip>
            <ElButton type="primary" :loading="runtimeLoading" @click="saveSettings">
              <ArtSvgIcon icon="ri:save-3-line" />
              保存并应用
            </ElButton>
          </div>
        </div>
      </section>

      <section class="filter-band">
        <div class="section-heading">
          <div>
            <span class="section-icon"><ArtSvgIcon icon="ri:filter-3-line" /></span>
            <strong>筛选条件</strong>
          </div>
          <span class="result-count">{{ pagination.total }} 条匹配记录</span>
        </div>

        <div class="filter-grid">
          <label class="field-control">
            <span>时间范围</span>
            <ElSelect v-model="filters.timeRange" @change="changeTimeRange">
              <ElOption v-for="option in timeRangeOptions" :key="option.value" v-bind="option" />
            </ElSelect>
          </label>
          <label class="field-control">
            <span>开始时间</span>
            <ElDatePicker
              v-model="filters.startTime"
              type="datetime"
              placeholder="选择开始时间"
              @change="markCustomRange"
            />
          </label>
          <label class="field-control">
            <span>结束时间</span>
            <ElDatePicker
              v-model="filters.endTime"
              type="datetime"
              placeholder="选择结束时间"
              @change="markCustomRange"
            />
          </label>
          <label class="field-control">
            <span>级别</span>
            <ElSelect v-model="filters.level" clearable placeholder="全部级别">
              <ElOption v-for="option in levelOptions" :key="option.value" v-bind="option" />
            </ElSelect>
          </label>
          <label class="field-control">
            <span>组件</span>
            <ElInput v-model="filters.component" clearable placeholder="例如 http.access" />
          </label>
          <label class="field-control">
            <span>request_id</span>
            <ElInput v-model="filters.requestId" clearable />
          </label>
          <label class="field-control">
            <span>用户 ID</span>
            <ElInputNumber v-model="filters.userId" :min="1" :controls="false" />
          </label>
          <label class="field-control">
            <span>HTTP 方法</span>
            <ElSelect v-model="filters.method" clearable placeholder="全部方法">
              <ElOption
                v-for="method in methodOptions"
                :key="method"
                :label="method"
                :value="method"
              />
            </ElSelect>
          </label>
          <label class="field-control">
            <span>路由</span>
            <ElInput v-model="filters.route" clearable placeholder="例如 /api/v1/workflows" />
          </label>
          <label class="field-control">
            <span>状态码</span>
            <ElInputNumber v-model="filters.statusCode" :min="100" :max="599" :controls="false" />
          </label>
          <label class="field-control keyword-field">
            <span>关键词</span>
            <ElInput v-model="filters.keyword" clearable placeholder="消息或 request_id">
              <template #prefix><ArtSvgIcon icon="ri:search-line" /></template>
            </ElInput>
          </label>
        </div>

        <div class="filter-actions">
          <ElButton type="primary" :loading="loading" @click="searchLogs">
            <ArtSvgIcon icon="ri:search-line" />
            搜索
          </ElButton>
          <ElTooltip content="重置筛选" placement="top">
            <ElButton circle aria-label="重置筛选" @click="resetFilters">
              <ArtSvgIcon icon="ri:restart-line" />
            </ElButton>
          </ElTooltip>
          <ElTooltip content="刷新日志和写入状态" placement="top">
            <ElButton circle aria-label="刷新日志和写入状态" :loading="loading" @click="refreshAll">
              <ArtSvgIcon icon="ri:refresh-line" />
            </ElButton>
          </ElTooltip>
        </div>
      </section>

      <section class="table-band">
        <ArtTable
          :loading="loading"
          :data="data"
          :columns="columns"
          :pagination="pagination"
          :show-table-header="false"
          height="min(52vh, 620px)"
          empty-text="当前筛选条件下暂无日志"
          @row-click="openDetail"
          @pagination:size-change="handleSizeChange"
          @pagination:current-change="handleCurrentChange"
        >
          <template #level="{ row }">
            <span class="level-cell" :class="`is-${row.level}`">
              <ArtSvgIcon :icon="levelIcon(row.level)" />
              {{ row.level }}
            </span>
          </template>
          <template #component="{ row }">
            <span class="component-cell"
              ><ArtSvgIcon icon="ri:terminal-box-line" />{{ row.component }}</span
            >
          </template>
          <template #message="{ row }">
            <div class="message-cell">
              <strong>{{ row.message }}</strong>
              <span v-if="row.route">{{ row.method }} {{ row.route }}</span>
            </div>
          </template>
          <template #requestId="{ row }">
            <code v-if="row.requestId">{{ row.requestId }}</code>
            <span v-else>--</span>
          </template>
          <template #http="{ row }">
            <span v-if="row.statusCode" class="http-cell">
              <b :class="httpStatusClass(row.statusCode)">{{ row.statusCode }}</b>
              <span>{{ row.durationMs ?? 0 }} ms</span>
            </span>
            <span v-else>--</span>
          </template>
        </ArtTable>
      </section>
    </div>

    <ElDrawer v-model="detailVisible" size="min(680px, 94vw)" :with-header="false">
      <div v-if="selectedLog" class="detail-drawer">
        <div class="drawer-head">
          <span class="title-mark"><ArtSvgIcon icon="ri:file-info-line" /></span>
          <div>
            <p>日志详情</p>
            <strong>{{ selectedLog.component }}</strong>
          </div>
          <ElButton circle text aria-label="关闭详情" @click="detailVisible = false">
            <ArtSvgIcon icon="ri:close-line" />
          </ElButton>
        </div>
        <div class="detail-message">
          <span class="level-cell" :class="`is-${selectedLog.level}`">
            <ArtSvgIcon :icon="levelIcon(selectedLog.level)" />
            {{ selectedLog.level }}
          </span>
          <p>{{ selectedLog.message }}</p>
        </div>
        <ElDescriptions :column="1" border>
          <ElDescriptionsItem label="时间">{{
            formatDateTime(selectedLog.loggedAt)
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="request_id">{{
            selectedLog.requestId || '--'
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="用户 ID">{{ selectedLog.userId || '--' }}</ElDescriptionsItem>
          <ElDescriptionsItem label="HTTP 请求">
            {{ selectedLog.route ? `${selectedLog.method} ${selectedLog.route}` : '--' }}
          </ElDescriptionsItem>
          <ElDescriptionsItem label="状态 / 耗时">
            {{ selectedLog.statusCode || '--' }} / {{ selectedLog.durationMs ?? '--' }} ms
          </ElDescriptionsItem>
        </ElDescriptions>
        <div class="details-json">
          <span><ArtSvgIcon icon="ri:braces-line" /> 结构化字段</span>
          <pre>{{ formattedDetails }}</pre>
        </div>
      </div>
    </ElDrawer>
  </div>
</template>

<script setup lang="ts">
  import { ElMessage } from 'element-plus'
  import { useAuth } from '@/hooks/core/useAuth'
  import { useTable } from '@/hooks/core/useTable'
  import {
    fetchGetSystemLogRuntime,
    fetchGetSystemLogs,
    fetchUpdateSystemLogRuntime
  } from '@/api/system'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'SystemLogsPanel' })

  type LogLevel = Api.System.SystemLogLevel
  type LogItem = Api.System.SystemLogItem
  type TimeRange = '1h' | '6h' | '24h' | '7d' | 'custom'

  interface LogFilters {
    timeRange: TimeRange
    startTime: Date | null
    endTime: Date | null
    level?: LogLevel
    component: string
    requestId: string
    userId?: number
    method: string
    route: string
    statusCode?: number
    keyword: string
  }

  const { hasAuth } = useAuth()
  const runtime = ref<Api.System.SystemLogRuntimeStatus>()
  const runtimeLoading = ref(false)
  const detailVisible = ref(false)
  const selectedLog = ref<LogItem>()
  const settingsForm = reactive<Api.System.SystemLogSettingsPayload>({
    level: 'info',
    retentionDays: 30
  })

  const levelOptions = [
    { label: 'debug', value: 'debug' as const },
    { label: 'info', value: 'info' as const },
    { label: 'warn', value: 'warn' as const },
    { label: 'error', value: 'error' as const }
  ]
  const timeRangeOptions = [
    { label: '最近 1 小时', value: '1h' as const },
    { label: '最近 6 小时', value: '6h' as const },
    { label: '最近 24 小时', value: '24h' as const },
    { label: '最近 7 天', value: '7d' as const },
    { label: '自定义', value: 'custom' as const }
  ]
  const methodOptions = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']
  const levelMeta: Record<LogLevel, { icon: string }> = {
    debug: { icon: 'ri:bug-line' },
    info: { icon: 'ri:information-line' },
    warn: { icon: 'ri:alarm-warning-line' },
    error: { icon: 'ri:close-circle-line' }
  }

  const rangeMilliseconds: Record<Exclude<TimeRange, 'custom'>, number> = {
    '1h': 60 * 60 * 1000,
    '6h': 6 * 60 * 60 * 1000,
    '24h': 24 * 60 * 60 * 1000,
    '7d': 7 * 24 * 60 * 60 * 1000
  }

  const createFilters = (): LogFilters => {
    const now = new Date()
    return {
      timeRange: '1h',
      startTime: new Date(now.getTime() - rangeMilliseconds['1h']),
      endTime: null,
      level: undefined,
      component: '',
      requestId: '',
      userId: undefined,
      method: '',
      route: '',
      statusCode: undefined,
      keyword: ''
    }
  }

  const filters = reactive<LogFilters>(createFilters())

  const buildQuery = (): Api.System.SystemLogSearchParams => ({
    startTime: filters.startTime?.toISOString(),
    endTime: filters.timeRange === 'custom' ? filters.endTime?.toISOString() : undefined,
    level: filters.level,
    component: filters.component || undefined,
    requestId: filters.requestId || undefined,
    userId: filters.userId,
    method: filters.method || undefined,
    route: filters.route || undefined,
    statusCode: filters.statusCode,
    keyword: filters.keyword || undefined
  })

  const appliedQuery = ref<Api.System.SystemLogSearchParams>(buildQuery())

  const {
    columns,
    data,
    loading,
    pagination,
    getData,
    replaceSearchParams,
    handleSizeChange,
    handleCurrentChange,
    refreshData
  } = useTable({
    core: {
      apiFn: fetchGetSystemLogs,
      apiParams: { limit: 20, ...appliedQuery.value },
      columnsFactory: () => [
        {
          prop: 'loggedAt',
          label: '时间',
          width: 180,
          formatter: (row: LogItem) => formatDateTime(row.loggedAt)
        },
        { prop: 'level', label: '级别', width: 110, useSlot: true },
        { prop: 'component', label: '组件', minWidth: 150, useSlot: true },
        { prop: 'message', label: '日志详情', minWidth: 360, useSlot: true },
        { prop: 'requestId', label: 'request_id', minWidth: 190, useSlot: true },
        {
          prop: 'userId',
          label: '用户',
          width: 90,
          formatter: (row: LogItem) => row.userId || '--'
        },
        { prop: 'http', label: '状态 / 耗时', width: 130, useSlot: true }
      ]
    }
  })

  const formattedDetails = computed(() => JSON.stringify(selectedLog.value?.details || {}, null, 2))
  const levelIcon = (level: unknown) => levelMeta[level as LogLevel]?.icon || 'ri:information-line'

  const loadRuntime = async () => {
    runtimeLoading.value = true
    try {
      runtime.value = await fetchGetSystemLogRuntime()
      settingsForm.level = runtime.value.level
      settingsForm.retentionDays = runtime.value.retentionDays
    } finally {
      runtimeLoading.value = false
    }
  }

  const changeTimeRange = (range: TimeRange) => {
    if (range === 'custom') return
    const now = new Date()
    filters.endTime = null
    filters.startTime = new Date(now.getTime() - rangeMilliseconds[range])
  }

  const markCustomRange = () => {
    filters.timeRange = 'custom'
  }

  const searchLogs = () => {
    if (filters.timeRange !== 'custom') changeTimeRange(filters.timeRange)
    if (filters.startTime && filters.endTime && filters.startTime > filters.endTime) {
      ElMessage.warning('开始时间不能晚于结束时间')
      return
    }
    appliedQuery.value = buildQuery()
    replaceSearchParams({ limit: pagination.size, ...appliedQuery.value })
    getData()
  }

  const resetFilters = () => {
    Object.assign(filters, createFilters())
    searchLogs()
  }

  const resetSettings = () => {
    settingsForm.level = 'info'
    settingsForm.retentionDays = 30
  }

  const saveSettings = async () => {
    runtimeLoading.value = true
    try {
      runtime.value = await fetchUpdateSystemLogRuntime({ ...settingsForm })
    } finally {
      runtimeLoading.value = false
    }
  }

  const refreshAll = async () => {
    if (filters.timeRange !== 'custom') changeTimeRange(filters.timeRange)
    appliedQuery.value = buildQuery()
    replaceSearchParams({ limit: pagination.size, ...appliedQuery.value })
    await Promise.all([refreshData(), loadRuntime()])
  }

  defineExpose({ refresh: refreshAll })

  const openDetail = (row: LogItem) => {
    selectedLog.value = row
    detailVisible.value = true
  }

  const httpStatusClass = (status: number) => {
    if (status >= 500) return 'is-error'
    if (status >= 400) return 'is-warn'
    if (status >= 300) return 'is-info'
    return 'is-success'
  }

  onMounted(() => void loadRuntime())
</script>

<style scoped lang="scss">
  .system-log-panel {
    min-width: 0;
    color: var(--art-gray-900);
  }

  .log-console {
    overflow: hidden;
    background: var(--default-box-color);
    border: 1px solid var(--art-gray-300);
    border-radius: 8px;
  }

  .console-header,
  .section-heading,
  .runtime-controls,
  .filter-actions,
  .drawer-head,
  .title-block,
  .status-item,
  .component-cell,
  .level-cell,
  .http-cell {
    display: flex;
    align-items: center;
  }

  .console-header {
    gap: 24px;
    justify-content: space-between;
    min-height: 92px;
    padding: 18px 22px;
    border-bottom: 1px solid var(--art-gray-300);
  }

  .title-block {
    gap: 12px;
    min-width: 220px;
  }

  .title-block h1 {
    margin: 0;
    font-size: 20px;
    line-height: 1.35;
  }

  .title-block p,
  .drawer-head p {
    margin: 3px 0 0;
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .title-mark,
  .section-icon {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    color: #fff;
    background: #2878ff;
  }

  .title-mark {
    width: 42px;
    height: 42px;
    font-size: 20px;
    border-radius: 8px;
    box-shadow: 0 6px 16px rgb(40 120 255 / 0.2);
  }

  .status-rail {
    display: grid;
    grid-template-columns: repeat(4, minmax(120px, auto));
    border: 1px solid var(--art-gray-300);
    border-radius: 8px;
  }

  .status-item {
    gap: 7px;
    min-height: 44px;
    padding: 0 14px;
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .status-item + .status-item {
    border-left: 1px solid var(--art-gray-300);
  }

  .status-item strong {
    margin-left: auto;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 13px;
    color: var(--art-gray-900);
  }

  .status-item.is-queue > :deep(.art-svg-icon) {
    color: #2878ff;
  }

  .status-item.is-written > :deep(.art-svg-icon) {
    color: #16a86b;
  }

  .status-item.is-dropped > :deep(.art-svg-icon) {
    color: #d68b00;
  }

  .status-item.is-failed > :deep(.art-svg-icon) {
    color: #e5484d;
  }

  .runtime-band,
  .filter-band {
    padding: 18px 22px;
    border-bottom: 1px solid var(--art-gray-300);
  }

  .runtime-band {
    background: color-mix(in srgb, var(--art-gray-200) 42%, var(--default-box-color));
  }

  .section-heading {
    gap: 16px;
    justify-content: space-between;
    margin-bottom: 14px;
  }

  .section-heading > div {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .section-icon {
    width: 26px;
    height: 26px;
    font-size: 14px;
    background: #147d78;
    border-radius: 6px;
  }

  .updated-at,
  .result-count {
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .runtime-controls {
    gap: 16px;
  }

  .runtime-controls .field-control {
    width: min(240px, 100%);
  }

  .runtime-actions {
    display: flex;
    gap: 10px;
    align-self: flex-end;
    margin-left: auto;
  }

  .field-control {
    display: flex;
    flex-direction: column;
    gap: 7px;
    min-width: 0;
    font-size: 12px;
    color: var(--art-gray-700);
  }

  .field-control > :deep(.el-select),
  .field-control > :deep(.el-date-editor),
  .field-control > :deep(.el-input-number) {
    width: 100%;
  }

  .filter-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 14px;
  }

  .keyword-field {
    grid-column: span 2;
  }

  .filter-actions {
    gap: 10px;
    margin-top: 16px;
  }

  .filter-actions > :last-child {
    margin-left: auto;
  }

  .filter-actions :deep(.art-svg-icon),
  .runtime-actions :deep(.art-svg-icon) {
    margin-right: 5px;
  }

  .filter-actions .is-circle :deep(.art-svg-icon) {
    margin-right: 0;
  }

  .table-band {
    min-height: 420px;
    padding: 10px 14px 16px;
  }

  .table-band :deep(.el-table__row) {
    cursor: pointer;
  }

  .table-band :deep(.el-table__row:hover td) {
    background: color-mix(in srgb, #2878ff 6%, var(--default-box-color)) !important;
  }

  .level-cell,
  .component-cell,
  .http-cell {
    gap: 6px;
  }

  .level-cell {
    width: fit-content;
    min-width: 74px;
    padding: 4px 8px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    border-radius: 6px;
  }

  .level-cell.is-debug {
    color: #7057c7;
    background: rgb(112 87 199 / 0.12);
  }

  .level-cell.is-info {
    color: #2878ff;
    background: rgb(40 120 255 / 0.12);
  }

  .level-cell.is-warn {
    color: #b87500;
    background: rgb(214 139 0 / 0.14);
  }

  .level-cell.is-error {
    color: #d63d43;
    background: rgb(229 72 77 / 0.13);
  }

  .component-cell {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 12px;
    color: var(--art-gray-800);
  }

  .component-cell > :deep(.art-svg-icon) {
    color: #147d78;
  }

  .message-cell {
    min-width: 0;
  }

  .message-cell strong,
  .message-cell span {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .message-cell strong {
    font-size: 13px;
    font-weight: 600;
    color: var(--art-gray-900);
  }

  .message-cell span {
    margin-top: 3px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    color: var(--art-gray-600);
  }

  code {
    display: block;
    overflow: hidden;
    font-size: 11px;
    color: var(--art-gray-700);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .http-cell b {
    padding: 2px 5px;
    font-size: 11px;
    border-radius: 4px;
  }

  .http-cell b.is-success {
    color: #15865a;
    background: rgb(22 168 107 / 0.12);
  }

  .http-cell b.is-info {
    color: #2878ff;
    background: rgb(40 120 255 / 0.12);
  }

  .http-cell b.is-warn {
    color: #b87500;
    background: rgb(214 139 0 / 0.14);
  }

  .http-cell b.is-error {
    color: #d63d43;
    background: rgb(229 72 77 / 0.13);
  }

  .http-cell span {
    font-size: 11px;
    color: var(--art-gray-600);
  }

  .detail-drawer {
    color: var(--art-gray-900);
  }

  .drawer-head {
    gap: 12px;
    padding-bottom: 18px;
    border-bottom: 1px solid var(--art-gray-300);
  }

  .drawer-head > div {
    flex: 1;
  }

  .drawer-head strong {
    display: block;
    margin-top: 3px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  }

  .detail-message {
    padding: 20px 0;
  }

  .detail-message p {
    margin: 12px 0 0;
    line-height: 1.7;
  }

  .details-json {
    margin-top: 20px;
  }

  .details-json > span {
    display: flex;
    gap: 7px;
    align-items: center;
    font-size: 12px;
    font-weight: 700;
  }

  .details-json pre {
    min-height: 120px;
    padding: 14px;
    margin: 10px 0 0;
    overflow: auto;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 12px;
    line-height: 1.6;
    color: var(--art-gray-800);
    background: var(--art-gray-200);
    border: 1px solid var(--art-gray-300);
    border-radius: 6px;
  }

  @media (max-width: 1200px) {
    .console-header {
      flex-direction: column;
      align-items: flex-start;
    }

    .status-rail {
      width: 100%;
    }

    .filter-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }

  @media (max-width: 768px) {
    .console-header,
    .runtime-band,
    .filter-band {
      padding: 16px;
    }

    .status-rail {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .status-item:nth-child(3) {
      border-left: 0;
    }

    .status-item:nth-child(n + 3) {
      border-top: 1px solid var(--art-gray-300);
    }

    .runtime-controls,
    .runtime-actions,
    .filter-actions {
      flex-direction: column;
      align-items: stretch;
    }

    .runtime-controls .field-control {
      width: 100%;
    }

    .runtime-actions,
    .filter-actions > :last-child {
      margin-left: 0;
    }

    .filter-grid {
      grid-template-columns: 1fr;
    }

    .keyword-field {
      grid-column: auto;
    }
  }
</style>
