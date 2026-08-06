/** 前端接口封装：assistant。 */
import request from '@/utils/http'
import { useUserStore } from '@/store/modules/user'
import { ApiStatus } from '@/utils/http/status'

interface AssistantStreamHandlers {
  onUser?: (message: Api.Assistant.Message) => void
  onReasoning?: (chunk: string) => void
  onContent?: (chunk: string) => void
  onDone?: (payload: { message?: Api.Assistant.Message; session?: Api.Assistant.Session }) => void
  onError?: (payload: { code?: number; msg?: string }) => void
}

export function fetchAssistantAgents() {
  return request.get<Api.Assistant.AgentSummary[]>({
    url: '/api/v1/assistant/agents'
  })
}

export function fetchAssistantSession(params: Api.Assistant.SessionQuery) {
  return request.get<Api.Assistant.Session>({
    url: '/api/v1/assistant/sessions/current',
    params
  })
}

export function fetchAssistantModelOptions(agentCode: string) {
  return request.get<Api.Assistant.ModelOptions>({
    url: `/api/v1/assistant/agents/${agentCode}/model-options`
  })
}

export function fetchAssistantMessages(sessionId: number) {
  return request.get<Api.Assistant.Message[]>({
    url: `/api/v1/assistant/sessions/${sessionId}/messages`
  })
}

export function deleteAssistantSession(sessionId: number) {
  return request.del<{ id: number }>({
    url: `/api/v1/assistant/sessions/${sessionId}`
  })
}

export function fetchAssistantSessions(params: Api.Assistant.SessionHistoryQuery) {
  return request.get<Api.Assistant.SessionHistoryResponse>({
    url: '/api/v1/assistant/sessions',
    params
  })
}

export async function streamAssistantSession(
  sessionId: number,
  payload: Api.Assistant.StreamRequest,
  handlers: AssistantStreamHandlers = {},
  signal?: AbortSignal
) {
  const userStore = useUserStore()
  const baseUrl = (import.meta.env.VITE_API_URL || '').trim().replace(/\/$/, '')
  const requestUrl = `${baseUrl}/api/v1/assistant/sessions/${sessionId}/stream`

  const response = await fetch(requestUrl, {
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
    userStore.logOut()
    throw new Error('UNAUTHORIZED')
  }

  if (!response.ok || !response.body) {
    throw new Error(`ASSISTANT_STREAM_FAILED:${response.status}`)
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8')
  let buffer = ''

  const consumeEventBlock = (block: string) => {
    const lines = block.split(/\r?\n/)
    let eventName = 'message'
    const dataLines: string[] = []

    lines.forEach((line) => {
      if (line.startsWith('event:')) {
        eventName = line.slice(6).trim()
      }
      if (line.startsWith('data:')) {
        dataLines.push(line.slice(5).trimStart())
      }
    })

    const payloadText = dataLines.join('\n')
    const eventPayload = payloadText ? JSON.parse(payloadText) : {}

    if (eventName === 'user' && eventPayload.message) {
      handlers.onUser?.(eventPayload.message)
      return
    }
    if (eventName === 'reasoning') {
      handlers.onReasoning?.(eventPayload.content || '')
      return
    }
    if (eventName === 'content') {
      handlers.onContent?.(eventPayload.content || '')
      return
    }
    if (eventName === 'error') {
      handlers.onError?.(eventPayload)
      return
    }
    if (eventName === 'done') {
      handlers.onDone?.(eventPayload)
    }
  }

  while (true) {
    const { done, value } = await reader.read()
    buffer += decoder.decode(value || new Uint8Array(), { stream: !done })
    buffer = buffer.replace(/\r\n/g, '\n')

    let separatorIndex = buffer.indexOf('\n\n')
    while (separatorIndex >= 0) {
      const block = buffer.slice(0, separatorIndex).trim()
      buffer = buffer.slice(separatorIndex + 2)
      if (block) {
        consumeEventBlock(block)
      }
      separatorIndex = buffer.indexOf('\n\n')
    }

    if (done) {
      const rest = buffer.trim()
      if (rest) {
        consumeEventBlock(rest)
      }
      break
    }
  }
}
