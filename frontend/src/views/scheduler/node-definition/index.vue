<template>
  <div class="art-full-height">
    <ElCard class="art-table-card">
      <ArtTableHeader
        :loading="loading"
        :show-zebra="false"
        layout="refresh,size,fullscreen,settings"
        @refresh="loadData"
      />

      <ArtTable
        :data="definitions"
        :loading="loading"
        :stripe="false"
        table-layout="auto"
        empty-height="320px"
      >
        <ElTableColumn label="节点名称" min-width="210">
          <template #default="{ row }">
            <div class="node-name">
              <span><ArtSvgIcon icon="ri:node-tree" /></span>
              <strong>{{ row.label }}</strong>
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="节点能力" min-width="220">
          <template #default="{ row }">{{ capabilityLabel(row) }}</template>
        </ElTableColumn>
        <ElTableColumn label="配置项" min-width="320">
          <template #default="{ row }">
            <div class="field-tags">
              <ElTag v-for="field in configFields(row)" :key="field" effect="plain" size="small">
                {{ field }}
              </ElTag>
              <span v-if="!configFields(row).length" class="muted">无需配置</span>
            </div>
          </template>
        </ElTableColumn>
        <ElTableColumn label="状态" width="100" align="center">
          <template #default><ElTag type="success" effect="plain">可用</ElTag></template>
        </ElTableColumn>
      </ArtTable>
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { fetchNodeDefinitions, type WorkflowNodeDefinitionItem } from '@/api/scheduler'

  defineOptions({ name: 'NodeDefinitions' })

  const loading = ref(false)
  const definitions = ref<WorkflowNodeDefinitionItem[]>([])
  const hiddenConfigFields = new Set(['entryKey', 'workflowCode'])
  const configFields = (row: WorkflowNodeDefinitionItem) =>
    Object.entries((row.configSchema?.properties || {}) as Record<string, any>)
      .filter(([key]) => !hiddenConfigFields.has(key))
      .map(([, item]) => String(item?.title || ''))
      .filter(Boolean)
  const capabilityLabel = (row: WorkflowNodeDefinitionItem) =>
    ({
      start: '启动工作流',
      terminal: '结束工作流',
      branch: '条件分流',
      loop: '循环处理',
      plain: '处理工作流数据'
    })[row.kind || 'plain']
  const loadData = async () => {
    loading.value = true
    try {
      definitions.value = await fetchNodeDefinitions()
    } finally {
      loading.value = false
    }
  }

  onMounted(() => void loadData())
</script>

<style scoped lang="scss">
  .node-name {
    display: flex;
    gap: 10px;
    align-items: center;
  }

  .node-name > span {
    display: grid;
    place-items: center;
    width: 34px;
    height: 34px;
    color: var(--el-color-primary);
    background: var(--el-color-primary-light-9);
    border-radius: 7px;
  }

  .field-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }

  .muted {
    color: var(--el-text-color-secondary);
  }
</style>
