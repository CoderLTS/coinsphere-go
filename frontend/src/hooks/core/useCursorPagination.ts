import { reactive } from 'vue'

export interface CursorPage<T> {
  records: T[]
  nextCursor: string
  hasMore: boolean
  total: number
}

export function useCursorPagination(initialLimit = 50) {
  const pagination = reactive({ current: 1, size: initialLimit, total: 0 })
  const cursors = new Map<number, string>([[1, '']])

  const requestParams = () => ({
    cursor: cursors.get(pagination.current) || undefined,
    limit: pagination.size
  })

  const applyPage = <T>(page: CursorPage<T>) => {
    pagination.total = page.total
    for (const pageNumber of cursors.keys()) {
      if (pageNumber > pagination.current) cursors.delete(pageNumber)
    }
    if (page.hasMore && page.nextCursor) {
      cursors.set(pagination.current + 1, page.nextCursor)
    }
  }

  const reset = (limit = pagination.size) => {
    pagination.current = 1
    pagination.size = limit
    pagination.total = 0
    cursors.clear()
    cursors.set(1, '')
  }

  const moveTo = (page: number) => {
    if (page < 1 || !cursors.has(page)) return false
    pagination.current = page
    return true
  }

  return { pagination, requestParams, applyPage, reset, moveTo }
}
