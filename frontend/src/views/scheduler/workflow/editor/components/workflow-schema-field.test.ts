import { describe, expect, it } from 'vitest'
import { buildSchemaFields } from './workflow-schema-field'

describe('workflow schema fields', () => {
  it('maps every supported structured shape without a JSON fallback', () => {
    const fields = buildSchemaFields(
      {
        amount: { type: 'string', format: 'decimal' },
        runAt: { type: 'string', format: 'date-time' },
        source: { type: 'string', format: 'code' },
        settings: { type: 'object', format: 'key-value' },
        parameterSchema: { type: 'object', format: 'field-schema' },
        risk: { type: 'object', properties: { leverage: { type: 'integer' } } },
        rows: {
          type: 'array',
          items: { type: 'object', properties: { name: { type: 'string' } } }
        },
        symbols: { type: 'array', items: { type: 'string' } }
      },
      ['amount']
    )

    expect(Object.fromEntries(fields.map((field) => [field.key, field.control]))).toEqual({
      amount: 'decimal',
      runAt: 'datetime',
      source: 'code',
      settings: 'keyValue',
      parameterSchema: 'fieldSchema',
      risk: 'object',
      rows: 'objectList',
      symbols: 'scalarList'
    })
    expect(fields.find((field) => field.key === 'amount')?.required).toBe(true)
    expect(fields.find((field) => field.key === 'risk')?.childFields[0]?.key).toBe('leverage')
    expect(fields.find((field) => field.key === 'rows')?.childFields[0]?.key).toBe('name')
    expect(fields.every((field) => !String(field.control).toLowerCase().includes('json'))).toBe(
      true
    )
  })
})
