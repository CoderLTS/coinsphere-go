<template>
  <main v-loading="loading" class="workflow-page">
    <header class="workflow-header">
      <div
        ><ElButton link @click="router.push('/scheduler/definition')">返回工作流</ElButton
        ><h1>{{ metadata.name || '新建工作流' }}</h1
        ><p v-if="workflow"
          >草稿 #{{ workflow.draftRevisionId }} · 已发布
          {{ workflow.publishedRevisionId ? '#' + workflow.publishedRevisionId : '暂无' }}</p
        ></div
      >
      <ElSpace wrap
        ><ElButton :disabled="!history.length" @click="undo">撤销</ElButton
        ><ElButton :disabled="!future.length" @click="redo">重做</ElButton
        ><ElButton
          v-if="canEdit && hasAuth('workflows.update')"
          :loading="validating"
          @click="validate"
          >检查定义</ElButton
        ><ElButton v-if="canEdit" type="primary" :loading="saving" @click="save">保存草稿</ElButton
        ><ElButton
          v-if="workflow?.permissions.includes('workflows.publish')"
          :disabled="dirty || selectedRevisionID !== workflow.draftRevisionId"
          :loading="publishing"
          @click="publish"
          >发布草稿</ElButton
        ><ElButton
          v-if="workflow?.permissions.includes('workflows.run')"
          :disabled="!workflow.publishedRevisionId"
          @click="openRun"
          >运行</ElButton
        ></ElSpace
      >
    </header>
    <ElAlert v-if="error" :title="error" type="error" :closable="false" />
    <ElForm inline class="workflow-meta"
      ><ElFormItem label="名称"
        ><ElInput v-model="metadata.name" :disabled="!canEdit" maxlength="120" /></ElFormItem
      ><ElFormItem label="说明"
        ><ElInput v-model="metadata.description" :disabled="!canEdit" maxlength="500" /></ElFormItem
      ><ElFormItem v-if="revisions.length" label="查看版本"
        ><ElSelect v-model="selectedRevisionID" @change="loadRevision"
          ><ElOption
            v-for="revision in revisions"
            :key="revision.id"
            :label="`v${revision.revisionNumber}${revision.id === workflow?.draftRevisionId ? ' · 草稿' : ''}${revision.id === workflow?.publishedRevisionId ? ' · 已发布' : ''}`"
            :value="revision.id" /></ElSelect></ElFormItem
    ></ElForm>
    <WorkflowGraphEditor
      :graph="graph"
      :definitions="definitions"
      :read-only="!canEdit"
      :secret-fields="revision?.secretFields"
      :secret-changes="secretChanges"
      :can-manage-secrets="canManageSecrets"
      @update:graph="commit"
      @update:secret-changes="secretChanges = $event"
    />
    <details class="definition-json"
      ><summary>完整定义</summary
      ><ElInput
        v-model="jsonDraft"
        :disabled="!canEdit"
        type="textarea"
        :rows="12"
        aria-label="完整工作流定义"
      /><ElButton @click="applyJSON">应用定义</ElButton></details
    >
    <ElDialog v-model="runVisible" title="运行工作流" width="min(640px, 94vw)"
      ><ElForm label-position="top"
        ><ElFormItem label="入口"
          ><ElSelect v-model="runEntry"
            ><ElOption
              v-for="(_, name) in publishedGraph?.entryPoints || {}"
              :key="name"
              :label="String(name)"
              :value="name" /></ElSelect></ElFormItem
        ><WorkflowSchemaFields
          :schema="runSchema"
          :config="runInput"
          @update="(key, value) => (runInput[key] = value)" /></ElForm
      ><details
        ><summary>完整输入</summary><ElInput v-model="runJSON" type="textarea" :rows="6" /></details
      ><template #footer
        ><ElButton @click="runVisible = false">取消</ElButton
        ><ElButton type="primary" :loading="running" @click="run">加入运行队列</ElButton></template
      ></ElDialog
    >
  </main>
</template>
<script setup lang="ts">
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { onBeforeRouteLeave } from 'vue-router'
  import { useAuth } from '@/hooks/core/useAuth'
  import {
    createWorkflow,
    fetchWorkflow,
    fetchWorkflowRevision,
    fetchWorkflowRevisions,
    fetchWorkflowNodeDefinitions,
    saveWorkflowRevision,
    publishWorkflow,
    validateWorkflowGraph,
    createWorkflowRun,
    type WorkflowGraph,
    type WorkflowDetail,
    type WorkflowRevision,
    type WorkflowNodeDefinition,
    type WorkflowSecretChange
  } from '@/api/workflows'
  import WorkflowGraphEditor from '@/components/workflow/WorkflowGraphEditor.vue'
  import WorkflowSchemaFields from '@/components/workflow/WorkflowSchemaFields.vue'
  import { cloneGraph } from '@/components/workflow/graph'
  const router = useRouter(),
    route = useRoute(),
    { hasAuth } = useAuth()
  const workflow = ref<WorkflowDetail>(),
    revision = ref<WorkflowRevision>(),
    revisions = ref<WorkflowRevision[]>([]),
    definitions = ref<WorkflowNodeDefinition[]>([])
  const graph = ref<WorkflowGraph>({
    schemaVersion: 3,
    entryPoints: { main: 'start' },
    nodes: [
      {
        nodeInstanceId: 'start',
        nodeType: 'core.manual',
        nodeVersion: '1.0.0',
        config: {},
        position: { x: 100, y: 160 }
      },
      {
        nodeInstanceId: 'end',
        nodeType: 'core.end',
        nodeVersion: '1.0.0',
        config: {},
        position: { x: 420, y: 160 }
      }
    ],
    edges: [
      {
        edgeId: 'start-end',
        sourceNodeInstanceId: 'start',
        sourcePort: 'out',
        targetNodeInstanceId: 'end',
        targetPort: 'in'
      }
    ]
  })
  const metadata = reactive({ name: '', description: '' }),
    secretChanges = ref<WorkflowSecretChange[]>([]),
    selectedRevisionID = ref(0),
    clean = ref('')
  const loading = ref(false),
    saving = ref(false),
    publishing = ref(false),
    validating = ref(false),
    running = ref(false),
    error = ref(''),
    jsonDraft = ref('')
  const history = ref<WorkflowGraph[]>([]),
    future = ref<WorkflowGraph[]>([])
  const snapshot = () => JSON.stringify({ graph: graph.value, metadata })
  const dirty = computed(() => snapshot() !== clean.value || secretChanges.value.length > 0)
  watch(
    graph,
    () => {
      jsonDraft.value = JSON.stringify(graph.value, null, 2)
    },
    { deep: true, immediate: true }
  )
  const canEdit = computed(() =>
    workflow.value
      ? workflow.value.permissions.includes('workflows.update')
      : hasAuth('workflows.create')
  )
  const canManageSecrets = computed(() =>
    workflow.value
      ? workflow.value.permissions.includes('workflows.secrets.manage')
      : hasAuth('workflows.secrets.manage')
  )
  const commit = (next: WorkflowGraph) => {
    if (!canEdit.value) return
    history.value.push(cloneGraph(graph.value))
    if (history.value.length > 100) history.value.shift()
    future.value = []
    graph.value = next
  }
  const undo = () => {
    const previous = history.value.pop()
    if (previous) {
      future.value.push(cloneGraph(graph.value))
      graph.value = previous
    }
  }
  const redo = () => {
    const next = future.value.pop()
    if (next) {
      history.value.push(cloneGraph(graph.value))
      graph.value = next
    }
  }
  const applyJSON = () => {
    try {
      const next = JSON.parse(jsonDraft.value)
      if (
        next.schemaVersion !== 3 ||
        !Array.isArray(next.nodes) ||
        !Array.isArray(next.edges) ||
        !next.entryPoints
      )
        throw new Error('定义需要节点、连线与入口')
      commit(next)
      error.value = ''
    } catch (cause: any) {
      error.value = cause.message
    }
  }
  const loadRevision = async (id: number) => {
    if (!workflow.value) return
    if (dirty.value) {
      try {
        await ElMessageBox.confirm('切换版本将放弃未保存的改动', '切换版本')
      } catch {
        selectedRevisionID.value = revision.value?.id || 0
        return
      }
    }
    revision.value = await fetchWorkflowRevision(workflow.value.id, id)
    graph.value = cloneGraph(revision.value.graph)
    secretChanges.value = []
    history.value = []
    future.value = []
    clean.value = snapshot()
  }
  const refresh = async (id: number) => {
    const [item, list] = await Promise.all([fetchWorkflow(id), fetchWorkflowRevisions(id)])
    workflow.value = item
    revisions.value = list.items
    Object.assign(metadata, { name: item.name, description: item.description })
    selectedRevisionID.value = item.draftRevisionId
    clean.value = snapshot()
    await loadRevision(item.draftRevisionId)
  }
  const validate = async () => {
    validating.value = true
    error.value = ''
    try {
      const result = await validateWorkflowGraph(graph.value)
      if (!result.valid) error.value = result.issues.map((issue) => issue.message).join('；')
      else ElMessage.success('定义检查通过')
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      validating.value = false
    }
  }
  const save = async () => {
    if (!canEdit.value) return
    saving.value = true
    error.value = ''
    try {
      if (!workflow.value) {
        const item = await createWorkflow({
          ...metadata,
          templateKey: 'blank',
          graph: graph.value,
          secretChanges: secretChanges.value
        })
        clean.value = snapshot()
        secretChanges.value = []
        await router.replace(`/scheduler/workflow/${item.id}/edit`)
        await refresh(item.id)
      } else {
        const saved = await saveWorkflowRevision(workflow.value.id, {
          expectedDraftRevisionId: workflow.value.draftRevisionId,
          metadata,
          graph: graph.value,
          secretChanges: secretChanges.value
        })
        revision.value = saved
        workflow.value.draftRevisionId = saved.id
        selectedRevisionID.value = saved.id
        secretChanges.value = []
        revisions.value = (await fetchWorkflowRevisions(workflow.value.id)).items
        clean.value = snapshot()
      }
      ElMessage.success('草稿已保存')
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      saving.value = false
    }
  }
  const publish = async () => {
    if (
      !workflow.value ||
      selectedRevisionID.value !== workflow.value.draftRevisionId ||
      dirty.value
    )
      return
    publishing.value = true
    try {
      workflow.value = await publishWorkflow(
        workflow.value.id,
        workflow.value.draftRevisionId,
        workflow.value.publishedRevisionId
      )
      ElMessage.success('草稿已发布')
    } finally {
      publishing.value = false
    }
  }
  const runVisible = ref(false),
    runEntry = ref('main'),
    runInput = reactive<Record<string, unknown>>({}),
    runJSON = ref('{}'),
    publishedGraph = ref<WorkflowGraph>()
  const runSchema = computed(
    () =>
      definitions.value.find(
        (item) =>
          item.type ===
          publishedGraph.value?.nodes.find(
            (node) => node.nodeInstanceId === publishedGraph.value?.entryPoints[runEntry.value]
          )?.nodeType
      )?.inputSchema || {}
  )
  watch(
    runInput,
    () => {
      runJSON.value = JSON.stringify(runInput, null, 2)
    },
    { deep: true }
  )
  const openRun = async () => {
    if (!workflow.value) return
    publishedGraph.value = (
      await fetchWorkflowRevision(workflow.value.id, workflow.value.publishedRevisionId)
    ).graph
    runEntry.value = Object.keys(publishedGraph.value.entryPoints)[0] || 'main'
    runVisible.value = true
  }
  const run = async () => {
    if (!workflow.value) return
    running.value = true
    try {
      const input = JSON.parse(runJSON.value)
      const current = await createWorkflowRun(workflow.value.id, {
        revisionId: workflow.value.publishedRevisionId,
        entryPoint: runEntry.value,
        input
      })
      runVisible.value = false
      await router.push(`/scheduler/execution/${current.id}/detail`)
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      running.value = false
    }
  }
  onBeforeRouteLeave(async () => {
    if (!dirty.value) return true
    try {
      await ElMessageBox.confirm('离开将放弃未保存的改动', '离开编辑器')
      return true
    } catch {
      return false
    }
  })
  onMounted(async () => {
    loading.value = true
    try {
      definitions.value = (await fetchWorkflowNodeDefinitions()).items
      const id = Number(route.params.definitionId)
      if (id) await refresh(id)
      else clean.value = snapshot()
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      loading.value = false
    }
  })
</script>
<style scoped>
  .workflow-page {
    padding: 24px;
    color: var(--el-text-color-primary);
  }
  .workflow-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 20px;
    padding-bottom: 20px;
    border-bottom: 1px solid var(--el-border-color);
  }
  h1 {
    margin: 8px 0;
    font-size: 24px;
    font-weight: 600;
  }
  p {
    margin: 0;
    color: var(--el-text-color-secondary);
  }
  .workflow-meta {
    margin-top: 20px;
  }
  .definition-json {
    margin-top: 18px;
  }
  summary {
    cursor: pointer;
    margin-bottom: 12px;
  }
  @media (max-width: 760px) {
    .workflow-page {
      padding: 12px;
    }
  }
</style>
