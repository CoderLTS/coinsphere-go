<!-- 超级管理员专用的平台智能助手。 -->
<template>
  <ElDrawer
    v-model="visible"
    :size="isMobile ? '100%' : '560px'"
    :with-header="false"
    @closed="stopStream"
  >
    <div class="assistant-shell">
      <header class="assistant-header">
        <div>
          <h2>{{ t('assistant.title') }}</h2>
          <span>{{ currentSession?.title || t('assistant.newSession') }}</span>
        </div>
        <div class="assistant-header__actions">
          <ElTooltip :content="t('assistant.history')">
            <button
              type="button"
              class="icon-button"
              :aria-label="t('assistant.history')"
              :disabled="isStreaming"
              @click="openHistory"
            >
              <ElIcon><ChatDotRound /></ElIcon>
            </button>
          </ElTooltip>
          <ElTooltip :content="t('assistant.newSession')">
            <button
              type="button"
              class="icon-button"
              :aria-label="t('assistant.newSession')"
              :disabled="isStreaming || !models.length"
              @click="startNewSession"
            >
              <ElIcon><CirclePlus /></ElIcon>
            </button>
          </ElTooltip>
          <ElTooltip :content="t('common.close')">
            <button
              type="button"
              class="icon-button"
              :aria-label="t('common.close')"
              @click="closeAssistant"
            >
              <ElIcon><Close /></ElIcon>
            </button>
          </ElTooltip>
        </div>
      </header>

      <main ref="messageContainer" v-loading="loading" class="assistant-body">
        <ElEmpty
          v-if="!loading && !models.length"
          class="assistant-empty"
          :image-size="82"
          :description="t('assistant.noModelsTitle')"
        >
          <ElButton class="assistant-empty__action" plain @click="goToModelConfig">
            {{ t('assistant.goModelConfig') }}
          </ElButton>
        </ElEmpty>

        <ElEmpty
          v-else-if="!loading && !messages.length"
          class="assistant-empty"
          :image-size="82"
          :description="t('assistant.emptyTitle')"
        />

        <article
          v-for="message in messages"
          :key="message.id"
          class="assistant-message"
          :class="{ 'assistant-message--user': message.role === 'user' }"
        >
          <ElAvatar :size="34" :src="message.role === 'user' ? userAvatar : assistantAvatar" />
          <div class="assistant-message__main">
            <div class="assistant-message__meta">
              <span>{{ message.role === 'user' ? userName : t('assistant.title') }}</span>
              <span>{{ formatDateTime(message.createdAt) }}</span>
            </div>

            <div v-if="message.tools.length" class="tool-statuses">
              <div v-for="tool in message.tools" :key="tool.name" class="tool-status">
                <ElIcon :class="{ 'is-loading': tool.status === 'running' }">
                  <Loading v-if="tool.status === 'running'" />
                  <CircleCheck v-else-if="tool.status === 'completed'" />
                  <CircleClose v-else />
                </ElIcon>
                <span>{{ tool.name }}</span>
                <small>{{ t(`assistant.tool.${tool.status}`) }}</small>
              </div>
            </div>

            <div class="assistant-message__bubble">
              <div v-if="message.streaming && !message.content" class="stream-loading">
                <span></span><span></span><span></span>
              </div>
              <AssistantRichText
                v-else-if="message.role === 'assistant'"
                :content="message.content"
                :streaming="message.streaming"
              />
              <template v-else>{{ message.content }}</template>
            </div>

            <section v-if="message.proposal" class="workflow-proposal">
              <div class="workflow-proposal__heading">
                <ElIcon><MagicStick /></ElIcon>
                <div>
                  <strong>{{ message.proposal.name }}</strong>
                  <span>{{ t('assistant.proposal.title') }}</span>
                </div>
              </div>
              <p v-if="message.proposal.description">{{ message.proposal.description }}</p>
              <div class="workflow-proposal__stats">
                <span>{{
                  t('assistant.proposal.nodes', { count: message.proposal.nodeCount })
                }}</span>
                <span>{{
                  t('assistant.proposal.edges', { count: message.proposal.edgeCount })
                }}</span>
              </div>
              <div v-if="message.proposal.nodeTypes.length" class="workflow-proposal__tags">
                <ElTag v-for="nodeType in message.proposal.nodeTypes" :key="nodeType" size="small">
                  {{ nodeType }}
                </ElTag>
              </div>
              <ElAlert
                v-if="message.proposal.missingSecrets.length"
                :title="t('assistant.proposal.missingSecrets')"
                :description="message.proposal.missingSecrets.join(', ')"
                type="warning"
                :closable="false"
                show-icon
              />
              <ElButton
                type="primary"
                :loading="confirmingMessageId === message.numericId"
                @click="handleProposal(message)"
              >
                <ElIcon><Edit v-if="message.proposal.workflowId" /><CirclePlus v-else /></ElIcon>
                {{
                  message.proposal.workflowId
                    ? t('assistant.proposal.edit')
                    : t('assistant.proposal.create')
                }}
              </ElButton>
            </section>
          </div>
        </article>
      </main>

      <footer class="assistant-footer">
        <div class="assistant-composer">
          <ElInput
            v-model="messageText"
            type="textarea"
            :autosize="{ minRows: 3, maxRows: 7 }"
            resize="none"
            :placeholder="t('assistant.placeholder')"
            :disabled="!models.length || isStreaming"
            @keydown.enter.exact.prevent="sendMessage"
          />
          <div class="assistant-composer__toolbar">
            <ElSelect
              v-model="selectedModelId"
              :placeholder="t('assistant.modelPlaceholder')"
              :disabled="isStreaming"
              @change="handleModelChange"
            >
              <ElOption
                v-for="model in models"
                :key="model.id"
                :label="model.displayName"
                :value="model.id"
              />
            </ElSelect>
            <ElButton
              circle
              :type="isStreaming ? 'danger' : 'primary'"
              :disabled="!isStreaming && !canSend"
              :aria-label="isStreaming ? t('assistant.stop') : t('assistant.send')"
              @click="isStreaming ? stopStream() : sendMessage()"
            >
              <ElIcon><VideoPause v-if="isStreaming" /><Promotion v-else /></ElIcon>
            </ElButton>
          </div>
        </div>
      </footer>
    </div>
  </ElDrawer>

  <ElDrawer
    v-model="historyVisible"
    :size="isMobile ? '100%' : '300px'"
    :with-header="false"
    append-to-body
  >
    <div class="history-panel">
      <header>
        <h2>{{ t('assistant.history') }}</h2>
        <button
          type="button"
          class="icon-button"
          :aria-label="t('common.close')"
          @click="historyVisible = false"
        >
          <ElIcon><Close /></ElIcon>
        </button>
      </header>
      <div v-loading="historyLoading" class="history-panel__body">
        <ElEmpty
          v-if="!historyLoading && !sessions.length"
          :description="t('assistant.historyEmpty')"
        />
        <div
          v-for="session in sessions"
          :key="session.id"
          class="history-item"
          :class="{ 'history-item--active': currentSession?.id === session.id }"
        >
          <button type="button" class="history-item__main" @click="openSession(session)">
            <strong>{{ session.title }}</strong>
            <span>{{ session.modelName }} · {{ formatDateTime(session.lastMessageAt) }}</span>
            <small>{{ session.latestPreview || t('assistant.historyEmptyPreview') }}</small>
          </button>
          <ElButton
            text
            circle
            :loading="deletingSessionId === session.id"
            @click.stop="deleteSession(session)"
          >
            <ElIcon><Delete /></ElIcon>
          </ElButton>
        </div>
      </div>
    </div>
  </ElDrawer>
</template>

<script setup lang="ts">
  import {
    ChatDotRound,
    CircleCheck,
    CircleClose,
    CirclePlus,
    Close,
    Delete,
    Edit,
    Loading,
    MagicStick,
    Promotion,
    VideoPause
  } from '@element-plus/icons-vue'
  import { useWindowSize } from '@vueuse/core'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { useI18n } from 'vue-i18n'
  import { useRouter } from 'vue-router'
  import defaultAvatar from '@imgs/user/avatar.webp'
  import assistantAvatar from '@/assets/images/avatar/avatar10.webp'
  import {
    confirmAssistantWorkflow,
    createAssistantSession,
    deleteAssistantSession,
    fetchAssistantMessages,
    fetchAssistantModels,
    fetchAssistantSessions,
    streamAssistantSession
  } from '@/api/assistant'
  import { useUserStore } from '@/store/modules/user'
  import { formatDateTime } from '@/utils/date'
  import { mittBus } from '@/utils/sys'
  import AssistantRichText from './AssistantRichText.vue'

  interface ToolState {
    name: string
    status: 'running' | 'completed' | 'failed'
  }

  interface ChatMessage extends Omit<Api.Assistant.Message, 'id'> {
    id: number | string
    numericId?: number
    streaming: boolean
    tools: ToolState[]
  }

  const { t } = useI18n()
  const router = useRouter()
  const userStore = useUserStore()
  const { width } = useWindowSize()
  const visible = ref(false)
  const historyVisible = ref(false)
  const loading = ref(false)
  const historyLoading = ref(false)
  const isStreaming = ref(false)
  const deletingSessionId = ref<number | null>(null)
  const confirmingMessageId = ref<number | null>(null)
  const selectedModelId = ref<number | null>(null)
  const currentSession = ref<Api.Assistant.Session | null>(null)
  const models = ref<Api.Assistant.ModelOption[]>([])
  const sessions = ref<Api.Assistant.Session[]>([])
  const messages = ref<ChatMessage[]>([])
  const messageText = ref('')
  const messageContainer = ref<HTMLElement | null>(null)
  let abortController: AbortController | null = null

  const isMobile = computed(() => width.value < 640)
  const isSuperAdmin = computed(() => userStore.info.roleCodes.includes('R_SUPER'))
  const userAvatar = computed(() => userStore.info.avatar || defaultAvatar)
  const userName = computed(() => userStore.info.username || 'User')
  const canSend = computed(() =>
    Boolean(selectedModelId.value && messageText.value.trim() && !loading.value)
  )

  const toChatMessage = (message: Api.Assistant.Message): ChatMessage => ({
    ...message,
    numericId: message.id,
    streaming: false,
    tools: []
  })

  const scrollToBottom = () =>
    nextTick(() => {
      if (messageContainer.value)
        messageContainer.value.scrollTop = messageContainer.value.scrollHeight
    })

  const loadSessions = async () => {
    sessions.value = await fetchAssistantSessions()
    if (currentSession.value) {
      currentSession.value =
        sessions.value.find((item) => item.id === currentSession.value?.id) || currentSession.value
    }
  }

  const openSession = async (session: Api.Assistant.Session) => {
    if (isStreaming.value) return
    loading.value = true
    try {
      currentSession.value = session
      selectedModelId.value = session.modelId
      messages.value = (await fetchAssistantMessages(session.id)).map(toChatMessage)
      historyVisible.value = false
      scrollToBottom()
    } finally {
      loading.value = false
    }
  }

  const openAssistant = async () => {
    if (!isSuperAdmin.value) return
    visible.value = true
    loading.value = true
    try {
      const [availableModels, history] = await Promise.all([
        fetchAssistantModels(),
        fetchAssistantSessions()
      ])
      models.value = availableModels
      sessions.value = history
      if (currentSession.value) {
        const latest = history.find((item) => item.id === currentSession.value?.id)
        if (latest) await openSession(latest)
        else startNewSession()
      } else if (history.length) {
        await openSession(history[0])
      } else {
        selectedModelId.value = availableModels[0]?.id || null
      }
    } catch (error) {
      ElMessage.error(resolveError(error, 'assistant.errors.loadConversation'))
    } finally {
      loading.value = false
    }
  }

  const closeAssistant = () => {
    stopStream()
    visible.value = false
  }

  const startNewSession = () => {
    currentSession.value = null
    messages.value = []
    messageText.value = ''
    if (!selectedModelId.value) selectedModelId.value = models.value[0]?.id || null
  }

  const handleModelChange = (modelId: number) => {
    if (currentSession.value?.modelId === modelId) return
    currentSession.value = null
    messages.value = []
  }

  const ensureDraft = (draftId: string): ChatMessage => {
    let draft = messages.value.find((item) => item.id === draftId)
    if (!draft) {
      draft = {
        id: draftId,
        role: 'assistant',
        content: '',
        createdAt: new Date().toISOString(),
        streaming: true,
        tools: []
      }
      messages.value.push(draft)
    }
    return draft
  }

  const sendMessage = async () => {
    const text = messageText.value.trim()
    if (!text || !selectedModelId.value || isStreaming.value) return
    isStreaming.value = true
    messageText.value = ''
    abortController = new AbortController()
    const draftId = `assistant-${Date.now()}`
    let streamError = ''

    try {
      if (!currentSession.value) {
        currentSession.value = await createAssistantSession({ modelId: selectedModelId.value })
      }
      await streamAssistantSession(
        currentSession.value.id,
        { text },
        {
          onUser: (message) => {
            messages.value.push(toChatMessage(message))
            ensureDraft(draftId)
            scrollToBottom()
          },
          onTool: (tool) => {
            const draft = ensureDraft(draftId)
            const current = draft.tools.find((item) => item.name === tool.name)
            if (current) current.status = tool.status
            else draft.tools.push(tool)
            scrollToBottom()
          },
          onContent: (chunk) => {
            ensureDraft(draftId).content += chunk
            scrollToBottom()
          },
          onProposal: ({ messageId, proposal }) => {
            const draft = ensureDraft(draftId)
            draft.numericId = messageId
            draft.proposal = proposal
          },
          onDone: ({ message, session }) => {
            if (message) {
              const index = messages.value.findIndex((item) => item.id === draftId)
              const completed = {
                ...toChatMessage(message),
                tools: index >= 0 ? messages.value[index].tools : []
              }
              if (index >= 0) messages.value.splice(index, 1, completed)
              else messages.value.push(completed)
            }
            if (session) currentSession.value = session
          },
          onError: ({ msg }) => {
            streamError = msg || t('assistant.errors.stream')
          }
        },
        abortController.signal
      )
      if (streamError) throw new Error(streamError)
      await loadSessions()
    } catch (error) {
      const index = messages.value.findIndex((item) => item.id === draftId)
      if (index >= 0 && !messages.value[index].content) messages.value.splice(index, 1)
      if (!(error instanceof DOMException && error.name === 'AbortError')) {
        ElMessage.error(resolveError(error, 'assistant.errors.stream'))
      }
      await loadSessions().catch(() => {})
    } finally {
      const draft = messages.value.find((item) => item.id === draftId)
      if (draft) draft.streaming = false
      abortController = null
      isStreaming.value = false
      scrollToBottom()
    }
  }

  const stopStream = () => abortController?.abort()

  const openHistory = async () => {
    historyVisible.value = true
    historyLoading.value = true
    try {
      await loadSessions()
    } finally {
      historyLoading.value = false
    }
  }

  const deleteSession = async (session: Api.Assistant.Session) => {
    try {
      await ElMessageBox.confirm(
        t('assistant.deleteSessionConfirm', { title: session.title }),
        t('assistant.deleteSession'),
        { type: 'warning' }
      )
    } catch {
      return
    }
    deletingSessionId.value = session.id
    try {
      await deleteAssistantSession(session.id)
      if (currentSession.value?.id === session.id) startNewSession()
      await loadSessions()
      ElMessage.success(t('assistant.deleteSessionSuccess'))
    } finally {
      deletingSessionId.value = null
    }
  }

  const handleProposal = async (message: ChatMessage) => {
    if (!message.proposal || !message.numericId) return
    if (message.proposal.workflowId && message.proposal.editUrl) {
      await router.push(message.proposal.editUrl)
      visible.value = false
      return
    }
    confirmingMessageId.value = message.numericId
    try {
      const result = await confirmAssistantWorkflow(message.numericId)
      message.proposal.workflowId = result.workflowId
      message.proposal.editUrl = result.editUrl
      ElMessage.success(t('assistant.proposal.created'))
    } finally {
      confirmingMessageId.value = null
    }
  }

  const goToModelConfig = () => {
    visible.value = false
    void router.push('/system/ai-models')
  }

  const resolveError = (error: unknown, fallbackKey: string) =>
    error instanceof Error && error.message ? error.message : t(fallbackKey)

  onMounted(() => mittBus.on('openAssistant', openAssistant))
  onUnmounted(() => {
    stopStream()
    mittBus.off('openAssistant', openAssistant)
  })
</script>

<style scoped lang="scss">
  .assistant-shell,
  .history-panel {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .assistant-header,
  .history-panel > header,
  .assistant-composer__toolbar,
  .workflow-proposal__heading,
  .workflow-proposal__stats,
  .history-item {
    display: flex;
    align-items: center;
  }

  .assistant-header {
    gap: 12px;
    min-height: 56px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .assistant-header > div:first-child {
    display: flex;
    gap: 12px;
    align-items: center;
    min-width: 0;
  }

  .assistant-header h2,
  .history-panel h2 {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
  }

  .assistant-header span {
    display: flex;
    align-items: center;
    max-width: 190px;
    min-height: 30px;
    padding: 0 10px;
    overflow: hidden;
    font-size: 11px;
    color: var(--el-text-color-primary);
    text-overflow: ellipsis;
    white-space: nowrap;
    background: var(--el-fill-color-light);
    border-radius: 10px;
  }

  .assistant-header__actions {
    display: flex;
    gap: 8px;
    margin-left: auto;
  }

  .icon-button {
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

  .icon-button:hover {
    color: var(--el-text-color-primary);
    background: var(--el-fill-color);
  }

  .icon-button:disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }

  .assistant-body {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 16px;
    padding-top: 16px;
    overflow-y: auto;
  }

  .assistant-empty {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 8px;
    align-items: center;
    justify-content: center;
    width: min(100%, 460px);
    padding: 6px 24px 0;
    margin: auto;
    color: var(--el-text-color-secondary);
    text-align: center;
    background: transparent;
    transform: translateY(-18px);
  }

  .assistant-empty :deep(.el-empty__description) {
    margin-top: 8px;
  }

  .assistant-empty__action {
    min-width: 84px;
    height: 34px;
    margin-top: 8px;
    color: var(--el-color-primary);
    background: var(--el-color-primary-light-9);
    border-color: var(--el-color-primary-light-5);
    border-radius: 10px;
  }

  .assistant-message {
    display: flex;
    gap: 12px;
    align-items: flex-start;
  }

  .assistant-message--user {
    flex-direction: row-reverse;
  }

  .assistant-message__main {
    display: flex;
    flex-direction: column;
    max-width: calc(100% - 48px);
  }

  .assistant-message--user .assistant-message__main {
    align-items: flex-end;
  }

  .assistant-message__meta {
    display: flex;
    gap: 8px;
    margin-bottom: 6px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .assistant-message--user .assistant-message__meta {
    justify-content: flex-end;
  }

  .assistant-message__bubble {
    max-width: 100%;
    padding: 12px 14px;
    overflow-wrap: anywhere;
    font-size: 13px;
    line-height: 1.7;
    white-space: pre-wrap;
    background: var(--el-fill-color-light);
    border-radius: 16px;
  }

  .assistant-message--user .assistant-message__bubble {
    background: var(--el-color-primary-light-9);
  }

  .assistant-message__bubble :deep(p),
  .assistant-message__bubble :deep(li),
  .assistant-message__bubble :deep(blockquote),
  .assistant-message__bubble :deep(pre),
  .assistant-message__bubble :deep(code) {
    font-size: inherit;
  }

  .tool-statuses {
    display: flex;
    flex-direction: column;
    gap: 4px;
    width: 100%;
    margin-bottom: 8px;
  }

  .tool-status {
    display: grid;
    grid-template-columns: 16px minmax(0, 1fr) auto;
    gap: 6px;
    align-items: center;
    min-height: 26px;
    padding: 3px 8px;
    font-size: 11px;
    color: var(--el-text-color-secondary);
    background: var(--el-fill-color-light);
    border-radius: 10px;
  }

  .tool-status span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .tool-status small {
    font-size: 11px;
  }

  .workflow-proposal {
    display: flex;
    flex-direction: column;
    gap: 12px;
    width: min(100%, 410px);
    padding: 14px;
    margin-top: 8px;
    background: var(--el-color-primary-light-9);
    border: 1px solid var(--el-color-primary-light-5);
    border-radius: 8px;
  }

  .workflow-proposal__heading {
    gap: 10px;
  }

  .workflow-proposal__heading > .el-icon {
    font-size: 20px;
    color: var(--el-color-primary);
  }

  .workflow-proposal__heading div {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .workflow-proposal__heading strong {
    overflow-wrap: anywhere;
    font-size: 14px;
  }

  .workflow-proposal__heading span,
  .workflow-proposal__stats,
  .workflow-proposal p {
    margin: 0;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .workflow-proposal__stats {
    gap: 16px;
  }

  .workflow-proposal__tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .workflow-proposal > .el-button {
    align-self: flex-end;
  }

  .stream-loading {
    display: flex;
    gap: 10px;
    align-items: center;
    min-height: 22px;
  }

  .stream-loading span {
    width: 7px;
    height: 7px;
    background: var(--el-color-primary);
    border-radius: 999px;
    animation: assistant-loading 1.1s infinite ease-in-out;
  }

  .stream-loading span:nth-child(2) {
    animation-delay: 0.15s;
  }

  .stream-loading span:nth-child(3) {
    animation-delay: 0.3s;
  }

  .assistant-footer {
    padding-top: 16px;
  }

  .assistant-composer {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 14px 14px 12px;
    background: var(--default-box-color);
    border: 1px solid var(--el-border-color);
    border-radius: 16px;
    transition:
      border-color 0.18s ease,
      box-shadow 0.18s ease;
  }

  .assistant-composer:hover {
    border-color: var(--el-border-color-darker);
  }

  .assistant-composer:focus-within {
    border-color: var(--el-color-primary-light-5);
    box-shadow: 0 0 0 4px color-mix(in srgb, var(--el-color-primary) 8%, transparent);
  }

  .assistant-composer :deep(.el-textarea__inner) {
    min-height: 92px !important;
    padding: 0;
    font-size: 14px;
    line-height: 1.7;
    color: var(--el-text-color-primary);
    background: transparent;
    border: 0;
    box-shadow: none;
  }

  .assistant-composer :deep(.el-textarea__inner::placeholder) {
    color: var(--el-text-color-placeholder);
  }

  .assistant-composer__toolbar {
    gap: 12px;
    justify-content: space-between;
  }

  .assistant-composer__toolbar .el-select {
    width: 118px;
    max-width: calc(100% - 52px);
    min-height: 30px;
    padding: 0 6px;
    background: var(--el-fill-color-light);
    border-radius: 10px;
  }

  .assistant-composer__toolbar .el-select:hover {
    background: var(--el-fill-color);
  }

  .assistant-composer__toolbar .el-select :deep(.el-select__wrapper) {
    min-height: auto;
    padding: 0;
    font-size: 11px;
    background: transparent;
    box-shadow: none;
  }

  .assistant-composer__toolbar > .el-button {
    width: 40px;
    height: 40px;
    padding: 0;
    box-shadow: 0 8px 18px color-mix(in srgb, var(--el-color-primary) 18%, transparent);
  }

  .assistant-composer__toolbar > .el-button :deep(.el-icon) {
    font-size: 16px;
  }

  .history-panel > header {
    gap: 12px;
    justify-content: space-between;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .history-panel__body {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 10px;
    min-height: 0;
    padding-top: 16px;
    overflow-y: auto;
  }

  .history-item {
    gap: 10px;
    align-items: flex-start;
    padding: 8px 10px;
    border: 1px solid var(--el-border-color);
    border-radius: 12px;
  }

  .history-item--active {
    background: var(--el-color-primary-light-9);
    border-color: var(--el-color-primary-light-5);
  }

  .history-item__main {
    display: flex;
    flex: 1;
    flex-direction: column;
    min-width: 0;
    padding: 0;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
  }

  .history-item__main strong,
  .history-item__main span,
  .history-item__main small {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .history-item__main strong {
    font-size: 13px;
    font-weight: 600;
  }

  .history-item__main span,
  .history-item__main small {
    margin-top: 4px;
    font-size: 11px;
    color: var(--el-text-color-secondary);
  }

  @keyframes assistant-loading {
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

  @media (max-width: 640px) {
    .assistant-header {
      flex-wrap: wrap;
      gap: 10px;
      min-height: auto;
    }

    .assistant-header > div:first-child {
      flex: 1 1 calc(100% - 112px);
    }

    .assistant-header__actions {
      margin-left: 0;
    }

    .assistant-header span {
      max-width: 180px;
    }

    .assistant-empty {
      transform: translateY(-8px);
    }

    .assistant-composer__toolbar {
      align-items: flex-end;
    }

    .assistant-composer__toolbar .el-select {
      width: 100%;
    }
  }
</style>
