import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useNotificationStore } from './notification'

const userStore = vi.hoisted(() => ({
  accessMode: 'authenticated' as 'authenticated' | 'guest',
  accessToken: 'header.payload.signature'
}))

vi.mock('./user', () => ({ useUserStore: () => userStore }))
vi.mock('@/api/notifications', () => ({
  fetchInAppNoticeList: vi.fn(),
  fetchReadAllInAppNotice: vi.fn(),
  fetchReadInAppNotice: vi.fn()
}))

class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static instances: FakeWebSocket[] = []

  readonly url: string
  readonly protocols: string[]
  readonly protocol = 'coinsphere.notifications.v1'
  readyState = FakeWebSocket.CONNECTING
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null

  constructor(url: string | URL, protocols: string | string[] = []) {
    this.url = String(url)
    this.protocols = typeof protocols === 'string' ? [protocols] : protocols
    FakeWebSocket.instances.push(this)
  }

  open() {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.({} as Event)
  }

  emit(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent)
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED
  }

  finishClose() {
    this.onclose?.({} as CloseEvent)
  }
}

const occurredAt = '2026-08-03T08:00:00.123456789Z'

describe('notification websocket', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    FakeWebSocket.instances = []
    userStore.accessMode = 'authenticated'
    userStore.accessToken = 'header.payload.signature'
    vi.stubGlobal('window', { location: { origin: 'https://app.example:8443' } })
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  it('使用页面同源 URL 并消费 version=1 的递增信封', () => {
    const store = useNotificationStore()
    store.connect()

    const socket = FakeWebSocket.instances[0]
    expect(socket).toBeDefined()
    const url = new URL(socket.url)
    expect(url.protocol).toBe('wss:')
    expect(url.host).toBe('app.example:8443')
    expect(url.pathname).toBe('/api/v1/ws/notifications')
    expect(url.search).toBe('')
    expect(socket.protocols).toEqual(['coinsphere.notifications.v1', userStore.accessToken])

    socket.open()
    expect(store.connected).toBe(true)
    socket.emit({
      type: 'notice.unread',
      version: 1,
      sequence: 1,
      occurredAt,
      data: { unreadCount: 2 }
    })
    socket.emit({
      type: 'notice.created',
      version: 1,
      sequence: 2,
      occurredAt,
      data: { record: { id: 42, messageTitle: 'new' }, unreadCount: 3 }
    })
    expect(store.unreadCount).toBe(3)
    expect(store.records[0]?.id).toBe(42)
    expect(store.latestIncomingNoticeId).toBe(42)

    socket.emit({
      type: 'notice.unread',
      version: 2,
      sequence: 3,
      occurredAt,
      data: { unreadCount: 90 }
    })
    socket.emit({
      type: 'notice.created',
      version: 1,
      sequence: 2,
      occurredAt,
      data: { record: { id: 99 }, unreadCount: 90 }
    })
    expect(store.unreadCount).toBe(3)
    expect(store.records).toHaveLength(1)

    // 未知类型仍占用合法序号，避免随后把同序号帧当成新事件。
    socket.emit({ type: 'future.event', version: 1, sequence: 3, occurredAt, data: {} })
    socket.emit({
      type: 'notice.unread',
      version: 1,
      sequence: 3,
      occurredAt,
      data: { unreadCount: 91 }
    })
    socket.emit({
      type: 'notice.unread',
      version: 1,
      sequence: 4,
      occurredAt,
      data: { unreadCount: 4 }
    })
    expect(store.unreadCount).toBe(4)
  })

  it('旧连接延迟关闭不影响新连接且重置每连接序号', () => {
    const store = useNotificationStore()
    store.connect()
    const first = FakeWebSocket.instances[0]
    first.open()
    first.emit({
      type: 'notice.unread',
      version: 1,
      sequence: 1,
      occurredAt,
      data: { unreadCount: 1 }
    })

    store.disconnect()
    store.connect()
    const second = FakeWebSocket.instances[1]
    second.open()
    first.finishClose()
    expect(store.connected).toBe(true)
    second.emit({
      type: 'notice.unread',
      version: 1,
      sequence: 1,
      occurredAt,
      data: { unreadCount: 5 }
    })
    expect(store.unreadCount).toBe(5)
  })
})
