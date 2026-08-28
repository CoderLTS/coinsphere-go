<template>
  <OfficialRunResult
    eyebrow="Connector"
    :title="connectorName"
    title-id="connector-result-title"
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

  const connectorName = computed(
    () =>
      ({
        'official.connector.http': 'HTTP 请求',
        'official.connector.webhook': 'Webhook 事件',
        'official.connector.websocket': 'WebSocket 事件'
      })[result.runNode.nodeType] || result.runNode.nodeType
  )
  const duration = computed(() =>
    result.runNode.durationMs === undefined ? '进行中' : `${result.runNode.durationMs} ms`
  )
  const items = computed(() => [
    { label: '运行', value: `#${result.run.id}` },
    { label: '节点', value: result.runNode.nodeInstanceId },
    { label: '执行池', value: result.runNode.executionPool },
    { label: '尝试', value: result.runNode.attempt },
    { label: '耗时', value: duration.value },
    { label: '操作键', value: result.runNode.operationKey, code: true }
  ])
</script>
