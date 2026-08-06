import { describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/utils/http', () => ({
  default: { post: mocks.post }
}))

import { logout } from './auth'

describe('auth API', () => {
  it('sends the captured token explicitly when logging out', () => {
    logout('captured-access-token')

    expect(mocks.post).toHaveBeenCalledWith({
      url: '/api/v1/auth/logout',
      headers: { Authorization: 'Bearer captured-access-token' },
      showErrorMessage: false
    })
  })
})
