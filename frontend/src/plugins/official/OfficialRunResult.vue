<template>
  <section class="official-run-result" :aria-labelledby="titleId">
    <header>
      <div>
        <p>{{ eyebrow }}</p>
        <h3 :id="titleId">{{ title }}</h3>
      </div>
      <span :data-status="result.nodeRun.status">{{ result.nodeRun.status }}</span>
    </header>
    <dl>
      <div v-for="item in items" :key="item.label">
        <dt>{{ item.label }}</dt>
        <dd
          ><code v-if="item.code">{{ item.value }}</code
          ><template v-else>{{ item.value }}</template></dd
        >
      </div>
    </dl>
  </section>
</template>

<script setup lang="ts">
  import type { WorkflowBatchDetail, WorkflowNodeRun } from '@/api/workflows'

  defineProps<{
    eyebrow: string
    title: string
    titleId: string
    result: { batch: WorkflowBatchDetail; nodeRun: WorkflowNodeRun }
    items: Array<{ label: string; value: string | number; code?: boolean }>
  }>()
</script>

<style scoped>
  .official-run-result {
    color: var(--el-text-color-primary);
    letter-spacing: 0;
  }

  header {
    display: flex;
    gap: 24px;
    align-items: flex-start;
    justify-content: space-between;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--el-border-color);
  }

  p,
  h3 {
    margin: 0;
  }

  p,
  dt {
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  h3 {
    margin-top: 4px;
    font-size: 18px;
    font-weight: 650;
  }

  header > span {
    padding: 4px 8px;
    font-size: 12px;
    font-weight: 600;
    color: var(--el-text-color-secondary);
    border: 1px solid var(--el-border-color);
    border-radius: 4px;
  }

  header > span[data-status='succeeded'] {
    color: var(--el-color-success-dark-2);
    background: var(--el-color-success-light-9);
    border-color: var(--el-color-success-light-5);
  }

  header > span[data-status='failed'],
  header > span[data-status='cancelled'] {
    color: var(--el-color-danger-dark-2);
    background: var(--el-color-danger-light-9);
    border-color: var(--el-color-danger-light-5);
  }

  dl {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    margin: 0;
  }

  dl > div {
    min-width: 0;
    padding: 14px 0;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  dd {
    min-width: 0;
    margin: 4px 0 0;
    font-size: 14px;
    overflow-wrap: anywhere;
  }

  code {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 12px;
  }

  @media (max-width: 540px) {
    dl {
      grid-template-columns: 1fr;
    }
  }
</style>
