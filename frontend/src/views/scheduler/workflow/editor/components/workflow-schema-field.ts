/**
 * 工作流编辑器辅助模块：workflow-schema-field。
 *
 * 把后端下发的 JSON Schema 片段翻译成「表单该用哪个控件」。
 * 抽成独立模块是为了让 WorkflowSchemaFields（一组字段）和 WorkflowSchemaField（单个控件）
 * 共用同一份解析结果，不各写一遍。
 *
 * 只实现项目实际用到的那一小部分 Schema 语义：
 * type / title / description / default / enum / minimum / maximum / items.properties。
 */

/** 控件类型。objectList 是「对象数组」的行编辑器，比让用户手写 JSON 好用得多。 */
export type SchemaFieldControl =
  | 'text'
  | 'enum'
  | 'multiEnum'
  | 'stringList'
  | 'number'
  | 'boolean'
  | 'json'
  | 'objectList'

export interface SchemaFieldMeta {
  key: string
  title: string
  description: string
  control: SchemaFieldControl
  options: Array<{ value: string; label: string }>
  min?: number
  max?: number
  step?: number
  placeholder: string
  multiline: boolean
  secret: boolean
  /** control 为 json 时：这个字段是数组还是对象，决定校验与空值。 */
  isArray: boolean
  /** control 为 objectList 时：每一行内部的子字段。 */
  itemFields: SchemaFieldMeta[]
}

/** 内容偏长、值得用多行文本框的字段名关键字。 */
const MULTILINE_HINTS = ['prompt', 'template', 'content', 'message', 'body']

const isMultiline = (key: string, schema: Record<string, any>) => {
  if (schema.type !== 'string' && schema.type !== undefined) return false
  const lowered = key.toLowerCase()
  return MULTILINE_HINTS.some((hint) => lowered.includes(hint))
}

const resolveControl = (schema: Record<string, any>): SchemaFieldControl => {
  if (Array.isArray(schema.enum) && schema.enum.length) return 'enum'
  if (schema.type === 'integer' || schema.type === 'number') return 'number'
  if (schema.type === 'boolean') return 'boolean'
  if (schema.type === 'array') {
    if (Array.isArray(schema.items?.enum) && schema.items.enum.length) return 'multiEnum'
    if (schema.items?.type === 'string') return 'stringList'
    return schema.items?.properties ? 'objectList' : 'json'
  }
  if (schema.type === 'object') return 'json'
  return 'text'
}

export function buildSchemaField(key: string, raw: unknown): SchemaFieldMeta {
  const schema = (raw || {}) as Record<string, any>
  const control = resolveControl(schema)
  const isArray = schema.type === 'array'

  let placeholder = ''
  if (control === 'json') {
    placeholder = isArray ? '[]' : '{}'
  } else if (schema.default !== undefined && schema.default !== '') {
    placeholder = `默认 ${schema.default}`
  }

  return {
    key,
    title: String(schema.title || key),
    description: String(schema.description || ''),
    control,
    options: (control === 'multiEnum' ? schema.items?.enum || [] : schema.enum || []).map(
      (item: unknown, index: number) => ({
        value: String(item),
        label: String(
          (control === 'multiEnum' ? schema.items?.enumLabels : schema.enumLabels)?.[index] || item
        )
      })
    ),
    min: typeof schema.minimum === 'number' ? schema.minimum : undefined,
    max: typeof schema.maximum === 'number' ? schema.maximum : undefined,
    step: schema.type === 'integer' ? 1 : undefined,
    placeholder,
    multiline: isMultiline(key, schema),
    secret: schema['x-coinsphere-secret'] === true,
    isArray,
    itemFields: control === 'objectList' ? buildSchemaFields(schema.items?.properties || {}) : []
  }
}

/** 把一份 properties 翻译成有序的字段列表。 */
export function buildSchemaFields(properties: Record<string, unknown>): SchemaFieldMeta[] {
  return Object.entries(properties || {}).map(([key, raw]) => buildSchemaField(key, raw))
}
