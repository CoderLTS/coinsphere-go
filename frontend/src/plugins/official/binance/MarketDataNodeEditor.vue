<template>
  <WorkflowSchemaFields
    :schema="schema"
    :ui-schema="uiSchema"
    :config="config"
    @update="(key, value) => emit('update', key, value)"
  />
</template>

<script setup lang="ts">
  import WorkflowSchemaFields from '@/views/scheduler/workflow/editor/components/WorkflowSchemaFields.vue'

  defineProps<{
    schema: Record<string, any>
    uiSchema?: Record<string, any>
    config: Record<string, any>
  }>()

  const emit = defineEmits<{
    (event: 'update', key: string, value: any): void
  }>()
</script>
