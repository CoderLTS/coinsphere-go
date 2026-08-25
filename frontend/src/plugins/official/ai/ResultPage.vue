<template>
  <OfficialRunResult
    eyebrow="AI"
    title="模型调用"
    title-id="ai-result-title"
    :result="result"
    :items="items"
  />
</template>

<script setup lang="ts">
  import type { WorkflowBatchDetail, WorkflowNodeRun } from '@/api/workflows'
  import OfficialRunResult from '../OfficialRunResult.vue'

  const { result } = defineProps<{
    result: { batch: WorkflowBatchDetail; nodeRun: WorkflowNodeRun }
  }>()
  const duration = computed(() =>
    result.nodeRun.durationMs === undefined ? '进行中' : `${result.nodeRun.durationMs} ms`
  )
  const items = computed(() => [
    { label: '批次', value: `#${result.batch.id}` },
    { label: '节点', value: result.nodeRun.nodeInstanceId },
    { label: '尝试', value: result.nodeRun.attempt },
    { label: '耗时', value: duration.value },
    { label: '诊断重放', value: result.batch.diagnostic ? '是' : '否' },
    { label: '操作键', value: result.nodeRun.operationKey, code: true }
  ])
</script>
