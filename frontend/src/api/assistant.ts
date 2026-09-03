/** 平台智能助手 API 与 SSE 事件解析。 */
import request from '@/utils/http'
import { useUserStore } from '@/store/modules/user'
import { ApiStatus } from '@/utils/http/status'

interface AssistantStreamHandlers {
  onUser?: (message: Api.Assistant.Message) => void
  onTool?: (payload: { name: string; status: 'running' | 'completed' | 'failed' }) => void
  onContent?: (chunk: string) => void
  onDone?: (payload: { message?: Api.Assistant.Message; session?: Api.Assistant.Session }) => void
  onError?: (payload: { code?: number; msg?: string }) => void
}

export function fetchAssistantModels() {
  return request.get<Api.Assistant.ModelOption[]>({ url: '/api/v1/assistant/models' })
}

export function fetchAssistantSessions() {
  return request.get<Api.Assistant.Session[]>({ url: '/api/v1/assistant/sessions' })
}

export function createAssistantSession(params: Api.Assistant.SessionCreateRequest) {
  return request.post<Api.Assistant.Session>({ url: '/api/v1/assistant/sessions', params })
}

export function fetchAssistantSession(sessionId: number) {
  return request.get<Api.Assistant.Session>({ url: `/api/v1/assistant/sessions/${sessionId}` })
}

export function fetchAssistantMessages(sessionId: number) {
  return request.get<Api.Assistant.Message[]>({
    url: `/api/v1/assistant/sessions/${sessionId}/messages`
  })
}

export function deleteAssistantSession(sessionId: number) {
  return request.del<{ id: number }>({ url: `/api/v1/assistant/sessions/${sessionId}` })
}

export async function streamAssistantSession(
  sessionId: number,
  payload: Api.Assistant.StreamRequest,
  handlers: AssistantStreamHandlers = {},
  signal?: AbortSignal
) {
  const userStore = useUserStore()
  const baseUrl = (import.meta.env.VITE_API_URL || '').trim().replace(/\/$/, '')
  const response = await fetch(`${baseUrl}/api/v1/assistant/sessions/${sessionId}/stream`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(userStore.accessToken ? { Authorization: `Bearer ${userStore.accessToken}` } : {})
    },
    body: JSON.stringify(payload),
    credentials: import.meta.env.VITE_WITH_CREDENTIALS === 'true' ? 'include' : 'same-origin',
    signal
  })

  if (response.status === ApiStatus.unauthorized) {
    userStore.clearSession()
    throw new Error('UNAUTHORIZED')
  }
  if (!response.ok || !response.body) {
    const error = await response.json().catch(() => null)
    throw new Error(error?.msg || `ASSISTANT_STREAM_FAILED:${response.status}`)
  }

  let streamDone = false
  const consumeEvent = (block: string) => {
    let eventName = 'message'
    const data: string[] = []
    block.split(/\r?\n/).forEach((line) => {
      if (line.startsWith('event:')) eventName = line.slice(6).trim()
      if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
    })
    const eventPayload = data.length ? JSON.parse(data.join('\n')) : {}

    if (eventName === 'user' && eventPayload.message) handlers.onUser?.(eventPayload.message)
    else if (eventName === 'tool') handlers.onTool?.(eventPayload)
    else if (eventName === 'content') handlers.onContent?.(eventPayload.content || '')
    else if (eventName === 'done') {
      streamDone = true
      handlers.onDone?.(eventPayload)
    } else if (eventName === 'error') handlers.onError?.(eventPayload)
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  while (true) {
    const { done, value } = await reader.read()
    buffer += decoder.decode(value || new Uint8Array(), { stream: !done }).replace(/\r\n/g, '\n')
    let separator = buffer.indexOf('\n\n')
    while (separator >= 0) {
      const block = buffer.slice(0, separator).trim()
      buffer = buffer.slice(separator + 2)
      if (block) consumeEvent(block)
      separator = buffer.indexOf('\n\n')
    }
    if (done) break
  }
  if (buffer.trim()) consumeEvent(buffer.trim())
  if (!streamDone) throw new Error('ASSISTANT_STREAM_INTERRUPTED')
}
