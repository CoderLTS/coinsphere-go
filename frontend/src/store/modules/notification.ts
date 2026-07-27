/** 状态管理模块：notification。 */
import { defineStore } from 'pinia'
import {
  fetchInAppNoticeList,
  fetchReadAllInAppNotice,
  fetchReadInAppNotice,
  type InAppNoticePage
} from '@/api/notifications'
import { useUserStore } from './user'

type WsPayload =
  | { type: 'notice.created'; record: Api.Notifications.InAppNoticeItem | Record<string, any>; unreadCount?: number }
  | { type: 'notice.unread'; unreadCount: number }
  | { type: 'pong' }

export const useNotificationStore = defineStore('notificationStore', () => {
  const records = ref<Api.Notifications.InAppNoticeItem[]>([])
  const unreadCount = ref(0)
  const current = ref(1)
  const size = ref(20)
  const total = ref(0)
  const hasMore = ref(false)
  const loading = ref(false)
  const connected = ref(false)
  const latestIncomingNoticeId = ref<number | null>(null)

  let socket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let manualClose = false

  const userStore = useUserStore()

  const resetState = () => {
    records.value = []
    unreadCount.value = 0
    current.value = 1
    total.value = 0
    hasMore.value = false
    loading.value = false
    latestIncomingNoticeId.value = null
  }

  const buildWsUrl = () => {
    const devProxy = import.meta.env.DEV
      ? import.meta.env.VITE_API_PROXY_URL || window.location.origin
      : import.meta.env.VITE_API_URL && import.meta.env.VITE_API_URL !== '/'
        ? import.meta.env.VITE_API_URL
        : window.location.origin
    const normalizedBase = String(devProxy).replace(/\/$/, '')
    const wsBase = normalizedBase.replace(/^http/i, 'ws')
    return `${wsBase}/ws/notifications?token=${encodeURIComponent(userStore.accessToken)}`
  }

  const applyPage = (page: InAppNoticePage, append = false) => {
    const normalizedRecords = page.records.map(normalizeNoticeRecord)
    records.value = append ? [...records.value, ...normalizedRecords] : normalizedRecords
    unreadCount.value = page.unreadCount
    current.value = page.current
    size.value = page.size
    total.value = page.total
    hasMore.value = page.hasMore
  }

  const loadNotices = async (options?: { append?: boolean }) => {
    if (loading.value || userStore.accessMode !== 'authenticated') {
      return
    }
    loading.value = true
    try {
      const nextCurrent = options?.append ? current.value + 1 : 1
      const page = await fetchInAppNoticeList({ current: nextCurrent, size: size.value })
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

  const handleWsMessage = (payload: WsPayload) => {
    if (payload.type === 'notice.unread') {
      unreadCount.value = payload.unreadCount
      return
    }
    if (payload.type !== 'notice.created') {
      return
    }
    const existingIndex = records.value.findIndex((item) => item.id === payload.record.id)
    if (existingIndex >= 0) {
      records.value.splice(existingIndex, 1, normalizeNoticeRecord(payload.record))
    } else {
      records.value.unshift(normalizeNoticeRecord(payload.record))
      if (records.value.length > size.value) {
        records.value = records.value.slice(0, size.value)
      }
      total.value += 1
    }
    unreadCount.value = payload.unreadCount ?? unreadCount.value + 1
    latestIncomingNoticeId.value = payload.record.id
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
    if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
      return
    }
    manualClose = false
    socket = new WebSocket(buildWsUrl())
    socket.onopen = () => {
      connected.value = true
    }
    socket.onmessage = (event) => {
      try {
        handleWsMessage(JSON.parse(event.data))
      } catch (error) {
        console.warn('Failed to parse notification websocket payload', error)
      }
    }
    socket.onclose = () => {
      connected.value = false
      socket = null
      scheduleReconnect()
    }
    socket.onerror = () => {
      connected.value = false
    }
  }

  const disconnect = () => {
    manualClose = true
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
    current,
    size,
    total,
    hasMore,
    loading,
    connected,
    latestIncomingNoticeId,
    loadNotices,
    markRead,
    markAllRead,
    connect,
    disconnect,
    resetState
  }
})

function normalizeNoticeRecord(raw: Api.Notifications.InAppNoticeItem | Record<string, any>): Api.Notifications.InAppNoticeItem {
  const record = raw as Record<string, any>
  return {
    id: Number(record.id || 0),
    workflowExecutionId: record.workflowExecutionId ?? null,
    workflowExecutionNodeId: record.workflowExecutionNodeId ?? null,
    workflowDefinitionId: record.workflowDefinitionId ?? null,
    workflowDefinitionCode: String(record.workflowDefinitionCode || ''),
    workflowDefinitionName: String(record.workflowDefinitionName || ''),
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
