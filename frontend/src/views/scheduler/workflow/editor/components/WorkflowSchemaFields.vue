<template>
  <template v-for="field in visibleFields" :key="field.key">
    <ElFormItem :label="field.title" :required="field.required">
      <div v-if="field.control === 'object'" class="schema-fields__nested">
        <WorkflowSchemaFields
          :schema="field.schema"
          :config="objectOf(field)"
          @update="(key, value) => updateObject(field, key, value)"
        />
      </div>

      <div v-else-if="field.control === 'objectList'" class="schema-fields__list">
        <div v-for="(row, index) in objectRows(field)" :key="index" class="schema-fields__row">
          <div class="schema-fields__row-head">
            <span>{{ field.title }} {{ index + 1 }}</span>
            <div class="schema-fields__row-actions">
              <ElButton
                :icon="ArrowUp"
                circle
                text
                :disabled="index === 0"
                :aria-label="`上移${field.title}`"
                @click="moveObjectRow(field, index, -1)"
              />
              <ElButton
                :icon="ArrowDown"
                circle
                text
                :disabled="index === objectRows(field).length - 1"
                :aria-label="`下移${field.title}`"
                @click="moveObjectRow(field, index, 1)"
              />
              <ElButton
                :icon="Delete"
                circle
                text
                type="danger"
                :aria-label="`删除${field.title}`"
                @click="removeObjectRow(field, index)"
              />
            </div>
          </div>
          <WorkflowSchemaFields
            :schema="field.schema.items || {}"
            :config="row"
            @update="(key, value) => updateObjectRow(field, index, key, value)"
          />
        </div>
        <ElButton :icon="Plus" class="schema-fields__add" @click="addObjectRow(field)">
          添加{{ field.title }}
        </ElButton>
      </div>

      <div v-else-if="field.control === 'scalarList'" class="schema-fields__list">
        <div
          v-for="(item, index) in scalarRows(field)"
          :key="index"
          class="schema-fields__scalar-row"
        >
          <WorkflowSchemaField
            v-if="field.itemField"
            :field="field.itemField"
            :value="item"
            @update="updateScalarRow(field, index, $event)"
          />
          <ElButton
            :icon="Delete"
            circle
            text
            type="danger"
            :aria-label="`删除${field.title}`"
            @click="removeScalarRow(field, index)"
          />
        </div>
        <ElButton :icon="Plus" class="schema-fields__add" @click="addScalarRow(field)">
          添加{{ field.title }}
        </ElButton>
      </div>

      <div v-else-if="field.control === 'keyValue'" class="schema-fields__list">
        <div v-for="(row, index) in pairRows(field)" :key="index" class="schema-fields__pair-row">
          <ElInput
            :model-value="row.key"
            aria-label="字段名称"
            placeholder="字段名称"
            @update:model-value="updatePair(field, index, { key: String($event) })"
          />
          <ElSelect
            :model-value="row.type"
            aria-label="字段类型"
            @update:model-value="updatePair(field, index, { type: String($event) as ScalarType })"
          >
            <ElOption label="文本" value="string" />
            <ElOption label="Decimal" value="decimal" />
            <ElOption label="整数" value="integer" />
            <ElOption label="布尔" value="boolean" />
          </ElSelect>
          <ElSwitch
            v-if="row.type === 'boolean'"
            :model-value="Boolean(row.value)"
            aria-label="字段值"
            @update:model-value="updatePair(field, index, { value: $event })"
          />
          <ElInputNumber
            v-else-if="row.type === 'integer'"
            :model-value="typeof row.value === 'number' ? row.value : undefined"
            aria-label="字段值"
            controls-position="right"
            @update:model-value="updatePair(field, index, { value: $event })"
          />
          <ElInput
            v-else
            :model-value="String(row.value ?? '')"
            :type="field.secret ? 'password' : 'text'"
            :show-password="field.secret"
            :autocomplete="field.secret ? 'new-password' : undefined"
            :inputmode="row.type === 'decimal' ? 'decimal' : undefined"
            aria-label="字段值"
            placeholder="字段值"
            @update:model-value="updatePair(field, index, { value: String($event) })"
          />
          <ElButton
            :icon="Delete"
            circle
            text
            type="danger"
            aria-label="删除字段"
            @click="removePair(field, index)"
          />
        </div>
        <ElButton :icon="Plus" class="schema-fields__add" @click="addPair(field)">
          添加字段
        </ElButton>
      </div>

      <div v-else-if="field.control === 'fieldSchema'" class="schema-fields__list">
        <div
          v-for="(row, index) in parameterRows(field)"
          :key="index"
          class="schema-fields__parameter"
        >
          <div class="schema-fields__row-head">
            <span>参数 {{ index + 1 }}</span>
            <ElButton
              :icon="Delete"
              circle
              text
              type="danger"
              aria-label="删除参数"
              @click="removeParameter(field, index)"
            />
          </div>
          <div class="schema-fields__parameter-grid">
            <ElInput
              :model-value="row.name"
              placeholder="参数名称"
              aria-label="参数名称"
              @update:model-value="updateParameter(field, index, { name: String($event) })"
            />
            <ElSelect
              :model-value="row.type"
              aria-label="参数类型"
              @update:model-value="
                updateParameter(field, index, { type: String($event) as ScalarType })
              "
            >
              <ElOption label="文本" value="string" />
              <ElOption label="Decimal" value="decimal" />
              <ElOption label="整数" value="integer" />
              <ElOption label="布尔" value="boolean" />
            </ElSelect>
            <label class="schema-fields__required">
              <ElSwitch
                :model-value="row.required"
                @update:model-value="updateParameter(field, index, { required: Boolean($event) })"
              />
              必填
            </label>
            <ElSwitch
              v-if="row.type === 'boolean'"
              :model-value="Boolean(row.defaultValue)"
              aria-label="默认值"
              @update:model-value="updateParameter(field, index, { defaultValue: $event })"
            />
            <ElInputNumber
              v-else-if="row.type === 'integer'"
              :model-value="typeof row.defaultValue === 'number' ? row.defaultValue : undefined"
              aria-label="默认值"
              placeholder="默认值"
              controls-position="right"
              @update:model-value="updateParameter(field, index, { defaultValue: $event })"
            />
            <ElInput
              v-else
              :model-value="String(row.defaultValue ?? '')"
              :inputmode="row.type === 'decimal' ? 'decimal' : undefined"
              aria-label="默认值"
              placeholder="默认值"
              @update:model-value="updateParameter(field, index, { defaultValue: String($event) })"
            />
            <ElInput
              v-if="row.type === 'decimal'"
              :model-value="String(row.minimum ?? '')"
              inputmode="decimal"
              placeholder="最小值"
              aria-label="最小值"
              @update:model-value="updateParameter(field, index, { minimum: String($event) })"
            />
            <ElInputNumber
              v-else-if="row.type === 'integer'"
              :model-value="typeof row.minimum === 'number' ? row.minimum : undefined"
              placeholder="最小值"
              aria-label="最小值"
              controls-position="right"
              @update:model-value="updateParameter(field, index, { minimum: $event })"
            />
            <ElInput
              v-if="row.type === 'decimal'"
              :model-value="String(row.maximum ?? '')"
              inputmode="decimal"
              placeholder="最大值"
              aria-label="最大值"
              @update:model-value="updateParameter(field, index, { maximum: String($event) })"
            />
            <ElInputNumber
              v-else-if="row.type === 'integer'"
              :model-value="typeof row.maximum === 'number' ? row.maximum : undefined"
              placeholder="最大值"
              aria-label="最大值"
              controls-position="right"
              @update:model-value="updateParameter(field, index, { maximum: $event })"
            />
            <ElInput
              class="schema-fields__enum"
              :model-value="row.enumText"
              placeholder="可选值，用逗号分隔"
              aria-label="可选值"
              @update:model-value="updateParameter(field, index, { enumText: String($event) })"
            />
          </div>
        </div>
        <ElButton :icon="Plus" class="schema-fields__add" @click="addParameter(field)">
          添加参数
        </ElButton>
      </div>

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
  import { ArrowDown, ArrowUp, Delete, Plus } from '@element-plus/icons-vue'
  import WorkflowSchemaField from './WorkflowSchemaField.vue'
  import { buildSchemaFields, type SchemaFieldMeta } from './workflow-schema-field'

  type ScalarType = 'string' | 'decimal' | 'integer' | 'boolean'
  interface PairRow {
    key: string
    type: ScalarType
    value: any
  }
  interface ParameterRow {
    name: string
    type: ScalarType
    required: boolean
    defaultValue: any
    minimum: any
    maximum: any
    enumText: string
  }

  const props = withDefaults(
    defineProps<{
      schema: Record<string, any>
      config: Record<string, any>
      keys?: string[]
    }>(),
    { keys: () => [] }
  )
  const emit = defineEmits<{ (e: 'update', key: string, value: any): void }>()

  const fields = computed(() => {
    const required = Array.isArray(props.schema?.required) ? props.schema.required.map(String) : []
    const built = buildSchemaFields(props.schema?.properties || {}, required)
    const optionMap = props.schema?.properties?.entryKey?.enumByWorkflowCode
    if (!optionMap || typeof optionMap !== 'object') return built
    const options = optionMap[String(props.config?.workflowCode || '')]
    return built.map((field) =>
      field.key === 'entryKey'
        ? {
            ...field,
            control: 'enum' as const,
            options: (Array.isArray(options) ? options : []).map((value) => ({
              value,
              label: String(value)
            })),
            placeholder: '请选择开始入口'
          }
        : field
    )
  })
  const visibleFields = computed(() =>
    props.keys.length
      ? fields.value.filter((field) => props.keys.includes(field.key))
      : fields.value
  )

  const isRecord = (value: unknown): value is Record<string, any> =>
    Boolean(value) && typeof value === 'object' && !Array.isArray(value)
  const emitUpdate = (key: string, value: any) => {
    emit('update', key, value)
    if (key === 'workflowCode' && props.schema?.properties?.entryKey?.enumByWorkflowCode) {
      emit('update', 'entryKey', '')
    }
  }
  const blankValue = (field?: SchemaFieldMeta) => {
    if (!field) return ''
    if (field.control === 'boolean') return false
    if (
      field.control === 'object' ||
      field.control === 'keyValue' ||
      field.control === 'fieldSchema'
    )
      return {}
    if (field.control === 'objectList' || field.control === 'scalarList') return []
    return field.schema.default ?? ''
  }

  const objectOf = (field: SchemaFieldMeta) =>
    isRecord(props.config?.[field.key]) ? props.config[field.key] : {}
  const updateObject = (field: SchemaFieldMeta, key: string, value: any) =>
    emit('update', field.key, { ...objectOf(field), [key]: value })

  const objectRows = (field: SchemaFieldMeta): Record<string, any>[] =>
    Array.isArray(props.config?.[field.key])
      ? props.config[field.key].map((row: unknown) => (isRecord(row) ? row : {}))
      : []
  const commitObjectRows = (field: SchemaFieldMeta, rows: Record<string, any>[]) =>
    emit('update', field.key, rows)
  const updateObjectRow = (field: SchemaFieldMeta, index: number, key: string, value: any) =>
    commitObjectRows(
      field,
      objectRows(field).map((row, rowIndex) =>
        rowIndex === index ? { ...row, [key]: value } : { ...row }
      )
    )
  const addObjectRow = (field: SchemaFieldMeta) =>
    commitObjectRows(field, [
      ...objectRows(field).map((row) => ({ ...row })),
      Object.fromEntries(field.childFields.map((child) => [child.key, blankValue(child)]))
    ])
  const removeObjectRow = (field: SchemaFieldMeta, index: number) =>
    commitObjectRows(
      field,
      objectRows(field)
        .filter((_, rowIndex) => rowIndex !== index)
        .map((row) => ({ ...row }))
    )
  const moveObjectRow = (field: SchemaFieldMeta, index: number, offset: number) => {
    const rows = objectRows(field).map((row) => ({ ...row }))
    const target = index + offset
    if (target < 0 || target >= rows.length) return
    ;[rows[index], rows[target]] = [rows[target], rows[index]]
    commitObjectRows(field, rows)
  }

  const scalarRows = (field: SchemaFieldMeta): any[] =>
    Array.isArray(props.config?.[field.key]) ? props.config[field.key] : []
  const updateScalarRow = (field: SchemaFieldMeta, index: number, value: any) =>
    emit(
      'update',
      field.key,
      scalarRows(field).map((entry, rowIndex) => (rowIndex === index ? value : entry))
    )
  const addScalarRow = (field: SchemaFieldMeta) =>
    emit('update', field.key, [...scalarRows(field), blankValue(field.itemField)])
  const removeScalarRow = (field: SchemaFieldMeta, index: number) =>
    emit(
      'update',
      field.key,
      scalarRows(field).filter((_, rowIndex) => rowIndex !== index)
    )

  const inferScalarType = (value: unknown): ScalarType => {
    if (typeof value === 'boolean') return 'boolean'
    if (typeof value === 'number') return 'integer'
    return 'string'
  }
  const pairRows = (field: SchemaFieldMeta): PairRow[] =>
    Object.entries(objectOf(field)).map(([key, value]) => ({
      key,
      value,
      type: inferScalarType(value)
    }))
  const defaultForType = (type: ScalarType) =>
    type === 'boolean' ? false : type === 'integer' ? 0 : ''
  const commitPairs = (field: SchemaFieldMeta, rows: PairRow[]) =>
    emit(
      'update',
      field.key,
      Object.fromEntries(
        rows.filter((row) => row.key.trim()).map((row) => [row.key.trim(), row.value])
      )
    )
  const updatePair = (field: SchemaFieldMeta, index: number, patch: Partial<PairRow>) => {
    const rows = pairRows(field)
    const current = rows[index]
    if (!current) return
    const next = { ...current, ...patch }
    if (patch.type && patch.type !== current.type) next.value = defaultForType(patch.type)
    rows[index] = next
    commitPairs(field, rows)
  }
  const addPair = (field: SchemaFieldMeta) =>
    commitPairs(field, [
      ...pairRows(field),
      { key: `field${pairRows(field).length + 1}`, type: 'string', value: '' }
    ])
  const removePair = (field: SchemaFieldMeta, index: number) =>
    commitPairs(
      field,
      pairRows(field).filter((_, rowIndex) => rowIndex !== index)
    )

  const parameterRows = (field: SchemaFieldMeta): ParameterRow[] =>
    Object.entries(objectOf(field)).map(([name, raw]) => {
      const spec = isRecord(raw) ? raw : {}
      const type = ['string', 'decimal', 'integer', 'boolean'].includes(String(spec.type))
        ? (spec.type as ScalarType)
        : 'string'
      return {
        name,
        type,
        required: Boolean(spec.required),
        defaultValue: spec.default,
        minimum: spec.minimum,
        maximum: spec.maximum,
        enumText: Array.isArray(spec.enum) ? spec.enum.join(', ') : ''
      }
    })
  const castParameter = (value: string, type: ScalarType) => {
    if (type === 'integer') return Number.parseInt(value, 10)
    if (type === 'boolean') return value === 'true'
    return value
  }
  const commitParameters = (field: SchemaFieldMeta, rows: ParameterRow[]) => {
    const entries = rows
      .filter((row) => row.name.trim())
      .map((row) => {
        const spec: Record<string, any> = { type: row.type, required: row.required }
        if (row.defaultValue !== '' && row.defaultValue !== undefined)
          spec.default = row.defaultValue
        if (['integer', 'decimal'].includes(row.type)) {
          if (row.minimum !== '' && row.minimum !== undefined) spec.minimum = row.minimum
          if (row.maximum !== '' && row.maximum !== undefined) spec.maximum = row.maximum
        }
        const enumValues = row.enumText
          .split(',')
          .map((value) => value.trim())
          .filter(Boolean)
          .map((value) => castParameter(value, row.type))
        if (enumValues.length) spec.enum = enumValues
        return [row.name.trim(), spec]
      })
    emit('update', field.key, Object.fromEntries(entries))
  }
  const updateParameter = (field: SchemaFieldMeta, index: number, patch: Partial<ParameterRow>) => {
    const rows = parameterRows(field)
    const current = rows[index]
    if (!current) return
    const next = { ...current, ...patch }
    if (patch.type && patch.type !== current.type) {
      next.defaultValue = defaultForType(patch.type)
      next.minimum = ''
      next.maximum = ''
      next.enumText = ''
    }
    rows[index] = next
    commitParameters(field, rows)
  }
  const addParameter = (field: SchemaFieldMeta) =>
    commitParameters(field, [
      ...parameterRows(field),
      {
        name: `parameter${parameterRows(field).length + 1}`,
        type: 'decimal',
        required: false,
        defaultValue: '',
        minimum: '',
        maximum: '',
        enumText: ''
      }
    ])
  const removeParameter = (field: SchemaFieldMeta, index: number) =>
    commitParameters(
      field,
      parameterRows(field).filter((_, rowIndex) => rowIndex !== index)
    )
</script>

<style scoped lang="scss">
  .schema-fields__nested,
  .schema-fields__list {
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: 100%;
  }

  .schema-fields__nested,
  .schema-fields__row,
  .schema-fields__parameter {
    padding: 10px;
    background: var(--el-fill-color-lighter);
    border: 1px solid var(--el-border-color-lighter);
    border-radius: 6px;
  }

  .schema-fields__row-head,
  .schema-fields__row-actions,
  .schema-fields__scalar-row,
  .schema-fields__pair-row,
  .schema-fields__required {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .schema-fields__row-head {
    justify-content: space-between;
    margin-bottom: 8px;
    font-size: 12px;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  .schema-fields__scalar-row > :first-child {
    flex: 1;
  }

  .schema-fields__pair-row {
    display: grid;
    grid-template-columns: minmax(90px, 0.8fr) 92px minmax(110px, 1fr) 32px;
  }

  .schema-fields__parameter-grid {
    display: grid;
    grid-template-columns: minmax(100px, 1fr) 100px 70px minmax(100px, 1fr);
    gap: 8px;
  }

  .schema-fields__enum {
    grid-column: 1 / -1;
  }

  .schema-fields__required {
    font-size: 12px;
    color: var(--el-text-color-regular);
  }

  .schema-fields__add {
    width: 100%;
    border-style: dashed;
  }

  .schema-fields__hint {
    margin-top: 4px;
    font-size: 12px;
    line-height: 18px;
    color: var(--el-text-color-secondary);
  }

  :deep(.el-form-item) {
    margin-bottom: 10px;
  }

  @media (max-width: 768px) {
    .schema-fields__pair-row,
    .schema-fields__parameter-grid {
      grid-template-columns: 1fr;
    }

    .schema-fields__enum {
      grid-column: auto;
    }
  }
</style>
