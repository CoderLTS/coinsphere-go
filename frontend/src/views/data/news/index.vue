<!-- 新闻数据管理页面或组件：index。 -->
<template>
  <div class="art-full-height">
    <ArtSearchBar
      v-model="searchForm"
      :items="formItems"
      :showExpand="false"
      @reset="handleReset"
      @search="handleSearch"
    />

    <ElCard class="art-table-card">
      <ArtTableHeader :showZebra="false" :loading="loading" v-model:columns="columnChecks" @refresh="loadNews">
        <template #left>
          <ElSpace wrap>
            <ElButton v-auth="'data.news.create'" @click="openAddDialog">
              {{ t('data.news.addNews') }}
            </ElButton>
          </ElSpace>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :columns="columns"
        :data="records"
        :pagination="pagination"
        :pagination-options="{ pageSizes: [10, 20, 50] }"
        :stripe="false"
        @pagination:size-change="handleSizeChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>

    <NewsDialog
      v-model:visible="dialogVisible"
      :type="dialogType"
      :news-data="currentNews"
      @submit="handleSubmit"
    />
  </div>
</template>

<script setup lang="ts">
  import { Delete, Edit, TrendCharts } from '@element-plus/icons-vue'
  import { ElButton, ElImage, ElLink, ElMessage, ElMessageBox } from 'element-plus'
  import type { Component } from 'vue'
  import { useI18n } from 'vue-i18n'
  import { fetchAssistantModelOptions } from '@/api/assistant'
  import {
    fetchCreateNews,
    fetchDeleteNews,
    fetchNewsList,
    fetchUpdateNews
  } from '@/api/data'
  import { useAuth } from '@/hooks/core/useAuth'
  import { useTableColumns } from '@/hooks/core/useTableColumns'
  import { mittBus } from '@/utils/sys'
  import NewsDialog, { type NewsFormPayload } from './modules/news-dialog.vue'

  defineOptions({ name: 'NewsDataPage' })

  const { t } = useI18n()
  const { hasAuth } = useAuth()
  const router = useRouter()
  const route = useRoute()
  const loading = ref(false)
  const searchForm = reactive({
    keyword: ''
  })
  const records = ref<Api.Data.NewsListItem[]>([])
  const dialogVisible = ref(false)
  const dialogType = ref<'add' | 'edit'>('add')
  const currentNews = ref<Api.Data.NewsListItem | null>(null)
  const pagination = reactive({
    current: 1,
    size: 10,
    total: 0
  })

  const formItems = computed(() => [
    {
      label: t('data.news.keywordLabel'),
      key: 'keyword',
      type: 'input',
      props: {
        clearable: true,
        placeholder: t('data.news.keywordPlaceholder')
      }
    }
  ])

  const ensureNewsAssistantReady = async () => {
    const options = await fetchAssistantModelOptions('news_analysis')
    if (options.models.length) {
      return true
    }
    ElMessage.warning(t('data.news.analysisNoModel'))
    router.push('/config/ai-model')
    return false
  }

  const renderIconAction = (options: {
    icon: Component
    title: string
    onClick: () => void
    type?: '' | 'primary' | 'danger'
  }) =>
    h(ElButton, {
      class: 'icon-action',
      size: 'small',
      circle: true,
      plain: true,
      icon: options.icon,
      type: options.type,
      title: options.title,
      onClick: options.onClick
    })

  const renderActions = (row: Api.Data.NewsListItem) =>
    h('div', { class: 'table-actions' }, [
      hasAuth('data.news.update')
        ? renderIconAction({
            icon: Edit,
            title: t('permissions.edit'),
            onClick: () => openEditDialog(row)
          })
        : null,
      hasAuth('data.news.delete')
        ? renderIconAction({
            icon: Delete,
            title: t('permissions.delete'),
            type: 'danger',
            onClick: () => handleDelete(row)
          })
        : null,
      hasAuth('data.news.analyze')
        ? renderIconAction({
            icon: TrendCharts,
            title: t('data.news.analysis'),
            type: 'primary',
            onClick: () => void goAnalysis(row)
          })
        : null
    ])

  const { columns, columnChecks } = useTableColumns<Api.Data.NewsListItem>(() => [
    {
      prop: 'imageUrl',
      label: t('data.news.columns.picture'),
      width: 110,
      align: 'center',
      formatter: (row) =>
        row.imageUrl
          ? h(ElImage, {
              src: row.imageUrl,
              fit: 'cover',
              class: 'news-cover',
              previewSrcList: [row.imageUrl],
              previewTeleported: true
            })
          : '--'
    },
    {
      prop: 'sourceMessageId',
      label: t('data.news.columns.messageId'),
      minWidth: 140,
      align: 'center'
    },
    {
      prop: 'title',
      label: t('data.news.columns.title'),
      minWidth: 140,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'summary',
      label: t('data.news.columns.summary'),
      minWidth: 140,
      align: 'center',
      showOverflowTooltip: true
    },
    {
      prop: 'publishedAt',
      label: t('data.news.columns.publishTime'),
      minWidth: 140,
      align: 'center'
    },
    {
      prop: 'sourceUrl',
      label: t('data.news.columns.source'),
      minWidth: 140,
      align: 'center',
      formatter: (row) =>
        row.sourceUrl || row.originalUrl
          ? h(
              ElLink,
              {
                href: row.sourceUrl || row.originalUrl,
                target: '_blank',
                type: 'primary'
              },
              () => t('data.news.sourceLink')
            )
          : '--'
    },
    {
      prop: 'operation',
      label: t('data.news.columns.action'),
      width: 150,
      fixed: 'right',
      align: 'center',
      formatter: (row) => renderActions(row)
    }
  ])

  const loadNews = async () => {
    loading.value = true
    try {
      const data = await fetchNewsList({
        current: pagination.current,
        size: pagination.size,
        keyword: searchForm.keyword || undefined
      })
      records.value = data.records
      pagination.total = data.total
    } finally {
      loading.value = false
    }
  }

  const handleSearch = () => {
    pagination.current = 1
    loadNews()
  }

  const handleReset = () => {
    searchForm.keyword = ''
    pagination.current = 1
    loadNews()
  }

  const handleCurrentChange = (current: number) => {
    pagination.current = current
    loadNews()
  }

  const handleSizeChange = (size: number) => {
    pagination.size = size
    pagination.current = 1
    loadNews()
  }

  const openAddDialog = () => {
    dialogType.value = 'add'
    currentNews.value = null
    dialogVisible.value = true
  }

  const openEditDialog = (row: Api.Data.NewsListItem) => {
    dialogType.value = 'edit'
    currentNews.value = row
    dialogVisible.value = true
  }

  const handleSubmit = async (payload: NewsFormPayload) => {
    if (dialogType.value === 'add') {
      await fetchCreateNews(payload)
    } else if (currentNews.value) {
      await fetchUpdateNews(currentNews.value.id, payload)
    }
    dialogVisible.value = false
    loadNews()
  }

  const handleDelete = async (row: Api.Data.NewsListItem) => {
    try {
      await ElMessageBox.confirm(
        t('data.news.deleteConfirm', { title: row.title }),
        t('common.tips'),
        {
          type: 'warning',
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel')
        }
      )
    } catch {
      return
    }
    await fetchDeleteNews(row.id)
    if (records.value.length === 1 && pagination.current > 1) {
      pagination.current -= 1
    }
    loadNews()
  }

  const consumeAssistantQuery = () => {
    if (route.query.assistantAgent !== 'news_analysis') return

    const newsId = Number(route.query.newsId)
    if (!Number.isFinite(newsId) || newsId <= 0) return

    const nextQuery = { ...route.query }
    delete nextQuery.assistantAgent
    delete nextQuery.newsId

    const target = records.value.find((item) => item.id === newsId)
    void ensureNewsAssistantReady().then((ready) => {
      if (ready) {
        mittBus.emit('openChat', {
          agentCode: 'news_analysis',
          newsId,
          newsTitle: target?.title,
          autoRun: true
        })
      }
      router.replace({
        path: route.path,
        query: nextQuery
      })
    })
  }

  const goAnalysis = async (row: Api.Data.NewsListItem) => {
    if (!(await ensureNewsAssistantReady())) return
    mittBus.emit('openChat', {
      agentCode: 'news_analysis',
      newsId: row.id,
      newsTitle: row.title,
      autoRun: true
    })
  }

  onMounted(async () => {
    await loadNews()
    consumeAssistantQuery()
  })

  watch(
    () => route.fullPath,
    () => {
      consumeAssistantQuery()
    }
  )
</script>

<style scoped lang="scss">
  .table-actions {
    display: flex;
    justify-content: center;
    gap: 8px;
  }

  .news-cover {
    width: 72px;
    height: 54px;
    border-radius: 14px;
    overflow: hidden;
    border: 1px solid var(--art-card-border);
  }
</style>
