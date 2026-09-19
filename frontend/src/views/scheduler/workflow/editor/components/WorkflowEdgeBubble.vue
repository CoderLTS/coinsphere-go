<!-- 工作流编辑器页面或组件：WorkflowEdgeBubble。 -->
<template>
  <div class="edge-bubble">
    <div class="edge-bubble__header">
      <strong>连线设置</strong>
      <span>{{ edge.id }}</span>
    </div>

    <ElForm label-position="top">
      <ElFormItem v-if="isSemanticEdge" label="分支标识">
        <ElInput :model-value="branchDisplayText" readonly />
        <div v-if="branchHint" class="edge-bubble__hint">{{ branchHint }}</div>
      </ElFormItem>
      <ElFormItem label="画布说明">
        <ElInput :model-value="displayLabel || '—'" readonly />
      </ElFormItem>
      <ElFormItem label="执行条件（CEL）">
        <ElInput
          v-model.trim="localForm.condition"
          class="edge-bubble__condition"
          type="textarea"
          :rows="3"
          resize="vertical"
          spellcheck="false"
        />
      </ElFormItem>
    </ElForm>

    <div class="edge-bubble__footer">
      <ElButton size="small" @click="$emit('cancel')">取消</ElButton>
      <ElButton size="small" type="primary" @click="$emit('confirm', cloneForm(localForm))"
        >确定</ElButton
      >
    </div>
  </div>
</template>

<script setup lang="ts">
  import type { WorkflowDomainEdge, WorkflowEdgeFormModel } from '../types'
  import { LOOP_NEXT_BRANCH } from '../node-registry'
  import { buildEdgeDisplayLabel } from '../workflow-editor.mapper'

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

  // 只要这条边是从「有语义的出口」拉出来的（分支的 true/false…、循环的 body/next），
  // 就把分支标识显示出来。原来写死判断 true/false，foreach 的 NEXT 边点开是一片空白。
  const isSemanticEdge = computed(() => {
    const port = props.edge.sourcePort || ''
    return Boolean(port) && port !== 'out'
  })

  const BRANCH_HINTS: Record<string, string> = {
    body: '循环体：每个元素跑一遍',
    [LOOP_NEXT_BRANCH]: '循环后继：全部元素跑完后继续',
    true: '条件成立时走这条',
    false: '条件不成立时走这条'
  }

  const branchDisplayText = computed(() => localForm.branch || props.edge.sourcePort || '')
  const branchHint = computed(() => BRANCH_HINTS[props.edge.sourcePort || ''] || '')
  const displayLabel = computed(() => buildEdgeDisplayLabel(localForm.condition))

  const localForm = reactive<WorkflowEdgeFormModel>({
    id: props.edge.id,
    source: props.edge.source,
    target: props.edge.target,
    sourcePort: props.edge.sourcePort || '',
    targetPort: props.edge.targetPort || '',
    branch: props.edge.data.branch || '',
    label: props.edge.data.label || '',
    condition: props.edge.data.condition || ''
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
        label: value.data.label || '',
        condition: value.data.condition || ''
      })
    },
    { deep: true }
  )
</script>

<style scoped lang="scss">
  .edge-bubble {
    width: 300px;
    max-width: calc(100vw - 24px);
    box-sizing: border-box;
    padding: 14px;
    background: color-mix(in srgb, var(--workflow-overlay-bg) 97%, transparent);
    backdrop-filter: blur(16px);
    border: 1px solid var(--workflow-overlay-border-soft);
    border-radius: 8px;
    box-shadow: 0 12px 28px rgb(31 35 48 / 0.14);
  }

  .edge-bubble__header {
    margin-bottom: 8px;

    strong {
      display: block;
      font-size: 14px;
      color: var(--workflow-overlay-text);
    }

    span {
      display: block;
      margin-top: 4px;
      font-size: 12px;
      color: var(--workflow-overlay-muted);
    }
  }

  .edge-bubble__hint {
    margin-top: 4px;
    font-size: 12px;
    line-height: 18px;
    color: var(--workflow-overlay-muted);
  }

  .edge-bubble__condition :deep(textarea) {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    line-height: 1.5;
  }

  .edge-bubble__footer {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
  }
</style>
