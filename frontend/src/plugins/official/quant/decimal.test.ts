import { describe, expect, it } from 'vitest'
import { decimalPercent } from './decimal'

describe('Quant Decimal percentage', () => {
  it('formats ratios without floating point arithmetic', () => {
    expect(decimalPercent('0.1234')).toBe('12.34')
    expect(decimalPercent('-0.01')).toBe('-1.00')
    expect(decimalPercent('1')).toBe('100.00')
  })
})
