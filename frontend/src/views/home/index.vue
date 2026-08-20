<template>
  <div class="ops-console" v-loading="loading && !overview">
    <header class="console-head"
      ><div><div class="eyebrow">运行中心</div><h1>系统总览</h1></div
      ><div class="head-actions"
        ><span>{{ refreshedAt ? `更新于 ${refreshedAt}` : '等待数据' }}</span
        ><ElButton :icon="Refresh" :loading="loading" @click="loadOverview">刷新</ElButton></div
      ></header
    >
    <template v-if="overview">
      <section class="status-strip"
        ><div class="overall" :class="`is-${overallTone}`"
          ><span
            ><ArtSvgIcon
              :icon="
                overallTone === 'success' ? 'ri:checkbox-circle-line' : 'ri:error-warning-line'
              " /></span
          ><div
            ><small>系统状态</small><strong>{{ overallLabel }}</strong></div
          ></div
        ><div v-for="item in headlineMetrics" :key="item.label" class="headline-metric"
          ><span>{{ item.label }}</span
          ><strong>{{ item.value }}</strong
          ><small>{{ item.caption }}</small></div
        ></section
      >
      <section class="runtime-grid"
        ><article
          ><header><span>应用运行态</span><ElTag type="success" effect="plain">在线</ElTag></header
          ><dl
            ><div
              ><dt>运行时长</dt><dd>{{ formatDuration(overview.process.uptimeSeconds) }}</dd></div
            ><div
              ><dt>Go 内存</dt
              ><dd
                >{{ formatBytes(overview.process.goMemoryAllocBytes) }} /
                {{ formatBytes(overview.process.goMemorySysBytes) }}</dd
              ></div
            ><div
              ><dt>Goroutine</dt><dd>{{ overview.process.goroutines }}</dd></div
            ><div
              ><dt>并发请求</dt><dd>{{ overview.http.requestsInFlight }}</dd></div
            ></dl
          ></article
        ><article
          ><header
            ><span>PostgreSQL</span
            ><ElTag
              :type="overview.database.status === 'healthy' ? 'success' : 'danger'"
              effect="plain"
              >{{ overview.database.status === 'healthy' ? '正常' : '不可用' }}</ElTag
            ></header
          ><dl
            ><div
              ><dt>连接占用</dt
              ><dd
                >{{ overview.database.inUse }} /
                {{ overview.database.maxOpenConnections || '不限' }}</dd
              ></div
            ><div
              ><dt>打开连接</dt><dd>{{ overview.database.openConnections }}</dd></div
            ><div
              ><dt>空闲连接</dt><dd>{{ overview.database.idle }}</dd></div
            ><div
              ><dt>等待次数</dt><dd>{{ overview.database.waitCount }}</dd></div
            ></dl
          ></article
        ><article
          ><header
            ><span>工作流</span
            ><ElTag :type="overview.workflow.failedCount ? 'warning' : 'success'" effect="plain">{{
              overview.workflow.failedCount ? '有失败' : '正常'
            }}</ElTag></header
          ><dl
            ><div
              ><dt>激活定义</dt><dd>{{ overview.workflow.activeDefinitions }}</dd></div
            ><div
              ><dt>运行中</dt><dd>{{ overview.workflow.runningCount }}</dd></div
            ><div
              ><dt>成功记录</dt><dd>{{ overview.workflow.successCount }}</dd></div
            ><div
              ><dt>失败记录</dt><dd>{{ overview.workflow.failedCount }}</dd></div
            ></dl
          ></article
        ><article
          ><header
            ><span>交易与行情</span
            ><ElTag
              :type="overview.trading.emergencyStopped ? 'danger' : 'success'"
              effect="plain"
              >{{ overview.trading.emergencyStopped ? '急停' : '可用' }}</ElTag
            ></header
          ><dl
            ><div
              ><dt>交易账户</dt
              ><dd
                >{{ overview.trading.activeAccountCount }} / {{ overview.trading.accountCount }}</dd
              ></div
            ><div
              ><dt>暂停账户</dt><dd>{{ overview.trading.pausedAccountCount }}</dd></div
            ><div
              ><dt>USDT 币种</dt><dd>{{ overview.market.instrumentCount }}</dd></div
            ><div
              ><dt>行情同步</dt><dd>{{ marketStatusLabel }}</dd></div
            ></dl
          ></article
        ></section
      >
      <section class="main-grid"
        ><article class="trend-panel"
          ><header class="panel-head"
            ><div><strong>HTTP 近 60 分钟</strong><span>请求量、失败量与平均延迟</span></div
            ><div class="legend"
              ><span><i class="requests"></i>请求</span><span><i class="failed"></i>失败</span
              ><span><i class="latency"></i>延迟</span></div
            ></header
          ><div ref="chartHost" class="trend-chart"></div></article
        ><aside class="worker-panel"
          ><header class="panel-head"
            ><div><strong>Worker 与队列</strong><span>45 秒无心跳即离线</span></div></header
          ><div v-for="worker in overview.workers" :key="worker.lane" class="worker-row"
            ><div class="worker-state"
              ><i :class="worker.status === 'online' ? 'online' : 'offline'"></i
              ><div
                ><strong>{{ worker.lane === 'realtime' ? '实时策略' : '回测任务' }}</strong
                ><span
                  >{{ worker.status === 'online' ? '在线' : '离线' }} ·
                  {{ formatTime(worker.lastHeartbeatAt) }}</span
                ></div
              ></div
            ><div class="queue-facts"
              ><div
                ><span>排队</span><strong>{{ worker.queuedCount }}</strong></div
              ><div
                ><span>执行</span><strong>{{ worker.activeCount }}</strong></div
              ></div
            ></div
          ></aside
        ></section
      >
      <section class="alerts-panel"
        ><header class="panel-head"
          ><div
            ><strong>待处理告警</strong
            ><span>{{
              overview.alerts.length ? `${overview.alerts.length} 项需要检查` : '当前没有待处理项'
            }}</span></div
          ></header
        ><div v-if="overview.alerts.length" class="alert-list"
          ><button
            v-for="item in overview.alerts"
            :key="`${item.title}-${item.path}`"
            type="button"
            @click="item.path && router.push(item.path)"
            ><span class="alert-level" :class="`is-${item.severity}`"></span
            ><div
              ><strong
                >{{ item.title }}<em v-if="item.count">{{ item.count }}</em></strong
              ><small>{{ item.description }}</small></div
            ><ArtSvgIcon v-if="item.path" icon="ri:arrow-right-s-line" /></button></div
        ><div v-else class="empty-alert"
          ><ArtSvgIcon icon="ri:checkbox-circle-line" />运行状态正常</div
        ></section
      >
    </template>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
  import * as echarts from 'echarts'
  import { fetchHomeOverview, type HomeOverview } from '@/api/home'

  defineOptions({ name: 'HomePage' })
  const router = useRouter()
  const loading = ref(false)
  const overview = ref<HomeOverview | null>(null)
  const refreshedAt = ref('')
  const chartHost = ref<HTMLElement>()
  let chart: echarts.ECharts | null = null
  let timer: number | null = null
  let resizeObserver: ResizeObserver | null = null
  const overallTone = computed(() =>
    !overview.value ||
    overview.value.database.status !== 'healthy' ||
    overview.value.trading.emergencyStopped ||
    overview.value.workers.some((item) => item.status !== 'online')
      ? 'danger'
      : overview.value.alerts.length
        ? 'warning'
        : 'success'
  )
  const overallLabel = computed(
    () => ({ success: '运行正常', warning: '需要关注', danger: '存在中断' })[overallTone.value]
  )
  const headlineMetrics = computed(() =>
    overview.value
      ? [
          {
            label: 'HTTP 请求',
            value: overview.value.http.requestsTotal.toLocaleString(),
            caption: `失败 ${overview.value.http.requestsFailed}`
          },
          {
            label: '工作流运行',
            value: overview.value.workflow.runningCount,
            caption: `激活 ${overview.value.workflow.activeDefinitions}`
          },
          {
            label: '队列任务',
            value: overview.value.workers.reduce((sum, item) => sum + item.queuedCount, 0),
            caption: `执行中 ${overview.value.workers.reduce((sum, item) => sum + item.activeCount, 0)}`
          },
          {
            label: '交易账户',
            value: overview.value.trading.accountCount,
            caption: `暂停 ${overview.value.trading.pausedAccountCount}`
          }
        ]
      : []
  )
  const marketStatusLabel = computed(
    () =>
      ({
        success: '正常',
        failed: '失败',
        running: '同步中',
        queued: '等待同步',
        not_synced: '未同步'
      })[overview.value?.market.status || ''] || '未知'
  )
  const formatBytes = (value: number) =>
    value >= 1024 ** 3
      ? `${(value / 1024 ** 3).toFixed(1)} GB`
      : `${(value / 1024 ** 2).toFixed(1)} MB`
  const formatDuration = (seconds: number) => {
    const days = Math.floor(seconds / 86400)
    const hours = Math.floor((seconds % 86400) / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    return days
      ? `${days} 天 ${hours} 小时`
      : hours
        ? `${hours} 小时 ${minutes} 分`
        : `${minutes} 分钟`
  }
  const formatTime = (value: string) =>
    value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '无心跳'
  const renderChart = () => {
    if (!overview.value || !chartHost.value) return
    chart ||= echarts.init(chartHost.value)
    const trend = overview.value.http.trend
    chart.setOption(
      {
        animation: false,
        grid: { left: 44, right: 48, top: 22, bottom: 30 },
        tooltip: { trigger: 'axis' },
        xAxis: {
          type: 'category',
          data: trend.map((item) =>
            new Date(item.time).toLocaleTimeString('zh-CN', {
              hour: '2-digit',
              minute: '2-digit',
              hour12: false
            })
          ),
          axisLabel: { interval: 9, color: '#8492a6' },
          axisLine: { lineStyle: { color: '#dcdfe6' } }
        },
        yAxis: [
          {
            type: 'value',
            minInterval: 1,
            axisLabel: { color: '#8492a6' },
            splitLine: { lineStyle: { color: '#ebeef5' } }
          },
          {
            type: 'value',
            axisLabel: { formatter: '{value} ms', color: '#8492a6' },
            splitLine: { show: false }
          }
        ],
        series: [
          {
            name: '请求',
            type: 'bar',
            data: trend.map((item) => item.requests),
            itemStyle: { color: '#409eff' },
            barMaxWidth: 10
          },
          {
            name: '失败',
            type: 'bar',
            data: trend.map((item) => item.failed),
            itemStyle: { color: '#f56c6c' },
            barMaxWidth: 10
          },
          {
            name: '延迟',
            type: 'line',
            yAxisIndex: 1,
            data: trend.map((item) => Number(item.averageLatencyMs.toFixed(1))),
            symbol: 'none',
            smooth: true,
            lineStyle: { width: 2, color: '#e6a23c' }
          }
        ]
      },
      true
    )
  }
  const loadOverview = async () => {
    if (loading.value || document.hidden) return
    loading.value = true
    try {
      overview.value = await fetchHomeOverview()
      refreshedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
      await nextTick()
      renderChart()
    } finally {
      loading.value = false
    }
  }
  const schedule = () => {
    if (timer !== null) window.clearInterval(timer)
    timer = document.hidden ? null : window.setInterval(() => void loadOverview(), 15000)
  }
  const visibilityChanged = () => {
    schedule()
    if (!document.hidden) void loadOverview()
  }
  onMounted(() => {
    void loadOverview()
    schedule()
    document.addEventListener('visibilitychange', visibilityChanged)
    if (chartHost.value) {
      resizeObserver = new ResizeObserver(() => chart?.resize())
      resizeObserver.observe(chartHost.value)
    }
  })
  onBeforeUnmount(() => {
    if (timer !== null) window.clearInterval(timer)
    document.removeEventListener('visibilitychange', visibilityChanged)
    resizeObserver?.disconnect()
    chart?.dispose()
  })
</script>

<style scoped lang="scss">
  .ops-console {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding-bottom: 24px;
  }

  .console-head,
  .head-actions,
  .status-strip,
  .runtime-grid article header,
  .panel-head,
  .worker-row,
  .worker-state,
  .legend,
  .alert-list button {
    display: flex;
    align-items: center;
  }

  .console-head,
  .runtime-grid article header,
  .panel-head,
  .worker-row {
    gap: 14px;
    justify-content: space-between;
  }

  .head-actions,
  .legend {
    gap: 10px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
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

  .status-strip {
    min-height: 92px;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color-light);
  }

  .overall {
    display: flex;
    gap: 12px;
    align-items: center;
    align-self: stretch;
    min-width: 210px;
    padding: 16px 20px;
    border-right: 1px solid var(--el-border-color-light);
  }

  .overall > span {
    display: grid;
    place-items: center;
    width: 40px;
    height: 40px;
    font-size: 23px;
    color: white;
    border-radius: 8px;
  }

  .overall.is-success > span {
    background: var(--el-color-success);
  }

  .overall.is-warning > span {
    background: var(--el-color-warning);
  }

  .overall.is-danger > span {
    background: var(--el-color-danger);
  }

  .overall small,
  .headline-metric span,
  .headline-metric small {
    display: block;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .overall strong {
    display: block;
    margin-top: 3px;
    font-size: 18px;
  }

  .headline-metric {
    flex: 1;
    min-width: 0;
    padding: 12px 18px;
    border-right: 1px solid var(--el-border-color-lighter);
  }

  .headline-metric:last-child {
    border: 0;
  }

  .headline-metric strong {
    display: block;
    margin: 5px 0 2px;
    font-size: 23px;
  }

  .runtime-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 12px;
  }

  .runtime-grid article,
  .trend-panel,
  .worker-panel,
  .alerts-panel {
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color-light);
  }

  .runtime-grid article {
    padding: 14px 16px;
  }

  .runtime-grid article header > span,
  .panel-head strong {
    font-weight: 700;
  }

  dl {
    margin: 10px 0 0;
  }

  dl > div {
    display: flex;
    gap: 10px;
    justify-content: space-between;
    padding: 8px 0;
    border-top: 1px solid var(--el-border-color-lighter);
  }

  dt {
    color: var(--el-text-color-secondary);
  }

  dd {
    margin: 0;
    font-weight: 600;
    text-align: right;
  }

  .main-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 360px;
    gap: 12px;
  }

  .panel-head {
    min-height: 54px;
    padding: 0 16px;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .panel-head span {
    display: block;
    margin-top: 3px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .legend span {
    display: flex;
    gap: 5px;
    align-items: center;
    margin: 0;
  }

  .legend i {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }

  .legend .requests {
    background: #409eff;
  }

  .legend .failed {
    background: #f56c6c;
  }

  .legend .latency {
    background: #e6a23c;
  }

  .trend-chart {
    width: 100%;
    height: 310px;
  }

  .worker-row {
    padding: 18px 16px;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .worker-row:last-child {
    border-bottom: 0;
  }

  .worker-state {
    gap: 9px;
  }

  .worker-state i {
    width: 9px;
    height: 9px;
    border-radius: 50%;
  }

  .worker-state i.online {
    background: var(--el-color-success);
    box-shadow: 0 0 0 4px var(--el-color-success-light-9);
  }

  .worker-state i.offline {
    background: var(--el-color-danger);
    box-shadow: 0 0 0 4px var(--el-color-danger-light-9);
  }

  .worker-state span {
    display: block;
    margin-top: 4px;
    font-size: 11px;
    color: var(--el-text-color-secondary);
  }

  .queue-facts {
    display: grid;
    grid-template-columns: repeat(2, 54px);
    text-align: right;
  }

  .queue-facts span {
    font-size: 11px;
    color: var(--el-text-color-secondary);
  }

  .queue-facts strong {
    display: block;
    margin-top: 3px;
    font-size: 18px;
  }

  .alert-list button {
    gap: 12px;
    width: 100%;
    padding: 12px 16px;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .alert-list button:hover {
    background: var(--el-fill-color-light);
  }

  .alert-list button > div {
    flex: 1;
    min-width: 0;
  }

  .alert-list strong,
  .alert-list small {
    display: block;
  }

  .alert-list small {
    margin-top: 4px;
    color: var(--el-text-color-secondary);
  }

  .alert-list em {
    padding: 1px 6px;
    margin-left: 6px;
    font-size: 11px;
    font-style: normal;
    color: var(--el-color-danger);
    background: var(--el-color-danger-light-9);
    border-radius: 8px;
  }

  .alert-level {
    width: 4px;
    height: 34px;
    background: var(--el-color-warning);
    border-radius: 2px;
  }

  .alert-level.is-danger {
    background: var(--el-color-danger);
  }

  .empty-alert {
    padding: 24px 16px;
    color: var(--el-color-success);
    text-align: center;
  }

  .empty-alert svg {
    margin-right: 6px;
  }

  @media (max-width: 1100px) {
    .runtime-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .main-grid {
      grid-template-columns: 1fr;
    }

    .status-strip {
      flex-wrap: wrap;
    }

    .overall {
      width: 100%;
      border-right: 0;
      border-bottom: 1px solid var(--el-border-color-light);
    }
  }

  @media (max-width: 680px) {
    .console-head {
      flex-direction: column;
      align-items: flex-start;
    }

    .runtime-grid {
      grid-template-columns: 1fr;
    }

    .headline-metric {
      flex-basis: 50%;
    }

    .status-strip {
      align-items: stretch;
    }
  }
</style>
