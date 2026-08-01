<!-- 工作流编辑器页面或组件：WorkflowEdgeBubble。 -->
<template>
  <div class="edge-bubble">
    <div class="edge-bubble__header">
      <strong>连线设置</strong>
      <span>{{ edge.id }}</span>
    </div>

    <ElForm label-position="top">
      <ElFormItem v-if="isConditionEdge" label="分支标识">
        <ElInput :model-value="branchDisplayText" readonly />
      </ElFormItem>
      <ElFormItem label="显示标签">
        <ElInput v-model.trim="localForm.label" placeholder="用于画布展示" />
      </ElFormItem>
    </ElForm>

    <div class="edge-bubble__footer">
      <ElButton size="small" @click="$emit('cancel')">取消</ElButton>
      <ElButton size="small" type="primary" @click="$emit('confirm', cloneForm(localForm))">确定</ElButton>
    </div>
  </div>
</template>

<script setup lang="ts">
  import type { WorkflowDomainEdge, WorkflowEdgeFormModel } from '../types'

  interface Props {
    edge: WorkflowDomainEdge
  }

  interface Emits {
    (e: 'confirm', value: WorkflowEdgeFormModel): void
    (e: 'cancel'): void
  }

  const props = defineProps<Props>()
  defineEmits<Emits>()

  const cloneForm = (edge: WorkflowEdgeFormModel): WorkflowEdgeFormModel => ({
    ...edge
  })

  const isConditionEdge = computed(() => ['true', 'false'].includes(props.edge.sourcePort || ''))
  const branchDisplayText = computed(() => localForm.branch || props.edge.sourcePort || '')

  const localForm = reactive<WorkflowEdgeFormModel>({
    id: props.edge.id,
    source: props.edge.source,
    target: props.edge.target,
    sourcePort: props.edge.sourcePort || '',
    targetPort: props.edge.targetPort || '',
    branch: props.edge.data.branch || '',
    label: props.edge.data.label || ''
  })

  watch(
    () => props.edge,
    (value) => {
      Object.assign(localForm, {
        id: value.id,
        source: value.source,
        target: value.target,
        sourcePort: value.sourcePort || '',
        targetPort: value.targetPort || '',
        branch: value.data.branch || '',
        label: value.data.label || ''
      })
    },
    { deep: true }
  )
</script>

<style scoped lang="scss">
  .edge-bubble {
    width: 240px;
    padding: 14px;
    border: 1px solid rgba(203, 213, 225, 0.86);
    border-radius: 18px;
    background: rgba(255, 255, 255, 0.98);
    box-shadow: 0 18px 32px rgba(15, 23, 42, 0.14);
    backdrop-filter: blur(16px);
  }

  .edge-bubble__header {
    margin-bottom: 8px;

    strong {
      display: block;
      font-size: 14px;
      color: #0f172a;
    }

    span {
      display: block;
      margin-top: 4px;
      font-size: 12px;
      color: #64748b;
    }
  }

  .edge-bubble__footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
</style>
