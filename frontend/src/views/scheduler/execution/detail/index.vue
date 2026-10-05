<template>
  <main v-loading="loading" class="run-page">
    <header class="run-header"
      ><div
        ><ElButton
          link
          @click="
            router.push({ path: '/scheduler/execution', query: { workflowId: run?.workflowId } })
          "
          >返回运行记录</ElButton
        ><h1>运行 #{{ run?.id }}</h1
        ><p v-if="run"
          >版本 #{{ run.revisionId }} · 入口 {{ run.entryPoint }} ·
          {{ run.diagnostic ? '诊断重放' : run.triggerType }}</p
        ></div
      ><ElSpace wrap
        ><ElTag>{{ runStatusLabel(run?.status) }}</ElTag
        ><ElButton @click="load">刷新</ElButton
        ><ElButton
          v-if="
            run &&
            run.permissions.includes('workflows.cancel') &&
            ['queued', 'running', 'waiting', 'retrying'].includes(run.status)
          "
          @click="action('cancel')"
          >取消运行</ElButton
        ><ElButton
          v-if="run && run.permissions.includes('workflows.retry') && run.status === 'failed'"
          :disabled="run.errorCategory === 'unknown_result'"
          @click="action('retry')"
          >重试</ElButton
        ><ElButton
          v-if="
            run &&
            run.permissions.includes('workflows.run') &&
            ['succeeded', 'failed', 'cancelled'].includes(run.status)
          "
          @click="action('replay')"
          >诊断重放</ElButton
        ></ElSpace
      ></header
    >
    <ElAlert
      v-if="error || run?.errorMessage"
      type="error"
      :title="error || run?.errorMessage || ''"
      :closable="false"
    />
    <ElTabs v-model="tab"
      ><ElTabPane label="执行轨迹" name="graph"
        ><WorkflowGraphCanvas
          v-if="revision"
          :graph="revision.graph"
          :definitions="definitions"
          :states="states"
          readonly
          @select="selectNode" /></ElTabPane
      ><ElTabPane label="节点记录" name="nodes"
        ><ElTable :data="run?.runNodes || []" @row-click="selectAttempt"
          ><ElTableColumn prop="nodeInstanceId" label="节点" min-width="180" /><ElTableColumn
            label="状态"
            ><template #default="{ row }">{{ runStatusLabel(row.status) }}</template></ElTableColumn
          ><ElTableColumn prop="attempt" label="尝试" width="70" /><ElTableColumn
            prop="loopIteration"
            label="循环次数"
            width="90" /><ElTableColumn
            prop="durationMs"
            label="耗时（ms）"
            width="120" /><ElTableColumn
            prop="errorMessage"
            label="错误"
            min-width="200" /></ElTable></ElTabPane
      ><ElTabPane label="日志" name="logs"
        ><ElTable :data="run?.logs || []"
          ><ElTableColumn prop="loggedAt" label="时间" width="210" /><ElTableColumn
            prop="level"
            label="级别"
            width="90" /><ElTableColumn
            prop="message"
            label="内容"
            min-width="280" /></ElTable></ElTabPane
      ><ElTabPane label="制品" name="artifacts"
        ><ElTable :data="run?.artifacts || []"
          ><ElTableColumn prop="nodeInstanceId" label="节点" /><ElTableColumn
            prop="mediaType"
            label="类型"
          /><ElTableColumn prop="sizeBytes" label="大小（字节）" /><ElTableColumn label="下载"
            ><template #default="{ row }"
              ><ElButton link @click="download(row)">下载制品</ElButton></template
            ></ElTableColumn
          ></ElTable
        ></ElTabPane
      ><ElTabPane label="输入与结果" name="data"
        ><h3>本次输入</h3><pre>{{ JSON.stringify(run?.input, null, 2) }}</pre
        ><h3>结果摘要</h3><pre>{{ JSON.stringify(run?.resultSummary, null, 2) }}</pre>
      </ElTabPane></ElTabs
    >
    <ElDrawer
      v-model="nodeVisible"
      :title="selectedNode?.nodeInstanceId || '节点结果'"
      size="min(860px, 100vw)"
      ><template v-if="selectedNode && run"
        ><p
          >尝试 {{ selectedNode.attempt }} · {{ runStatusLabel(selectedNode.status) }} · 循环
          {{ selectedNode.loopIteration }}</p
        ><ElAlert v-if="panelError" type="error" :title="panelError" :closable="false" /><ElTabs
          ><ElTabPane label="输入">
            <pre>{{ JSON.stringify(selectedNode.inputSummary, null, 2) }}</pre></ElTabPane
          ><ElTabPane label="输出">
            <pre>{{ JSON.stringify(selectedNode.outputSummary, null, 2) }}</pre></ElTabPane
          ><ElTabPane v-for="panel in panels" :key="panel.id" :label="panel.title"
            ><component
              :is="panel.component"
              :result="{ run, runNode: selectedNode }" /></ElTabPane></ElTabs></template
    ></ElDrawer>
  </main>
</template>
<script setup lang="ts">
  import type { Component } from 'vue'
  import {
    fetchWorkflowRun,
    fetchWorkflowRevision,
    fetchWorkflowNodeDefinitions,
    applyWorkflowRunAction,
    downloadWorkflowArtifact,
    type WorkflowRunDetail,
    type WorkflowRevision,
    type WorkflowNodeDefinition,
    type WorkflowRunNode,
    type WorkflowArtifact
  } from '@/api/workflows'
  import { fetchPluginCatalog, type PluginCatalogItem } from '@/api/pluginCatalog'
  import { loadPluginComponent } from '@/plugins'
  import { runStatusLabel } from '@/components/workflow/status'
  import WorkflowGraphCanvas from '@/components/workflow/WorkflowGraphCanvas.vue'
  const route = useRoute(),
    router = useRouter()
  const run = ref<WorkflowRunDetail>(),
    revision = ref<WorkflowRevision>(),
    definitions = ref<WorkflowNodeDefinition[]>([]),
    catalog = ref<PluginCatalogItem[]>([])
  const loading = ref(false),
    error = ref(''),
    tab = ref('graph'),
    nodeVisible = ref(false),
    selectedNode = ref<WorkflowRunNode>(),
    panelError = ref('')
  const panels = shallowRef<{ id: string; title: string; component: Component }[]>([])
  const states = computed(() =>
    Object.fromEntries(
      (run.value?.runNodes || []).map((node) => [node.nodeInstanceId, node.status])
    )
  )
  let timer: ReturnType<typeof setTimeout> | undefined,
    requestID = 0
  const load = async () => {
    clearTimeout(timer)
    const request = ++requestID
    loading.value = !run.value
    error.value = ''
    try {
      const id = Number(route.params.executionId)
      if (!Number.isSafeInteger(id) || id <= 0) throw new Error('运行编号无效')
      const current = await fetchWorkflowRun(id)
      if (request !== requestID) return
      if (revision.value?.id !== current.revisionId) {
        const [snapshot, nodes, plugins] = await Promise.all([
          fetchWorkflowRevision(current.workflowId, current.revisionId),
          fetchWorkflowNodeDefinitions(),
          fetchPluginCatalog()
        ])
        if (request !== requestID) return
        revision.value = snapshot
        definitions.value = nodes.items
        catalog.value = plugins.items
      }
      run.value = current
      if (selectedNode.value)
        selectedNode.value = current.runNodes.find((node) => node.id === selectedNode.value?.id)
      if (['queued', 'running', 'waiting', 'retrying'].includes(current.status))
        timer = setTimeout(load, 3000)
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      if (request === requestID) loading.value = false
    }
  }
  const action = async (action: 'cancel' | 'retry' | 'replay') => {
    if (!run.value) return
    const result = await applyWorkflowRunAction(run.value.id, action)
    if (result.id !== run.value.id) await router.push(`/scheduler/execution/${result.id}/detail`)
    else await load()
  }
  let panelRequest = 0
  const selectAttempt = async (node: WorkflowRunNode) => {
    selectedNode.value = node
    nodeVisible.value = true
    panels.value = []
    panelError.value = ''
    const request = ++panelRequest
    try {
      const candidates = catalog.value.flatMap((plugin) =>
        plugin.runPanels
          .filter((panel) => panel.nodeTypes.includes(node.nodeType))
          .map((panel) => ({ plugin, panel }))
      )
      const loaded = await Promise.all(
        candidates.map(async ({ plugin, panel }) => ({
          id: `${plugin.id}/${panel.panelKey}`,
          title: panel.title,
          component: await loadPluginComponent(plugin.id, 'runPanels', panel.panelKey)
        }))
      )
      if (request === panelRequest) panels.value = loaded
    } catch (cause: any) {
      if (request === panelRequest) panelError.value = cause.message
    }
  }
  const selectNode = (id: string, kind: 'node' | 'edge') => {
    if (kind !== 'node') return
    const node = run.value?.runNodes.filter((node) => node.nodeInstanceId === id).at(-1)
    if (node) void selectAttempt(node)
  }
  const download = async (artifact: WorkflowArtifact) => {
    const blob = await downloadWorkflowArtifact(artifact.downloadUrl)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = artifact.sha256
    link.click()
    URL.revokeObjectURL(url)
  }
  watch(
    () => route.params.executionId,
    () => {
      run.value = undefined
      revision.value = undefined
      nodeVisible.value = false
      void load()
    },
    { immediate: true }
  )
  onBeforeUnmount(() => {
    requestID++
    panelRequest++
    clearTimeout(timer)
  })
</script>
<style scoped>
  .run-page {
    padding: 24px;
    color: var(--el-text-color-primary);
  }
  .run-header {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 16px;
    align-items: center;
    margin-bottom: 20px;
  }
  h1 {
    margin: 8px 0;
    font-size: 26px;
    font-weight: 600;
  }
  p {
    color: var(--el-text-color-secondary);
  }
  pre {
    padding: 16px;
    overflow: auto;
    background: var(--el-fill-color-lighter);
    font-size: 12px;
  }
  @media (max-width: 760px) {
    .run-page {
      padding: 12px;
    }
  }
</style>
