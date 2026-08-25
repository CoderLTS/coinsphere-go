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
  import type { WorkflowBatchDetail, WorkflowNodeRun } from '@/api/workflows'
  import OfficialRunResult from '../OfficialRunResult.vue'

  const { result } = defineProps<{
    result: { batch: WorkflowBatchDetail; nodeRun: WorkflowNodeRun }
  }>()

  const connectorName = computed(
    () =>
      ({
        'official.connector.http': 'HTTP 请求',
        'official.connector.webhook': 'Webhook 事件',
        'official.connector.websocket': 'WebSocket 事件'
      })[result.nodeRun.nodeType] || result.nodeRun.nodeType
  )
  const duration = computed(() =>
    result.nodeRun.durationMs === undefined ? '进行中' : `${result.nodeRun.durationMs} ms`
  )
  const items = computed(() => [
    { label: '批次', value: `#${result.batch.id}` },
    { label: '节点', value: result.nodeRun.nodeInstanceId },
    { label: '执行池', value: result.nodeRun.executionPool },
    { label: '尝试', value: result.nodeRun.attempt },
    { label: '耗时', value: duration.value },
    { label: '操作键', value: result.nodeRun.operationKey, code: true }
  ])
</script>
