<!-- 配置中心概览页面：index。 -->
<template>
  <div class="config-overview-page">
    <section class="overview-hero">
      <div>
        <div class="overview-hero__badge">配置管理</div>
        <h1>配置概览</h1>
        <p>当前只保留模型配置、智能体配置和通知渠道三类核心配置能力。</p>
      </div>
      <div class="overview-hero__actions">
        <ElButton type="primary" @click="router.push('/config/ai-model')">模型配置</ElButton>
        <ElButton @click="router.push('/config/assistant-agent')">智能体配置</ElButton>
        <ElButton @click="router.push('/config/notify-channel')">通知渠道</ElButton>
        <ElButton :loading="loading" @click="loadPageData">刷新</ElButton>
      </div>
    </section>

    <div class="summary-grid">
      <article v-for="card in summaryCards" :key="card.key" class="summary-card">
        <span>{{ card.label }}</span>
        <strong>{{ card.value }}</strong>
        <small>{{ card.description }}</small>
      </article>
    </div>

    <div class="panel-grid">
      <ElCard class="panel-card" shadow="never">
        <template #header>
          <div class="panel-card__title">智能体概况</div>
        </template>
        <div v-if="!agentList.length" class="empty-block">暂无智能体</div>
        <div v-else class="agent-list">
          <div v-for="item in agentList" :key="item.id" class="agent-item">
            <div>
              <strong>{{ item.displayName }}</strong>
              <p>{{ item.description || '未填写描述' }}</p>
            </div>
            <div class="agent-item__tags">
              <ElTag effect="plain">{{ item.dataSourceType }}</ElTag>
              <ElTag :type="item.isEnabled ? 'success' : 'info'" effect="plain">
                {{ item.isEnabled ? '启用中' : '已停用' }}
              </ElTag>
              <ElTag effect="plain">绑定 {{ item.bindingCount }}</ElTag>
            </div>
          </div>
        </div>
      </ElCard>

      <ElCard class="panel-card" shadow="never">
        <template #header>
          <div class="panel-card__title">通知概况</div>
        </template>
        <div class="summary-block">
          <div>
            <span>通知渠道</span>
            <strong>{{ overview.notifySummary.channelCount }}</strong>
          </div>
          <div>
            <span>启用渠道</span>
            <strong>{{ overview.notifySummary.enabledChannelCount }}</strong>
          </div>
          <div>
            <span>推送记录</span>
            <strong>{{ overview.notifySummary.deliveryCount }}</strong>
          </div>
          <div>
            <span>最近投递</span>
            <strong>{{ overview.notifySummary.latestDeliveryAt || '--' }}</strong>
          </div>
        </div>
      </ElCard>
    </div>

    <ElCard class="table-card" shadow="never">
      <template #header>
        <div class="panel-card__title">模型健康度</div>
      </template>
      <ElTable :data="overview.models" v-loading="loading" table-layout="fixed">
        <ElTableColumn prop="displayName" label="模型名称" min-width="160" align="center" />
        <ElTableColumn prop="providerName" label="提供方" min-width="140" align="center" />
        <ElTableColumn prop="modelIdentifier" label="模型标识" min-width="160" align="center" />
        <ElTableColumn label="状态" min-width="110" align="center">
          <template #default="{ row }">
            <ElTag :type="row.isEnabled ? 'success' : 'info'" effect="plain">
              {{ row.isEnabled ? '启用' : '停用' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="校验结果" min-width="110" align="center">
          <template #default="{ row }">
            <ElTag
              :type="
                row.lastValidationStatus === 'success'
                  ? 'success'
                  : row.lastValidationStatus === 'failed'
                    ? 'danger'
                    : 'info'
              "
              effect="plain"
            >
              {{ row.lastValidationStatus || 'unknown' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="lastValidatedAt" label="最近校验" min-width="160" align="center" />
        <ElTableColumn label="绑定智能体" min-width="200" align="center">
          <template #default="{ row }">
            <div v-if="row.boundAgents.length" class="bound-tags">
              <ElTag v-for="agent in row.boundAgents" :key="agent.id" size="small" effect="plain">
                {{ agent.displayName }}
              </ElTag>
            </div>
            <span v-else class="muted">未绑定</span>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { fetchConfigOverview, type ConfigOverviewResponse } from '@/api/config'

  defineOptions({ name: 'ConfigOverviewPage' })

  const router = useRouter()
  const loading = ref(false)
  const overview = reactive<ConfigOverviewResponse>({
    models: [],
    agents: [],
    notifySummary: {
      channelCount: 0,
      enabledChannelCount: 0,
      latestDeliveryStatus: 'unknown',
      latestDeliveryAt: '',
      deliveryCount: 0
    }
  })

  const agentList = computed(() => overview.agents)
  const enabledModelCount = computed(() => overview.models.filter((item) => item.isEnabled).length)
  const enabledAgentCount = computed(() => overview.agents.filter((item) => item.isEnabled).length)

  const summaryCards = computed(() => [
    {
      key: 'models',
      label: '模型总数',
      value: overview.models.length,
      description: '当前用户可用的模型配置数量'
    },
    {
      key: 'enabledModels',
      label: '启用模型',
      value: enabledModelCount.value,
      description: '当前处于启用状态的模型'
    },
    {
      key: 'agents',
      label: '智能体',
      value: overview.agents.length,
      description: '系统内可维护的智能体模板'
    },
    {
      key: 'enabledAgents',
      label: '启用智能体',
      value: enabledAgentCount.value,
      description: '当前可被使用的智能体模板'
    },
    {
      key: 'channels',
      label: '通知渠道',
      value: overview.notifySummary.channelCount,
      description: '当前账号可见的通知渠道数量'
    },
    {
      key: 'deliveries',
      label: '推送记录',
      value: overview.notifySummary.deliveryCount,
      description: '累计通知投递记录'
    }
  ])

  const loadPageData = async () => {
    loading.value = true
    try {
      const data = await fetchConfigOverview()
      overview.models = data.models
      overview.agents = data.agents
      overview.notifySummary = data.notifySummary
    } finally {
      loading.value = false
    }
  }

  onMounted(() => {
    void loadPageData()
  })
</script>

<style scoped lang="scss">
  .config-overview-page {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .overview-hero {
    display: flex;
    gap: 16px;
    justify-content: space-between;
    padding: 24px 28px;
    background: linear-gradient(135deg, rgb(255 255 255 / 0.98), rgb(239 245 255 / 0.9));
    border: 1px solid var(--el-border-color-light);
    border-radius: 24px;

    h1 {
      margin: 12px 0 8px;
      font-size: 28px;
    }

    p {
      margin: 0;
      line-height: 1.8;
      color: var(--el-text-color-secondary);
    }
  }

  .overview-hero__badge {
    display: inline-flex;
    padding: 6px 12px;
    font-size: 12px;
    color: #255ee8;
    background: rgb(37 94 232 / 0.12);
    border-radius: 999px;
  }

  .overview-hero__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    align-self: flex-start;
  }

  .summary-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
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
      color: var(--el-text-color-primary);
    }

    small {
      margin-top: 8px;
      line-height: 1.6;
    }
  }

  .panel-grid {
    display: grid;
    grid-template-columns: 1.3fr 1fr;
    gap: 20px;
  }

  .panel-card,
  .table-card {
    border-radius: 20px;
  }

  .panel-card__title {
    font-size: 16px;
    font-weight: 600;
  }

  .agent-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .agent-item {
    display: flex;
    gap: 16px;
    justify-content: space-between;
    padding: 14px 16px;
    background: rgb(148 163 184 / 0.05);
    border: 1px solid var(--el-border-color-lighter);
    border-radius: 16px;

    p {
      margin: 8px 0 0;
      line-height: 1.7;
      color: var(--el-text-color-secondary);
    }
  }

  .agent-item__tags,
  .bound-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .summary-block {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;

    div {
      padding: 16px;
      background: rgb(148 163 184 / 0.05);
      border: 1px solid var(--el-border-color-lighter);
      border-radius: 16px;
    }

    span,
    strong {
      display: block;
    }

    span {
      font-size: 12px;
      color: var(--el-text-color-secondary);
    }

    strong {
      margin-top: 10px;
      font-size: 22px;
    }
  }

  .empty-block,
  .muted {
    color: var(--el-text-color-secondary);
  }

  @media (max-width: 1100px) {
    .overview-hero,
    .panel-grid {
      flex-direction: column;
      grid-template-columns: 1fr;
    }
  }
</style>
