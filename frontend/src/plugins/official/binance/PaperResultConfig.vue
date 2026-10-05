<template>
  <ElFormItem label="工作流"
    ><ElSelect
      :model-value="scope.workflowId"
      filterable
      remote
      :remote-method="search"
      @update:model-value="selectWorkflow"
      ><ElOption
        v-for="workflow in workflows"
        :key="workflow.id"
        :label="workflow.name"
        :value="workflow.id" /></ElSelect
  ></ElFormItem>
  <ElFormItem label="Paper 节点"
    ><ElSelect
      :model-value="scope.paperNodeInstanceId"
      @update:model-value="emit('update:scope', { ...scope, paperNodeInstanceId: $event })"
      ><ElOption v-for="id in nodeIDs" :key="id" :label="id" :value="id" /></ElSelect
  ></ElFormItem>
  <WorkflowSchemaFields :schema="filterSchema" :config="filters" @update="setFilter" />
</template>
<script setup lang="ts">
  import { fetchWorkflows, fetchWorkflowRevision, type WorkflowSummary } from '@/api/workflows'
  import WorkflowSchemaFields from '@/components/workflow/WorkflowSchemaFields.vue'
  const props = defineProps<{
    scope: Record<string, any>
    filters: Record<string, any>
    filterSchema: Record<string, any>
  }>()
  const emit = defineEmits<{
    (event: 'update:scope', scope: Record<string, unknown>): void
    (event: 'update:filters', filters: Record<string, unknown>): void
  }>()
  const workflows = ref<WorkflowSummary[]>([]),
    nodeIDs = ref<string[]>([])
  const search = async (keyword = '') => {
    workflows.value = (await fetchWorkflows({ keyword, limit: 20 })).records
  }
  const selectWorkflow = async (id: number) => {
    const workflow = workflows.value.find((item) => item.id === id)
    if (!workflow) return
    const revision = await fetchWorkflowRevision(id, workflow.draftRevisionId)
    nodeIDs.value = revision.graph.nodes
      .filter((node) => node.nodeType === 'official.binance.paper_execute')
      .map((node) => node.nodeInstanceId)
    emit('update:scope', { workflowId: id, paperNodeInstanceId: nodeIDs.value[0] || '' })
  }
  const setFilter = (key: string, value: unknown) => {
    const next = { ...props.filters }
    if (value === '' || value === undefined) delete next[key]
    else next[key] = value
    emit('update:filters', next)
  }
  onMounted(() => search())
</script>
