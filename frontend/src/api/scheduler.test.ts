import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  randomUUID: vi.fn()
}))

vi.mock('@/utils/http', () => ({
  default: { get: mocks.get, post: mocks.post }
}))

import {
  fetchCancelWorkflowExecution,
  fetchDecideWorkflowAction,
  fetchRerunWorkflowExecution,
  fetchRunWorkflowDefinition,
  fetchWorkflowDefinitionExecutions,
  fetchWorkflowExecutionDetail,
  fetchWorkflowExecutionList
} from './scheduler'

describe('scheduler API', () => {
  beforeEach(() => {
    mocks.post.mockReset()
    mocks.get.mockReset()
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
      url: '/api/v1/workflow-executions',
      params: { workflowDefinitionId: 42, ...params },
      headers: { 'Idempotency-Key': 'run-key-1' },
      showSuccessMessage: true
    })
    expect(mocks.post).toHaveBeenNthCalledWith(2, {
      url: '/api/v1/workflow-executions',
      params: { workflowDefinitionId: 42, ...params },
      headers: { 'Idempotency-Key': 'run-key-2' },
      showSuccessMessage: true
    })
  })

  it('uses the explicit workflow execution routes', () => {
    mocks.randomUUID.mockReturnValue('rerun-key')

    fetchWorkflowExecutionList({ status: 'running' })
    fetchWorkflowDefinitionExecutions(42, { limit: 20 })
    fetchWorkflowExecutionDetail(9)
    fetchCancelWorkflowExecution(9)
    fetchRerunWorkflowExecution(9)

    expect(mocks.get).toHaveBeenNthCalledWith(1, {
      url: '/api/v1/workflow-executions',
      params: { status: 'running' }
    })
    expect(mocks.get).toHaveBeenNthCalledWith(2, {
      url: '/api/v1/workflow-executions',
      params: { limit: 20, workflowDefinitionId: 42 }
    })
    expect(mocks.get).toHaveBeenNthCalledWith(3, {
      url: '/api/v1/workflow-executions/9'
    })
    expect(mocks.post).toHaveBeenNthCalledWith(1, {
      url: '/api/v1/workflow-executions/9/cancel',
      showSuccessMessage: true
    })
    expect(mocks.post).toHaveBeenNthCalledWith(2, {
      url: '/api/v1/workflow-executions/9/rerun',
      headers: { 'Idempotency-Key': 'rerun-key' },
      showSuccessMessage: true
    })
  })

  it('sends idempotency and reauthentication headers for workflow decisions', () => {
    mocks.randomUUID.mockReturnValue('decision-key')
    const params = { decision: 'approved' as const, formData: { reason: 'reviewed' } }

    fetchDecideWorkflowAction('action/id', params, 'reauth-token')

    expect(mocks.post).toHaveBeenCalledWith({
      url: '/api/v1/workflow-actions/action%2Fid/decisions',
      params,
      headers: {
        'Idempotency-Key': 'decision-key',
        'X-Reauth-Token': 'reauth-token'
      },
      showSuccessMessage: true
    })
  })
})
