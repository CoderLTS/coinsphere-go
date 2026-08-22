<!-- 通用组件：art-chat-window/index。 -->
<template>
  <ElDrawer v-model="visible" :size="isMobile ? '100%' : '560px'" :with-header="false">
    <div class="chat-window">
      <div class="chat-header">
        <span class="chat-header__title">{{ t('assistant.title') }}</span>

        <div class="chat-select chat-select--agent">
          <ElSelect
            v-model="currentAgentCode"
            class="chat-select__control"
            popper-class="chat-window__select-popper"
            :disabled="loadingAgents || loadingConversation || isStreaming"
            @change="handleAgentChange"
          >
            <ElOption
              v-for="item in agentList"
              :key="item.code"
              :label="item.displayName"
              :value="item.code"
            />
          </ElSelect>
        </div>

        <div class="chat-header__actions">
          <button
            type="button"
            class="chat-icon-button"
            :disabled="historyDisabled"
            @click="openHistory"
          >
            <ElIcon><ChatDotRound /></ElIcon>
          </button>
          <button
            type="button"
            class="chat-icon-button"
            :disabled="newSessionDisabled"
            @click="startNewSession"
          >
            <ElIcon><CirclePlus /></ElIcon>
          </button>
          <button
            v-if="canRetry"
            type="button"
            class="chat-icon-button"
            :disabled="loadingConversation || isStreaming"
            @click="retryAnalysis"
          >
            <ElIcon><RefreshRight /></ElIcon>
          </button>
        </div>

        <button type="button" class="chat-icon-button chat-icon-button--close" @click="closeChat">
          <ElIcon><Close /></ElIcon>
        </button>
      </div>

      <div
        ref="messageContainer"
        v-loading="loadingConversation"
        class="chat-body"
        :class="{ 'chat-body--empty': showEmptyState }"
      >
        <ElAlert
          v-if="hintText"
          class="chat-body__alert"
          :title="hintText"
          type="info"
          :closable="false"
        />

        <div
          v-if="showEmptyState"
          class="chat-empty"
          :class="{ 'chat-empty--no-model': !availableModels.length }"
        >
          <ElEmpty class="chat-empty__main" :image-size="82">
            <template #description>
              <div class="chat-empty__copy">
                <div class="chat-empty__title">{{ emptyPrimaryText }}</div>
                <div class="chat-empty__text">{{ emptySecondaryText }}</div>
              </div>
            </template>

            <ElButton
              v-if="!availableModels.length"
              class="chat-empty__action"
              plain
              @click="goToModelConfig"
            >
              {{ t('assistant.goModelConfig') }}
            </ElButton>

            <div v-else-if="showStarterPrompts" class="chat-starters">
              <ElButton
                v-for="prompt in currentAgent?.starterPrompts || []"
                :key="prompt"
                size="small"
                plain
                @click="messageText = prompt"
              >
                {{ prompt }}
              </ElButton>
            </div>
          </ElEmpty>
        </div>

        <template v-else>
          <div
            v-for="item in messages"
            :key="item.id"
            class="chat-message"
            :class="{ 'chat-message--me': item.isMe }"
          >
            <ElAvatar :size="34" :src="item.isMe ? userAvatar : currentAgentAvatar">{{
              item.sender.slice(0, 1)
            }}</ElAvatar>
            <div class="chat-message__content">
              <div class="chat-message__meta">
                <span>{{ item.sender }}</span>
                <span>{{ item.createdAt.slice(11, 16) }}</span>
              </div>
              <div v-if="item.reasoning && !item.isMe" class="chat-message__reasoning">
                <strong>{{ t('assistant.reasoning') }}</strong>
                <div class="chat-message__reasoning-panel">
                  <AssistantRichText :content="item.reasoning" />
                </div>
              </div>
              <div class="chat-message__bubble" :class="{ 'chat-message__bubble--me': item.isMe }">
                <template v-if="item.isMe">{{
                  item.content || t('assistant.generating')
                }}</template>
                <div
                  v-else-if="item.contentType === 'streaming' && !item.content && !item.reasoning"
                  class="chat-message__loading"
                >
                  <div class="chat-message__loading-dots">
                    <span></span>
                    <span></span>
                    <span></span>
                  </div>
                  <span>{{ t('assistant.generating') }}</span>
                </div>
                <AssistantRichText
                  v-else
                  :streaming="item.contentType === 'streaming'"
                  :content="item.content || t('assistant.generating')"
                />
              </div>
            </div>
          </div>
        </template>
      </div>

      <div class="chat-footer">
        <div v-if="isNewsAgent && currentSession?.newsId" class="chat-footer__hint">
          {{ t('assistant.newsScope') }} #{{ currentSession.newsId }}
        </div>

        <div class="chat-footer__panel">
          <ElInput
            v-model="messageText"
            type="textarea"
            :autosize="{ minRows: 3, maxRows: 7 }"
            resize="none"
            :placeholder="inputPlaceholder"
            :disabled="inputDisabled"
            @keydown.enter.exact.prevent="sendMessage"
          />
          <div class="chat-footer__toolbar">
            <div class="chat-footer__toolbar-main">
              <div class="chat-select chat-select--model">
                <ElSelect
                  v-model="selectedModelId"
                  class="chat-select__control"
                  clearable
                  filterable
                  popper-class="chat-window__select-popper chat-window__select-popper--model"
                  :loading="loadingModels"
                  :disabled="toolDisabled"
                  :placeholder="t('assistant.modelPlaceholder')"
                  @change="handleModelChange"
                >
                  <ElOption
                    v-for="item in availableModels"
                    :key="item.id"
                    :label="item.displayName"
                    :value="item.id"
                  />
                </ElSelect>
              </div>

              <button
                type="button"
                class="chat-toggle"
                :class="{ 'chat-toggle--active': reasoningEnabled }"
                :disabled="toolDisabled"
                @click="reasoningEnabled = !reasoningEnabled"
              >
                <span class="chat-toggle__dot"></span>
                {{ t('assistant.reasoningToggle') }}
              </button>
            </div>

            <ElButton
              class="chat-footer__send-button"
              circle
              :type="isStreaming ? 'danger' : 'primary'"
              :disabled="!isStreaming && !canSend"
              @click="handlePrimaryAction"
            >
              <ElIcon><component :is="isStreaming ? VideoPause : Promotion" /></ElIcon>
            </ElButton>
          </div>
        </div>
        <div class="chat-footer__text">{{ footerHint }}</div>
      </div>
    </div>
  </ElDrawer>

  <ElDrawer
    v-model="historyVisible"
    :size="isMobile ? '100%' : '300px'"
    :with-header="false"
    append-to-body
  >
    <div class="history-panel">
      <div class="history-panel__header">
        <div>
          <div class="history-panel__title">{{ t('assistant.history') }}</div>
          <div class="history-panel__subtitle">{{ currentAgent?.displayName || '--' }}</div>
        </div>
        <button
          type="button"
          class="chat-icon-button chat-icon-button--close"
          @click="historyVisible = false"
        >
          <ElIcon><Close /></ElIcon>
        </button>
      </div>

      <div
        ref="historyContainer"
        v-loading="historyLoading && !historyItems.length"
        class="history-panel__body"
        @scroll.passive="handleHistoryScroll"
      >
        <ElEmpty
          v-if="!historyLoading && !historyItems.length"
          :description="t('assistant.historyEmpty')"
        />

        <div
          v-for="item in historyItems"
          :key="item.id"
          class="history-item"
          :class="{ 'history-item--active': currentSession?.id === item.id }"
        >
          <button type="button" class="history-item__main" @click="openHistorySession(item)">
            <div class="history-item__title">{{ item.title || item.agentName }}</div>
            <div class="history-item__meta">
              <span>{{ item.lastMessageAt || item.updatedAt }}</span>
            </div>
          </button>
          <ElButton
            circle
            text
            :disabled="deletingSessionId === item.id"
            @click.stop="deleteHistorySession(item)"
          >
            <ElIcon v-if="deletingSessionId === item.id" class="is-loading"><Loading /></ElIcon>
            <ElIcon v-else><Delete /></ElIcon>
          </ElButton>
        </div>
      </div>
    </div>
  </ElDrawer>
</template>

<script setup lang="ts">
  import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
  import { useStorage, useWindowSize } from '@vueuse/core'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import {
    ChatDotRound,
    CirclePlus,
    Close,
    Delete,
    Loading,
    Promotion,
    RefreshRight,
    VideoPause
  } from '@element-plus/icons-vue'
  import { useI18n } from 'vue-i18n'
  import { useRouter } from 'vue-router'
  import defaultAvatar from '@imgs/user/avatar.webp'
  import assistantAvatar from '@/assets/images/avatar/avatar10.webp'
  import AssistantRichText from './AssistantRichText.vue'
  import {
    deleteAssistantSession,
    fetchAssistantAgents,
    fetchAssistantMessages,
    fetchAssistantModelOptions,
    fetchAssistantSession,
    fetchAssistantSessions,
    streamAssistantSession
  } from '@/api/assistant'
  import { useUserStore } from '@/store/modules/user'
  import { mittBus, type OpenChatPayload } from '@/utils/sys'

  interface ChatMessage {
    id: number | string
    sender: string
    content: string
    reasoning: string
    createdAt: string
    isMe: boolean
    contentType: string
  }

  const { t } = useI18n()
  const router = useRouter()
  const userStore = useUserStore()
  const { width } = useWindowSize()

  const visible = ref(false)
  const historyVisible = ref(false)
  const loadingAgents = ref(false)
  const loadingConversation = ref(false)
  const loadingModels = ref(false)
  const historyLoading = ref(false)
  const historyLoadingMore = ref(false)
  const historyHasMore = ref(true)
  const historyCursor = ref('')
  const isStreaming = ref(false)
  const deletingSessionId = ref<number | null>(null)
  const currentAgentCode = ref<Api.Assistant.AgentCode>('system_general')
  const currentPayload = ref<OpenChatPayload | null>(null)
  const currentSession = ref<Api.Assistant.Session | null>(null)
  const currentNewsTitle = ref('')
  const selectedModelId = ref<number | null>(null)
  const modelOptions = ref<Api.Assistant.ModelOptions | null>(null)
  const agentList = ref<Api.Assistant.AgentSummary[]>([])
  const messages = ref<ChatMessage[]>([])
  const historyItems = ref<Api.Assistant.SessionHistoryItem[]>([])
  const messageText = ref('')
  const messageContainer = ref<HTMLElement | null>(null)
  const historyContainer = ref<HTMLElement | null>(null)
  const streamAbortController = ref<AbortController | null>(null)
  const reasoningEnabled = useStorage<boolean>('coinsphere-assistant-reasoning-enabled', true)

  const isMobile = computed(() => width.value < 640)
  const userAvatar = computed(() => userStore.getUserInfo.avatar || defaultAvatar)
  const currentAgent = computed(
    () => agentList.value.find((item) => item.code === currentAgentCode.value) || null
  )
  const currentAgentAvatar = computed(() => currentAgent.value?.avatar || assistantAvatar)
  const isNewsAgent = computed(() => currentAgent.value?.dataSourceType === 'news_context')
  const availableModels = computed(() => modelOptions.value?.models || [])
  const hintText = computed(() => {
    if (!selectedModelId.value && availableModels.value.length > 1)
      return t('assistant.modelSelectionRequired')
    return ''
  })
  const emptyPrimaryText = computed(() => {
    if (!availableModels.value.length) return t('assistant.noModelsTitle')
    return (
      currentAgent.value?.welcomeMessage ||
      (isNewsAgent.value ? t('assistant.emptyNews') : t('assistant.emptyGeneral'))
    )
  })
  const emptySecondaryText = computed(() => {
    if (!availableModels.value.length) return t('assistant.noModelsDescription')
    return isNewsAgent.value ? t('assistant.emptyNews') : t('assistant.emptyGeneral')
  })
  const showStarterPrompts = computed(() =>
    Boolean(
      currentAgent.value?.starterPrompts?.length &&
        availableModels.value.length &&
        !messages.value.length
    )
  )
  const inputPlaceholder = computed(() =>
    isNewsAgent.value ? t('assistant.placeholderNews') : t('assistant.placeholderGeneral')
  )
  const footerHint = computed(() =>
    isNewsAgent.value ? t('assistant.newsTip') : t('assistant.generalTip')
  )
  const inputDisabled = computed(
    () =>
      loadingConversation.value ||
      isStreaming.value ||
      !currentSession.value ||
      !selectedModelId.value
  )
  const toolDisabled = computed(
    () => loadingModels.value || loadingConversation.value || isStreaming.value
  )
  const canRetry = computed(
    () =>
      isNewsAgent.value && Boolean(currentSession.value?.newsId) && Boolean(selectedModelId.value)
  )
  const canSend = computed(() =>
    Boolean(
      messageText.value.trim() &&
        currentSession.value &&
        selectedModelId.value &&
        !loadingConversation.value &&
        !isStreaming.value
    )
  )
  const showEmptyState = computed(() => !loadingConversation.value && !messages.value.length)
  const historyDisabled = computed(
    () =>
      !currentPayload.value || loadingConversation.value || loadingModels.value || isStreaming.value
  )
  const newSessionDisabled = computed(
    () =>
      !currentPayload.value ||
      !selectedModelId.value ||
      loadingConversation.value ||
      loadingModels.value ||
      isStreaming.value
  )

  const normalizeMessage = (message: Api.Assistant.Message): ChatMessage => ({
    id: message.id,
    sender:
      message.role === 'user'
        ? userStore.getUserInfo.username
        : currentAgent.value?.displayName || t('assistant.title'),
    content: message.content || '',
    reasoning: message.reasoning || '',
    createdAt: message.createdAt,
    isMe: message.role === 'user',
    contentType: message.contentType
  })

  const scrollToBottom = () =>
    nextTick(() =>
      setTimeout(() => {
        if (messageContainer.value) {
          messageContainer.value.scrollTop = messageContainer.value.scrollHeight
        }
      }, 50)
    )

  const scrollReasoningPanelToBottom = () =>
    nextTick(() =>
      setTimeout(() => {
        const panels = messageContainer.value?.querySelectorAll('.chat-message__reasoning-panel')
        const latestPanel = panels?.item(panels.length - 1) as HTMLElement | null
        if (latestPanel) {
          latestPanel.scrollTop = latestPanel.scrollHeight
        }
      }, 50)
    )

  const abortStream = () => {
    streamAbortController.value?.abort()
    streamAbortController.value = null
    isStreaming.value = false
  }

  const resolveErrorMessage = (error: unknown, fallbackKey: string) => {
    if (error instanceof Error) {
      if (error.message === 'UNAUTHORIZED') return t('httpMsg.unauthorized')
      if (error.message.startsWith('ASSISTANT_STREAM_FAILED')) return t(fallbackKey)
      return error.message
    }
    return t(fallbackKey)
  }

  const ensureAgents = async () => {
    if (agentList.value.length) return
    loadingAgents.value = true
    try {
      agentList.value = await fetchAssistantAgents()
    } finally {
      loadingAgents.value = false
    }
  }

  const resetRuntimeState = () => {
    historyVisible.value = false
    modelOptions.value = null
    messages.value = []
    historyItems.value = []
    currentSession.value = null
    currentNewsTitle.value = ''
    selectedModelId.value = null
    deletingSessionId.value = null
    historyCursor.value = ''
    historyHasMore.value = true
    abortStream()
  }

  const resolvePreferredModelId = (
    payload: OpenChatPayload,
    options: Api.Assistant.ModelOptions
  ) => {
    const ids = new Set(options.models.map((item) => item.id))
    if (payload.modelConfigId && ids.has(payload.modelConfigId)) return payload.modelConfigId
    if (options.defaultModelId && ids.has(options.defaultModelId)) return options.defaultModelId
    return options.models.length === 1 ? options.models[0].id : null
  }

  const loadConversation = async (
    payload: OpenChatPayload,
    modelConfigId: number | null,
    forceNew = false
  ) => {
    loadingConversation.value = true
    messages.value = []
    currentSession.value = null
    try {
      const session = await fetchAssistantSession({
        agentCode: payload.agentCode,
        newsId: payload.newsId,
        modelConfigId: modelConfigId ?? undefined,
        forceNew
      })
      currentPayload.value = {
        ...payload,
        agentCode: session.agentCode,
        modelConfigId: session.modelConfigId || undefined,
        autoRun: false
      }
      currentSession.value = session
      currentAgentCode.value = session.agentCode
      selectedModelId.value = session.modelConfigId ?? modelConfigId
      currentNewsTitle.value = payload.newsTitle || session.title
      messages.value = (await fetchAssistantMessages(session.id)).map(normalizeMessage)
      scrollToBottom()
    } catch (error) {
      currentSession.value = null
      ElMessage.error(resolveErrorMessage(error, 'assistant.errors.loadConversation'))
    } finally {
      loadingConversation.value = false
    }
  }

  const prepareChat = async (payload: OpenChatPayload) => {
    visible.value = true
    resetRuntimeState()
    currentPayload.value = { ...payload }
    currentAgentCode.value = payload.agentCode
    currentNewsTitle.value = payload.newsTitle || ''
    let shouldAutoAnalyze = false

    try {
      await ensureAgents()
      const agent = agentList.value.find((item) => item.code === payload.agentCode)
      if (!agent) throw new Error(t('assistant.errors.agentUnavailable'))
      if (agent.dataSourceType === 'news_context' && !payload.newsId) {
        throw new Error(t('assistant.errors.newsContextRequired'))
      }

      loadingModels.value = true
      const options = await fetchAssistantModelOptions(payload.agentCode)
      modelOptions.value = options
      const preferredModelId = resolvePreferredModelId(payload, options)
      selectedModelId.value = preferredModelId

      if (!options.models.length) {
        return
      }

      await loadConversation(payload, payload.modelConfigId ?? preferredModelId ?? null)
      shouldAutoAnalyze =
        payload.autoRun === true &&
        agent.dataSourceType === 'news_context' &&
        !messages.value.some((item) => item.contentType === 'analysis_result')
    } catch (error) {
      ElMessage.error(resolveErrorMessage(error, 'assistant.errors.loadModels'))
    } finally {
      loadingModels.value = false
    }

    if (shouldAutoAnalyze && currentSession.value) {
      await startStream('analyze')
    }
  }

  const loadHistory = async (reset = false) => {
    if (!currentPayload.value) return
    if (reset) {
      historyCursor.value = ''
      historyHasMore.value = true
      historyItems.value = []
    }
    if (!historyHasMore.value && !reset) return
    const loadingRef = reset ? historyLoading : historyLoadingMore
    if (loadingRef.value) return
    loadingRef.value = true
    try {
      const result = await fetchAssistantSessions({
        agentCode: currentPayload.value.agentCode,
        cursor: historyCursor.value || undefined,
        limit: 10
      })
      historyItems.value = reset ? result.records : [...historyItems.value, ...result.records]
      historyHasMore.value = Boolean(result.hasMore)
      historyCursor.value = result.nextCursor
    } catch (error) {
      ElMessage.error(resolveErrorMessage(error, 'assistant.errors.loadHistory'))
    } finally {
      loadingRef.value = false
    }
  }

  const startStream = async (mode: Api.Assistant.StreamMode, text = '') => {
    if (!currentSession.value || !selectedModelId.value) return

    const controller = new AbortController()
    const userDraftId = text ? `user-${Date.now()}` : ''
    const assistantDraftId = `assistant-${Date.now()}`

    if (text) {
      messages.value.push({
        id: userDraftId,
        sender: userStore.getUserInfo.username,
        content: text,
        reasoning: '',
        createdAt: new Date().toISOString(),
        isMe: true,
        contentType: 'text'
      })
    }

    messages.value.push({
      id: assistantDraftId,
      sender: currentAgent.value?.displayName || t('assistant.title'),
      content: '',
      reasoning: '',
      createdAt: new Date().toISOString(),
      isMe: false,
      contentType: 'streaming'
    })

    scrollToBottom()
    abortStream()
    streamAbortController.value = controller
    isStreaming.value = true

    try {
      await streamAssistantSession(
        currentSession.value.id,
        {
          agentCode: currentAgentCode.value,
          mode,
          text: text || undefined,
          newsId: currentSession.value.newsId || undefined,
          enableReasoning: reasoningEnabled.value
        },
        {
          onUser: (message) => {
            const index = messages.value.findIndex((item) => item.id === userDraftId)
            if (index >= 0) messages.value.splice(index, 1, normalizeMessage(message))
          },
          onReasoning: (chunk) => {
            if (!reasoningEnabled.value) return
            const item = messages.value.find((entry) => entry.id === assistantDraftId)
            if (item) item.reasoning += chunk
            scrollReasoningPanelToBottom()
            scrollToBottom()
          },
          onContent: (chunk) => {
            const item = messages.value.find((entry) => entry.id === assistantDraftId)
            if (item) item.content += chunk
            scrollToBottom()
          },
          onDone: ({ message, session }) => {
            if (message) {
              const index = messages.value.findIndex((item) => item.id === assistantDraftId)
              if (index >= 0) messages.value.splice(index, 1, normalizeMessage(message))
            }
            if (session) currentSession.value = session
            void loadHistory(true)
          },
          onError: ({ msg }) => {
            if (msg) ElMessage.error(msg)
          }
        },
        controller.signal
      )
    } catch (error) {
      if (!(error instanceof DOMException && error.name === 'AbortError')) {
        ElMessage.error(resolveErrorMessage(error, 'assistant.errors.stream'))
      }
    } finally {
      if (streamAbortController.value === controller) streamAbortController.value = null
      isStreaming.value = false
    }
  }

  const sendMessage = async () => {
    const text = messageText.value.trim()
    if (!text) return
    messageText.value = ''
    await startStream('chat', text)
  }

  const retryAnalysis = async () => {
    if (canRetry.value) await startStream('retry')
  }

  const startNewSession = async () => {
    if (currentPayload.value && selectedModelId.value) {
      await loadConversation(currentPayload.value, selectedModelId.value, true)
      await loadHistory(true)
    }
  }

  const handlePrimaryAction = async () => {
    if (isStreaming.value) {
      abortStream()
      return
    }
    await sendMessage()
  }

  const openHistory = async () => {
    historyVisible.value = true
    await loadHistory(true)
  }

  const handleHistoryScroll = async () => {
    const node = historyContainer.value
    if (!node || historyLoading.value || historyLoadingMore.value || !historyHasMore.value) return
    if (node.scrollTop + node.clientHeight >= node.scrollHeight - 28) {
      await loadHistory(false)
    }
  }

  const openHistorySession = async (item: Api.Assistant.SessionHistoryItem) => {
    historyVisible.value = false
    currentAgentCode.value = item.agentCode
    currentPayload.value = {
      agentCode: item.agentCode,
      newsId: item.newsId || undefined,
      newsTitle: item.title,
      autoRun: false,
      modelConfigId: item.modelConfigId || undefined
    }
    currentSession.value = item
    currentNewsTitle.value = item.newsId ? item.title : ''
    selectedModelId.value = item.modelConfigId ?? null
    try {
      modelOptions.value = await fetchAssistantModelOptions(item.agentCode)
      messages.value = (await fetchAssistantMessages(item.id)).map(normalizeMessage)
      scrollToBottom()
    } catch (error) {
      ElMessage.error(resolveErrorMessage(error, 'assistant.errors.loadConversation'))
    }
  }

  const deleteHistorySession = async (item: Api.Assistant.SessionHistoryItem) => {
    try {
      await ElMessageBox.confirm(
        t('assistant.deleteSessionConfirm', { title: item.title || item.agentName }),
        t('assistant.deleteSession'),
        {
          type: 'warning',
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel')
        }
      )
    } catch {
      return
    }

    deletingSessionId.value = item.id
    try {
      await deleteAssistantSession(item.id)
      ElMessage.success(t('assistant.deleteSessionSuccess'))
      await loadHistory(true)
      if (currentSession.value?.id === item.id) {
        currentSession.value = null
        messages.value = []
      }
    } catch (error) {
      ElMessage.error(resolveErrorMessage(error, 'assistant.errors.deleteSession'))
    } finally {
      deletingSessionId.value = null
    }
  }

  const handleModelChange = async (value: number | null) => {
    selectedModelId.value = value
    if (currentPayload.value && value) {
      await loadConversation({ ...currentPayload.value, modelConfigId: value }, value)
    }
  }

  const handleAgentChange = async (agentCode: string) => {
    if (!agentCode || agentCode === currentPayload.value?.agentCode) return
    const target = agentList.value.find((item) => item.code === agentCode)
    if (!target) return

    if (target.dataSourceType === 'news_context') {
      const newsId = currentPayload.value?.newsId || currentSession.value?.newsId
      if (!newsId) {
        ElMessage.warning(t('assistant.errors.newsContextRequired'))
        currentAgentCode.value = currentPayload.value?.agentCode || currentAgentCode.value
        return
      }
      await prepareChat({ agentCode, newsId, newsTitle: currentNewsTitle.value, autoRun: false })
      return
    }

    await prepareChat({ agentCode, autoRun: false })
  }

  const handleOpenChat = async (payload: OpenChatPayload) => {
    if (userStore.accessMode !== 'authenticated') return
    await prepareChat(payload)
  }

  const closeChat = () => {
    abortStream()
    historyVisible.value = false
    visible.value = false
  }

  const goToModelConfig = () => {
    closeChat()
    void router.push('/workbench')
  }

  onMounted(() => {
    mittBus.on('openChat', handleOpenChat)
  })

  onUnmounted(() => {
    abortStream()
    mittBus.off('openChat', handleOpenChat)
  })
</script>

<style scoped lang="scss">
  .chat-window,
  .history-panel {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .chat-header {
    display: flex;
    gap: 12px;
    align-items: center;
    min-height: 56px;
    padding-bottom: 16px;
    border-bottom: 1px solid rgb(226 232 240 / 0.78);
  }

  .chat-header__title,
  .history-panel__title {
    font-size: 20px;
    font-weight: 600;
  }

  .chat-header__title {
    flex: 0 0 auto;
  }

  .chat-header__actions,
  .history-panel__header,
  .chat-footer__toolbar,
  .chat-footer__toolbar-main,
  .history-item {
    display: flex;
    align-items: center;
  }

  .chat-header__actions {
    gap: 8px;
    margin-left: auto;
  }

  .chat-select {
    display: flex;
    align-items: center;
    min-width: 0;
    min-height: 30px;
    padding: 0 6px;
    background: rgb(248 250 252 / 0.88);
    border-radius: 10px;
    transition: background-color 0.18s ease;
  }

  .chat-select--agent {
    flex: 0 1 156px;
  }

  .chat-select--model {
    width: 118px;
    max-width: 100%;
  }

  .chat-select__control {
    width: 100%;
  }

  .chat-select :deep(.el-select__wrapper) {
    min-height: auto;
    padding: 0;
    font-size: 11px;
    background: transparent;
    box-shadow: none;
  }

  .chat-select:hover {
    background: rgb(241 245 249 / 0.96);
  }

  .chat-select :deep(.el-select__wrapper.is-focused) {
    box-shadow: none;
  }

  .chat-select :deep(.el-select__selected-item),
  .chat-select :deep(.el-select__placeholder),
  .chat-select :deep(.el-input__inner) {
    font-size: 11px;
    color: var(--el-text-color-primary);
  }

  .chat-select :deep(.el-select__placeholder) {
    color: var(--el-text-color-secondary);
  }

  .chat-select :deep(.el-select__caret) {
    font-size: 11px;
    color: rgb(100 116 139 / 0.82);
  }

  .chat-icon-button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    padding: 0;
    color: var(--el-text-color-secondary);
    cursor: pointer;
    background: transparent;
    border: 0;
    border-radius: 10px;
    transition:
      background-color 0.18s ease,
      color 0.18s ease,
      opacity 0.18s ease;
  }

  .chat-icon-button:hover {
    color: var(--el-text-color-primary);
    background: rgb(148 163 184 / 0.12);
  }

  .chat-icon-button:disabled {
    cursor: not-allowed;
    opacity: 0.46;
  }

  .chat-body,
  .history-panel__body {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 16px;
    padding-top: 16px;
    overflow-y: auto;
  }

  .chat-body--empty {
    justify-content: center;
  }

  .chat-body__alert {
    margin-bottom: 2px;
  }

  .chat-empty {
    width: min(100%, 460px);
    margin: auto;
    transform: translateY(-18px);
  }

  .chat-empty__main {
    padding: 6px 24px 0;
    margin-top: 0;
    background: transparent;
    border: 0;
    border-radius: 0;
    box-shadow: none;
  }

  .chat-empty__main :deep(.el-empty__description) {
    margin-top: 8px;
  }

  .chat-empty__copy {
    display: flex;
    flex-direction: column;
    gap: 8px;
    max-width: 420px;
    margin: 0 auto;
    text-align: center;
  }

  .chat-empty__title {
    font-size: 16px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  .chat-empty__text,
  .history-panel__subtitle,
  .history-item__meta,
  .history-item__preview,
  .chat-footer__hint,
  .chat-footer__text {
    margin: 0;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .chat-empty__text {
    font-size: 13px;
    line-height: 1.7;
  }

  .chat-empty__action {
    min-width: 84px;
    height: 34px;
    margin-top: 8px;
    color: #3b82f6;
    background: rgb(239 246 255 / 0.8);
    border-color: rgb(125 166 255 / 0.28);
    border-radius: 10px;
  }

  .chat-starters {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    justify-content: center;
    margin-top: 16px;
  }

  .chat-message {
    display: flex;
    gap: 12px;
    align-items: flex-start;
  }

  .chat-message--me {
    flex-direction: row-reverse;
  }

  .chat-message__content {
    display: flex;
    flex-direction: column;
    max-width: calc(100% - 48px);
  }

  .chat-message__meta {
    display: flex;
    gap: 8px;
    margin-bottom: 6px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .chat-message--me .chat-message__content {
    align-items: flex-end;
  }

  .chat-message--me .chat-message__meta {
    justify-content: flex-end;
  }

  .chat-message__reasoning,
  .chat-message__bubble {
    padding: 12px 14px;
    font-size: 13px;
    line-height: 1.7;
    border-radius: 16px;
  }

  .chat-message__reasoning {
    margin-bottom: 8px;
    background: rgb(59 130 246 / 0.08);

    strong {
      display: block;
      margin-bottom: 8px;
      font-size: 12px;
      color: #3b82f6;
    }
  }

  .chat-message__reasoning-panel {
    max-height: 168px;
    padding-right: 4px;
    overflow-y: auto;
    font-size: 12px;
  }

  .chat-message__bubble {
    white-space: pre-wrap;
    background: rgb(15 23 42 / 0.06);
  }

  .chat-message__bubble :deep(p),
  .chat-message__bubble :deep(li),
  .chat-message__bubble :deep(blockquote),
  .chat-message__bubble :deep(pre),
  .chat-message__bubble :deep(code),
  .chat-message__reasoning-panel :deep(p),
  .chat-message__reasoning-panel :deep(li),
  .chat-message__reasoning-panel :deep(blockquote),
  .chat-message__reasoning-panel :deep(pre),
  .chat-message__reasoning-panel :deep(code) {
    font-size: inherit;
  }

  .chat-message__bubble--me {
    background: rgb(77 140 255 / 0.16);
  }

  .chat-message__loading {
    display: inline-flex;
    gap: 10px;
    align-items: center;
    min-height: 22px;
    font-size: 13px;
    color: var(--el-text-color-secondary);
  }

  .chat-message__loading-dots {
    display: inline-flex;
    gap: 4px;
    align-items: center;

    span {
      width: 7px;
      height: 7px;
      background: rgb(77 140 255 / 0.72);
      border-radius: 999px;
      animation: chat-loading-bounce 1.1s infinite ease-in-out;
    }

    span:nth-child(2) {
      animation-delay: 0.15s;
    }

    span:nth-child(3) {
      animation-delay: 0.3s;
    }
  }

  .chat-footer {
    padding-top: 16px;
  }

  .chat-footer__hint {
    display: inline-flex;
    align-items: center;
    min-height: 28px;
    padding: 0 10px;
    margin-bottom: 12px;
    background: rgb(239 246 255 / 0.88);
    border-radius: 999px;
  }

  .chat-footer__panel {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 14px 14px 12px;
    background: rgb(255 255 255 / 0.96);
    border: 1px solid rgb(203 213 225 / 0.88);
    border-radius: 16px;
    transition:
      border-color 0.18s ease,
      box-shadow 0.18s ease;
  }

  .chat-footer__panel:hover {
    border-color: rgb(148 163 184 / 0.92);
  }

  .chat-footer__panel:focus-within {
    border-color: rgb(77 140 255 / 0.42);
    box-shadow: 0 0 0 4px rgb(77 140 255 / 0.08);
  }

  .chat-footer__panel :deep(.el-textarea__inner) {
    min-height: 92px !important;
    padding: 0;
    font-size: 14px;
    line-height: 1.7;
    color: var(--el-text-color-primary);
    background: transparent;
    border: 0;
    box-shadow: none;
  }

  .chat-footer__panel :deep(.el-textarea__inner::placeholder) {
    color: rgb(148 163 184 / 0.95);
  }

  .chat-footer__toolbar {
    gap: 12px;
    justify-content: space-between;
  }

  .chat-footer__toolbar-main {
    flex: 1;
    gap: 10px;
    min-width: 0;
  }

  .chat-toggle {
    display: inline-flex;
    gap: 7px;
    align-items: center;
    justify-content: center;
    min-width: 72px;
    height: 28px;
    padding: 0 10px;
    font-size: 11px;
    font-weight: 600;
    color: var(--el-text-color-secondary);
    cursor: pointer;
    background: transparent;
    border: 0;
    border-radius: 999px;
    transition: all 0.18s ease;
  }

  .chat-toggle:hover {
    color: var(--el-text-color-primary);
    background: rgb(241 245 249 / 0.96);
  }

  .chat-toggle--active {
    color: #3b82f6;
    background: rgb(77 140 255 / 0.14);
  }

  .chat-toggle:disabled {
    cursor: not-allowed;
    opacity: 0.56;
  }

  .chat-toggle__dot {
    width: 5px;
    height: 5px;
    background: currentcolor;
    border-radius: 50%;
  }

  .chat-footer__send-button {
    width: 40px;
    height: 40px;
    padding: 0;
    box-shadow: 0 8px 18px rgb(77 140 255 / 0.18);
  }

  .chat-footer__send-button :deep(.el-icon) {
    font-size: 16px;
  }

  .chat-footer__text {
    padding-left: 2px;
    margin-top: 10px;
    line-height: 1.5;
  }

  .history-panel__header {
    gap: 12px;
    justify-content: space-between;
    padding-bottom: 16px;
    border-bottom: 1px solid rgb(226 232 240 / 0.78);
  }

  .history-panel__subtitle {
    margin-top: 6px;
  }

  .history-panel__body {
    gap: 10px;
  }

  .history-item {
    gap: 10px;
    align-items: flex-start;
    padding: 8px 10px;
    border: 1px solid var(--el-border-color);
    border-radius: 12px;
  }

  .history-item--active {
    background: rgb(77 140 255 / 0.06);
    border-color: rgb(77 140 255 / 0.36);
  }

  .history-item__main {
    flex: 1;
    min-width: 0;
    padding: 0;
    text-align: left;
    background: transparent;
    border: 0;
  }

  .history-item__title {
    overflow: hidden;
    font-size: 13px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .history-item__meta {
    margin-top: 4px;
    font-size: 11px;
  }

  :global(.chat-window__select-popper--model.el-select__popper) {
    min-width: 118px !important;
  }

  :global(.chat-window__select-popper--model .el-select-dropdown__item) {
    height: 30px;
    padding: 0 10px;
    font-size: 12px;
    line-height: 30px;
  }

  :global(.chat-window__select-popper--model .el-select-dropdown__list) {
    padding: 4px 0;
  }

  @media (max-width: 640px) {
    .chat-header {
      flex-wrap: wrap;
      gap: 10px;
      min-height: auto;
    }

    .chat-select--agent {
      flex: 1 1 calc(100% - 112px);
      order: 3;
    }

    .chat-header__actions {
      order: 4;
      margin-left: 0;
    }

    .chat-icon-button--close {
      margin-left: auto;
    }

    .chat-empty {
      transform: translateY(-8px);
    }

    .chat-empty__main {
      padding-right: 16px;
      padding-left: 16px;
    }

    .chat-footer__toolbar {
      flex-direction: column;
      align-items: stretch;
    }

    .chat-footer__toolbar-main {
      flex-wrap: wrap;
    }

    .chat-select--model,
    .chat-toggle {
      width: 100%;
    }

    .chat-footer__send-button {
      align-self: flex-end;
    }
  }

  @keyframes chat-loading-bounce {
    0%,
    80%,
    100% {
      opacity: 0.35;
      transform: scale(0.82);
    }

    40% {
      opacity: 1;
      transform: scale(1);
    }
  }
</style>
