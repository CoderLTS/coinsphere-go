<!--
  工作流编辑器组件：WorkflowSchemaFields。

  按后端下发的 configSchema 渲染一组节点配置字段。后端每种节点都声明了完整的 JSON Schema，
  前端没必要再为每个节点手写一遍一模一样的表单 —— 那样每加一种节点都要改
  WorkflowNodeEditorCard 那个大文件。开始、任务、通知、条件这几种有联动、有远程选项，
  仍然保留各自的定制表单；其余节点全走这里。

  本组件是「受控」的：不直接改父组件传进来的 config，每次改动都 emit('update', key, value)，
  由父组件写回自己的草稿并决定何时提交。

  控件映射见 workflow-schema-field.ts。其中对象数组（switch 的 cases、state.set 的 assignments）
  用行编辑器而不是 JSON 文本域 —— 手写 JSON 容易写错，也看不出每行该填什么；
  有固定 properties 的对象也会按字段分组，只有动态对象才提供键值行与高级 JSON。
-->
<template>
  <template v-for="field in visibleFields" :key="field.key">
    <ElFormItem :label="field.title">
      <!-- 对象数组：一行一条，行内再按子 schema 渲染。 -->
      <template v-if="field.control === 'objectList'">
        <div class="schema-fields__list">
          <div v-for="(row, index) in rowsOf(field)" :key="index" class="schema-fields__row">
            <div class="schema-fields__row-head">
              <span class="schema-fields__row-title">{{ field.title }} {{ index + 1 }}</span>
              <div class="schema-fields__row-actions">
                <ElButton
                  link
                  type="primary"
                  :disabled="index === 0"
                  @click="moveRow(field, index, -1)"
                >
                  上移
                </ElButton>
                <ElButton
                  link
                  type="primary"
                  :disabled="index === rowsOf(field).length - 1"
                  @click="moveRow(field, index, 1)"
                >
                  下移
                </ElButton>
                <ElButton link type="danger" @click="removeRow(field, index)">删除</ElButton>
              </div>
            </div>

            <ElFormItem
              v-for="sub in field.itemFields"
              :key="sub.key"
              :label="sub.title"
              class="schema-fields__sub"
            >
              <WorkflowSchemaField
                :field="sub"
                :value="row?.[sub.key]"
                @update="updateRow(field, index, sub.key, $event)"
              />
              <div v-if="sub.description" class="schema-fields__hint">{{ sub.description }}</div>
            </ElFormItem>
          </div>

          <ElButton class="schema-fields__add" @click="addRow(field)">
            添加{{ field.title }}
          </ElButton>
        </div>
      </template>

      <!-- 固定结构对象按字段分组展示，避免把有 schema 的参数退化为整块 JSON。 -->
      <template v-else-if="field.control === 'object'">
        <div class="schema-fields__object">
          <div v-for="sub in field.objectFields" :key="sub.key" class="schema-fields__object-field">
            <label>{{ sub.title }}</label>
            <WorkflowSchemaField
              :field="sub"
              :value="config[field.key]?.[sub.key]"
              @update="updateObjectField(field, sub.key, $event)"
            />
            <div v-if="sub.description" class="schema-fields__hint">{{ sub.description }}</div>
          </div>
        </div>
        <details class="schema-fields__advanced">
          <summary>高级 JSON</summary>
          <ElInput
            v-model="jsonDrafts[field.key]"
            type="textarea"
            :rows="4"
            :placeholder="field.placeholder"
            @blur="commitJsonField(field)"
          />
          <div v-if="jsonErrors[field.key]" class="schema-fields__error">
            {{ jsonErrors[field.key] }}
          </div>
        </details>
      </template>

      <!-- 动态对象优先用键值行编辑；高级 JSON 仍保留给嵌套结构。 -->
      <template v-else-if="field.control === 'objectMap'">
        <div class="schema-fields__map">
          <div
            v-for="(entry, index) in mapEntriesOf(field)"
            :key="`${field.key}-${index}`"
            class="schema-fields__map-row"
          >
            <ElInput
              :model-value="entry.key"
              class="schema-fields__map-key"
              placeholder="参数名"
              @change="updateMapKey(field, index, $event)"
            />
            <ElInput
              :model-value="mapValueText(entry.value)"
              class="schema-fields__map-value"
              placeholder="参数值"
              @change="updateMapValue(field, index, $event)"
            />
            <ElButton link type="danger" @click="removeMapEntry(field, index)">删除</ElButton>
          </div>
          <ElEmpty v-if="!mapEntriesOf(field).length" :image-size="32" description="暂无参数" />
          <ElButton class="schema-fields__add" @click="addMapEntry(field)">
            添加{{ field.title }}
          </ElButton>
        </div>
        <details class="schema-fields__advanced">
          <summary>高级 JSON</summary>
          <ElInput
            v-model="jsonDrafts[field.key]"
            type="textarea"
            :rows="4"
            :placeholder="field.placeholder"
            @blur="commitJsonField(field)"
          />
          <div v-if="jsonErrors[field.key]" class="schema-fields__error">
            {{ jsonErrors[field.key] }}
          </div>
        </details>
      </template>

      <!-- 非对象数组 / 对象：仍用 JSON 文本域，失焦时校验并回写。 -->
      <template v-else-if="field.control === 'json'">
        <ElInput
          v-model="jsonDrafts[field.key]"
          type="textarea"
          :rows="4"
          :placeholder="field.placeholder"
          @blur="commitJsonField(field)"
        />
        <div v-if="jsonErrors[field.key]" class="schema-fields__error">
          {{ jsonErrors[field.key] }}
        </div>
      </template>

      <WorkflowSchemaField
        v-else
        :field="field"
        :value="config[field.key]"
        @update="emitUpdate(field.key, $event)"
      />

      <div v-if="field.description" class="schema-fields__hint">{{ field.description }}</div>
    </ElFormItem>
  </template>
</template>

<script setup lang="ts">
  import WorkflowSchemaField from './WorkflowSchemaField.vue'
  import { buildSchemaFields, type SchemaFieldMeta } from './workflow-schema-field'
  import { fetchGetOutboundProxies } from '@/api/system'

  interface Props {
    /** 后端下发的该节点类型的 configSchema。 */
    schema: Record<string, any>
    /** 节点当前配置（只读展示，改动一律通过 update 事件回传）。 */
    config: Record<string, any>
    /** 独立 UI Schema：控制字段顺序、控件、占位符和简单条件显示。 */
    uiSchema?: Record<string, any>
    /** 只渲染这几个字段；留空表示渲染 schema 里的全部字段。 */
    keys?: string[]
  }

  interface Emits {
    (e: 'update', key: string, value: any): void
  }

  const props = defineProps<Props>()
  const emit = defineEmits<Emits>()
  const proxyOptions = ref<Array<{ value: number; label: string }>>([{ value: 0, label: '直连' }])

  watch(
    () => Boolean(props.schema?.properties?.proxyId?.['x-coinsphere-proxy']),
    async (required) => {
      if (!required) return
      try {
        const proxies = await fetchGetOutboundProxies()
        proxyOptions.value = [
          { value: 0, label: '直连' },
          ...proxies
            .filter((proxy) => proxy.isEnabled)
            .map((proxy) => ({ value: proxy.id, label: proxy.name }))
        ]
      } catch {
        proxyOptions.value = [{ value: 0, label: '直连' }]
      }
    },
    { immediate: true }
  )

  const fields = computed(() => {
    const order = Array.isArray(props.uiSchema?.['ui:order']) ? props.uiSchema['ui:order'] : []
    const rank = new Map(order.map((key: string, index: number) => [key, index]))
    const built = buildSchemaFields(props.schema?.properties || {})
      .map((field) => {
        const ui = props.uiSchema?.[field.key] || {}
        const schema = props.schema?.properties?.[field.key] || {}
        return {
          ...field,
          ...(schema['x-coinsphere-proxy']
            ? { control: 'enum' as const, options: proxyOptions.value }
            : {}),
          multiline: ui['ui:widget'] === 'textarea' || field.multiline,
          placeholder: String(ui['ui:placeholder'] || field.placeholder)
        }
      })
      .filter((field) => {
        const condition = props.uiSchema?.[field.key]?.['ui:condition']
        return !condition || props.config?.[condition.field] === condition.equals
      })
      .sort(
        (left, right) =>
          (rank.get(left.key) ?? Number.MAX_SAFE_INTEGER) -
          (rank.get(right.key) ?? Number.MAX_SAFE_INTEGER)
      )
    const optionMap = props.schema?.properties?.entryKey?.enumByWorkflowCode
    if (!optionMap || typeof optionMap !== 'object') return built
    const options = optionMap[String(props.config?.workflowCode || '')]
    return built.map((field) =>
      field.key === 'entryKey'
        ? {
            ...field,
            control: 'enum' as const,
            options: Array.isArray(options) ? options : [],
            placeholder: '请选择开始入口'
          }
        : field
    )
  })
  const visibleFields = computed(() =>
    props.keys?.length
      ? fields.value.filter((field) => props.keys?.includes(field.key))
      : fields.value
  )

  const emitUpdate = (key: string, value: any) => {
    emit('update', key, value)
    if (key === 'workflowCode' && props.schema?.properties?.entryKey?.enumByWorkflowCode) {
      emit('update', 'entryKey', '')
    }
  }

  // ---------- 对象数组行编辑 ----------

  const rowsOf = (field: SchemaFieldMeta): Record<string, any>[] => {
    const value = props.config?.[field.key]
    return Array.isArray(value) ? value : []
  }

  /** 所有行操作都构造新数组再整体回传，不就地改父组件的数据。 */
  const commitRows = (field: SchemaFieldMeta, rows: Record<string, any>[]) => {
    emit('update', field.key, rows)
  }

  const updateRow = (field: SchemaFieldMeta, index: number, key: string, value: any) => {
    const rows = rowsOf(field).map((row, i) =>
      i === index ? { ...row, [key]: value } : { ...row }
    )
    commitRows(field, rows)
  }

  const addRow = (field: SchemaFieldMeta) => {
    const blank: Record<string, any> = {}
    field.itemFields.forEach((sub) => {
      blank[sub.key] = sub.control === 'boolean' ? false : sub.control === 'number' ? undefined : ''
    })
    commitRows(field, [...rowsOf(field).map((row) => ({ ...row })), blank])
  }

  const removeRow = (field: SchemaFieldMeta, index: number) => {
    commitRows(
      field,
      rowsOf(field)
        .filter((_, i) => i !== index)
        .map((row) => ({ ...row }))
    )
  }

  /** 上下移动：switch 的 cases 是「自上往下第一个命中的胜出」，顺序有语义。 */
  const moveRow = (field: SchemaFieldMeta, index: number, offset: number) => {
    const rows = rowsOf(field).map((row) => ({ ...row }))
    const target = index + offset
    if (target < 0 || target >= rows.length) return
    ;[rows[index], rows[target]] = [rows[target], rows[index]]
    commitRows(field, rows)
  }

  const updateObjectField = (field: SchemaFieldMeta, key: string, value: any) => {
    const current = props.config?.[field.key]
    emit('update', field.key, {
      ...(current && typeof current === 'object' && !Array.isArray(current) ? current : {}),
      [key]: value
    })
  }

  // ---------- 动态对象 ----------

  const mapEntriesOf = (field: SchemaFieldMeta) => {
    const value = props.config?.[field.key]
    if (!value || typeof value !== 'object' || Array.isArray(value)) return []
    return Object.entries(value).map(([key, entryValue]) => ({ key, value: entryValue }))
  }

  const commitMap = (field: SchemaFieldMeta, entries: Array<{ key: string; value: any }>) => {
    emit(
      'update',
      field.key,
      Object.fromEntries(
        entries.filter((entry) => entry.key.trim()).map((entry) => [entry.key.trim(), entry.value])
      )
    )
  }

  const addMapEntry = (field: SchemaFieldMeta) => {
    const entries = mapEntriesOf(field)
    let key = 'parameter'
    let suffix = 1
    while (entries.some((entry) => entry.key === key)) key = `parameter${suffix++}`
    commitMap(field, [...entries, { key, value: '' }])
  }

  const updateMapKey = (field: SchemaFieldMeta, index: number, value: string) => {
    const entries = mapEntriesOf(field).map((entry) => ({ ...entry }))
    if (!entries[index]) return
    const nextKey = String(value || '').trim()
    if (
      !nextKey ||
      entries.some((entry, entryIndex) => entryIndex !== index && entry.key === nextKey)
    ) {
      return
    }
    entries[index].key = nextKey
    commitMap(field, entries)
  }

  const mapValueText = (value: unknown) =>
    value === undefined || value === null
      ? ''
      : typeof value === 'object'
        ? JSON.stringify(value)
        : String(value)

  const coerceMapValue = (current: unknown, value: string) => {
    if (typeof current === 'number') {
      const parsed = Number(value)
      return Number.isFinite(parsed) ? parsed : current
    }
    if (typeof current === 'boolean') return value === 'true'
    return value
  }

  const updateMapValue = (field: SchemaFieldMeta, index: number, value: string) => {
    const entries = mapEntriesOf(field).map((entry) => ({ ...entry }))
    if (!entries[index]) return
    entries[index].value = coerceMapValue(entries[index].value, String(value ?? ''))
    commitMap(field, entries)
  }

  const removeMapEntry = (field: SchemaFieldMeta, index: number) => {
    commitMap(
      field,
      mapEntriesOf(field).filter((_, entryIndex) => entryIndex !== index)
    )
  }

  // ---------- JSON 字段 ----------

  /** JSON 字段在文本域里编辑，失焦才回写，中途的半成品不会污染节点配置。 */
  const jsonDrafts = reactive<Record<string, string>>({})
  const jsonErrors = reactive<Record<string, string>>({})

  const formatJson = (value: unknown, isArray: boolean) => {
    if (value === undefined || value === null) return isArray ? '[]' : '{}'
    try {
      return JSON.stringify(value, null, 2)
    } catch {
      return isArray ? '[]' : '{}'
    }
  }

  const syncJsonDrafts = () => {
    fields.value
      .filter(
        (field) =>
          field.control === 'json' || field.control === 'object' || field.control === 'objectMap'
      )
      .forEach((field) => {
        jsonDrafts[field.key] = formatJson(props.config?.[field.key], field.isArray)
        jsonErrors[field.key] = ''
      })
  }

  watch(() => [props.schema, props.config], syncJsonDrafts, { immediate: true, deep: true })

  const commitJsonField = (field: SchemaFieldMeta) => {
    const text = String(jsonDrafts[field.key] ?? '').trim()
    if (!text) {
      jsonErrors[field.key] = ''
      emit('update', field.key, field.isArray ? [] : {})
      return
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      jsonErrors[field.key] = 'JSON 格式不正确，请检查后再试。'
      return
    }
    const shapeOk = field.isArray
      ? Array.isArray(parsed)
      : Boolean(parsed) && typeof parsed === 'object' && !Array.isArray(parsed)
    if (!shapeOk) {
      jsonErrors[field.key] = field.isArray ? '需要一个 JSON 数组。' : '需要一个 JSON 对象。'
      return
    }
    jsonErrors[field.key] = ''
    emit('update', field.key, parsed)
  }
</script>

<style scoped lang="scss">
  .schema-fields__list {
    display: flex;
    flex-direction: column;
    gap: 10px;
    width: 100%;
  }

  .schema-fields__row {
    padding: 10px 12px;
    background: var(--el-fill-color-lighter, #f8fafc);
    border: 1px solid var(--el-border-color-lighter, #e2e8f0);
    border-radius: 6px;
  }

  .schema-fields__row-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 6px;
  }

  .schema-fields__row-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--el-text-color-primary, #0f172a);
  }

  .schema-fields__row-actions {
    display: flex;
    gap: 4px;
  }

  .schema-fields__sub {
    margin-bottom: 8px;

    &:last-child {
      margin-bottom: 0;
    }
  }

  .schema-fields__add {
    width: 100%;
    border-style: dashed;
  }

  .schema-fields__object {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
    width: 100%;
    padding: 10px;
    background: var(--el-fill-color-lighter, #f8fafc);
    border: 1px solid var(--el-border-color-lighter, #e2e8f0);
    border-radius: 6px;
  }

  .schema-fields__object-field {
    min-width: 0;
  }

  .schema-fields__object-field > label {
    display: block;
    margin-bottom: 5px;
    font-size: 12px;
    color: var(--el-text-color-secondary, #64748b);
  }

  .schema-fields__map {
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: 100%;
  }

  .schema-fields__map-row {
    display: grid;
    grid-template-columns: minmax(110px, 0.8fr) minmax(0, 1.2fr) auto;
    gap: 8px;
    align-items: center;
  }

  .schema-fields__advanced {
    margin-top: 10px;
    color: var(--el-text-color-secondary, #94a3b8);
    font-size: 12px;
  }

  .schema-fields__advanced summary {
    width: fit-content;
    cursor: pointer;
    user-select: none;
  }

  .schema-fields__advanced > .el-textarea {
    margin-top: 8px;
  }

  .schema-fields__hint {
    margin-top: 4px;
    font-size: 12px;
    line-height: 18px;
    color: var(--el-text-color-secondary, #94a3b8);
  }

  .schema-fields__error {
    margin-top: 4px;
    font-size: 12px;
    line-height: 18px;
    color: var(--el-color-danger, #dc2626);
  }

  @media (max-width: 480px) {
    .schema-fields__object {
      grid-template-columns: 1fr;
    }

    .schema-fields__map-row {
      grid-template-columns: 1fr auto;
    }

    .schema-fields__map-value {
      grid-column: 1 / -1;
      grid-row: 2;
    }
  }
</style>
