<!--
  工作流编辑器组件：WorkflowSchemaField。

  按 schema 渲染「一个标量字段」的输入控件。抽成独立组件是因为它有两个使用场景：
  属性面板的顶层字段，以及 objectList 行编辑器里每一行的子字段 —— 两处共用同一套控件映射，
  不必把 enum / number / boolean / text 四个分支在模板里写两遍。

  受控组件：不改传进来的值，改动一律 emit('update', value)。
-->
<template>
  <ElSelect
    v-if="field.control === 'enum'"
    :model-value="value"
    class="schema-field__full"
    clearable
    :placeholder="field.placeholder"
    @update:model-value="$emit('update', $event)"
  >
    <ElOption
      v-for="option in field.options"
      :key="option.value"
      :label="option.label"
      :value="option.value"
    />
  </ElSelect>

  <ElCheckboxGroup
    v-else-if="field.control === 'multiEnum'"
    :model-value="arrayValue"
    class="schema-field__checks"
    @update:model-value="$emit('update', $event)"
  >
    <ElCheckbox v-for="option in field.options" :key="option.value" :value="option.value">
      {{ option.label }}
    </ElCheckbox>
  </ElCheckboxGroup>

  <ElSelect
    v-else-if="field.control === 'stringList'"
    :model-value="arrayValue"
    class="schema-field__full"
    multiple
    filterable
    allow-create
    default-first-option
    :placeholder="field.placeholder"
    @update:model-value="$emit('update', $event)"
  >
    <ElOption
      v-for="option in field.options"
      :key="option.value"
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
    @update:model-value="$emit('update', $event)"
  />

  <ElSwitch
    v-else-if="field.control === 'boolean'"
    :model-value="Boolean(value)"
    @update:model-value="$emit('update', $event)"
  />

  <ElInput
    v-else
    :model-value="textValue"
    :type="field.secret ? 'password' : field.multiline ? 'textarea' : 'text'"
    :rows="field.multiline ? 4 : undefined"
    :show-password="field.secret"
    :placeholder="field.placeholder"
    @update:model-value="$emit('update', $event)"
    @blur="handleBlur"
  />
</template>

<script setup lang="ts">
  import type { SchemaFieldMeta } from './workflow-schema-field'

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
  const arrayValue = computed(() => (Array.isArray(props.value) ? props.value : []))

  /** 文本字段失焦时顺手去掉首尾空白，省得路径里混进空格导致取值取不到。 */
  const handleBlur = () => {
    if (typeof props.value === 'string' && props.value !== props.value.trim()) {
      emit('update', props.value.trim())
    }
  }
</script>

<style scoped lang="scss">
  .schema-field__full {
    width: 100%;
  }

  .schema-field__checks {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 16px;

    :deep(.el-checkbox) {
      margin-right: 0;
    }
  }
</style>
