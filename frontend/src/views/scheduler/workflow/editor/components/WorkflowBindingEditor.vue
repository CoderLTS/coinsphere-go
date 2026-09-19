<template>
  <div class="binding">
    <div class="binding__head">
      <ElSelect :model-value="binding?.kind || ''" aria-label="输入方式" @change="changeKind">
        <ElOption label="未设置" value="" />
        <ElOption label="固定值" value="literal" />
        <ElOption label="上游节点" value="node" />
        <ElOption label="触发事件" value="event" />
        <ElOption label="Profile" value="profile" />
      </ElSelect>
    </div>
    <template v-if="binding?.kind === 'node'">
      <ElSelect
        :model-value="fieldKey"
        filterable
        placeholder="选择上游节点字段"
        @change="selectField"
      >
        <ElOption
          v-for="(option, index) in options"
          :key="index"
          :value="String(index)"
          :label="option.label"
        />
      </ElSelect>
      <ElSelect
        :model-value="binding.nodeInstanceId"
        placeholder="节点"
        @change="(id) => update({ ...binding!, nodeInstanceId: id })"
      >
        <ElOption
          v-for="node in upstreamNodes"
          :key="node.nodeInstanceId"
          :value="node.nodeInstanceId"
          :label="node.label || node.nodeType"
        />
      </ElSelect>
      <ElSelect
        :model-value="binding.fieldPath || []"
        multiple
        filterable
        allow-create
        default-first-option
        placeholder="嵌套字段路径（逐段添加）"
        @update:model-value="(path) => update({ ...binding!, fieldPath: path })"
      >
        <ElOption v-for="key in pathOptions" :key="key" :value="key" :label="key" />
      </ElSelect>
    </template>
    <ElSelect
      v-else-if="binding?.kind === 'event'"
      :model-value="binding.fieldPath || []"
      multiple
      filterable
      allow-create
      default-first-option
      placeholder="触发事件字段路径（逐段添加）"
      @update:model-value="(path) => update({ ...binding!, fieldPath: path })"
    >
      <ElOption value="data" label="data" /><ElOption value="triggeredAt" label="triggeredAt" />
    </ElSelect>
    <ElInput
      v-else-if="binding?.kind === 'profile'"
      :model-value="binding.profileKey"
      placeholder="Profile 引用键"
      @update:model-value="(profileKey) => update({ ...binding!, profileKey: String(profileKey) })"
    />
    <WorkflowSchemaFields
      v-else-if="binding?.kind === 'literal' && schema.type === 'object'"
      :schema="{ type: 'object', properties: { value: schema } }"
      :config="{ value: binding.value }"
      @update="(_, value) => update({ kind: 'literal', value })"
    />
    <WorkflowSchemaField
      v-else-if="binding?.kind === 'literal'"
      :field="buildSchemaField('value', schema)"
      :value="binding.value"
      @update="(value) => update({ kind: 'literal', value })"
    />
  </div>
</template>

<script setup lang="ts">
  import type {
    WorkflowInputBinding,
    WorkflowGraph,
    WorkflowNodeDefinition,
    WorkflowBindingKind
  } from '@/api/workflows'
  import { ancestors, outputFields } from '../canvas'
  import WorkflowSchemaField from './WorkflowSchemaField.vue'
  import WorkflowSchemaFields from './WorkflowSchemaFields.vue'
  import { buildSchemaField } from './workflow-schema-field'
  const props = defineProps<{
    binding?: WorkflowInputBinding
    schema: Record<string, any>
    graph: WorkflowGraph
    definitions: WorkflowNodeDefinition[]
    nodeId: string
  }>()
  const emit = defineEmits<{ (event: 'update', value: WorkflowInputBinding | undefined): void }>()
  const update = (value: WorkflowInputBinding | undefined) => emit('update', value)
  const options = computed(() => outputFields(props.graph, props.definitions, props.nodeId))
  const upstreamNodes = computed(() =>
    props.graph.nodes.filter((n) => ancestors(props.graph, props.nodeId).has(n.nodeInstanceId))
  )
  const fieldKey = computed(() =>
    String(
      options.value.findIndex(
        (o) =>
          o.nodeId === props.binding?.nodeInstanceId &&
          o.path.join('.') === props.binding?.fieldPath?.join('.')
      )
    )
  )
  const pathOptions = computed(() => [
    ...new Set(
      options.value.filter((o) => o.nodeId === props.binding?.nodeInstanceId).flatMap((o) => o.path)
    )
  ])
  const selectField = (index: string) => {
    const o = options.value[Number(index)]
    if (o) update({ kind: 'node', nodeInstanceId: o.nodeId, fieldPath: o.path })
  }
  function changeKind(kind: WorkflowBindingKind | '') {
    if (!kind) return update(undefined)
    if (kind === 'literal')
      return update({
        kind,
        value:
          props.schema.default ??
          (props.schema.type === 'boolean'
            ? false
            : props.schema.type === 'object'
              ? {}
              : props.schema.type === 'array'
                ? []
                : props.schema.type === 'number' || props.schema.type === 'integer'
                  ? 0
                  : '')
      })
    update(kind === 'profile' ? { kind, profileKey: '' } : { kind, fieldPath: [] })
  }
</script>

<style scoped>
  .binding {
    display: grid;
    gap: 8px;
    width: 100%;
  }

  .binding__head {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .binding__child {
    padding-left: 10px;
    border-left: 2px solid var(--el-border-color);
  }

  .binding__preview {
    padding: 10px;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
    background: var(--el-fill-color-light);
  }
</style>
