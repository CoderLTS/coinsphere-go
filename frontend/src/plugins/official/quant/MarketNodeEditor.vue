<template>
  <WorkflowSchemaFields :schema="schema" :keys="Object.keys(schema.properties || {}).filter(key => key !== 'source')" :config="config" @update="update" />
  <ElFormItem label="策略表达式"><CodeEditor :model-value="String(config.source || '')" :config="config" @update:model-value="value => emit('update', 'source', value)" /></ElFormItem>
</template>
<script setup lang="ts">
import WorkflowSchemaFields from '@/views/scheduler/workflow/editor/components/WorkflowSchemaFields.vue'
import CodeEditor from './CodeEditor.vue'
defineProps<{ schema: Record<string, any>; config: Record<string, any> }>()
const emit = defineEmits<{ (event: 'update', key: string, value: any): void }>()
const update = (key: string, value: any) => emit('update', key, value)
</script>
