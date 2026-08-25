<template>
  <section class="delivery-result" aria-labelledby="delivery-title">
    <header>
      <div>
        <p>Notification ledger</p>
        <h2 id="delivery-title">{{ view.name }}</h2>
      </div>
      <ElButton circle title="刷新" :loading="loading" @click="load">
        <ArtSvgIcon icon="ri:refresh-line" />
      </ElButton>
    </header>
    <ol v-loading="loading" class="delivery-list">
      <li v-for="delivery in deliveries" :key="delivery.id">
        <span class="delivery-list__mark" :data-status="delivery.status"></span>
        <div>
          <strong>{{ delivery.title }}</strong>
          <p>{{ delivery.message }}</p>
          <small>{{ delivery.subjectKey }} · {{ formatTime(delivery.createdAt) }} UTC</small>
        </div>
        <ElTag :type="delivery.status === 'delivered' ? 'success' : 'danger'" effect="plain">
          {{ delivery.status === 'delivered' ? '已送达' : '失败' }}
        </ElTag>
      </li>
      <li v-if="!deliveries.length" class="delivery-list__empty">暂无通知投递</li>
    </ol>
  </section>
</template>

<script setup lang="ts">
  import { fetchNotificationResult, type NotificationDelivery } from '@/api/paper'
  import type { ResultView } from '@/api/resultViews'

  const props = defineProps<{ view: ResultView }>()
  const deliveries = ref<NotificationDelivery[]>([])
  const loading = ref(false)
  const formatTime = (value: string) =>
    new Intl.DateTimeFormat('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
      timeZone: 'UTC'
    }).format(new Date(value))
  const load = async () => {
    loading.value = true
    try {
      deliveries.value = (await fetchNotificationResult(props.view.id)).items
    } finally {
      loading.value = false
    }
  }

  watch(() => props.view.id, load, { immediate: true })
</script>

<style scoped>
  .delivery-result {
    color: var(--el-text-color-primary);
    letter-spacing: 0;
  }

  .delivery-result header,
  .delivery-list > li {
    display: flex;
    align-items: center;
  }

  .delivery-result header {
    justify-content: space-between;
    min-height: 52px;
    padding-bottom: 14px;
    border-bottom: 1px solid var(--el-border-color);
  }

  .delivery-result header p,
  .delivery-result header h2,
  .delivery-list p {
    margin: 0;
  }

  .delivery-result header p,
  .delivery-list small {
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .delivery-result header h2 {
    margin-top: 3px;
    font-size: 20px;
    font-weight: 650;
  }

  .delivery-list {
    padding: 0;
    margin: 0;
    list-style: none;
  }

  .delivery-list > li {
    display: grid;
    grid-template-columns: 10px minmax(0, 1fr) auto;
    gap: 12px;
    min-height: 76px;
    padding: 12px 0;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .delivery-list__mark {
    width: 8px;
    height: 8px;
    background: var(--el-color-success);
    border-radius: 50%;
  }

  .delivery-list__mark[data-status='failed'] {
    background: var(--el-color-danger);
  }

  .delivery-list strong,
  .delivery-list p,
  .delivery-list small {
    display: block;
  }

  .delivery-list p {
    margin-top: 4px;
    font-size: 13px;
  }

  .delivery-list small {
    margin-top: 5px;
  }

  .delivery-list__empty {
    display: block !important;
    padding: 32px 0 !important;
    color: var(--el-text-color-secondary);
    text-align: center;
  }

  @media (max-width: 600px) {
    .delivery-list > li {
      grid-template-columns: 10px minmax(0, 1fr);
    }

    .delivery-list :deep(.el-tag) {
      grid-column: 2;
      justify-self: start;
    }
  }
</style>
