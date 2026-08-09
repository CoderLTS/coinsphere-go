/** 状态管理模块：notification。 */
import { defineStore } from 'pinia'
import {
  fetchInAppNoticeList,
  fetchReadAllInAppNotice,
  fetchReadInAppNotice,
  type InAppNoticePage
} from '@/api/notifications'
import { useUserStore } from './user'

type NotificationWsEnvelope = {
  type: string
  version: 1
  sequence: number
  occurredAt: string
  data: Record<string, any>
}

export function decodeNotificationWsEnvelope(
  value: unknown,
  lastSequence: number
): NotificationWsEnvelope | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return null
  }
  const envelope = value as Record<string, unknown>
  const sequence = typeof envelope.sequence === 'number' ? envelope.sequence : Number.NaN
  if (
    envelope.version !== 1 ||
    typeof envelope.type !== 'string' ||
    envelope.type === '' ||
    !Number.isSafeInteger(sequence) ||
    sequence <= lastSequence ||
    typeof envelope.occurredAt !== 'string' ||
    !envelope.occurredAt.endsWith('Z') ||
    !envelope.data ||
    typeof envelope.data !== 'object' ||
    Array.isArray(envelope.data)
  ) {
    return null
  }
  return { ...envelope, version: 1, sequence } as NotificationWsEnvelope
}

const NOTIFICATION_WS_PROTOCOL = 'coinsphere.notifications.v1'

export function buildNotificationWsUrl(pageOrigin: string) {
  const url = new URL('/api/v1/ws/notifications', pageOrigin)
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new Error('notification websocket requires an HTTP page origin')
  }
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

export const useNotificationStore = defineStore('notificationStore', () => {
  const records = ref<Api.Notifications.InAppNoticeItem[]>([])
  const unreadCount = ref(0)
  const size = ref(20)
  const nextCursor = ref('')
  const total = ref(0)
  const hasMore = ref(false)
  const loading = ref(false)
  const connected = ref(false)
  const latestIncomingNoticeId = ref<number | null>(null)

  let socket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let manualClose = false
  let lastSequence = 0

  const userStore = useUserStore()

  const resetState = () => {
    records.value = []
    unreadCount.value = 0
    nextCursor.value = ''
    total.value = 0
    hasMore.value = false
    loading.value = false
    latestIncomingNoticeId.value = null
  }

  const applyPage = (page: InAppNoticePage, append = false) => {
    const normalizedRecords = page.records.map(normalizeNoticeRecord)
    records.value = append ? [...records.value, ...normalizedRecords] : normalizedRecords
    unreadCount.value = page.unreadCount
    nextCursor.value = page.nextCursor
    total.value = page.total
    hasMore.value = page.hasMore
  }

  const loadNotices = async (options?: { append?: boolean }) => {
    if (loading.value || userStore.accessMode !== 'authenticated') {
      return
    }
    loading.value = true
    try {
      const page = await fetchInAppNoticeList({
        cursor: options?.append ? nextCursor.value || undefined : undefined,
        limit: size.value
      })
      applyPage(page, Boolean(options?.append))
    } finally {
      loading.value = false
    }
  }

  const markRead = async (deliveryId: number) => {
    await fetchReadInAppNotice(deliveryId)
    const record = records.value.find((item) => item.id === deliveryId)
    if (record && !record.isRead) {
      record.isRead = true
      record.readAt = new Date().toISOString()
      unreadCount.value = Math.max(unreadCount.value - 1, 0)
    }
  }

  const markAllRead = async () => {
    await fetchReadAllInAppNotice()
    records.value = records.value.map((item) => ({
      ...item,
      isRead: true,
      readAt: item.readAt || new Date().toISOString()
    }))
    unreadCount.value = 0
  }

  const applySignalDecision = (signalId: string, status: string) => {
    records.value = records.value.map((item) =>
      item.strategySignalId === signalId ? { ...item, strategySignalStatus: status } : item
    )
  }

  const handleWsMessage = (envelope: NotificationWsEnvelope) => {
    if (envelope.type === 'notice.unread') {
      const nextUnreadCount = envelope.data.unreadCount
      if (
        typeof nextUnreadCount === 'number' &&
        Number.isSafeInteger(nextUnreadCount) &&
        nextUnreadCount >= 0
      ) {
        unreadCount.value = nextUnreadCount
      }
      return
    }
    if (
      envelope.type !== 'notice.created' ||
      !envelope.data.record ||
      typeof envelope.data.record !== 'object' ||
      Array.isArray(envelope.data.record)
    ) {
      return
    }
    const record = envelope.data.record as Record<string, any>
    const normalizedRecord = normalizeNoticeRecord(record)
    if (!Number.isSafeInteger(normalizedRecord.id) || normalizedRecord.id <= 0) {
      return
    }
    const existingIndex = records.value.findIndex((item) => item.id === normalizedRecord.id)
    if (existingIndex >= 0) {
      records.value.splice(existingIndex, 1, normalizedRecord)
    } else {
      records.value.unshift(normalizedRecord)
      if (records.value.length > size.value) {
        records.value = records.value.slice(0, size.value)
      }
      total.value += 1
    }
    const nextUnreadCount = envelope.data.unreadCount
    unreadCount.value =
      typeof nextUnreadCount === 'number' &&
      Number.isSafeInteger(nextUnreadCount) &&
      nextUnreadCount >= 0
        ? nextUnreadCount
        : unreadCount.value + 1
    latestIncomingNoticeId.value = normalizedRecord.id
  }

  const scheduleReconnect = () => {
    if (manualClose || userStore.accessMode !== 'authenticated') {
      return
    }
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
    }
    reconnectTimer = setTimeout(() => {
      connect()
    }, 3000)
  }

  const connect = () => {
    if (userStore.accessMode !== 'authenticated' || !userStore.accessToken) {
      return
    }
    if (
      socket &&
      (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)
    ) {
      return
    }
    manualClose = false
    lastSequence = 0
    const currentSocket = new WebSocket(buildNotificationWsUrl(window.location.origin), [
      NOTIFICATION_WS_PROTOCOL,
      userStore.accessToken
    ])
    socket = currentSocket
    currentSocket.onopen = () => {
      if (socket !== currentSocket) return
      if (currentSocket.protocol !== NOTIFICATION_WS_PROTOCOL) {
        manualClose = true
        currentSocket.close(1002, 'unexpected websocket protocol')
        return
      }
      connected.value = true
    }
    currentSocket.onmessage = (event) => {
      if (socket !== currentSocket) return
      try {
        const envelope = decodeNotificationWsEnvelope(JSON.parse(event.data), lastSequence)
        if (!envelope) {
          return
        }
        lastSequence = envelope.sequence
        handleWsMessage(envelope)
      } catch {
        console.warn('Failed to parse notification websocket payload')
      }
    }
    currentSocket.onclose = () => {
      if (socket !== currentSocket) return
      connected.value = false
      socket = null
      scheduleReconnect()
    }
    currentSocket.onerror = () => {
      if (socket !== currentSocket) return
      connected.value = false
    }
  }

  const disconnect = () => {
    manualClose = true
    lastSequence = 0
    connected.value = false
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    if (socket) {
      socket.close()
      socket = null
    }
    resetState()
  }

  return {
    records,
    unreadCount,
    size,
    total,
    hasMore,
    loading,
    connected,
    latestIncomingNoticeId,
    loadNotices,
    markRead,
    markAllRead,
    applySignalDecision,
    connect,
    disconnect,
    resetState
  }
})

function normalizeNoticeRecord(
  raw: Api.Notifications.InAppNoticeItem | Record<string, any>
): Api.Notifications.InAppNoticeItem {
  const record = raw as Record<string, any>
  return {
    id: Number(record.id || 0),
    workflowExecutionId: record.workflowExecutionId ?? null,
    workflowExecutionNodeId: record.workflowExecutionNodeId ?? null,
    workflowDefinitionId: record.workflowDefinitionId ?? null,
    workflowDefinitionCode: String(record.workflowDefinitionCode || ''),
    workflowDefinitionName: String(record.workflowDefinitionName || ''),
    strategySignalId: record.strategySignalId ? String(record.strategySignalId) : null,
    strategySignalMode: String(record.strategySignalMode || ''),
    strategySignalStatus: String(record.strategySignalStatus || ''),
    strategySignalExpiresAt: String(record.strategySignalExpiresAt || ''),
    targetType: String(record.targetType || ''),
    targetId: record.targetId ?? null,
    targetLabel: String(record.targetLabel || ''),
    recipientId: record.recipientId ?? null,
    recipientLabel: String(record.recipientLabel || ''),
    channelType: String(record.channelType || 'in_app'),
    channelTypeLabel: String(record.channelTypeLabel || ''),
    channelDisplayName: String(record.channelDisplayName || ''),
    deliveryStatus: String(record.deliveryStatus || record.status || ''),
    deliveryStatusLabel: String(record.deliveryStatusLabel || ''),
    messageTitle: String(record.messageTitle || record.title || ''),
    messageContent: String(record.messageContent || record.content || ''),
    providerResponseText: String(record.providerResponseText || ''),
    errorMessage: String(record.errorMessage || ''),
    isRead: Boolean(record.isRead),
    readAt: String(record.readAt || ''),
    sentAt: String(record.sentAt || ''),
    createdAt: String(record.createdAt || '')
  }
}
