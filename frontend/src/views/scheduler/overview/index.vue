<!-- 调度概览页 -->
<template>
  <div class="scheduler-overview-page">
    <section class="hero-panel">
      <div>
        <div class="hero-badge">工作流调度</div>
        <h1>工作流执行概览</h1>
        <p>
          调度中心当前围绕工作流定义、激活版本、运行态入口和执行队列展开。
          手动触发、Webhook、事件和定时任务都会先进入执行队列，再由运行时 Worker 异步消费。
        </p>
      </div>
      <div class="hero-actions">
        <ElButton type="primary" @click="router.push('/scheduler/definition')">工作流定义</ElButton>
        <ElButton @click="router.push('/scheduler/execution')">执行记录</ElButton>
        <ElButton :loading="loading" @click="loadOverview">刷新</ElButton>
      </div>
    </section>

    <div class="summary-grid">
      <article v-for="card in summaryCards" :key="card.key" class="summary-card">
        <span>{{ card.label }}</span>
        <strong>{{ card.value }}</strong>
        <small>{{ card.description }}</small>
      </article>
    </div>

    <ElCard class="art-table-card" shadow="never">
      <template #header>
        <div class="panel-title">最近激活的工作流定义</div>
      </template>

      <ElTable :data="overview.definitions" :loading="loading" stripe>
        <ElTableColumn prop="workflowDefinitionName" label="工作流名称" min-width="220" />
        <ElTableColumn label="定义版本" min-width="180">
          <template #default="{ row }">
            {{ row.workflowDefinitionCode }} / v{{ row.workflowDefinitionVersion }}
          </template>
        </ElTableColumn>
        <ElTableColumn label="激活状态" width="120" align="center">
          <template #default="{ row }">
            <ElTag :type="row.isActive ? 'success' : 'info'" effect="plain">
              {{ row.isActive ? '已激活' : '未激活' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="executionCount" label="执行次数" width="120" align="center" />
      </ElTable>
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { fetchSchedulerOverview, type WorkflowOverview } from '@/api/scheduler'

  defineOptions({ name: 'SchedulerOverviewPage' })

  const router = useRouter()
  const loading = ref(false)
  const overview = reactive<WorkflowOverview>({
    stats: {
      definitionCount: 0,
      activeDefinitionCount: 0,
      executionCount: 0,
      latestExecutedAt: '',
      pendingCount: 0,
      queuedCount: 0,
      runningCount: 0,
      retryWaitingCount: 0,
      oldestPendingAgeMs: 0,
      staleRunningCount: 0
    },
    definitions: []
  })

  const formatLag = (value: number) => {
    if (!value) return '当前没有排队任务'
    if (value < 1000) return `${value} ms`
    const seconds = value / 1000
    if (seconds < 60) return `${seconds.toFixed(seconds >= 10 ? 0 : 1)} s`
    const minutes = seconds / 60
    if (minutes < 60) return `${minutes.toFixed(minutes >= 10 ? 0 : 1)} min`
    const hours = minutes / 60
    return `${hours.toFixed(hours >= 10 ? 0 : 1)} h`
  }

  const summaryCards = computed(() => [
    {
      key: 'definitions',
      label: '工作流定义',
      value: overview.stats.definitionCount,
      description: '当前可管理的最新定义数量'
    },
    {
      key: 'activeDefinitions',
      label: '激活定义',
      value: overview.stats.activeDefinitionCount,
      description: '当前正在接收自动触发的定义数量'
    },
    {
      key: 'executions',
      label: '累计执行',
      value: overview.stats.executionCount,
      description: overview.stats.latestExecutedAt
        ? `最近执行：${overview.stats.latestExecutedAt}`
        : '暂无执行记录'
    },
    {
      key: 'queued',
      label: '排队中',
      value: overview.stats.queuedCount || overview.stats.pendingCount,
      description: `最早排队等待：${formatLag(overview.stats.oldestPendingAgeMs)}`
    },
    {
      key: 'retryWaiting',
      label: '等待重试',
      value: overview.stats.retryWaitingCount,
      description: '已退避，等待下一次自动重试'
    },
    {
      key: 'running',
      label: '运行中',
      value: overview.stats.runningCount,
      description: `疑似超时执行：${overview.stats.staleRunningCount}`
    }
  ])

  const loadOverview = async () => {
    loading.value = true
    try {
      const data = await fetchSchedulerOverview()
      overview.stats = data.stats
      overview.definitions = data.definitions
    } finally {
      loading.value = false
    }
  }

  onMounted(() => {
    void loadOverview()
  })
</script>

<style scoped lang="scss">
  .scheduler-overview-page {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .hero-panel {
    display: flex;
    gap: 18px;
    justify-content: space-between;
    padding: 28px;
    background: linear-gradient(135deg, rgb(250 252 255 / 0.98), rgb(232 241 255 / 0.92));
    border: 1px solid rgb(56 104 255 / 0.12);
    border-radius: 28px;

    h1 {
      margin: 14px 0 10px;
      font-size: 30px;
    }

    p {
      max-width: 760px;
      margin: 0;
      line-height: 1.8;
      color: var(--el-text-color-secondary);
    }
  }

  .hero-badge {
    display: inline-flex;
    padding: 6px 12px;
    font-size: 12px;
    color: #255ee8;
    background: rgb(45 117 255 / 0.12);
    border-radius: 999px;
  }

  .hero-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    align-self: flex-start;
  }

  .summary-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
    gap: 16px;
  }

  .summary-card {
    padding: 18px 20px;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color-light);
    border-radius: 20px;

    span,
    small {
      display: block;
      color: var(--el-text-color-secondary);
    }

    strong {
      display: block;
      margin-top: 12px;
      font-size: 30px;
    }

    small {
      margin-top: 8px;
      line-height: 1.6;
    }
  }

  .panel-title {
    font-size: 16px;
    font-weight: 600;
  }
</style>
