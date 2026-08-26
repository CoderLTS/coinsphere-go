<template>
  <div class="node-console">
    <header class="console-head">
      <div><div class="eyebrow">工作流调度</div><h1>节点定义</h1></div>
      <ElButton :icon="Refresh" :loading="loading" @click="loadData">刷新</ElButton>
    </header>

    <ElCard shadow="never">
      <ElTabs model-value="builtin">
        <ElTabPane label="内置节点" name="builtin">
          <ElTable :data="definitions" v-loading="loading">
            <ElTableColumn label="节点" min-width="210">
              <template #default="{ row }">
                <div class="node-name">
                  <span><ArtSvgIcon icon="ri:node-tree" /></span>
                  <strong>{{ row.label }}</strong>
                </div>
              </template>
            </ElTableColumn>
            <ElTableColumn label="能力" min-width="220">
              <template #default="{ row }">{{ capabilityLabel(row) }}</template>
            </ElTableColumn>
            <ElTableColumn label="配置项" min-width="320">
              <template #default="{ row }">
                <div class="field-tags">
                  <ElTag
                    v-for="field in configFields(row)"
                    :key="field"
                    effect="plain"
                    size="small"
                  >
                    {{ field }}
                  </ElTag>
                  <span v-if="!configFields(row).length" class="muted">无需配置</span>
                </div>
              </template>
            </ElTableColumn>
            <ElTableColumn label="状态" width="100">
              <template #default><ElTag type="success" effect="plain">可用</ElTag></template>
            </ElTableColumn>
          </ElTable>
        </ElTabPane>
      </ElTabs>
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
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
  .node-console {
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding-bottom: 24px;
  }

  .console-head,
  .node-name {
    display: flex;
    align-items: center;
  }

  .console-head {
    gap: 12px;
    justify-content: space-between;
  }

  .eyebrow {
    font-size: 12px;
    font-weight: 600;
    color: var(--el-color-primary);
    letter-spacing: 0;
  }

  h1 {
    margin: 6px 0 0;
    font-size: 24px;
  }

  .node-name {
    gap: 10px;
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

  .node-name small {
    display: block;
    margin-top: 3px;
    color: var(--el-text-color-secondary);
  }

  .field-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }

  .muted {
    color: var(--el-text-color-secondary);
  }

  @media (max-width: 680px) {
    .console-head {
      flex-direction: column;
      align-items: flex-start;
    }
  }
</style>
