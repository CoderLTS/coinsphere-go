<template>
  <div class="ops-console" v-loading="loading && !overview">
    <header class="console-head">
      <div>
        <div class="page-context"><ArtSvgIcon icon="ri:radar-line" /> 运行中心</div>
        <h1>系统运维总览</h1>
        <p>一眼掌握服务健康、请求趋势和运行资源。</p>
      </div>
      <div class="head-actions">
        <div class="refresh-state">
          <span class="live-indicator" aria-hidden="true"></span>
          <span>自动刷新 15 秒</span>
          <time>{{ refreshedAt ? `更新于 ${refreshedAt}` : '等待数据' }}</time>
        </div>
        <ElTooltip content="立即刷新" placement="top">
          <ElButton
            circle
            :icon="Refresh"
            :loading="loading"
            aria-label="立即刷新数据"
            @click="loadOverview"
          />
        </ElTooltip>
      </div>
    </header>

    <template v-if="overview">
      <section class="status-layout" aria-label="系统状态">
        <article class="status-card art-card" :class="`is-${overallTone}`">
          <div class="status-card__main">
            <span class="status-card__icon"><ArtSvgIcon :icon="overallIcon" /></span>
            <div>
              <span class="status-card__label">平台状态</span>
              <h2>{{ overallLabel }}</h2>
              <p>{{ overallDescription }}</p>
            </div>
          </div>
          <div class="status-card__signal" :aria-label="overallLabel">
            <span class="signal-bars" aria-hidden="true"><i></i><i></i><i></i><i></i></span>
            <strong>LIVE</strong>
          </div>
        </article>

        <div class="metric-grid">
          <article v-for="item in headlineMetrics" :key="item.label" class="metric-card art-card">
            <span class="metric-card__icon" :class="`is-${item.tone}`">
              <ArtSvgIcon :icon="item.icon" />
            </span>
            <div>
              <span>{{ item.label }}</span>
              <strong>{{ item.value }}</strong>
              <small>{{ item.caption }}</small>
            </div>
          </article>
        </div>
      </section>

      <section class="main-grid">
        <article class="panel chart-panel">
          <header class="panel-head">
            <div>
              <div class="panel-title"><ArtSvgIcon icon="ri:line-chart-line" /> 请求趋势</div>
              <p>HTTP 近 60 分钟 · 按分钟聚合</p>
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
              <span class="service-icon is-indigo"><ArtSvgIcon icon="ri:server-line" /></span>
              <div><strong>HTTP 网关</strong><small>请求处理器</small></div>
              <span class="service-value"><i class="status-dot is-success"></i>运行中</span>
            </li>
            <li>
              <span class="service-icon is-teal"><ArtSvgIcon icon="ri:database-2-line" /></span>
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
              <span class="service-icon is-coral"><ArtSvgIcon icon="ri:pulse-line" /></span>
              <div><strong>观测指标</strong><small>请求采样窗口</small></div>
              <span class="service-value"><i class="status-dot is-success"></i>实时</span>
            </li>
            <li>
              <span class="service-icon is-violet"><ArtSvgIcon icon="ri:git-branch-line" /></span>
              <div><strong>数据库结构</strong><small>当前迁移版本</small></div>
              <span class="service-value service-value--mono"
                >v{{ overview.database.schemaVersion }}</span
              >
            </li>
          </ul>
        </article>
      </section>

      <section class="bottom-grid">
        <article class="panel resource-panel">
          <header class="panel-head">
            <div>
              <div class="panel-title"><ArtSvgIcon icon="ri:cpu-line" /> 运行资源</div>
              <p>进程级实时指标</p>
            </div>
            <span class="panel-count">UTC+8</span>
          </header>
          <div class="resource-list">
            <div v-for="item in resourceRows" :key="item.label" class="resource-row">
              <span class="resource-icon" :class="`is-${item.tone}`"
                ><ArtSvgIcon :icon="item.icon"
              /></span>
              <div class="resource-copy">
                <div
                  ><strong>{{ item.label }}</strong
                  ><span>{{ item.value }}</span></div
                >
                <small>{{ item.caption }}</small>
                <div v-if="item.meter !== null" class="meter" aria-hidden="true">
                  <span :style="{ width: `${item.meter}%` }"></span>
                </div>
              </div>
            </div>
          </div>
        </article>

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
              <span
                class="activity-marker"
                :class="item.failed ? 'is-danger' : 'is-success'"
              ></span>
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
    </template>

    <div v-else-if="!loading" class="empty-state art-card">
      <ArtSvgIcon icon="ri:cloud-off-line" />
      <strong>暂时无法读取运行状态</strong>
      <span>请检查服务连接后重试。</span>
      <ElButton type="primary" :icon="Refresh" @click="loadOverview">重新读取</ElButton>
    </div>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
  import * as echarts from 'echarts'
  import { fetchHomeOverview, type HomeOverview } from '@/api/home'
  import { formatDateTime } from '@/utils/date'

  defineOptions({ name: 'HomePage' })

  type Tone = 'indigo' | 'teal' | 'coral' | 'violet'

  const router = useRouter()
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
  const overallIcon = computed(() =>
    overallTone.value === 'success' ? 'ri:checkbox-circle-line' : 'ri:error-warning-line'
  )
  const overallLabel = computed(() => (overallTone.value === 'success' ? '运行正常' : '存在中断'))
  const overallDescription = computed(() =>
    overallTone.value === 'success'
      ? '核心服务均可访问，系统正在稳定处理请求。'
      : '数据库连接异常，请优先检查依赖服务。'
  )
  const failureRate = computed(() => {
    const http = overview.value?.http
    if (!http || http.requestsTotal === 0) return '0.00%'
    return `${((http.requestsFailed / http.requestsTotal) * 100).toFixed(2)}%`
  })
  const averageLatency = computed(() => {
    const trend = overview.value?.http.trend.filter((item) => item.requests > 0) || []
    const requests = trend.reduce((sum, item) => sum + item.requests, 0)
    if (!requests) return '--'
    const total = trend.reduce((sum, item) => sum + item.averageLatencyMs * item.requests, 0)
    return `${(total / requests).toFixed(1)} ms`
  })
  const headlineMetrics = computed(() => {
    if (!overview.value) return []
    return [
      {
        label: 'HTTP 请求',
        value: overview.value.http.requestsTotal.toLocaleString(),
        caption: `失败率 ${failureRate.value}`,
        icon: 'ri:arrow-left-right-line',
        tone: 'indigo' as Tone
      },
      {
        label: '平均延迟',
        value: averageLatency.value,
        caption: '近 60 分钟加权',
        icon: 'ri:timer-flash-line',
        tone: 'coral' as Tone
      },
      {
        label: '数据库连接',
        value: `${overview.value.database.inUse} / ${overview.value.database.maxOpenConnections || '不限'}`,
        caption: `打开 ${overview.value.database.openConnections} · 空闲 ${overview.value.database.idle}`,
        icon: 'ri:database-line',
        tone: 'teal' as Tone
      },
      {
        label: 'Goroutine',
        value: overview.value.process.goroutines.toLocaleString(),
        caption: `并发请求 ${overview.value.http.requestsInFlight}`,
        icon: 'ri:git-pull-request-line',
        tone: 'violet' as Tone
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
  const memoryPercent = computed(() => {
    const process = overview.value?.process
    if (!process?.goMemorySysBytes) return 0
    return Math.min(100, Math.round((process.goMemoryAllocBytes / process.goMemorySysBytes) * 100))
  })
  const resourceRows = computed(() => {
    if (!overview.value) return []
    return [
      {
        label: 'Go 内存',
        value: formatBytes(overview.value.process.goMemoryAllocBytes),
        caption: `系统分配 ${formatBytes(overview.value.process.goMemorySysBytes)}`,
        meter: memoryPercent.value,
        icon: 'ri:memory-line',
        tone: 'indigo'
      },
      {
        label: '运行时长',
        value: formatDuration(overview.value.process.uptimeSeconds),
        caption: '进程启动以来',
        meter: null,
        icon: 'ri:time-line',
        tone: 'teal'
      },
      {
        label: '并发请求',
        value: overview.value.http.requestsInFlight.toLocaleString(),
        caption: '当前正在处理',
        meter: null,
        icon: 'ri:loader-4-line',
        tone: 'coral'
      }
    ]
  })
  const quickActions = [
    {
      label: '工作流调度',
      caption: '执行队列',
      icon: 'ri:node-tree',
      tone: 'indigo',
      path: '/scheduler/definition'
    },
    {
      label: '量化数据',
      caption: '行情元数据',
      icon: 'ri:line-chart-line',
      tone: 'teal',
      path: '/quant-data'
    },
    {
      label: '用户管理',
      caption: '账号与权限',
      icon: 'ri:team-line',
      tone: 'violet',
      path: '/system/user'
    },
    {
      label: '系统菜单',
      caption: '导航配置',
      icon: 'ri:menu-4-line',
      tone: 'coral',
      path: '/system/menu'
    }
  ]
  const recentWindows = computed(() =>
    (overview.value?.http.trend || [])
      .filter((item) => item.requests > 0 || item.failed > 0)
      .slice(-5)
      .reverse()
  )
  const openAction = (path: string) => void router.push(path)

  const observeChart = () => {
    if (resizeObserver || !chartHost.value) return
    resizeObserver = new ResizeObserver(() => chart?.resize())
    resizeObserver.observe(chartHost.value)
  }

  const renderChart = () => {
    if (!overview.value || !chartHost.value) return
    chart ||= echarts.init(chartHost.value)
    const trend = overview.value.http.trend
    chart.setOption(
      {
        animation: false,
        grid: { left: 40, right: 44, top: 20, bottom: 30 },
        tooltip: { trigger: 'axis' },
        xAxis: {
          type: 'category',
          data: trend.map((item) => formatDateTime(item.time).slice(11, 16)),
          axisLabel: { interval: 9, color: '#94a3b8' },
          axisLine: { lineStyle: { color: '#dce3ec' } }
        },
        yAxis: [
          {
            type: 'value',
            minInterval: 1,
            axisLabel: { color: '#94a3b8' },
            splitLine: { lineStyle: { color: '#edf1f5' } }
          },
          {
            type: 'value',
            axisLabel: { formatter: '{value} ms', color: '#94a3b8' },
            splitLine: { show: false }
          }
        ],
        series: [
          {
            name: '请求',
            type: 'bar',
            data: trend.map((item) => item.requests),
            itemStyle: { color: '#4969d8', borderRadius: [3, 3, 0, 0] },
            barMaxWidth: 10
          },
          {
            name: '失败',
            type: 'bar',
            data: trend.map((item) => item.failed),
            itemStyle: { color: '#cf5b42', borderRadius: [3, 3, 0, 0] },
            barMaxWidth: 10
          },
          {
            name: '延迟',
            type: 'line',
            yAxisIndex: 1,
            data: trend.map((item) => Number(item.averageLatencyMs.toFixed(1))),
            symbol: 'none',
            smooth: true,
            lineStyle: { width: 2, color: '#0f8b8d' },
            areaStyle: { color: 'rgba(15, 139, 141, 0.08)' }
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
    --ops-ink: var(--el-text-color-primary);
    --ops-muted: var(--el-text-color-secondary);
    --ops-line: var(--el-border-color-lighter);
    --ops-panel: var(--el-fill-color-blank);

    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
    padding-bottom: 24px;
    color: var(--ops-ink);
  }

  .console-head,
  .head-actions,
  .refresh-state,
  .status-card__main,
  .panel-head,
  .panel-title,
  .legend,
  .service-list li,
  .resource-row,
  .resource-copy > div,
  .activity-row {
    display: flex;
    align-items: center;
  }

  .console-head,
  .panel-head {
    justify-content: space-between;
  }

  .page-context {
    display: flex;
    gap: 6px;
    align-items: center;
    font-size: 11px;
    font-weight: 700;
    color: var(--theme-color);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  h1 {
    margin: 5px 0 0;
    font-size: 27px;
    font-weight: 700;
    line-height: 1.25;
  }

  .console-head p,
  .panel-head p {
    margin: 5px 0 0;
    font-size: 12px;
    color: var(--ops-muted);
  }

  .head-actions,
  .refresh-state {
    gap: 11px;
  }

  .refresh-state {
    font-size: 11px;
    color: var(--ops-muted);

    time {
      padding-left: 11px;
      font-variant-numeric: tabular-nums;
      border-left: 1px solid var(--ops-line);
    }
  }

  .live-indicator,
  .status-dot {
    display: inline-block;
    width: 7px;
    height: 7px;
    border-radius: 50%;
  }

  .live-indicator {
    background: #0f8b8d;
    box-shadow: 0 0 0 4px color-mix(in srgb, #0f8b8d 13%, transparent);
  }

  .status-layout {
    display: grid;
    grid-template-columns: minmax(260px, 0.9fr) minmax(0, 2.1fr);
    gap: 16px;
  }

  .status-card,
  .metric-card,
  .panel,
  .empty-state {
    background: var(--ops-panel);
    border: 1px solid var(--art-card-border);
    border-radius: 6px;
  }

  .status-card {
    display: flex;
    align-items: center;
    justify-content: space-between;
    min-height: 122px;
    padding: 20px;
    overflow: hidden;
    border-left: 3px solid;

    &.is-success {
      border-left-color: #0f8b8d;
    }

    &.is-danger {
      border-left-color: #cf5b42;
    }
  }

  .status-card__main {
    gap: 13px;
    min-width: 0;
  }

  .status-card__icon,
  .metric-card__icon,
  .service-icon,
  .resource-icon,
  .quick-icon {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    border-radius: 8px;
  }

  .status-card__icon {
    width: 44px;
    height: 44px;
    font-size: 23px;
    color: #0f8b8d;
    background: color-mix(in srgb, #0f8b8d 12%, transparent);
  }

  .is-danger .status-card__icon {
    color: #cf5b42;
    background: color-mix(in srgb, #cf5b42 12%, transparent);
  }

  .status-card__label,
  .metric-card span,
  .metric-card small,
  .service-list small,
  .resource-copy small,
  .quick-grid small,
  .activity-row span,
  .activity-empty span {
    color: var(--ops-muted);
  }

  .status-card__label,
  .metric-card span {
    font-size: 11px;
  }

  .status-card h2 {
    margin: 3px 0 0;
    font-size: 20px;
  }

  .status-card p {
    max-width: 260px;
    margin: 5px 0 0;
    overflow: hidden;
    font-size: 11px;
    color: var(--ops-muted);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .status-card__signal {
    display: flex;
    gap: 8px;
    align-items: flex-end;
    align-self: stretch;
    padding: 4px 0 3px 16px;
    border-left: 1px solid var(--ops-line);

    strong {
      align-self: flex-end;
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 10px;
      color: var(--ops-muted);
      letter-spacing: 0.1em;
    }
  }

  .signal-bars {
    display: flex;
    gap: 3px;
    align-items: flex-end;
    height: 28px;

    i {
      display: block;
      width: 4px;
      background: #0f8b8d;
      border-radius: 2px 2px 0 0;

      &:nth-child(1) {
        height: 8px;
        opacity: 0.45;
      }

      &:nth-child(2) {
        height: 14px;
        opacity: 0.65;
      }

      &:nth-child(3) {
        height: 20px;
        opacity: 0.82;
      }

      &:nth-child(4) {
        height: 28px;
      }
    }
  }

  .status-card.is-danger .signal-bars i {
    background: #cf5b42;
  }

  .metric-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 12px;
  }

  .metric-card {
    display: flex;
    gap: 12px;
    align-items: center;
    min-width: 0;
    min-height: 122px;
    padding: 16px;
  }

  .metric-card__icon {
    width: 38px;
    height: 38px;
    font-size: 19px;
  }

  .metric-card__icon.is-indigo,
  .service-icon.is-indigo,
  .quick-icon.is-indigo,
  .resource-icon.is-indigo {
    color: #4969d8;
    background: color-mix(in srgb, #4969d8 12%, transparent);
  }

  .metric-card__icon.is-teal,
  .service-icon.is-teal,
  .quick-icon.is-teal,
  .resource-icon.is-teal {
    color: #0f8b8d;
    background: color-mix(in srgb, #0f8b8d 12%, transparent);
  }

  .metric-card__icon.is-coral,
  .service-icon.is-coral,
  .quick-icon.is-coral,
  .resource-icon.is-coral {
    color: #cf5b42;
    background: color-mix(in srgb, #cf5b42 12%, transparent);
  }

  .metric-card__icon.is-violet,
  .service-icon.is-violet,
  .quick-icon.is-violet,
  .resource-icon.is-violet {
    color: #8257c7;
    background: color-mix(in srgb, #8257c7 12%, transparent);
  }

  .metric-card > div,
  .service-list li > div,
  .resource-copy,
  .quick-grid button > span:nth-child(2),
  .activity-row > div {
    min-width: 0;
  }

  .metric-card strong,
  .metric-card small,
  .service-list strong,
  .service-list small,
  .resource-copy strong,
  .resource-copy small,
  .quick-grid strong,
  .quick-grid small,
  .activity-row strong,
  .activity-row span {
    display: block;
  }

  .metric-card strong {
    margin: 6px 0 2px;
    overflow: hidden;
    font-size: 21px;
    font-variant-numeric: tabular-nums;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .metric-card small,
  .service-list small,
  .resource-copy small,
  .quick-grid small {
    font-size: 10px;
  }

  .main-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.55fr) minmax(290px, 0.8fr);
    gap: 16px;
  }

  .bottom-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.15fr) minmax(260px, 0.9fr) minmax(260px, 0.9fr);
    gap: 16px;
  }

  .panel {
    min-width: 0;
    overflow: hidden;
  }

  .panel-head {
    min-height: 64px;
    padding: 0 16px;
    border-bottom: 1px solid var(--ops-line);
  }

  .panel-title {
    gap: 7px;
    font-size: 13px;
    font-weight: 700;
  }

  .panel-title :deep(.art-svg-icon) {
    font-size: 16px;
    color: var(--theme-color);
  }

  .panel-count {
    font-size: 10px;
    font-variant-numeric: tabular-nums;
    color: var(--ops-muted);
  }

  .legend {
    gap: 10px;
    font-size: 10px;
    color: var(--ops-muted);

    span {
      display: flex;
      gap: 4px;
      align-items: center;
    }

    i {
      width: 7px;
      height: 7px;
      border-radius: 2px;
    }

    .requests {
      background: #4969d8;
    }

    .failed {
      background: #cf5b42;
    }

    .latency {
      background: #0f8b8d;
    }
  }

  .trend-chart {
    width: 100%;
    height: 286px;
    padding: 4px 4px 0;
  }

  .service-list,
  .activity-list,
  .resource-list {
    padding: 0 16px;
    margin: 0;
    list-style: none;
  }

  .service-list li {
    gap: 10px;
    min-height: 58px;
    border-bottom: 1px solid var(--ops-line);

    &:last-child {
      border-bottom: 0;
    }
  }

  .service-icon,
  .resource-icon {
    width: 30px;
    height: 30px;
    font-size: 15px;
  }

  .service-list li > div {
    flex: 1;
  }

  .service-list strong,
  .resource-copy strong,
  .quick-grid strong {
    font-size: 12px;
  }

  .service-value {
    display: flex;
    flex: 0 0 auto;
    gap: 5px;
    align-items: center;
    font-size: 11px;
    color: var(--ops-muted);
  }

  .service-value--mono {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  }

  .status-dot.is-success,
  .activity-marker.is-success {
    background: #0f8b8d;
  }

  .status-dot.is-danger,
  .activity-marker.is-danger {
    background: #cf5b42;
  }

  .resource-list {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding-top: 16px;
    padding-bottom: 16px;
  }

  .resource-row {
    gap: 10px;
  }

  .resource-copy {
    flex: 1;
  }

  .resource-copy > div {
    gap: 8px;
    justify-content: space-between;

    span {
      flex: 0 0 auto;
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 11px;
      color: var(--ops-muted);
    }
  }

  .meter {
    height: 4px;
    margin-top: 7px;
    overflow: hidden;
    background: var(--el-fill-color-light);
    border-radius: 3px;

    span {
      display: block;
      height: 100%;
      background: #4969d8;
      border-radius: inherit;
    }
  }

  .quick-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
    padding: 12px 16px 16px;

    button {
      display: flex;
      gap: 8px;
      align-items: center;
      min-width: 0;
      padding: 10px 8px;
      color: inherit;
      text-align: left;
      cursor: pointer;
      background: var(--el-fill-color-lighter);
      border: 1px solid transparent;
      border-radius: 5px;

      &:hover,
      &:focus-visible {
        border-color: var(--theme-color);
        outline: none;
      }
    }
  }

  .quick-icon {
    width: 28px;
    height: 28px;
    font-size: 15px;
  }

  .quick-arrow {
    flex: 0 0 auto;
    margin-left: auto;
    color: var(--ops-muted);
  }

  .activity-list {
    padding-top: 2px;
    padding-bottom: 4px;
  }

  .activity-row {
    gap: 9px;
    min-height: 47px;
    border-bottom: 1px solid var(--ops-line);

    &:last-child {
      border-bottom: 0;
    }

    > div {
      flex: 1;
    }

    strong {
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 11px;
    }

    time {
      flex: 0 0 auto;
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 10px;
      color: var(--ops-muted);
    }
  }

  .activity-marker {
    flex: 0 0 auto;
    width: 7px;
    height: 7px;
    border-radius: 50%;
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
    min-height: 170px;
    font-size: 12px;

    :deep(.art-svg-icon) {
      font-size: 25px;
      color: var(--theme-color);
    }
  }

  .empty-state {
    gap: 10px;
    min-height: 320px;

    > :deep(.art-svg-icon) {
      font-size: 36px;
      color: var(--theme-color);
    }

    strong {
      font-size: 16px;
      color: var(--ops-ink);
    }

    span {
      font-size: 12px;
    }
  }

  @media (max-width: 1180px) {
    .status-layout,
    .main-grid {
      grid-template-columns: 1fr;
    }

    .bottom-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));

      .resource-panel {
        grid-column: span 2;
      }
    }
  }

  @media (max-width: 760px) {
    .console-head {
      flex-direction: column;
      gap: 12px;
      align-items: stretch;
    }

    .head-actions {
      justify-content: space-between;
    }

    .status-layout {
      gap: 12px;
    }

    .metric-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .bottom-grid {
      grid-template-columns: 1fr;

      .resource-panel {
        grid-column: auto;
      }
    }

    .status-card {
      min-height: 112px;
      padding: 16px;
    }

    .status-card__signal {
      display: none;
    }
  }

  @media (max-width: 480px) {
    h1 {
      font-size: 23px;
    }

    .refresh-state {
      gap: 7px;
      font-size: 10px;

      time {
        display: none;
      }
    }

    .metric-grid {
      grid-template-columns: 1fr;
    }

    .metric-card {
      min-height: 90px;
    }

    .trend-chart {
      height: 248px;
    }
  }
</style>
