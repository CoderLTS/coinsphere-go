<!-- 工作流编辑器页面或组件：WorkflowPropertyPanel。 -->
<template>
  <section class="property-panel">
    <ElEmpty description="当前编辑器已改为原位编辑模式，此组件保留为兼容占位。" :image-size="72" />
  </section>
</template>

<script setup lang="ts">
  import type { TaskDefinitionItem } from '@/api/scheduler'
  import type {
    WorkflowDomainEdge,
    WorkflowDomainNode,
    WorkflowEditorDraftState,
    WorkflowEditorIssue
  } from '../types'

  interface Props {
    selectedNode: WorkflowDomainNode | null
    selectedEdge: WorkflowDomainEdge | null
    draftState: WorkflowEditorDraftState
    taskDefinitions: TaskDefinitionItem[]
    issues: WorkflowEditorIssue[]
    collapsed?: boolean
  }

  interface Emits {
    (e: 'toggle'): void
    (e: 'update-draft', value: null): void
    (e: 'commit-draft'): void
    (e: 'discard-draft'): void
  }

  withDefaults(defineProps<Props>(), {
    collapsed: false
  })

  defineEmits<Emits>()
</script>

<style scoped lang="scss">
  .property-panel {
    width: 360px;
    max-height: calc(100vh - 168px);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px;
    border: 1px solid rgba(203, 213, 225, 0.75);
    border-radius: 20px;
    background: rgba(255, 255, 255, 0.94);
    box-shadow: 0 18px 36px rgba(15, 23, 42, 0.08);
    backdrop-filter: blur(14px);
  }
</style>
