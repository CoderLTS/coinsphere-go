<!-- 首页页面：index。 -->
<template>
  <div class="home-page">
    <section class="hero-panel">
      <div class="hero-main">
        <span class="hero-badge">coinsphere</span>
        <h1>工作流驱动的执行中心</h1>
        <p>首页聚焦新闻数据、工作流定义、激活状态和最近执行，帮助你快速判断系统运行情况。</p>
        <div class="hero-actions">
          <ElButton v-if="isGuest" type="primary" @click="goLogin">登录</ElButton>
          <template v-else>
            <ElButton type="primary" @click="router.push('/scheduler/definition')"
              >工作流定义</ElButton
            >
            <ElButton @click="router.push('/scheduler/execution')">执行记录</ElButton>
            <ElButton @click="router.push('/data/news')">新闻数据</ElButton>
          </template>
        </div>
      </div>
      <div class="hero-side">
        <article v-for="card in summaryCards" :key="card.key" class="hero-stat">
          <span>{{ card.label }}</span>
          <strong>{{ card.value }}</strong>
          <small>{{ card.description }}</small>
        </article>
      </div>
    </section>

    <ElRow :gutter="20">
      <ElCol :xs="24" :xl="15">
        <ElCard shadow="never" class="panel-card">
          <template #header>
            <div class="panel-header">
              <span>最近新闻</span>
              <ElButton text @click="isGuest ? goLogin() : router.push('/data/news')">
                {{ isGuest ? '去登录' : '查看全部' }}
              </ElButton>
            </div>
          </template>
          <div v-if="loading" class="panel-state"><ElSkeleton :rows="5" animated /></div>
          <div v-else-if="!overview.recentNews.length" class="panel-empty">暂无新闻</div>
          <div v-else class="news-list">
            <article v-for="item in overview.recentNews" :key="item.id" class="news-item">
              <div class="news-item__head">
                <h3>{{ item.title }}</h3>
                <span>{{ item.publishedAt || '--' }}</span>
              </div>
              <p>{{ item.summary }}</p>
            </article>
          </div>
        </ElCard>
      </ElCol>

      <ElCol :xs="24" :xl="9">
        <ElCard shadow="never" class="panel-card">
          <template #header>
            <div class="panel-header">
              <span>最近工作流</span>
              <ElButton text @click="isGuest ? goLogin() : router.push('/scheduler/definition')">
                {{ isGuest ? '去登录' : '查看全部' }}
              </ElButton>
            </div>
          </template>
          <div v-if="loading" class="panel-state"><ElSkeleton :rows="4" animated /></div>
          <div v-else-if="!overview.definitions.length" class="panel-empty">暂无工作流</div>
          <div v-else class="definition-list">
            <article
              v-for="item in overview.definitions"
              :key="item.workflowDefinitionId"
              class="definition-item"
            >
              <div class="definition-item__head">
                <div>
                  <strong>{{ item.workflowDefinitionName }}</strong>
                  <p>{{ item.workflowDefinitionCode }}</p>
                </div>
                <ElTag :type="item.isActive ? 'success' : 'info'" effect="plain">
                  {{ item.isActive ? '已激活' : '未激活' }}
                </ElTag>
              </div>
              <div class="definition-item__meta">
                <span>执行 {{ item.runCount }} 次</span>
                <span>{{ item.createdAt || '--' }}</span>
              </div>
            </article>
          </div>
        </ElCard>
      </ElCol>
    </ElRow>
  </div>
</template>

<script setup lang="ts">
  import { fetchHomeOverview, type HomeOverview } from '@/api/home'
  import { useUserStore } from '@/store/modules/user'

  defineOptions({ name: 'HomePage' })

  const router = useRouter()
  const userStore = useUserStore()
  const loading = ref(true)
  const overview = reactive<HomeOverview>({
    stats: {
      newsTotal: 0,
      newsToday: 0,
      activeDefinitions: 0
    },
    recentNews: [],
    definitions: []
  })

  const isGuest = computed(() => userStore.accessMode === 'guest')

  const summaryCards = computed(() => [
    {
      key: 'newsTotal',
      label: '新闻总数',
      value: overview.stats.newsTotal,
      description: '当前新闻数据总量'
    },
    {
      key: 'newsToday',
      label: '今日新增',
      value: overview.stats.newsToday,
      description: '最近 24 小时新增新闻'
    },
    {
      key: 'activeDefinitions',
      label: '激活定义',
      value: overview.stats.activeDefinitions,
      description: '当前处于激活状态的工作流定义'
    }
  ])

  const loadOverview = async () => {
    loading.value = true
    try {
      const data = await fetchHomeOverview()
      overview.stats = data.stats
      overview.recentNews = data.recentNews
      overview.definitions = data.definitions
    } finally {
      loading.value = false
    }
  }

  const goLogin = () => {
    router.push({ name: 'Login', query: { redirect: '/home' } })
  }

  onMounted(() => {
    void loadOverview()
  })
</script>

<style scoped lang="scss">
  .home-page {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .hero-panel {
    display: grid;
    grid-template-columns: minmax(0, 1.2fr) minmax(320px, 0.8fr);
    gap: 18px;
    padding: 30px;
    background:
      radial-gradient(circle at top left, rgb(55 125 255 / 0.24), transparent 28%),
      radial-gradient(circle at bottom right, rgb(44 200 155 / 0.16), transparent 30%),
      linear-gradient(135deg, rgb(255 255 255 / 0.98), rgb(239 245 255 / 0.92));
    border: 1px solid rgb(71 97 255 / 0.12);
    border-radius: 30px;
  }

  .hero-main h1 {
    margin: 16px 0 12px;
    font-size: 34px;
  }

  .hero-main p {
    max-width: 620px;
    margin: 0;
    line-height: 1.9;
    color: var(--el-text-color-secondary);
  }

  .hero-badge {
    display: inline-flex;
    padding: 6px 12px;
    font-size: 12px;
    color: #255ee8;
    background: rgb(37 94 232 / 0.12);
    border-radius: 999px;
  }

  .hero-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    margin-top: 24px;
  }

  .hero-side {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 14px;
  }

  .hero-stat {
    padding: 20px;
    background: rgb(255 255 255 / 0.84);
    border: 1px solid rgb(255 255 255 / 0.72);
    border-radius: 22px;

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
      margin-top: 10px;
      line-height: 1.6;
    }
  }

  .panel-card {
    border-radius: 24px;
  }

  .panel-header {
    display: flex;
    gap: 12px;
    align-items: center;
    justify-content: space-between;
    font-size: 16px;
    font-weight: 600;
  }

  .panel-state,
  .panel-empty {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 220px;
    color: var(--el-text-color-secondary);
  }

  .news-list,
  .definition-list {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .news-item,
  .definition-item {
    padding: 16px 18px;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color-lighter);
    border-radius: 18px;
  }

  .news-item__head,
  .definition-item__head,
  .definition-item__meta {
    display: flex;
    gap: 14px;
    align-items: flex-start;
    justify-content: space-between;
  }

  .news-item__head h3 {
    margin: 0;
    font-size: 15px;
    line-height: 1.6;
  }

  .news-item__head span,
  .definition-item p,
  .definition-item__meta,
  .news-item p {
    color: var(--el-text-color-secondary);
  }

  .news-item p,
  .definition-item p {
    margin: 10px 0 0;
    line-height: 1.7;
  }

  .definition-item__meta {
    margin-top: 14px;
    font-size: 12px;
  }

  @media (max-width: 1200px) {
    .hero-panel {
      grid-template-columns: 1fr;
    }
  }
</style>
