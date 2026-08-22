<template>
  <ElSelect
    v-if="field.control === 'enum'"
    :model-value="value"
    class="schema-field__full"
    clearable
    filterable
    :placeholder="field.placeholder"
    @update:model-value="$emit('update', $event)"
  >
    <ElOption
      v-for="option in field.options"
      :key="String(option.value)"
      :label="option.label"
      :value="option.value"
    />
  </ElSelect>

  <ElInputNumber
    v-else-if="field.control === 'number'"
    :model-value="numberValue"
    class="schema-field__full"
    :min="field.min"
    :max="field.max"
    :step="field.step"
    controls-position="right"
    @update:model-value="$emit('update', $event)"
  />

  <ElSwitch
    v-else-if="field.control === 'boolean'"
    :model-value="Boolean(value)"
    @update:model-value="$emit('update', $event)"
  />

  <ElInput
    v-else-if="field.control === 'decimal'"
    :model-value="textValue"
    class="schema-field__full"
    inputmode="decimal"
    :placeholder="field.placeholder"
    @update:model-value="$emit('update', String($event))"
    @blur="handleBlur"
  />

  <input
    v-else-if="field.control === 'datetime'"
    class="schema-field__datetime"
    type="datetime-local"
    :value="localDateTime"
    @change="handleDateTimeChange"
  />

  <WorkflowCodeField
    v-else-if="field.control === 'code'"
    :model-value="textValue"
    :language="field.language"
    @update:model-value="$emit('update', $event)"
  />

  <ElInput
    v-else
    :model-value="textValue"
    :type="field.secret ? 'password' : field.multiline ? 'textarea' : 'text'"
    :rows="field.multiline ? 4 : undefined"
    :show-password="field.secret"
    :autocomplete="field.secret ? 'new-password' : undefined"
    :placeholder="field.placeholder"
    @update:model-value="$emit('update', $event)"
    @blur="handleBlur"
  />
</template>

<script setup lang="ts">
  import type { SchemaFieldMeta } from './workflow-schema-field'
  import WorkflowCodeField from './WorkflowCodeField.vue'

  interface Props {
    field: SchemaFieldMeta
    value: any
  }

  interface Emits {
    (e: 'update', value: any): void
  }

  const props = defineProps<Props>()
  const emit = defineEmits<Emits>()

  const textValue = computed(() =>
    props.value === undefined || props.value === null ? '' : String(props.value)
  )
  const numberValue = computed(() => (typeof props.value === 'number' ? props.value : undefined))
  const localDateTime = computed(() => {
    const text = textValue.value.trim()
    if (!text) return ''
    const date = new Date(text)
    if (Number.isNaN(date.getTime())) return text.slice(0, 16)
    const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
    return local.toISOString().slice(0, 16)
  })

  const handleDateTimeChange = (event: Event) => {
    const value = (event.target as HTMLInputElement).value
    emit('update', value ? new Date(value).toISOString() : '')
  }

  const handleBlur = () => {
    if (typeof props.value === 'string' && props.value !== props.value.trim()) {
      emit('update', props.value.trim())
    }
  }
</script>

<style scoped lang="scss">
  .schema-field__full,
  .schema-field__datetime {
    width: 100%;
  }

  .schema-field__datetime {
    height: 32px;
    padding: 0 11px;
    color: var(--el-text-color-regular);
    color-scheme: light dark;
    background: var(--el-fill-color-blank);
    border: 1px solid var(--el-border-color);
    border-radius: 4px;
    outline: none;

    &:focus-visible {
      border-color: var(--el-color-primary);
    }
  }
</style>
