<template>
  <section aria-label="业务节点结果">
    <p>运行 #{{ result.run.id }} · {{ result.runNode.nodeInstanceId }}</p>
    <p>{{ result.runNode.outputSummary.message }}</p>
  </section>
</template>
<script setup lang="ts">
import type { WorkflowRunDetail, WorkflowRunNode } from "@/api/workflows";
defineProps<{ result: { run: WorkflowRunDetail; runNode: WorkflowRunNode } }>();
</script>
