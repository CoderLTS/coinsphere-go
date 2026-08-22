<template>
  <ElDialog
    :model-value="modelValue"
    title="运行工作流"
    width="min(560px, 94vw)"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <ElForm v-if="workflow" label-position="top">
      <ElFormItem label="工作流">
        <ElInput :model-value="`${workflow.displayName} · v${workflow.version}`" disabled />
      </ElFormItem>
      <ElFormItem label="开始入口" required>
        <ElCheckboxGroup v-model="entryKeys">
          <ElCheckbox v-for="entry in entries" :key="entry.value" :value="entry.value">
            {{ entry.label }}
          </ElCheckbox>
        </ElCheckboxGroup>
      </ElFormItem>
      <WorkflowSchemaFields
        :schema="inputSchema"
        :config="{ inputs }"
        @update="(_, value) => (inputs = value)"
      />
    </ElForm>
    <template #footer>
      <ElButton @click="$emit('update:modelValue', false)">取消</ElButton>
      <ElButton type="primary" :loading="submitting" @click="submit">运行</ElButton>
    </template>
  </ElDialog>
</template>

<script setup lang="ts">
  import { ElMessage } from 'element-plus'
  import {
    fetchRunWorkflowDefinition,
    type RunWorkflowDefinitionResponse,
    type WorkflowDefinitionItem
  } from '@/api/scheduler'
  import WorkflowSchemaFields from '@/views/scheduler/workflow/editor/components/WorkflowSchemaFields.vue'

  const props = defineProps<{
    modelValue: boolean
    workflow: WorkflowDefinitionItem | null
  }>()
  const emit = defineEmits<{
    (e: 'update:modelValue', value: boolean): void
    (e: 'started', result: RunWorkflowDefinitionResponse): void
  }>()

  const submitting = ref(false)
  const entryKeys = ref<string[]>([])
  const inputs = ref<Record<string, any>>({})
  const entries = computed(() =>
    (props.workflow?.graph.nodes || [])
      .filter((node) => node.type === 'start.manual')
      .map((node) => ({
        value: String(node.config?.entryKey || '').trim(),
        label: String(node.config?.displayName || node.label || '手动开始').trim()
      }))
      .filter((item) => item.value)
  )
  const inputSchema = {
    type: 'object',
    properties: {
      inputs: {
        type: 'object',
        title: '运行参数',
        format: 'key-value',
        description: '按节点需要添加字段；无需参数时留空。'
      }
    }
  }

  const submit = async () => {
    if (!props.workflow || !entryKeys.value.length) {
      ElMessage.warning('请选择开始入口。')
      return
    }
    submitting.value = true
    try {
      const result = await fetchRunWorkflowDefinition(props.workflow.id, {
        startEntryKeys: entryKeys.value,
        inputs: inputs.value
      })
      emit('update:modelValue', false)
      emit('started', result)
    } finally {
      submitting.value = false
    }
  }

  watch(
    () => [props.modelValue, props.workflow?.id] as const,
    ([visible]) => {
      if (!visible) return
      entryKeys.value = entries.value[0] ? [entries.value[0].value] : []
      inputs.value = {}
    },
    { immediate: true }
  )
</script>
