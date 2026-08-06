import { describe, expect, it } from 'vitest'

import { useCursorPagination } from './useCursorPagination'

describe('useCursorPagination', () => {
  it('uses the returned cursor for the next page', () => {
    const { applyPage, moveTo, requestParams } = useCursorPagination(10)

    applyPage({ records: [], nextCursor: 'page-2', hasMore: true, total: 11 })

    expect(moveTo(2)).toBe(true)
    expect(requestParams()).toEqual({ cursor: 'page-2', limit: 10 })
  })

  it('clears future cursors after refreshing a previous page', () => {
    const { applyPage, moveTo, requestParams } = useCursorPagination(10)

    applyPage({ records: [], nextCursor: 'page-2', hasMore: true, total: 21 })
    moveTo(2)
    applyPage({ records: [], nextCursor: 'page-3', hasMore: true, total: 21 })
    moveTo(3)
    moveTo(1)
    applyPage({ records: [], nextCursor: 'updated-page-2', hasMore: true, total: 21 })

    expect(moveTo(3)).toBe(false)
    expect(moveTo(2)).toBe(true)
    expect(requestParams()).toEqual({ cursor: 'updated-page-2', limit: 10 })
  })
})
