<template>
  <section
    v-if="value"
    class="art-notification-panel art-card-sm"
    aria-label="站内收件箱"
    @click.stop
  >
    <header
      ><h2>站内收件箱</h2
      ><ElButton link :disabled="!unreadCount" @click="notificationStore.markAllRead"
        >全部已读</ElButton
      ></header
    >
    <ElAlert v-if="error" :title="error" type="error" :closable="false" />
    <div v-loading="loading" class="notice-list">
      <button
        v-for="item in records"
        :key="item.id"
        :class="{ read: item.isRead }"
        @click="open(item)"
      >
        <strong>{{ item.title }}</strong
        ><p>{{ item.message }}</p
        ><time>{{ formatDateTime(item.createdAt) }}</time
        ><span v-if="!item.isRead">未读</span>
      </button>
      <ElEmpty v-if="!loading && !records.length" description="暂无站内通知" />
      <ElButton
        v-if="notificationStore.hasMore"
        @click="notificationStore.loadNotices({ append: true })"
        >加载更多</ElButton
      >
    </div>
  </section>
</template>
<script setup lang="ts">
  import { useNotificationStore } from '@/store/modules/notification'
  import { formatDateTime } from '@/utils/date'
  defineOptions({ name: 'ArtNotification' })
  const props = defineProps<{ value: boolean }>()
  const notificationStore = useNotificationStore()
  const { records, loading, unreadCount } = storeToRefs(notificationStore)
  const error = ref('')
  const open = async (item: Api.Notifications.InAppNoticeItem) => {
    try {
      if (!item.isRead) await notificationStore.markRead(item.id)
    } catch (cause: any) {
      error.value = cause.message
    }
  }
  watch(
    () => props.value,
    async (visible) => {
      if (!visible) return
      error.value = ''
      try {
        await notificationStore.loadNotices()
      } catch (cause: any) {
        error.value = cause.message
      }
    }
  )
</script>
<style scoped>
  .art-notification-panel {
    position: absolute;
    top: 58px;
    right: 20px;
    width: min(360px, calc(100vw - 24px));
    max-height: min(500px, 75vh);
    overflow: auto;
    z-index: 100;
    background: var(--el-bg-color);
    padding: 16px;
    box-shadow: var(--el-box-shadow);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  h2 {
    font-size: 16px;
    margin: 0;
  }
  .notice-list > button:not(.el-button) {
    width: 100%;
    text-align: left;
    background: none;
    border: 0;
    border-bottom: 1px solid var(--el-border-color);
    color: var(--el-text-color-primary);
    padding: 14px 0;
    cursor: pointer;
  }
  .notice-list p {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    margin: 6px 0;
  }
  .notice-list time {
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }
  .notice-list span {
    float: right;
    font-size: 12px;
    color: var(--el-color-primary);
  }
  .read {
    opacity: 0.7;
  }
</style>
