<template>
  <div class="config-overview-page">
    <header class="page-head">
      <h1>配置概览</h1>
      <div class="page-actions">
        <ElButton @click="router.push('/config/ai-model')">模型配置</ElButton>
        <ElButton @click="router.push('/config/assistant-agent')">智能体配置</ElButton>
        <ElButton :icon="Refresh" :loading="loading" circle title="刷新" @click="loadPageData" />
      </div>
    </header>

    <section class="summary-grid">
      <div
        ><span>模型</span><strong>{{ overview.models.length }}</strong></div
      >
      <div
        ><span>已启用</span><strong class="is-success">{{ enabledModelCount }}</strong></div
      >
      <div
        ><span>智能体</span><strong>{{ overview.agents.length }}</strong></div
      >
      <div
        ><span>已绑定</span><strong>{{ bindingCount }}</strong></div
      >
    </section>

    <ElCard shadow="never">
      <ElTable :data="overview.models" v-loading="loading" table-layout="fixed">
        <ElTableColumn prop="displayName" label="模型" min-width="170" />
        <ElTableColumn prop="providerName" label="提供方" min-width="140" />
        <ElTableColumn label="启用状态" width="110">
          <template #default="{ row }">
            <ElTag :type="row.isEnabled ? 'success' : 'info'" effect="plain">
              {{ row.isEnabled ? '已启用' : '已停用' }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="智能体绑定" min-width="220">
          <template #default="{ row }">
            <div v-if="row.boundAgents.length" class="bound-tags">
              <ElTag v-for="agent in row.boundAgents" :key="agent.id" size="small" effect="plain">
                {{ agent.displayName }}
              </ElTag>
            </div>
            <span v-else class="muted">未绑定</span>
          </template>
        </ElTableColumn>
        <ElTableColumn label="最近校验" min-width="190">
          <template #default="{ row }">
            <div class="validation-cell">
              <ElTag :type="validationTagType(row.lastValidationStatus)" effect="plain">
                {{ validationLabel(row.lastValidationStatus) }}
              </ElTag>
              <span>{{ row.lastValidatedAt || '--' }}</span>
            </div>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
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

  const enabledModelCount = computed(() => overview.models.filter((item) => item.isEnabled).length)
  const bindingCount = computed(() =>
    overview.models.reduce((total, item) => total + item.boundAgents.length, 0)
  )
  const validationTagType = (status: string) =>
    status === 'success' ? 'success' : status === 'failed' ? 'danger' : 'info'
  const validationLabel = (status: string) =>
    ({ success: '通过', failed: '失败', pending: '校验中', unknown: '未校验' })[status] || '未校验'

  const loadPageData = async () => {
    loading.value = true
    try {
      Object.assign(overview, await fetchConfigOverview())
    } finally {
      loading.value = false
    }
  }

  onMounted(() => void loadPageData())
</script>

<style scoped lang="scss">
  .config-overview-page {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .page-head,
  .page-actions,
  .validation-cell,
  .bound-tags {
    display: flex;
    align-items: center;
  }

  .page-head {
    gap: 12px;
    justify-content: space-between;
  }

  h1 {
    margin: 0;
    font-size: 24px;
  }

  .page-actions,
  .validation-cell,
  .bound-tags {
    gap: 8px;
  }

  .summary-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 12px;
  }

  .summary-grid > div {
    padding: 14px 16px;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color-light);
  }

  .summary-grid span,
  .validation-cell span,
  .muted {
    color: var(--el-text-color-secondary);
  }

  .summary-grid strong {
    display: block;
    margin-top: 6px;
    font-size: 24px;
  }

  .is-success {
    color: var(--el-color-success);
  }

  .bound-tags {
    flex-wrap: wrap;
  }

  @media (max-width: 720px) {
    .page-head {
      flex-direction: column;
      align-items: flex-start;
    }

    .summary-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
