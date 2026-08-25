import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), request: vi.fn() }))
vi.mock('@/utils/http', () => ({ default: mocks }))

import {
  decidePaperSignal,
  exportPaperResult,
  fetchNotificationResult,
  fetchPaperResult
} from './paper'

describe('shared Paper API', () => {
  beforeEach(() => Object.values(mocks).forEach((mock) => mock.mockReset()))

  it('scopes every plugin request through one result view', () => {
    fetchPaperResult(12)
    decidePaperSignal(12, 9, 'approve')
    exportPaperResult(12)
    fetchNotificationResult(13)

    expect(mocks.get).toHaveBeenNthCalledWith(1, {
      url: '/api/v1/result-views/12/plugins/official.quant/paper'
    })
    expect(mocks.post).toHaveBeenCalledWith({
      url: '/api/v1/result-views/12/plugins/official.quant/signals/9/approve',
      showSuccessMessage: true
    })
    expect(mocks.request).toHaveBeenCalledWith({
      url: '/api/v1/result-views/12/plugins/official.quant/paper/export',
      method: 'GET',
      responseType: 'blob',
      rawResponse: true
    })
    expect(mocks.get).toHaveBeenNthCalledWith(2, {
      url: '/api/v1/result-views/13/plugins/official.notification/deliveries'
    })
  })
})
