<template>
  <div class="ops-console" :class="{ 'is-light': !isDark }" v-loading="loading && !overview">
    <div class="dashboard-shell">
      <header class="console-head">
        <div class="brand-block">
          <div class="page-context"><ArtSvgIcon icon="ri:radar-line" /> 运维监控</div>
          <h1>系统运维总览</h1>
          <div class="status-meta">
            <span class="live-state"><i></i> 在线</span>
            <span>{{ refreshedAt ? `刷新：${refreshedAt}` : '等待数据' }}</span>
          </div>
        </div>
        <div class="head-actions">
          <label class="range-control">
            <span>时间范围</span>
            <select v-model.number="selectedRange" aria-label="时间范围">
              <option :value="15">近 15 分钟</option>
              <option :value="30">近 30 分钟</option>
              <option :value="60">近 60 分钟</option>
            </select>
            <ArtSvgIcon icon="ri:arrow-down-s-line" />
          </label>
        </div>
      </header>

      <template v-if="overview">
        <section class="overview-grid" aria-label="系统状态与核心指标">
          <article class="health-card">
            <div class="health-card__header">
              <div class="panel-title"><ArtSvgIcon icon="ri:dashboard-3-line" /> 实时信息</div>
              <span class="info-mark" aria-label="当前服务健康状态">i</span>
            </div>
            <div class="health-card__body">
              <div class="health-ring" :class="`is-${overallTone}`">
                <div>
                  <strong>{{ healthPercent }}%</strong>
                  <span>{{ overallLabel }}</span>
                </div>
              </div>
              <div class="health-copy">
                <div class="health-tabs" aria-label="监控窗口">
                  <button
                    v-for="range in [1, 5, 30, 60]"
                    :key="range"
                    type="button"
                    :class="{ active: selectedRange === range }"
                    @click="selectedRange = range"
                  >
                    {{ range }}min
                  </button>
                </div>
                <span class="eyebrow">当前</span>
                <div class="health-numbers">
                  <strong>{{ currentQps }}</strong
                  ><small>QPS</small>
                  <strong>{{ overview.http.requestsInFlight.toLocaleString() }}</strong
                  ><small>并发</small>
                </div>
                <div class="health-foot">
                  <span><b>峰值</b>{{ peakQps }} QPS</span>
                  <span><b>平均</b>{{ averageQps }} QPS</span>
                </div>
              </div>
            </div>
            <div class="health-sparkline" aria-hidden="true"><i></i></div>
            <p class="health-description">{{ overallDescription }}</p>
          </article>

          <div class="metric-grid">
            <article v-for="item in headlineMetrics" :key="item.label" class="metric-card">
              <div class="metric-card__head">
                <span class="metric-icon" :class="`is-${item.tone}`"
                  ><ArtSvgIcon :icon="item.icon"
                /></span>
                <span>{{ item.label }}</span>
                <span class="info-mark" aria-hidden="true">i</span>
              </div>
              <strong class="metric-value" :class="`is-${item.tone}`">{{ item.value }}</strong>
              <div v-if="item.meter !== null" class="metric-meter" aria-hidden="true">
                <span :style="{ width: `${item.meter}%` }"></span>
              </div>
              <div class="metric-caption">{{ item.caption }}</div>
            </article>
          </div>
        </section>

        <section class="resource-strip" aria-label="运行资源">
          <article v-for="item in resourceRows" :key="item.label" class="resource-card">
            <div class="resource-card__head">
              <span class="resource-icon" :class="`is-${item.tone}`"
                ><ArtSvgIcon :icon="item.icon"
              /></span>
              <span>{{ item.label }}</span>
              <span class="info-mark" aria-hidden="true">i</span>
            </div>
            <strong :class="`is-${item.tone}`">{{ item.value }}</strong>
            <small>{{ item.caption }}</small>
            <div v-if="item.meter !== null" class="resource-meter" aria-hidden="true">
              <span :style="{ width: `${item.meter}%` }"></span>
            </div>
          </article>
        </section>

        <section class="content-grid">
          <article class="panel trend-panel">
            <header class="panel-head">
              <div>
                <div class="panel-title"><ArtSvgIcon icon="ri:line-chart-line" /> 请求趋势</div>
                <p>HTTP 请求、失败数与平均延迟</p>
              </div>
              <div class="legend" aria-label="图表图例">
                <span><i class="requests"></i>请求</span>
                <span><i class="failed"></i>失败</span>
                <span><i class="latency"></i>延迟</span>
              </div>
            </header>
            <div ref="chartHost" class="trend-chart"></div>
          </article>

          <article class="panel service-panel">
            <header class="panel-head">
              <div>
                <div class="panel-title"><ArtSvgIcon icon="ri:stack-line" /> 运行节点</div>
                <p>关键依赖与应用实例</p>
              </div>
              <span class="panel-count">4 项</span>
            </header>
            <ul class="service-list">
              <li>
                <span class="service-icon is-blue"><ArtSvgIcon icon="ri:server-line" /></span>
                <div><strong>HTTP 网关</strong><small>请求处理器</small></div>
                <span class="service-value"><i class="status-dot is-green"></i>运行中</span>
              </li>
              <li>
                <span class="service-icon is-cyan"><ArtSvgIcon icon="ri:database-2-line" /></span>
                <div
                  ><strong>PostgreSQL</strong
                  ><small>连接池 · 等待 {{ overview.database.waitCount }}</small></div
                >
                <span class="service-value"
                  ><i class="status-dot" :class="`is-${overallTone}`"></i
                  >{{ overview.database.status === 'healthy' ? '健康' : '不可用' }}</span
                >
              </li>
              <li>
                <span class="service-icon is-purple"><ArtSvgIcon icon="ri:pulse-line" /></span>
                <div><strong>观测指标</strong><small>请求采样窗口</small></div>
                <span class="service-value"><i class="status-dot is-green"></i>实时</span>
              </li>
              <li>
                <span class="service-icon is-red"><ArtSvgIcon icon="ri:git-branch-line" /></span>
                <div><strong>数据库结构</strong><small>当前迁移版本</small></div>
                <span class="service-value service-value--mono"
                  >v{{ overview.database.schemaVersion }}</span
                >
              </li>
            </ul>
          </article>
        </section>

        <section class="bottom-grid">
          <article class="panel quick-panel">
            <header class="panel-head">
              <div>
                <div class="panel-title"><ArtSvgIcon icon="ri:apps-2-line" /> 快速入口</div>
                <p>进入常用运维工作区</p>
              </div>
            </header>
            <div class="quick-grid">
              <button
                v-for="item in quickActions"
                :key="item.label"
                type="button"
                @click="openAction(item.path)"
              >
                <span class="quick-icon" :class="`is-${item.tone}`"
                  ><ArtSvgIcon :icon="item.icon"
                /></span>
                <span
                  ><strong>{{ item.label }}</strong
                  ><small>{{ item.caption }}</small></span
                >
                <ArtSvgIcon icon="ri:arrow-right-s-line" class="quick-arrow" />
              </button>
            </div>
          </article>

          <article class="panel activity-panel">
            <header class="panel-head">
              <div>
                <div class="panel-title"><ArtSvgIcon icon="ri:history-line" /> 请求窗口</div>
                <p>最近有流量的时间段</p>
              </div>
              <span class="panel-count">{{ recentWindows.length }} 条</span>
            </header>
            <div v-if="recentWindows.length" class="activity-list">
              <div v-for="item in recentWindows" :key="item.time" class="activity-row">
                <span class="activity-marker" :class="item.failed ? 'is-red' : 'is-green'"></span>
                <div
                  ><strong>{{ formatDateTime(item.time).slice(11, 16) }}</strong
                  ><span>请求 {{ item.requests }} · 失败 {{ item.failed }}</span></div
                >
                <time>{{ item.averageLatencyMs.toFixed(1) }}ms</time>
              </div>
            </div>
            <div v-else class="activity-empty"
              ><ArtSvgIcon icon="ri:moon-clear-line" /><span>暂无请求记录</span></div
            >
          </article>
        </section>

        <section v-if="hasAuth('system.logs.view')" class="home-log-section">
          <SystemLogsPanel ref="logsPanel" />
        </section>
      </template>

      <div v-else-if="!loading" class="empty-state">
        <ArtSvgIcon icon="ri:cloud-off-line" />
        <strong>暂时无法读取运行状态</strong>
        <span>请检查服务连接后重试。</span>
        <ElButton type="primary" :icon="Refresh" @click="loadOverview">重新读取</ElButton>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
  import * as echarts from 'echarts'
  import { useSettingStore } from '@/store/modules/setting'
  import { useAuth } from '@/hooks/core/useAuth'
  import { fetchHomeOverview, type HomeOverview } from '@/api/home'
  import { formatDateTime } from '@/utils/date'
  import SystemLogsPanel from './components/SystemLogsPanel.vue'

  defineOptions({ name: 'HomePage' })

  type Tone = 'blue' | 'green' | 'red' | 'purple' | 'cyan'

  const router = useRouter()
  const { hasAuth } = useAuth()
  const settingStore = useSettingStore()
  const { isDark } = storeToRefs(settingStore)
  const loading = ref(false)
  const overview = ref<HomeOverview | null>(null)
  const refreshedAt = ref('')
  const selectedRange = ref(60)
  const chartHost = ref<HTMLElement>()
  const logsPanel = ref<{ refresh: () => Promise<void> }>()
  let chart: echarts.ECharts | null = null
  let timer: number | null = null
  let logRefreshTimer: number | null = null
  let resizeObserver: ResizeObserver | null = null

  const overallTone = computed<Tone>(() =>
    !overview.value || overview.value.database.status !== 'healthy' ? 'red' : 'green'
  )
  const overallLabel = computed(() => (overallTone.value === 'green' ? '健康' : '异常'))
  const overallDescription = computed(() =>
    overallTone.value === 'green'
      ? '核心服务均可访问，系统正在稳定处理请求。'
      : '数据库连接异常，请优先检查依赖服务。'
  )
  const failureRateValue = computed(() => {
    const http = overview.value?.http
    if (!http || http.requestsTotal === 0) return 0
    return (http.requestsFailed / http.requestsTotal) * 100
  })
  const failureRate = computed(() => `${failureRateValue.value.toFixed(2)}%`)
  const successRate = computed(() => `${Math.max(0, 100 - failureRateValue.value).toFixed(3)}%`)
  const visibleTrend = computed(() =>
    (overview.value?.http.trend || []).slice(-selectedRange.value)
  )
  const currentQps = computed(() => {
    const last = visibleTrend.value.at(-1)
    return last?.requests.toFixed(1) || '0.0'
  })
  const peakQps = computed(() => {
    const peak = Math.max(...visibleTrend.value.map((item) => item.requests), 0)
    return peak.toFixed(1)
  })
  const averageQps = computed(() => {
    if (!visibleTrend.value.length) return '0.0'
    return (
      visibleTrend.value.reduce((sum, item) => sum + item.requests, 0) / visibleTrend.value.length
    ).toFixed(1)
  })
  const averageLatency = computed(() => {
    const trend = visibleTrend.value.filter((item) => item.requests > 0)
    const requests = trend.reduce((sum, item) => sum + item.requests, 0)
    if (!requests) return '--'
    const total = trend.reduce((sum, item) => sum + item.averageLatencyMs * item.requests, 0)
    return `${(total / requests).toFixed(1)} ms`
  })
  const headlineMetrics = computed(() => {
    if (!overview.value) return []
    return [
      {
        label: '请求数',
        value: overview.value.http.requestsTotal.toLocaleString(),
        caption: `近 ${selectedRange.value} 分钟累计`,
        icon: 'ri:arrow-left-right-line',
        tone: 'blue' as Tone,
        meter: null
      },
      {
        label: 'SLA 成功率',
        value: successRate.value,
        caption: `异常数 ${overview.value.http.requestsFailed}`,
        icon: 'ri:shield-check-line',
        tone: 'green' as Tone,
        meter: Math.max(0, 100 - failureRateValue.value)
      },
      {
        label: '请求错误',
        value: failureRate.value,
        caption: `失败请求 ${overview.value.http.requestsFailed}`,
        icon: 'ri:error-warning-line',
        tone: 'green' as Tone,
        meter: null
      },
      {
        label: '平均延迟',
        value: averageLatency.value,
        caption: '按请求数加权',
        icon: 'ri:timer-flash-line',
        tone: 'purple' as Tone,
        meter: null
      },
      {
        label: '并发请求',
        value: overview.value.http.requestsInFlight.toLocaleString(),
        caption: '当前正在处理',
        icon: 'ri:loader-4-line',
        tone: 'red' as Tone,
        meter: null
      },
      {
        label: '数据库连接',
        value: `${overview.value.database.inUse} / ${overview.value.database.maxOpenConnections || '不限'}`,
        caption: `打开 ${overview.value.database.openConnections} · 空闲 ${overview.value.database.idle}`,
        icon: 'ri:database-line',
        tone: 'cyan' as Tone,
        meter: overview.value.database.maxOpenConnections
          ? Math.min(
              100,
              (overview.value.database.inUse / overview.value.database.maxOpenConnections) * 100
            )
          : null
      }
    ]
  })
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
  const healthPercent = computed(() => (overallTone.value === 'green' ? 100 : 0))
  const memoryPercent = computed(() => {
    const process = overview.value?.process
    if (!process?.goMemorySysBytes) return 0
    return Math.min(100, Math.round((process.goMemoryAllocBytes / process.goMemorySysBytes) * 100))
  })
  const resourceRows = computed(() => {
    if (!overview.value) return []
    return [
      {
        label: '进程内存',
        value: `${memoryPercent.value}%`,
        caption: `${formatBytes(overview.value.process.goMemoryAllocBytes)} / ${formatBytes(overview.value.process.goMemorySysBytes)}`,
        meter: memoryPercent.value,
        icon: 'ri:memory-line',
        tone: 'blue' as Tone
      },
      {
        label: '数据库',
        value: overview.value.database.status === 'healthy' ? '正常' : '异常',
        caption: `连接 ${overview.value.database.openConnections} · 等待 ${overview.value.database.waitCount}`,
        meter: overview.value.database.status === 'healthy' ? 100 : 0,
        icon: 'ri:database-2-line',
        tone: overallTone.value
      },
      {
        label: '连接池',
        value: `${overview.value.database.inUse}`,
        caption: `活跃 / 空闲 ${overview.value.database.idle}`,
        meter: overview.value.database.maxOpenConnections
          ? Math.min(
              100,
              (overview.value.database.inUse / overview.value.database.maxOpenConnections) * 100
            )
          : null,
        icon: 'ri:links-line',
        tone: 'cyan' as Tone
      },
      {
        label: 'Goroutine',
        value: overview.value.process.goroutines.toLocaleString(),
        caption: '当前运行中的协程',
        meter: null,
        icon: 'ri:git-pull-request-line',
        tone: 'purple' as Tone
      },
      {
        label: '运行时长',
        value: formatDuration(overview.value.process.uptimeSeconds),
        caption: '进程启动以来',
        meter: null,
        icon: 'ri:time-line',
        tone: 'green' as Tone
      }
    ]
  })
  const quickActions = [
    {
      label: '工作流调度',
      caption: '执行队列',
      icon: 'ri:node-tree',
      tone: 'blue',
      path: '/scheduler/definition'
    },
    {
      label: '插件能力',
      caption: '节点与页面',
      icon: 'ri:puzzle-2-line',
      tone: 'cyan',
      path: '/config/plugins'
    },
    {
      label: '用户管理',
      caption: '账号与权限',
      icon: 'ri:team-line',
      tone: 'purple',
      path: '/system/user'
    },
    {
      label: '系统菜单',
      caption: '导航配置',
      icon: 'ri:menu-4-line',
      tone: 'red',
      path: '/system/menu'
    }
  ]
  const recentWindows = computed(() =>
    visibleTrend.value
      .filter((item) => item.requests > 0 || item.failed > 0)
      .slice(-5)
      .reverse()
  )
  const openAction = (path: string) => void router.push(path)
  const chartTheme = computed(() =>
    isDark.value
      ? {
          axis: '#8495af',
          grid: '#26364d',
          line: '#31425a',
          tooltip: '#111d31',
          tooltipBorder: '#344862',
          text: '#eaf2ff',
          area: 'rgba(37, 198, 154, 0.12)'
        }
      : {
          axis: '#718096',
          grid: '#dfe6ef',
          line: '#cbd5e1',
          tooltip: '#ffffff',
          tooltipBorder: '#d7e0ea',
          text: '#1e293b',
          area: 'rgba(37, 170, 137, 0.12)'
        }
  )

  const observeChart = () => {
    if (resizeObserver || !chartHost.value) return
    resizeObserver = new ResizeObserver(() => chart?.resize())
    resizeObserver.observe(chartHost.value)
  }
  const renderChart = () => {
    if (!overview.value || !chartHost.value) return
    chart ||= echarts.init(chartHost.value)
    const trend = visibleTrend.value
    chart.setOption(
      {
        animation: false,
        backgroundColor: 'transparent',
        grid: { left: 42, right: 42, top: 24, bottom: 32 },
        tooltip: {
          trigger: 'axis',
          backgroundColor: chartTheme.value.tooltip,
          borderColor: chartTheme.value.tooltipBorder,
          textStyle: { color: chartTheme.value.text }
        },
        xAxis: {
          type: 'category',
          data: trend.map((item) => formatDateTime(item.time).slice(11, 16)),
          axisLabel: {
            interval: Math.max(0, Math.ceil(trend.length / 6) - 1),
            color: chartTheme.value.axis
          },
          axisLine: { lineStyle: { color: chartTheme.value.line } }
        },
        yAxis: [
          {
            type: 'value',
            minInterval: 1,
            axisLabel: { color: chartTheme.value.axis },
            splitLine: { lineStyle: { color: chartTheme.value.grid } }
          },
          {
            type: 'value',
            axisLabel: { formatter: '{value} ms', color: chartTheme.value.axis },
            splitLine: { show: false }
          }
        ],
        series: [
          {
            name: '请求',
            type: 'bar',
            data: trend.map((item) => item.requests),
            itemStyle: { color: '#4388ff', borderRadius: [4, 4, 0, 0] },
            barMaxWidth: 12
          },
          {
            name: '失败',
            type: 'bar',
            data: trend.map((item) => item.failed),
            itemStyle: { color: '#ff6570', borderRadius: [4, 4, 0, 0] },
            barMaxWidth: 12
          },
          {
            name: '延迟',
            type: 'line',
            yAxisIndex: 1,
            data: trend.map((item) => Number(item.averageLatencyMs.toFixed(1))),
            symbol: 'none',
            smooth: true,
            lineStyle: { width: 2, color: '#25c69a' },
            areaStyle: { color: chartTheme.value.area }
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
      refreshedAt.value = formatDateTime(new Date())
      await nextTick()
      renderChart()
      observeChart()
      if (logsPanel.value) {
        if (logRefreshTimer !== null) window.clearTimeout(logRefreshTimer)
        logRefreshTimer = window.setTimeout(() => void logsPanel.value?.refresh(), 400)
      }
    } finally {
      loading.value = false
    }
  }
  const schedule = () => {
    if (timer !== null) window.clearInterval(timer)
    if (logRefreshTimer !== null) window.clearTimeout(logRefreshTimer)
    timer = document.hidden ? null : window.setInterval(() => void loadOverview(), 15000)
  }
  const visibilityChanged = () => {
    schedule()
    if (!document.hidden) void loadOverview()
  }
  watch([selectedRange, isDark], async () => {
    await nextTick()
    renderChart()
  })
  onMounted(() => {
    void loadOverview()
    schedule()
    document.addEventListener('visibilitychange', visibilityChanged)
  })
  onBeforeUnmount(() => {
    if (timer !== null) window.clearInterval(timer)
    if (logRefreshTimer !== null) window.clearTimeout(logRefreshTimer)
    document.removeEventListener('visibilitychange', visibilityChanged)
    resizeObserver?.disconnect()
    chart?.dispose()
  })
</script>

<style scoped lang="scss">
  .ops-console {
    --ops-bg: #061526;
    --ops-shell: #18253a;
    --ops-card: #0d182d;
    --ops-card-soft: #111f35;
    --ops-line: #30435d;
    --ops-ink: #edf4ff;
    --ops-muted: #8392a9;
    --ops-blue: #4388ff;
    --ops-green: #35da8a;
    --ops-red: #ff6570;
    --ops-purple: #ae7cff;
    --ops-cyan: #27c7c0;

    min-width: 0;
    min-height: calc(100vh - 32px);
    padding: 18px;
    color: var(--ops-ink);
    background: var(--ops-bg);
    border-radius: 18px;
  }

  .home-log-section {
    margin-top: 16px;
  }

  .dashboard-shell {
    max-width: 1600px;
    padding: 22px 24px 28px;
    margin: 0 auto;
    background: var(--ops-shell);
    border: 1px solid #344760;
    border-radius: 22px;
    box-shadow: 0 18px 40px rgb(0 0 0 / 0.16);
  }

  .console-head,
  .head-actions,
  .status-meta,
  .panel-head,
  .panel-title,
  .legend,
  .metric-card__head,
  .resource-card__head,
  .service-list li,
  .activity-row {
    display: flex;
    align-items: center;
  }

  .console-head,
  .panel-head {
    justify-content: space-between;
  }

  .console-head {
    gap: 20px;
    padding-bottom: 18px;
    border-bottom: 1px solid var(--ops-line);
  }

  .page-context,
  .panel-title {
    display: flex;
    gap: 8px;
    align-items: center;
    font-weight: 700;
  }

  .page-context {
    font-size: 12px;
    color: var(--ops-blue);
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }

  h1 {
    margin: 5px 0 4px;
    font-size: clamp(22px, 2vw, 30px);
    line-height: 1.2;
  }

  .status-meta {
    gap: 12px;
    font-size: 11px;
    color: var(--ops-muted);
  }

  .live-state {
    display: inline-flex;
    gap: 6px;
    align-items: center;
    color: var(--ops-green);
  }

  .live-state i,
  .status-dot,
  .activity-marker {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }

  .live-state i {
    background: var(--ops-green);
    box-shadow: 0 0 0 4px rgb(53 218 138 / 0.13);
  }

  .head-actions {
    gap: 10px;
  }

  .range-control {
    height: 42px;
    color: var(--ops-ink);
    background: #233249;
    border: 1px solid #40536e;
    border-radius: 10px;
  }

  .range-control {
    position: relative;
    display: flex;
    gap: 8px;
    align-items: center;
    min-width: 148px;
    padding: 0 12px;
    font-size: 12px;
  }

  .range-control span {
    color: var(--ops-muted);
  }

  .range-control select {
    flex: 1;
    min-width: 0;
    color: var(--ops-ink);
    appearance: none;
    cursor: pointer;
    background: transparent;
    border: 0;
    outline: none;
  }

  .range-control > :deep(.art-svg-icon) {
    color: var(--ops-muted);
    pointer-events: none;
  }

  .overview-grid {
    display: grid;
    grid-template-columns: minmax(350px, 1.05fr) minmax(0, 2fr);
    gap: 16px;
    padding-top: 20px;
  }

  .health-card,
  .metric-card,
  .resource-card,
  .panel,
  .empty-state {
    background: var(--ops-card);
    border: 1px solid #1c2d46;
    border-radius: 16px;
  }

  .health-card {
    position: relative;
    min-height: 304px;
    padding: 20px;
    overflow: hidden;
  }

  .health-card__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    color: var(--ops-muted);
  }

  .health-card__header .panel-title {
    color: var(--ops-ink);
  }

  .info-mark {
    display: inline-grid;
    place-items: center;
    width: 16px;
    height: 16px;
    font-size: 10px;
    font-weight: 700;
    color: var(--ops-muted);
    border: 1px solid #52627a;
    border-radius: 50%;
  }

  .health-card__body {
    display: grid;
    grid-template-columns: 150px minmax(0, 1fr);
    gap: 20px;
    align-items: center;
    margin-top: 26px;
  }

  .health-ring {
    display: grid;
    place-items: center;
    width: 132px;
    aspect-ratio: 1;
    border: 10px solid #41516a;
    border-radius: 50%;
  }

  .health-ring > div {
    display: grid;
    gap: 2px;
    text-align: center;
  }

  .health-ring strong {
    font-size: 25px;
    line-height: 1;
  }

  .health-ring span {
    font-size: 12px;
    color: var(--ops-muted);
  }

  .health-ring.is-green {
    border-color: var(--ops-green);
    box-shadow: 0 0 0 8px rgb(53 218 138 / 0.08);
  }

  .health-ring.is-red {
    border-color: var(--ops-red);
    box-shadow: 0 0 0 8px rgb(255 101 112 / 0.08);
  }

  .health-copy {
    min-width: 0;
  }

  .health-tabs {
    display: flex;
    gap: 5px;
    margin-bottom: 19px;
  }

  .health-tabs button {
    padding: 5px 9px;
    font-size: 11px;
    font-weight: 700;
    color: var(--ops-muted);
    cursor: pointer;
    background: #2a3b54;
    border: 0;
    border-radius: 4px;
  }

  .health-tabs button.active,
  .health-tabs button:hover,
  .health-tabs button:focus-visible {
    color: white;
    background: var(--ops-blue);
    outline: none;
  }

  .eyebrow,
  .metric-card__head > span:nth-child(2),
  .resource-card__head > span:nth-child(2) {
    font-size: 11px;
    color: var(--ops-muted);
  }

  .health-numbers {
    display: flex;
    gap: 6px;
    align-items: baseline;
    margin-top: 5px;
    white-space: nowrap;
  }

  .health-numbers strong {
    font-size: 26px;
    line-height: 1;
  }

  .health-numbers small {
    margin-right: 8px;
    font-size: 11px;
    color: var(--ops-muted);
  }

  .health-foot {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
    margin-top: 19px;
    font-size: 11px;
    color: var(--ops-muted);
  }

  .health-foot span {
    display: grid;
    gap: 3px;
  }

  .health-foot b {
    font-size: 11px;
    color: var(--ops-ink);
  }

  .health-sparkline {
    height: 28px;
    margin: 10px 0 0 150px;
    overflow: hidden;
    border-bottom: 2px solid #2f6ed6;
    opacity: 0.8;
    transform: skewX(-26deg);
  }

  .health-sparkline i {
    display: block;
    width: 72%;
    height: 20px;
    margin-left: 8%;
    border-bottom: 2px solid var(--ops-blue);
    border-radius: 50%;
  }

  .health-description {
    margin: 13px 0 0;
    font-size: 11px;
    color: var(--ops-muted);
  }

  .metric-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
  }

  .metric-card {
    min-height: 144px;
    padding: 17px 18px;
  }

  .metric-card__head {
    gap: 8px;
    font-size: 12px;
    color: var(--ops-muted);
  }

  .metric-card__head a {
    margin-left: auto;
    font-size: 11px;
    color: var(--ops-blue);
    text-decoration: none;
  }

  .metric-card__head .info-mark {
    margin-left: 2px;
  }

  .metric-icon,
  .resource-icon,
  .service-icon,
  .quick-icon {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    border-radius: 8px;
  }

  .metric-icon {
    width: 22px;
    height: 22px;
    font-size: 15px;
    background: rgb(67 136 255 / 0.15);
  }

  .metric-value {
    display: block;
    margin-top: 14px;
    font-size: clamp(23px, 2.5vw, 33px);
    font-variant-numeric: tabular-nums;
    line-height: 1;
    color: var(--ops-ink);
  }

  .metric-meter,
  .resource-meter {
    height: 6px;
    margin-top: 13px;
    overflow: hidden;
    background: #2f4058;
    border-radius: 99px;
  }

  .metric-meter span,
  .resource-meter span {
    display: block;
    height: 100%;
    background: var(--ops-green);
    border-radius: inherit;
  }

  .metric-caption {
    margin-top: 10px;
    overflow: hidden;
    font-size: 11px;
    color: var(--ops-muted);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .resource-strip {
    display: grid;
    grid-template-columns: repeat(5, minmax(0, 1fr));
    gap: 14px;
    padding: 18px 0 0;
  }

  .resource-card {
    min-height: 120px;
    padding: 16px;
  }

  .resource-card__head {
    gap: 7px;
    font-size: 11px;
    color: var(--ops-muted);
  }

  .resource-card > strong {
    display: block;
    margin-top: 13px;
    font-size: 22px;
    line-height: 1;
  }

  .resource-card small {
    display: block;
    margin-top: 8px;
    overflow: hidden;
    font-size: 10px;
    color: var(--ops-muted);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .resource-meter {
    height: 4px;
    margin-top: 10px;
  }

  .content-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.75fr) minmax(300px, 0.85fr);
    gap: 16px;
    padding-top: 18px;
  }

  .bottom-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.3fr);
    gap: 16px;
    padding-top: 16px;
  }

  .panel {
    min-width: 0;
    padding: 18px;
  }

  .panel-title {
    gap: 8px;
    font-size: 15px;
  }

  .panel-title :deep(.art-svg-icon) {
    font-size: 18px;
    color: var(--ops-blue);
  }

  .panel-head p {
    margin: 6px 0 0;
    font-size: 11px;
    color: var(--ops-muted);
  }

  .panel-count {
    padding: 5px 9px;
    font-size: 10px;
    color: var(--ops-muted);
    background: #202f45;
    border-radius: 6px;
  }

  .legend {
    gap: 12px;
    font-size: 10px;
    color: var(--ops-muted);
  }

  .legend span {
    display: inline-flex;
    gap: 5px;
    align-items: center;
  }

  .legend i {
    width: 7px;
    height: 7px;
    border-radius: 50%;
  }

  .legend .requests {
    background: var(--ops-blue);
  }

  .legend .failed {
    background: var(--ops-red);
  }

  .legend .latency {
    background: var(--ops-green);
  }

  .trend-chart {
    width: 100%;
    height: 280px;
    margin-top: 12px;
  }

  .service-list,
  .activity-list {
    padding: 0;
    margin: 14px 0 0;
    list-style: none;
  }

  .service-list li {
    gap: 10px;
    min-height: 54px;
    border-bottom: 1px solid #22344c;
  }

  .service-list li:last-child {
    border-bottom: 0;
  }

  .service-list li > div {
    display: grid;
    gap: 4px;
    min-width: 0;
  }

  .service-list strong {
    font-size: 12px;
  }

  .service-list small {
    overflow: hidden;
    font-size: 10px;
    color: var(--ops-muted);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .service-icon {
    width: 30px;
    height: 30px;
    font-size: 16px;
    background: #20314a;
  }

  .service-value {
    display: inline-flex;
    gap: 6px;
    align-items: center;
    margin-left: auto;
    font-size: 10px;
    color: var(--ops-muted);
    white-space: nowrap;
  }

  .service-value--mono {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    color: var(--ops-purple);
  }

  .status-dot.is-green,
  .activity-marker.is-green {
    background: var(--ops-green);
  }

  .status-dot.is-red,
  .activity-marker.is-red {
    background: var(--ops-red);
  }

  .quick-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
    margin-top: 16px;
  }

  .quick-grid button {
    display: flex;
    gap: 10px;
    align-items: center;
    min-height: 58px;
    padding: 10px;
    color: var(--ops-ink);
    text-align: left;
    cursor: pointer;
    background: var(--ops-card-soft);
    border: 1px solid #22344c;
    border-radius: 10px;
  }

  .quick-grid button:hover,
  .quick-grid button:focus-visible {
    border-color: var(--ops-blue);
    outline: none;
  }

  .quick-grid button > span:nth-child(2) {
    display: grid;
    gap: 3px;
    min-width: 0;
  }

  .quick-grid strong {
    font-size: 11px;
  }

  .quick-grid small {
    font-size: 10px;
    color: var(--ops-muted);
  }

  .quick-icon {
    width: 30px;
    height: 30px;
    font-size: 16px;
    background: #20314a;
  }

  .quick-arrow {
    margin-left: auto;
    color: var(--ops-muted);
  }

  .activity-row {
    gap: 10px;
    min-height: 47px;
    border-bottom: 1px solid #22344c;
  }

  .activity-row:last-child {
    border-bottom: 0;
  }

  .activity-row > div {
    display: grid;
    gap: 3px;
  }

  .activity-row strong {
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }

  .activity-row span {
    font-size: 10px;
    color: var(--ops-muted);
  }

  .activity-row time {
    margin-left: auto;
    font-size: 10px;
    font-variant-numeric: tabular-nums;
    color: var(--ops-muted);
  }

  .activity-empty,
  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    color: var(--ops-muted);
  }

  .activity-empty {
    gap: 8px;
    min-height: 174px;
    font-size: 12px;
  }

  .activity-empty :deep(.art-svg-icon) {
    font-size: 25px;
    color: var(--ops-blue);
  }

  .empty-state {
    gap: 10px;
    min-height: 360px;
    margin-top: 18px;
  }

  .empty-state > :deep(.art-svg-icon) {
    font-size: 38px;
    color: var(--ops-blue);
  }

  .empty-state strong {
    font-size: 16px;
    color: var(--ops-ink);
  }

  .empty-state span {
    font-size: 12px;
  }

  :deep(.el-loading-mask) {
    background: rgb(6 21 38 / 0.8);
  }

  .is-blue {
    color: var(--ops-blue);
  }

  .is-green {
    color: var(--ops-green);
  }

  .is-red {
    color: var(--ops-red);
  }

  .is-purple {
    color: var(--ops-purple);
  }

  .is-cyan {
    color: var(--ops-cyan);
  }

  .ops-console.is-light {
    --ops-bg: #eef3f8;
    --ops-shell: #f8fbff;
    --ops-card: #fff;
    --ops-card-soft: #f2f6fa;
    --ops-line: #d9e2ec;
    --ops-ink: #1e293b;
    --ops-muted: #718096;
  }

  .ops-console.is-light .dashboard-shell {
    border-color: #d4dfeb;
    box-shadow: 0 18px 40px rgb(44 62 80 / 0.1);
  }

  .ops-console.is-light .health-card,
  .ops-console.is-light .metric-card,
  .ops-console.is-light .resource-card,
  .ops-console.is-light .panel,
  .ops-console.is-light .empty-state {
    border-color: #dce5ee;
  }

  .ops-console.is-light .range-control {
    color: var(--ops-ink);
    background: #fff;
    border-color: #c9d6e4;
  }

  .ops-console.is-light .range-control select {
    color: var(--ops-ink);
  }

  .ops-console.is-light .health-ring {
    border-color: #d5e0ea;
  }

  .ops-console.is-light .health-ring.is-green {
    border-color: #25bd72;
  }

  .ops-console.is-light .health-ring.is-red {
    border-color: #e15460;
  }

  .ops-console.is-light .health-tabs button {
    color: #718096;
    background: #e7edf4;
  }

  .ops-console.is-light .health-tabs button.active,
  .ops-console.is-light .health-tabs button:hover,
  .ops-console.is-light .health-tabs button:focus-visible {
    color: white;
    background: #4388ff;
  }

  .ops-console.is-light .metric-icon,
  .ops-console.is-light .resource-icon,
  .ops-console.is-light .service-icon,
  .ops-console.is-light .quick-icon {
    background: #edf3f9;
  }

  .ops-console.is-light .metric-meter,
  .ops-console.is-light .resource-meter {
    background: #dfe7f0;
  }

  .ops-console.is-light .panel-count {
    background: #edf2f7;
  }

  .ops-console.is-light .service-list li,
  .ops-console.is-light .activity-row {
    border-color: #e1e8f0;
  }

  .ops-console.is-light .quick-grid button {
    background: #f7faff;
    border-color: #dce5ee;
  }

  .ops-console.is-light .health-sparkline {
    border-color: #8db2ef;
  }

  .ops-console.is-light :deep(.el-loading-mask) {
    background: rgb(238 243 248 / 0.8);
  }

  @media (max-width: 1220px) {
    .overview-grid,
    .content-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 900px) {
    .resource-strip {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }

    .bottom-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 680px) {
    .ops-console {
      padding: 8px;
    }

    .dashboard-shell {
      padding: 16px 14px 20px;
      border-radius: 16px;
    }

    .console-head {
      flex-direction: column;
      align-items: stretch;
    }

    .health-card__body {
      grid-template-columns: 120px minmax(0, 1fr);
      gap: 12px;
    }

    .health-ring {
      width: 112px;
      border-width: 8px;
    }

    .health-sparkline {
      margin-left: 120px;
    }

    .metric-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 10px;
    }

    .resource-strip {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 10px;
    }

    .panel {
      padding: 14px;
    }
  }

  @media (max-width: 440px) {
    .range-control {
      flex: 1 1 100%;
    }

    .health-card__body {
      grid-template-columns: 1fr;
      justify-items: center;
    }

    .health-copy {
      width: 100%;
    }

    .health-sparkline {
      margin-left: 0;
    }

    .metric-grid,
    .resource-strip,
    .quick-grid {
      grid-template-columns: 1fr;
    }

    .trend-chart {
      height: 236px;
    }
  }
</style>
