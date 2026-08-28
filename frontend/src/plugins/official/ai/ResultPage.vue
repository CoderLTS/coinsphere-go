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
  import type { WorkflowRunDetail, WorkflowRunNode } from '@/api/workflows'
  import OfficialRunResult from '../OfficialRunResult.vue'

  const { result } = defineProps<{
    result: { run: WorkflowRunDetail; runNode: WorkflowRunNode }
  }>()
  const duration = computed(() =>
    result.runNode.durationMs === undefined ? '进行中' : `${result.runNode.durationMs} ms`
  )
  const items = computed(() => [
    { label: '运行', value: `#${result.run.id}` },
    { label: '节点', value: result.runNode.nodeInstanceId },
    { label: '尝试', value: result.runNode.attempt },
    { label: '耗时', value: duration.value },
    { label: '诊断重放', value: result.run.diagnostic ? '是' : '否' },
    { label: '操作键', value: result.runNode.operationKey, code: true }
  ])
</script>
