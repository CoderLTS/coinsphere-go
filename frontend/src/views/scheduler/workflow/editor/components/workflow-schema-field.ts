/** 把后端 JSON Schema 的已用子集映射为结构化控件。 */
export type SchemaFieldControl =
  | 'text'
  | 'enum'
  | 'number'
  | 'boolean'
  | 'decimal'
  | 'datetime'
  | 'code'
  | 'object'
  | 'objectList'
  | 'scalarList'
  | 'keyValue'
  | 'fieldSchema'

export interface SchemaFieldMeta {
  key: string
  title: string
  description: string
  control: SchemaFieldControl
  options: Array<{ value: string | number; label: string }>
  min?: number
  max?: number
  step?: number
  placeholder: string
  multiline: boolean
  required: boolean
  language: string
  secret: boolean
  schema: Record<string, any>
  itemField?: SchemaFieldMeta
  childFields: SchemaFieldMeta[]
}

const MULTILINE_HINTS = ['prompt', 'instruction', 'template', 'content', 'message', 'body']

const resolveControl = (schema: Record<string, any>): SchemaFieldControl => {
  if (Array.isArray(schema.enum) && schema.enum.length) return 'enum'
  if (schema.format === 'decimal') return 'decimal'
  if (schema.format === 'date-time') return 'datetime'
  if (schema.format === 'code') return 'code'
  if (schema.format === 'field-schema') return 'fieldSchema'
  if (schema.format === 'key-value') return 'keyValue'
  if (schema.type === 'integer' || schema.type === 'number') return 'number'
  if (schema.type === 'boolean') return 'boolean'
  if (schema.type === 'array') return schema.items?.properties ? 'objectList' : 'scalarList'
  if (schema.type === 'object') return schema.properties ? 'object' : 'keyValue'
  return 'text'
}

const placeholderFor = (control: SchemaFieldControl, schema: Record<string, any>) => {
  if (control === 'decimal') return '0.00'
  if (control === 'datetime') return '请选择时间'
  if (control === 'enum') return '请选择'
  if (schema.default !== undefined && schema.default !== '') return `默认 ${schema.default}`
  return ''
}

export function buildSchemaField(
  key: string,
  raw: unknown,
  requiredKeys: string[] = []
): SchemaFieldMeta {
  const schema = (raw || {}) as Record<string, any>
  const control = resolveControl(schema)
  const optionValues = Array.isArray(schema.enum) ? schema.enum : []
  const itemSchema = (schema.items || {}) as Record<string, any>
  const childSchema =
    control === 'objectList'
      ? itemSchema
      : control === 'object'
        ? schema
        : ({} as Record<string, any>)

  return {
    key,
    title: String(schema.title || key),
    description: String(schema.description || ''),
    control,
    options: optionValues.map((item: unknown, index: number) => ({
      value: item as string | number,
      label: String(schema.enumLabels?.[index] || item)
    })),
    min: typeof schema.minimum === 'number' ? schema.minimum : undefined,
    max: typeof schema.maximum === 'number' ? schema.maximum : undefined,
    step: schema.type === 'integer' ? 1 : undefined,
    placeholder: placeholderFor(control, schema),
    multiline:
      schema.format === 'multiline' ||
      MULTILINE_HINTS.some((hint) => key.toLowerCase().includes(hint)),
    required: requiredKeys.includes(key),
    language: String(schema.language || 'plaintext'),
    secret: Boolean(schema.secret),
    schema,
    itemField:
      control === 'scalarList' ? buildSchemaField(`${key}-item`, itemSchema, []) : undefined,
    childFields: buildSchemaFields(
      (childSchema.properties || {}) as Record<string, unknown>,
      Array.isArray(childSchema.required) ? childSchema.required.map(String) : []
    )
  }
}

export function buildSchemaFields(
  properties: Record<string, unknown>,
  requiredKeys: string[] = []
): SchemaFieldMeta[] {
  return Object.entries(properties || {}).map(([key, raw]) =>
    buildSchemaField(key, raw, requiredKeys)
  )
}
