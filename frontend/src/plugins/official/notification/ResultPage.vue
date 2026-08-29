<template>
  <section class="delivery-result" aria-labelledby="delivery-title">
    <header>
      <div>
        <p>站内、钉钉、QQ 与邮件</p>
        <h2 id="delivery-title">通知投递</h2>
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
          <small>
            {{ channelLabel(delivery.channel) }} · {{ recipientLabel(delivery) }} · 尝试
            {{ delivery.attemptCount }} 次 · {{ delivery.subjectKey }} ·
            {{ formatTime(delivery.deliveredAt || delivery.createdAt) }} UTC+8
          </small>
          <small v-if="delivery.lastErrorCategory" class="delivery-list__error">
            错误类别：{{ delivery.lastErrorCategory }}
          </small>
        </div>
        <ElTag :type="statusType(delivery.status)" effect="plain">
          {{ statusLabel(delivery.status) }}
        </ElTag>
      </li>
      <li v-if="!deliveries.length" class="delivery-list__empty">暂无通知投递</li>
    </ol>
  </section>
</template>

<script setup lang="ts">
  import { fetchNotificationDeliveries, type NotificationDelivery } from '@/api/notifications'
  import { formatDateTime as formatTime } from '@/utils/date'

  const deliveries = ref<NotificationDelivery[]>([])
  const channelLabel = (channel: NotificationDelivery['channel']) =>
    ({ in_app: '站内通知', dingtalk: '钉钉', qq: 'QQ', smtp: '邮件' })[channel]
  const recipientLabel = (delivery: NotificationDelivery) =>
    delivery.recipientUserId ? `用户 #${delivery.recipientUserId}` : '外部目标'
  const statusLabel = (status: NotificationDelivery['status']) =>
    ({ pending: '发送中', delivered: '已送达', failed: '失败' })[status]
  const statusType = (status: NotificationDelivery['status']) =>
    status === 'delivered' ? 'success' : status === 'failed' ? 'danger' : 'info'
  const loading = ref(false)
  const load = async () => {
    loading.value = true
    try {
      deliveries.value = (await fetchNotificationDeliveries()).items
    } finally {
      loading.value = false
    }
  }

  onMounted(load)
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

  .delivery-list__mark[data-status='pending'] {
    background: var(--el-color-info);
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

  .delivery-list__error {
    color: var(--el-color-danger) !important;
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
