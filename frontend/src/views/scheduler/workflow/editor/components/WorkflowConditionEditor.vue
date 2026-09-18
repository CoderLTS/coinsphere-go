<template>
  <ElFormItem label="组合方式"><ElRadioGroup :model-value="config.match || 'all'" @change="value => emit('update', 'match', value)"><ElRadioButton value="all">全部满足</ElRadioButton><ElRadioButton value="any">任一满足</ElRadioButton></ElRadioGroup></ElFormItem>
  <div v-for="(rule, index) in rules" :key="index" class="condition-rule">
    <ElSelect :model-value="rule.fieldPath.join('.')" filterable allow-create placeholder="输入字段" @change="path => change(index, 'fieldPath', String(path).split('.'))"><ElOption v-for="field in fields" :key="field" :value="field" :label="field" /></ElSelect>
    <ElSelect :model-value="rule.operator" @change="value => change(index, 'operator', value)"><ElOption v-for="(label, op) in operators" :key="op" :value="op" :label="label" /></ElSelect>
    <ElInput v-if="!['true', 'false', 'exists'].includes(rule.operator)" :model-value="String(rule.value ?? '')" placeholder="比较值" @update:model-value="value => change(index, 'value', value)" />
    <ElButton text type="danger" @click="emit('update', 'rules', rules.filter((_, i) => i !== index))">删除</ElButton>
  </div>
  <ElButton @click="emit('update', 'rules', [...rules, { fieldPath: [], operator: 'true' }])">添加判断</ElButton>
</template>
<script setup lang="ts">
const props = defineProps<{ config: Record<string, any>; fields: string[] }>()
const emit = defineEmits<{ (event: 'update', key: string, value: any): void }>()
const rules = computed<Array<{ fieldPath: string[]; operator: string; value?: unknown }>>(() => props.config.rules || [])
const operators = { true: '为真', false: '为假', eq: '等于', ne: '不等于', gt: '大于', gte: '大于等于', lt: '小于', lte: '小于等于', contains: '包含', exists: '存在' }
function change(index: number, key: string, value: unknown) { emit('update', 'rules', rules.value.map((rule, i) => i === index ? { ...rule, [key]: value } : rule)) }
</script>
<style scoped>.condition-rule { display: grid; gap: 8px; padding: 10px; margin-bottom: 10px; border: 1px solid var(--el-border-color); border-radius: 6px; }</style>
