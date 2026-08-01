import { describe, expect, it } from 'vitest'

import { ApiStatus } from './status'

describe('ApiStatus', () => {
  it('保留认证与服务端错误的标准 HTTP 状态码', () => {
    expect(ApiStatus.unauthorized).toBe(401)
    expect(ApiStatus.forbidden).toBe(403)
    expect(ApiStatus.internalServerError).toBe(500)
  })
})
