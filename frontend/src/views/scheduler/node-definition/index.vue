<template>
  <div class="node-definition-page art-full-height">
    <ElCard class="art-table-card">
      <ArtTableHeader :loading="loading" :show-zebra="false" @refresh="loadDefinitions">
        <template #left>
          <div>
            <div class="page-title">节点定义</div>
            <div class="page-subtitle">工作流可编排的内置节点类型及其配置结构。</div>
          </div>
        </template>
      </ArtTableHeader>

      <ElTable v-loading="loading" :data="definitions" stripe>
        <ElTableColumn prop="label" label="节点名称" min-width="180" />
        <ElTableColumn prop="typeCode" label="类型编码" min-width="230" show-overflow-tooltip />
        <ElTableColumn prop="kind" label="图语义" width="120" align="center" />
        <ElTableColumn label="分支" min-width="170">
          <template #default="{ row }">
            {{ row.branches?.length ? row.branches.join(' / ') : '--' }}
          </template>
        </ElTableColumn>
        <ElTableColumn label="配置 Schema" min-width="260">
          <template #default="{ row }">
            <ElButton link type="primary" @click="openSchema(row)">查看 Schema</ElButton>
          </template>
        </ElTableColumn>
      </ElTable>
    </ElCard>

    <ElDialog v-model="schemaVisible" :title="schemaTitle" width="720px">
      <pre class="schema-view">{{ schemaText }}</pre>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import { computed, onMounted, ref } from 'vue'
  import { ElButton, ElCard, ElDialog, ElTable, ElTableColumn } from 'element-plus'
  import { fetchNodeDefinitions, type WorkflowNodeDefinitionItem } from '@/api/scheduler'

  defineOptions({ name: 'NodeDefinitions' })

  const loading = ref(false)
  const definitions = ref<WorkflowNodeDefinitionItem[]>([])
  const selected = ref<WorkflowNodeDefinitionItem | null>(null)
  const schemaVisible = ref(false)
  const schemaTitle = computed(() =>
    selected.value ? `${selected.value.label} / ${selected.value.typeCode}` : '配置 Schema'
  )
  const schemaText = computed(() => JSON.stringify(selected.value?.configSchema || {}, null, 2))

  const loadDefinitions = async () => {
    loading.value = true
    try {
      definitions.value = await fetchNodeDefinitions()
    } finally {
      loading.value = false
    }
  }

  const openSchema = (definition: WorkflowNodeDefinitionItem) => {
    selected.value = definition
    schemaVisible.value = true
  }

  onMounted(() => void loadDefinitions())
</script>

<style scoped lang="scss">
  .page-title {
    font-size: 16px;
    font-weight: 600;
    color: var(--art-text-gray-900);
  }

  .page-subtitle {
    margin-top: 4px;
    font-size: 13px;
    color: var(--art-text-gray-600);
  }

  .schema-view {
    max-height: 60vh;
    padding: 16px;
    margin: 0;
    overflow: auto;
    color: var(--art-text-gray-800);
    white-space: pre-wrap;
    background: var(--art-bg-color-page);
    border: 1px solid var(--art-border-color);
    border-radius: 4px;
  }
</style>
