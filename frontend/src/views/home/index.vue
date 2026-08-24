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
        ></section
      >
      <section
        ><article class="trend-panel"
          ><header class="panel-head"
            ><div><strong>HTTP 近 60 分钟</strong><span>请求量、失败量与平均延迟</span></div
            ><div class="legend"
              ><span><i class="requests"></i>请求</span><span><i class="failed"></i>失败</span
              ><span><i class="latency"></i>延迟</span></div
            ></header
          ><div ref="chartHost" class="trend-chart"></div></article
      ></section>
    </template>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
  import * as echarts from 'echarts'
  import { fetchHomeOverview, type HomeOverview } from '@/api/home'

  defineOptions({ name: 'HomePage' })
  const loading = ref(false)
  const overview = ref<HomeOverview | null>(null)
  const refreshedAt = ref('')
  const chartHost = ref<HTMLElement>()
  let chart: echarts.ECharts | null = null
  let timer: number | null = null
  let resizeObserver: ResizeObserver | null = null
  const overallTone = computed(() =>
    !overview.value || overview.value.database.status !== 'healthy' ? 'danger' : 'success'
  )
  const overallLabel = computed(
    () => ({ success: '运行正常', danger: '存在中断' })[overallTone.value]
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
            label: 'Migration',
            value: `v${overview.value.database.schemaVersion}`,
            caption: '数据库结构版本'
          },
          {
            label: '数据库连接',
            value: overview.value.database.openConnections,
            caption: `使用中 ${overview.value.database.inUse}`
          },
          {
            label: 'Goroutine',
            value: overview.value.process.goroutines,
            caption: `请求中 ${overview.value.http.requestsInFlight}`
          }
        ]
      : []
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
  .legend {
    display: flex;
    align-items: center;
  }

  .console-head,
  .runtime-grid article header,
  .panel-head {
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
    letter-spacing: 0;
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
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }

  .runtime-grid article,
  .trend-panel {
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

  @media (max-width: 1100px) {
    .runtime-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
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
