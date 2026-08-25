import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/utils/http', () => ({ default: mocks }))

import {
  applyResultViewBatchAction,
  createResultView,
  fetchResultViewBatches,
  fetchResultViews,
  pauseResultViewWorkflow,
  replaceResultViewGrants,
  revokeResultView
} from './resultViews'

describe('result view API', () => {
  beforeEach(() => Object.values(mocks).forEach((mock) => mock.mockReset()))

  it('uses the core view lifecycle routes', () => {
    const payload = {
      name: 'Paper',
      pluginId: 'official.quant',
      pageKey: 'paper',
      scope: { workflowId: 41, signalNodeInstanceId: 'signal', paperNodeInstanceId: 'paper' },
      filters: { market: 'spot' },
      allowedActions: ['approve'],
      userIds: [2],
      roleCodes: ['R_USER']
    }

    fetchResultViews()
    createResultView(payload)
    replaceResultViewGrants(7, { userIds: [3], roleCodes: [] })
    revokeResultView(7)
    fetchResultViewBatches(7)
    applyResultViewBatchAction(7, 19, 'cancel')
    pauseResultViewWorkflow(7)

    expect(mocks.get).toHaveBeenCalledWith({ url: '/api/v1/result-views' })
    expect(mocks.post).toHaveBeenNthCalledWith(1, {
      url: '/api/v1/result-views',
      params: payload,
      showSuccessMessage: true
    })
    expect(mocks.put).toHaveBeenCalledWith({
      url: '/api/v1/result-views/7/grants',
      params: { userIds: [3], roleCodes: [] },
      showSuccessMessage: true
    })
    expect(mocks.post).toHaveBeenNthCalledWith(2, {
      url: '/api/v1/result-views/7/revoke',
      showSuccessMessage: true
    })
    expect(mocks.get).toHaveBeenNthCalledWith(2, {
      url: '/api/v1/result-views/7/batches'
    })
    expect(mocks.post).toHaveBeenNthCalledWith(3, {
      url: '/api/v1/result-views/7/batches/19/cancel',
      showSuccessMessage: true
    })
    expect(mocks.post).toHaveBeenNthCalledWith(4, {
      url: '/api/v1/result-views/7/workflow/pause',
      showSuccessMessage: true
    })
  })
})
