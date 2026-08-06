import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
  randomUUID: vi.fn()
}))

vi.mock('@/utils/http', () => ({
  default: { post: mocks.post }
}))

import { fetchRunWorkflowDefinition } from './scheduler'

describe('scheduler API', () => {
  beforeEach(() => {
    mocks.post.mockReset()
    mocks.randomUUID.mockReset()
    vi.stubGlobal('crypto', { randomUUID: mocks.randomUUID })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('assigns one idempotency key to each manual workflow run request', () => {
    mocks.randomUUID.mockReturnValueOnce('run-key-1').mockReturnValueOnce('run-key-2')
    const params = { startEntryKeys: ['manual-entry'], inputs: { symbol: 'BTCUSDT' } }

    fetchRunWorkflowDefinition(42, params)
    fetchRunWorkflowDefinition(42, params)

    expect(mocks.randomUUID).toHaveBeenCalledTimes(2)
    expect(mocks.post).toHaveBeenNthCalledWith(1, {
      url: '/api/v1/workflows/42/executions',
      params,
      headers: { 'Idempotency-Key': 'run-key-1' },
      showSuccessMessage: true
    })
    expect(mocks.post).toHaveBeenNthCalledWith(2, {
      url: '/api/v1/workflows/42/executions',
      params,
      headers: { 'Idempotency-Key': 'run-key-2' },
      showSuccessMessage: true
    })
  })
})
