<!-- 工作流编辑器页面或组件：WorkflowEdgeBubble。 -->
<template>
  <div class="edge-bubble">
    <div class="edge-bubble__header">
      <strong>连线设置</strong>
      <span>{{ edge.id }}</span>
    </div>

    <ElForm label-position="top">
      <template v-if="localForm.kind === 'data'">
        <ElFormItem label="输出字段">
          <ElSelect v-model="localForm.sourcePointer" class="edge-bubble__full">
            <ElOption
              v-for="option in sourceFieldOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="输入字段">
          <ElSelect v-model="localForm.targetPointer" class="edge-bubble__full">
            <ElOption
              v-for="option in targetFieldOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </ElSelect>
        </ElFormItem>
      </template>
      <ElFormItem v-if="isSemanticEdge" label="分支标识">
        <ElInput :model-value="branchDisplayText" readonly />
        <div v-if="branchHint" class="edge-bubble__hint">{{ branchHint }}</div>
      </ElFormItem>
      <ElFormItem label="显示标签">
        <ElInput v-model.trim="localForm.label" placeholder="用于画布展示" />
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
  import type { WorkflowDomainEdge, WorkflowDomainNode, WorkflowEdgeFormModel } from '../types'
  import { LOOP_NEXT_BRANCH } from '../node-registry'

  interface Props {
    edge: WorkflowDomainEdge
    nodes: WorkflowDomainNode[]
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
    return localForm.kind === 'flow' && Boolean(port) && port !== 'out'
  })

  const pointerToken = (value: string) => value.replace(/~/g, '~0').replace(/\//g, '~1')
  const fieldOptions = (schema: Record<string, any>, label: string) => {
    const options = [{ value: '', label: `${label}（全部）` }]
    const visit = (current: Record<string, any>, pointer: string, path: string) => {
      const properties = current?.properties || {}
      Object.entries(properties).forEach(([key, raw]) => {
        const child = (raw || {}) as Record<string, any>
        const nextPointer = `${pointer}/${pointerToken(key)}`
        const nextPath = path ? `${path}.${String(child.title || key)}` : String(child.title || key)
        options.push({ value: nextPointer, label: nextPath })
        if (child.type === 'object') visit(child, nextPointer, nextPath)
      })
    }
    if (schema?.type === 'object') visit(schema, '', '')
    return options
  }

  const sourcePort = computed(() =>
    props.nodes
      .find((node) => node.id === props.edge.source)
      ?.ports.find(
        (port) =>
          port.edgeKind === 'data' && port.role === 'out' && port.portId === props.edge.sourcePort
      )
  )
  const targetPort = computed(() =>
    props.nodes
      .find((node) => node.id === props.edge.target)
      ?.ports.find(
        (port) =>
          port.edgeKind === 'data' && port.role === 'in' && port.portId === props.edge.targetPort
      )
  )
  const sourceFieldOptions = computed(() =>
    fieldOptions(sourcePort.value?.schema || {}, sourcePort.value?.label || '输出')
  )
  const targetFieldOptions = computed(() =>
    fieldOptions(targetPort.value?.schema || {}, targetPort.value?.label || '输入')
  )

  const BRANCH_HINTS: Record<string, string> = {
    body: '循环体：每个元素跑一遍',
    [LOOP_NEXT_BRANCH]: '循环后继：全部元素跑完后继续',
    true: '条件成立时走这条',
    false: '条件不成立时走这条'
  }

  const branchDisplayText = computed(() => localForm.branch || props.edge.sourcePort || '')
  const branchHint = computed(() => BRANCH_HINTS[props.edge.sourcePort || ''] || '')

  const localForm = reactive<WorkflowEdgeFormModel>({
    id: props.edge.id,
    source: props.edge.source,
    target: props.edge.target,
    sourcePort: props.edge.sourcePort || '',
    targetPort: props.edge.targetPort || '',
    kind: props.edge.data.kind,
    branch: props.edge.data.branch || '',
    label: props.edge.data.label || '',
    sourcePointer: props.edge.data.sourcePointer || '',
    targetPointer: props.edge.data.targetPointer || ''
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
        kind: value.data.kind,
        branch: value.data.branch || '',
        label: value.data.label || '',
        sourcePointer: value.data.sourcePointer || '',
        targetPointer: value.data.targetPointer || ''
      })
    },
    { deep: true }
  )
</script>

<style scoped lang="scss">
  .edge-bubble {
    width: 240px;
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

  .edge-bubble__full {
    width: 100%;
  }

  .edge-bubble__footer {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
  }
</style>
