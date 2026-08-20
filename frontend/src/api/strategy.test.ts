import { describe, expect, it, vi } from 'vitest'

vi.mock('@/utils/http', () => ({
  default: { get: vi.fn(), post: vi.fn(), put: vi.fn() }
}))

import { parseStrategyParameterSchema } from './strategy'

describe('strategy parameter schema', () => {
  it('accepts only JSON objects', () => {
    expect(parseStrategyParameterSchema('{"lookback":{"type":"integer"}}')).toEqual({
      lookback: { type: 'integer' }
    })
    expect(parseStrategyParameterSchema('[]')).toBeNull()
    expect(parseStrategyParameterSchema('{')).toBeNull()
  })
})
