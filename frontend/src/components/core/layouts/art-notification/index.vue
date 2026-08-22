<!-- 通用组件：art-notification/index。 -->
<template>
  <div
    v-show="visible"
    class="art-notification-panel art-card-sm !shadow-xl"
    :style="{
      transform: show ? 'scaleY(1)' : 'scaleY(0.9)',
      opacity: show ? 1 : 0
    }"
    @click.stop
  >
    <div class="flex-cb px-3.5 mt-3.5">
      <span class="text-base font-medium text-g-800">{{ $t('notice.title') }}</span>
      <div class="notification-actions">
        <span
          class="text-xs text-g-800 px-1.5 py-1 c-p select-none rounded hover:bg-g-200"
          :class="{ 'opacity-50 pointer-events-none': !unreadCount }"
          @click="handleReadAll"
        >
          {{ $t('notice.btnRead') }}
        </span>
      </div>
    </div>

    <ul class="box-border flex items-end w-full h-12.5 px-3.5 border-b-d">
      <li
        v-for="(item, index) in barList"
        :key="index"
        class="h-12 leading-12 mr-5 overflow-hidden text-[13px] text-g-700 c-p select-none"
        :class="{ 'bar-active': barActiveIndex === index }"
        @click="changeBar(index)"
      >
        {{ item.name }} ({{ item.num }})
      </li>
    </ul>

    <div class="w-full h-[calc(100%-95px)]">
      <div class="h-[calc(100%-60px)] overflow-y-auto scrollbar-thin">
        <div v-if="loading && barActiveIndex === 0 && !noticeList.length" class="px-3.5 pt-3.5">
          <ElSkeleton animated :rows="4" />
        </div>

        <ul v-show="barActiveIndex === 0 && noticeList.length">
          <li
            v-for="item in noticeList"
            :key="item.id"
            class="box-border flex-c px-3.5 py-3.5 c-p last:border-b-0 hover:bg-g-200/60"
            :class="{ 'opacity-70': item.isRead }"
            @click="handleOpenRecord(item)"
          >
            <div
              class="relative size-9 leading-9 text-center rounded-lg flex-cc bg-theme/12 text-theme"
            >
              <ArtSvgIcon class="text-lg !bg-transparent" icon="ri:notification-3-line" />
              <span v-if="!item.isRead" class="notice-badge-dot"></span>
            </div>
            <div class="w-[calc(100%-45px)] ml-3.5 overflow-hidden">
              <h4 class="text-sm font-normal leading-5.5 text-g-900 line-clamp-2">{{
                item.messageTitle
              }}</h4>
              <p class="mt-1.5 text-xs text-g-500 line-clamp-2">{{
                item.messageContent || item.createdAt
              }}</p>
              <p class="mt-1 text-xs text-g-400">{{ item.createdAt }}</p>
            </div>
          </li>
        </ul>

        <ul v-show="barActiveIndex === 1 && msgList.length">
          <li
            v-for="(item, index) in msgList"
            :key="index"
            class="box-border flex-c px-3.5 py-3.5 c-p last:border-b-0 hover:bg-g-200/60"
          >
            <div class="size-9 leading-9 text-center rounded-lg flex-cc bg-success/12 text-success">
              <ArtSvgIcon class="text-lg !bg-transparent" icon="ri:message-3-line" />
            </div>
            <div class="w-[calc(100%-45px)] ml-3.5 overflow-hidden">
              <h4 class="text-sm font-normal leading-5.5 text-g-900 line-clamp-2">{{
                item.title
              }}</h4>
              <p class="mt-1.5 text-xs text-g-500">{{ item.time }}</p>
            </div>
          </li>
        </ul>

        <ul v-show="barActiveIndex === 2 && pendingList.length">
          <li
            v-for="item in pendingList"
            :key="item.id"
            class="box-border px-3.5 py-3.5 last:border-b-0"
          >
            <div class="flex items-start gap-3">
              <div
                class="size-9 shrink-0 leading-9 text-center rounded-lg flex-cc bg-warning/12 text-warning"
              >
                <ArtSvgIcon class="text-lg !bg-transparent" icon="ri:exchange-dollar-line" />
              </div>
              <div class="min-w-0 flex-1">
                <h4 class="text-sm font-normal leading-5.5 text-g-900 line-clamp-2">{{
                  item.messageTitle
                }}</h4>
                <p class="mt-1 text-xs text-g-500 line-clamp-2">{{ item.messageContent }}</p>
                <p class="mt-1 text-xs text-g-400">
                  {{ item.strategySignalExpiresAt || item.createdAt }}
                </p>
              </div>
            </div>
            <div class="mt-3 grid grid-cols-2 gap-2">
              <ElButton
                size="small"
                type="primary"
                :icon="Check"
                :loading="decisionLoading === `${item.strategySignalId}:approved`"
                :disabled="Boolean(decisionLoading)"
                @click.stop="handleSignalDecision(item, 'approved')"
              >
                {{ t('notice.decision.approve') }}
              </ElButton>
              <ElButton
                size="small"
                type="danger"
                plain
                :icon="Close"
                :loading="decisionLoading === `${item.strategySignalId}:rejected`"
                :disabled="Boolean(decisionLoading)"
                @click.stop="handleSignalDecision(item, 'rejected')"
              >
                {{ t('notice.decision.reject') }}
              </ElButton>
            </div>
          </li>
        </ul>

        <div
          v-show="currentTabIsEmpty && !(loading && barActiveIndex === 0)"
          class="relative top-25 h-full text-g-500 text-center !bg-transparent"
        >
          <ArtSvgIcon icon="system-uicons:inbox" class="text-5xl" />
          <p class="mt-3.5 text-xs !bg-transparent">
            {{ $t('notice.text[0]') }}{{ barList[barActiveIndex].name }}
          </p>
        </div>
      </div>

      <div class="relative box-border w-full px-3.5">
        <ElButton
          class="w-full mt-3"
          :disabled="footerDisabled"
          :loading="loading"
          @click="handleFooterAction"
        >
          {{ footerLabel }}
        </ElButton>
      </div>
    </div>

    <div class="h-25"></div>
  </div>
</template>

<script setup lang="ts">
  import { useI18n } from 'vue-i18n'
  import { useRouter } from 'vue-router'
  import { Check, Close } from '@element-plus/icons-vue'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { fetchReauth } from '@/api/auth'
  import { fetchApproveStrategySignal, fetchRejectStrategySignal } from '@/api/signals'
  import { useNotificationStore } from '@/store/modules/notification'

  defineOptions({ name: 'ArtNotification' })

  const props = defineProps<{
    value: boolean
  }>()

  const emit = defineEmits<{
    'update:value': [value: boolean]
  }>()

  const { t } = useI18n()
  const router = useRouter()
  const notificationStore = useNotificationStore()
  const { records, loading, unreadCount } = storeToRefs(notificationStore)

  const show = ref(false)
  const visible = ref(false)
  const barActiveIndex = ref(0)
  const decisionLoading = ref<string | null>(null)

  const isPendingSignal = (item: Api.Notifications.InAppNoticeItem) =>
    Boolean(item.strategySignalId) &&
    item.strategySignalMode === 'manual' &&
    item.strategySignalStatus === 'active'

  const noticeList = computed(() => records.value.filter((item) => !isPendingSignal(item)))
  const msgList = computed(() => [] as Array<{ title: string; time: string }>)
  const pendingList = computed(() => records.value.filter(isPendingSignal))

  const barList = computed(() => [
    { name: t('notice.bar[0]'), num: noticeList.value.length },
    { name: t('notice.bar[1]'), num: msgList.value.length },
    { name: t('notice.bar[2]'), num: pendingList.value.length }
  ])

  const currentTabIsEmpty = computed(() => {
    if (barActiveIndex.value === 0) {
      return noticeList.value.length === 0
    }
    if (barActiveIndex.value === 1) {
      return msgList.value.length === 0
    }
    return pendingList.value.length === 0
  })

  const footerLabel = computed(() => t('notice.viewAll'))

  const footerDisabled = computed(() => false)

  watch(
    () => props.value,
    async (open) => {
      if (open) {
        visible.value = true
        setTimeout(() => {
          show.value = true
        }, 5)
        await notificationStore.loadNotices()
        return
      }
      show.value = false
      setTimeout(() => {
        visible.value = false
      }, 350)
    }
  )

  const changeBar = (index: number) => {
    barActiveIndex.value = index
  }

  const handleOpenRecord = async (item: Api.Notifications.InAppNoticeItem) => {
    if (!item.isRead) {
      await notificationStore.markRead(item.id)
    }
  }

  const handleReadAll = async () => {
    if (!unreadCount.value) {
      return
    }
    await notificationStore.markAllRead()
  }

  const handleSignalDecision = async (
    item: Api.Notifications.InAppNoticeItem,
    decision: 'approved' | 'rejected'
  ) => {
    const signalId = item.strategySignalId
    if (!signalId || decisionLoading.value) {
      return
    }
    const actionKey = `${signalId}:${decision}`
    decisionLoading.value = actionKey
    try {
      let result: Api.Notifications.StrategySignalDecision
      const idempotencyKey = `coinsphere-signal:${signalId}:${decision}`
      if (decision === 'approved') {
        const prompt = await ElMessageBox.prompt(
          t('notice.decision.reauthMessage'),
          t('notice.decision.reauthTitle'),
          {
            inputType: 'password',
            inputPlaceholder: t('notice.decision.passwordPlaceholder'),
            inputValidator: (value) => Boolean(String(value || '').trim()),
            inputErrorMessage: t('notice.decision.passwordRequired'),
            confirmButtonText: t('notice.decision.approve'),
            cancelButtonText: t('common.cancel')
          }
        )
        const reauth = await fetchReauth(prompt.value)
        result = await fetchApproveStrategySignal(signalId, idempotencyKey, reauth.reauthToken)
      } else {
        await ElMessageBox.confirm(
          t('notice.decision.rejectMessage'),
          t('notice.decision.rejectTitle'),
          {
            type: 'warning',
            confirmButtonText: t('notice.decision.reject'),
            cancelButtonText: t('common.cancel')
          }
        )
        result = await fetchRejectStrategySignal(signalId, idempotencyKey)
      }
      notificationStore.applySignalDecision(signalId, result.status)
      if (!item.isRead) {
        try {
          await notificationStore.markRead(item.id)
        } catch {
          // The decision is already committed; the request layer reports the independent read-state failure.
        }
      }
    } catch (error: any) {
      if (
        error === 'cancel' ||
        error === 'close' ||
        error?.action === 'cancel' ||
        error?.action === 'close'
      ) {
        return
      }
      ElMessage.error(error?.message || t('notice.decision.failed'))
    } finally {
      decisionLoading.value = null
    }
  }

  const handleFooterAction = async () => {
    emit('update:value', false)
    await router.push('/workbench')
  }
</script>

<style scoped>
  @reference '@styles/core/tailwind.css';

  .art-notification-panel {
    @apply absolute
    top-14.5
    right-5
    w-90
    h-125
    overflow-hidden
    transition-all
    duration-300
    origin-top
    will-change-[top,left]
    max-[640px]:top-[65px]
    max-[640px]:right-0
    max-[640px]:w-full
    max-[640px]:h-[80vh];
  }

  .bar-active {
    color: var(--theme-color) !important;
    border-bottom: 2px solid var(--theme-color);
  }

  .notification-actions {
    display: inline-flex;
    gap: 6px;
    align-items: center;
  }

  .notice-badge-dot {
    position: absolute;
    top: 3px;
    right: 3px;
    width: 7px;
    height: 7px;
    background: #ef4444;
    border-radius: 999px;
  }

  .scrollbar-thin::-webkit-scrollbar {
    width: 5px !important;
  }

  .dark .scrollbar-thin::-webkit-scrollbar-track {
    background-color: var(--default-box-color);
  }

  .dark .scrollbar-thin::-webkit-scrollbar-thumb {
    background-color: #222 !important;
  }
</style>
