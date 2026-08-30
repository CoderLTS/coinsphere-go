<template>
  <WorkflowSchemaFields
    :schema="schema"
    :ui-schema="uiSchema"
    :config="config"
    :keys="['market', 'instrument', 'checkInterval', 'name', 'interval']"
    @update="emitUpdate"
  />
  <WorkflowSchemaFields :schema="parameterSchema" :config="parameters" @update="updateParameter" />
</template>

<script setup lang="ts">
  import WorkflowSchemaFields from './WorkflowSchemaFields.vue'

  interface Props {
    schema: Record<string, any>
    uiSchema?: Record<string, any>
    config: Record<string, any>
  }

  interface Emits {
    (event: 'update', key: string, value: any): void
  }

  const props = defineProps<Props>()
  const emit = defineEmits<Emits>()

  const parameterSchema = computed(() => props.schema?.properties?.parameters || {})
  const parameters = computed(() =>
    props.config?.parameters && typeof props.config.parameters === 'object'
      ? props.config.parameters
      : {}
  )

  const emitUpdate = (key: string, value: any) => emit('update', key, value)

  const updateParameter = (key: string, value: any) => {
    emit('update', 'parameters', { ...parameters.value, [key]: value })
  }
</script>
