<template>
  <div v-loading="loading" class="workflow-editor">
    <header class="workflow-editor__toolbar">
      <ElButton @click="router.push('/scheduler/definition')">返回</ElButton>
      <ElInput v-model="name" aria-label="工作流名称" class="workflow-editor__name" placeholder="工作流名称" />
      <ElTag :type="workflow?.status === 'active' ? 'success' : 'info'">{{ workflow?.status === 'active' ? '已激活' : '未激活' }}</ElTag>
      <span class="workflow-editor__revision">{{ revision ? `修订 ${revision.revisionNumber}` : '未保存' }}{{ dirty ? ' · 有更改' : '' }}</span>
      <ElButton @click="connectionsVisible = true">连接</ElButton>
      <ElButton @click="validate">检查</ElButton>
      <ElButton type="primary" :loading="saving" @click="save">保存</ElButton>
      <ElButton :disabled="!workflow || dirty" @click="toggleLifecycle">{{ workflow?.status === 'active' ? '停用' : '激活' }}</ElButton>
      <ElButton :disabled="!workflow || dirty" @click="openRun()">运行</ElButton>
    </header>
    <ElAlert v-if="issues.length" type="error" :closable="false" :title="issues.join('；')" />
    <div class="workflow-editor__body">
      <aside class="workflow-editor__library">
        <ElInput v-model="search" placeholder="搜索节点" clearable aria-label="搜索节点" />
        <ElSelect v-model="pluginFilter" aria-label="筛选插件"><ElOption value="" label="全部插件" /><ElOption v-for="plugin in plugins" :key="plugin" :value="plugin" :label="plugin" /></ElSelect>
        <ElCheckbox v-model="showAdvanced">显示高级节点</ElCheckbox>
        <section v-for="group in palette" :key="group.name">
          <h3>{{ group.name }}</h3>
          <button v-for="definition in group.nodes" :key="definition.type" class="workflow-editor__material" :style="{ borderLeftColor: definition.color }" :title="definition.description" @click="addNode(definition)">
            <strong>{{ definition.title }}</strong><small>{{ definition.description }}</small>
          </button>
        </section>
      </aside>
      <main class="workflow-editor__canvas">
        <div ref="canvasHost" class="workflow-editor__graph" />
        <div class="workflow-editor__zoom"><ElButton @click="canvas?.zoom(-0.1)">缩小</ElButton><ElButton @click="canvas?.zoomToFit({ padding: 60, maxScale: 1 })">适应画布</ElButton><ElButton @click="canvas?.zoom(0.1)">放大</ElButton></div>
        <div v-if="!graph.nodes.length" class="workflow-editor__empty">点击左侧节点开始编排；每个触发节点都是独立入口。</div>
      </main>
      <aside class="workflow-editor__inspector">
        <template v-if="selected && definition">
          <div class="workflow-editor__panel-title"><h3>{{ definition.title }}</h3><ElButton text type="danger" @click="removeSelection">删除节点</ElButton></div>
          <p class="workflow-editor__hint">{{ definition.description }}</p>
          <ElForm label-position="top">
            <ElFormItem label="节点名称"><ElInput v-model="selected.label" @input="syncLabel" /></ElFormItem>
            <h4>基本配置</h4>
            <WorkflowConditionEditor v-if="selected.nodeType === 'core.condition'" :config="selected.config" :fields="conditionFields" @update="updateConfig" />
            <template v-else-if="selected.nodeType === 'core.schedule'">
              <ElFormItem label="调度方式"><ElRadioGroup :model-value="selected.config.cronExpression !== undefined ? 'cron' : 'interval'" @change="changeSchedule"><ElRadioButton value="interval">固定间隔</ElRadioButton><ElRadioButton value="cron">Cron</ElRadioButton></ElRadioGroup></ElFormItem>
              <WorkflowSchemaFields :schema="basicSchema" :config="selected.config" :keys="selected.config.cronExpression !== undefined ? ['cronExpression', 'timeZone'] : ['everySeconds']" @update="updateConfig" />
            </template>
            <component :is="pluginEditor || WorkflowSchemaFields" v-else :schema="basicSchema" :ui-schema="definition.uiSchema" :config="selected.config" :graph="graph" :node-id="selected.nodeInstanceId" @update="updateConfig" />
            <WorkflowProfileBindings v-if="definition.profileSlots?.length" :node="selected" :definition="definition" @update="updateProfileBindings" />
            <h4>输入数据</h4>
            <p v-if="definition.kind === 'trigger'" class="workflow-editor__hint">此节点接收触发事件；可在下游选择它输出的字段。</p>
            <template v-else>
              <ElFormItem v-for="(property, key) in inputProperties" :key="key" :label="String(property.title || key)" :required="inputRequired.includes(String(key))">
                <WorkflowBindingEditor :binding="selected.inputBindings?.[key]" :schema="property" :graph="graph" :definitions="definitions" :node-id="selected.nodeInstanceId" @update="binding => updateBinding(String(key), binding)" />
              </ElFormItem>
              <div v-if="definition.inputSchema.additionalProperties !== false" class="workflow-editor__add-input"><ElInput v-model="newInput" placeholder="添加输入字段" /><ElButton @click="addInput">添加</ElButton></div>
            </template>
            <template v-if="definition.connectionType">
              <h4>连接</h4>
              <ElSelect v-model="selected.connectionId" placeholder="选择可复用连接" clearable><ElOption v-for="connection in matchingConnections" :key="connection.id" :value="connection.id" :label="`${connection.name}${connection.enabled ? '' : '（已停用）'}`" :disabled="!connection.enabled" /></ElSelect>
              <ElButton text @click="connectionsVisible = true">管理连接</ElButton>
            </template>
            <template v-for="field in localSecrets" :key="field.name">
              <ElFormItem :label="field.title"><ElInput :model-value="secretValue(field.name)" type="password" show-password autocomplete="new-password" :placeholder="revision?.secretFields?.[selected.nodeInstanceId]?.[field.name] ? '已配置，留空保留' : '请输入凭据'" @update:model-value="value => updateSecret(field.name, value)" /></ElFormItem>
            </template>
            <details><summary>高级设置</summary>
              <WorkflowSchemaFields :schema="advancedSchema" :ui-schema="definition.uiSchema" :config="selected.config" @update="updateConfig" />
              <p class="workflow-editor__hint">{{ selected.nodeType }} · {{ selected.nodeVersion }}<br />{{ selected.nodeInstanceId }}</p>
            </details>
          </ElForm>
        </template>
        <template v-else-if="selectedEdge">
          <h3>连线</h3><ElForm label-position="top"><ElFormItem label="显示名称"><ElInput v-model="selectedEdge.label" @input="syncEdgeLabel" /></ElFormItem><ElFormItem label="出口">{{ selectedEdge.sourcePort }}</ElFormItem></ElForm><ElButton type="danger" plain @click="removeSelection">删除连线</ElButton>
        </template>
        <template v-else>
          <h3>工作流</h3><ElInput v-model="description" type="textarea" placeholder="描述此工作流" />
          <p class="workflow-editor__hint">连接端口决定执行路径，多个入口各自创建独立运行。选择节点编辑参数和输入。</p>
          <h4>触发入口</h4>
          <div v-for="trigger in triggerNodes" :key="trigger.nodeInstanceId" class="workflow-editor__trigger">
            <ElButton text @click="selectNode(trigger.nodeInstanceId)">{{ trigger.label || definitionFor(trigger)?.title }}</ElButton>
            <small>{{ triggerStatus(trigger.nodeInstanceId) }}</small>
          </div>
        </template>
      </aside>
    </div>
    <WorkflowConnections v-model="connectionsVisible" @changed="loadConnections" />
    <ElDialog v-model="runVisible" title="运行工作流" width="min(600px, 95vw)">
      <ElAlert title="使用已保存的修订执行" type="info" :closable="false" />
      <ElForm label-position="top">
        <ElFormItem label="修订"><ElSelect v-model="runRevisionId" @change="loadRunRevision"><ElOption v-for="item in revisions" :key="item.id" :value="item.id" :label="`修订 ${item.revisionNumber}`" /></ElSelect></ElFormItem>
        <ElFormItem label="触发入口"><ElSelect v-model="runTriggerId"><ElOption v-for="item in runTriggers" :key="item.nodeInstanceId" :value="item.nodeInstanceId" :label="item.label || definitionFor(item)?.title || item.nodeInstanceId" /></ElSelect></ElFormItem>
        <ElAlert v-if="runIsOperation" title="该入口是逐帧操作，会使用入口绑定的行情与回放 Profile。" type="info" :closable="false" />
        <ElFormItem v-if="runIsOperation" label="策略结果节点">
          <ElSelect v-model="runResultNodeId" placeholder="选择一个结果节点">
            <ElOption v-for="item in runResultNodes" :key="item.nodeInstanceId" :value="item.nodeInstanceId" :label="item.label || definitionFor(item)?.title || item.nodeInstanceId" />
          </ElSelect>
        </ElFormItem>
        <WorkflowSchemaFields :schema="{ type: 'object', properties: { data: { type: 'object', title: '触发输入' } } }" :config="runInput" @update="(key, value) => runInput[key] = value" />
      </ElForm>
      <template #footer><ElButton @click="runVisible = false">取消</ElButton><ElButton type="primary" :loading="running" :disabled="!runTriggerId || (runIsOperation && !runResultNodeId)" @click="run">开始运行</ElButton></template>
    </ElDialog>
  </div>
</template>
<script setup lang="ts">
import { Graph } from '@antv/x6'
import type { Component } from 'vue'
import { ElMessage } from 'element-plus'
import { onBeforeRouteLeave } from 'vue-router'
import { useElementSize } from '@vueuse/core'
import * as api from '@/api/workflows'
import { fetchConnections, type Connection } from '@/api/connections'
import { loadPluginNodeEditor } from '@/plugins'
import { canvasNode, canvasEdges, schemaDefaults, outputFields } from './canvas'
import WorkflowSchemaFields from './components/WorkflowSchemaFields.vue'
import WorkflowBindingEditor from './components/WorkflowBindingEditor.vue'
import WorkflowConditionEditor from './components/WorkflowConditionEditor.vue'
import WorkflowConnections from './components/WorkflowConnections.vue'
import WorkflowProfileBindings from './components/WorkflowProfileBindings.vue'
const router = useRouter(), route = useRoute()
const loading = ref(true), saving = ref(false), running = ref(false)
const definitions = ref<api.WorkflowNodeDefinition[]>([]), connections = ref<Connection[]>([])
const workflow = ref<api.WorkflowDetail>(), revision = ref<api.WorkflowRevision>()
const graph = ref<api.WorkflowGraph>({ schemaVersion: 3, profileRefs: [], nodes: [], edges: [] })
const name = ref('新工作流'), description = ref(''), baseline = ref(''), issues = ref<string[]>([])
const canvasHost = ref<HTMLDivElement>(), selectedId = ref(''), selectedEdgeId = ref('')
const canvas = shallowRef<Graph>(), pluginEditor = shallowRef<Component>()
const search = ref(''), pluginFilter = ref(''), newInput = ref(''), connectionsVisible = ref(false), showAdvanced = ref(false)
const secretChanges = ref<api.WorkflowSecretChange[]>([])
const snapshot = () => JSON.stringify({ graph: graph.value, name: name.value, description: description.value })
const dirty = computed(() => snapshot() !== baseline.value || secretChanges.value.length > 0)
const definitionFor = (node: api.WorkflowGraphNode) => definitions.value.find(d => d.type === node.nodeType && d.version === node.nodeVersion)
const definition = computed(() => selected.value && definitionFor(selected.value))
const selected = computed(() => graph.value.nodes.find(n => n.nodeInstanceId === selectedId.value))
const selectedEdge = computed(() => graph.value.edges.find(e => e.edgeId === selectedEdgeId.value))
const triggerNodes = computed(() => graph.value.nodes.filter(n => { const desc = definitionFor(n); return desc?.kind === 'trigger' || Boolean(desc?.capabilities.manualTrigger) }))
const plugins = computed(() => [...new Set(definitions.value.map(d => d.type.startsWith('core.') ? 'core' : d.type.split('.').slice(0, -1).join('.')))])
const palette = computed(() => {
  const nodes = definitions.value.filter(d => d.available && (showAdvanced.value || search.value || d.visibility !== 'advanced') && (!pluginFilter.value || d.type.startsWith(`${pluginFilter.value}.`)) && `${d.title} ${d.description}`.toLowerCase().includes(search.value.toLowerCase()))
  const groups = [
    { name: '入口', roles: ['entry', 'trigger'] },
    { name: '数据与资源', roles: ['data'] },
    { name: '计算', roles: ['compute'] },
    { name: '判断与控制', roles: ['control'] },
    { name: '副作用', roles: ['effect'] },
    { name: '结束', roles: ['end'] },
  ]
  return groups.map(group => ({ name: group.name, nodes: nodes.filter(d => group.roles.includes(d.role || (d.kind === 'trigger' ? 'trigger' : 'compute'))) })).filter(g => g.nodes.length)
})
const matchingConnections = computed(() => connections.value.filter(c => c.type === definition.value?.connectionType))
const localSecrets = computed(() => definition.value?.secretFields.filter(f => !definition.value?.connectionFields?.includes(f.name)) || [])
function configSchema(advanced: boolean) {
  const desc = definition.value
  const properties = Object.fromEntries(Object.entries(desc?.configSchema.properties || {}).filter(([key, value]) => {
    const field = value as Record<string, any>
    return !desc?.connectionFields?.includes(key) && !field['x-coinsphere-secret'] && Boolean(desc?.configGroups?.some(group => group.advanced && group.fields.includes(key)) || field['x-group'] === 'advanced' || desc?.uiSchema?.[key]?.['ui:group'] === 'advanced' || key === 'timeoutSeconds') === advanced
  }))
  return { ...desc?.configSchema, properties }
}
const basicSchema = computed(() => configSchema(false)), advancedSchema = computed(() => configSchema(true))
const inputProperties = computed<Record<string, any>>(() => ({ ...definition.value?.inputSchema.properties, ...Object.fromEntries(Object.keys(selected.value?.inputBindings || {}).filter(k => !definition.value?.inputSchema.properties?.[k]).map(k => [k, { title: k }])) }))
const inputRequired = computed(() => (definition.value?.inputSchema.required || []) as string[])
const conditionFields = computed(() => Object.keys(selected.value?.inputBindings || {}))
watch(() => selected.value?.nodeType, async type => { pluginEditor.value = undefined; if (!type) return; const editor = await loadPluginNodeEditor(type); if (selected.value?.nodeType === type) pluginEditor.value = editor })
function updateConfig(key: string, value: any) { if (selected.value) selected.value.config[key] = value }
function changeSchedule(mode: string | number | boolean | undefined) { if (selected.value) selected.value.config = mode === 'cron' ? { cronExpression: '0 * * * *', timeZone: 'UTC' } : { everySeconds: 3600 } }
function updateBinding(key: string, binding?: api.WorkflowInputBinding) { if (!selected.value) return; selected.value.inputBindings ||= {}; if (binding) selected.value.inputBindings[key] = binding; else delete selected.value.inputBindings[key] }
function updateProfileBindings(bindings: Record<string, api.ProfileRef>) {
  if (!selected.value) return
  selected.value.profileBindings = Object.keys(bindings).length ? bindings : undefined
  const refs = new Map((graph.value.profileRefs || []).map(ref => [`${ref.pluginId}:${ref.profileId}:${ref.version}:${ref.type}`, ref]))
  Object.values(bindings).forEach(ref => refs.set(`${ref.pluginId}:${ref.profileId}:${ref.version}:${ref.type}`, ref))
  const used = new Set(graph.value.nodes.flatMap(node => Object.values(node.profileBindings || {}).map(ref => `${ref.pluginId}:${ref.profileId}:${ref.version}:${ref.type}`)))
  graph.value.profileRefs = [...refs.values()].filter(ref => used.has(`${ref.pluginId}:${ref.profileId}:${ref.version}:${ref.type}`))
}
function addInput() { const key = newInput.value.trim(); if (key && !selected.value?.inputBindings?.[key]) updateBinding(key, { kind: 'literal', value: '' }); newInput.value = '' }
const secretValue = (field: string) => secretChanges.value.find(c => c.nodeInstanceId === selectedId.value && c.field === field)?.value || ''
function updateSecret(field: string, value: string) { secretChanges.value = secretChanges.value.filter(c => c.nodeInstanceId !== selectedId.value || c.field !== field); if (value) secretChanges.value.push({ nodeInstanceId: selectedId.value, field, value }) }
function triggerStatus(id: string) { const runtime = workflow.value?.triggers.find(t => t.nodeInstanceId === id); return runtime ? `${runtime.status}${runtime.errorCategory ? ` · ${runtime.errorCategory}` : ''}${runtime.nextScheduledAt ? ` · ${new Date(runtime.nextScheduledAt).toLocaleString()}` : ''}` : '保存后注册' }
function selectNode(id: string) { selectedId.value = id; selectedEdgeId.value = '' }
function syncLabel() { if (selected.value) canvas.value?.getCellById(selectedId.value)?.attr('label/text', selected.value.label || definition.value?.title) }
function syncEdgeLabel() { const edge = canvas.value?.getCellById(selectedEdgeId.value); if (edge?.isEdge()) edge.setLabels(selectedEdge.value?.label ? [selectedEdge.value.label] : []) }
function render() { canvas.value?.fromJSON({ nodes: graph.value.nodes.map(n => canvasNode(n, definitionFor(n))), edges: canvasEdges(graph.value) }); canvas.value?.zoomToFit({ padding: 60, maxScale: 1 }) }
function addNode(desc: api.WorkflowNodeDefinition) {
  const node: api.WorkflowGraphNode = { nodeInstanceId: crypto.randomUUID(), nodeType: desc.type, nodeVersion: desc.version, label: desc.title, config: schemaDefaults({ properties: Object.fromEntries(Object.entries(desc.configSchema.properties || {}).filter(([k]) => !desc.connectionFields?.includes(k))) }), position: { x: 100 + graph.value.nodes.length % 4 * 260, y: 100 + Math.floor(graph.value.nodes.length / 4) * 130 }, inputBindings: {} }
  graph.value.nodes.push(node); canvas.value?.addNode(canvasNode(node, desc)); selectNode(node.nodeInstanceId)
}
function removeSelection() {
  const id = selectedId.value || selectedEdgeId.value
  canvas.value?.removeCell(id)
  graph.value.nodes = graph.value.nodes.filter(n => n.nodeInstanceId !== id)
  graph.value.edges = graph.value.edges.filter(e => e.edgeId !== id && e.sourceNodeInstanceId !== id && e.targetNodeInstanceId !== id)
  secretChanges.value = secretChanges.value.filter(c => c.nodeInstanceId !== id)
  const used = new Set(graph.value.nodes.flatMap(node => Object.values(node.profileBindings || {}).map(ref => `${ref.pluginId}:${ref.profileId}:${ref.version}:${ref.type}`)))
  graph.value.profileRefs = (graph.value.profileRefs || []).filter(ref => used.has(`${ref.pluginId}:${ref.profileId}:${ref.version}:${ref.type}`))
  selectedId.value = ''; selectedEdgeId.value = ''
}
function autoBind(nodeId: string) {
  const node = graph.value.nodes.find(n => n.nodeInstanceId === nodeId); if (!node) return
  const fields = outputFields(graph.value, definitions.value, nodeId)
  for (const [key, raw] of Object.entries(definitionFor(node)?.inputSchema.properties || {})) {
    if (node.inputBindings?.[key]) continue
    const schema = raw as Record<string, any>
    const candidates = fields.filter(f => f.path.at(-1) === key && f.schema.type === schema.type)
    if (candidates.length === 1) { node.inputBindings ||= {}; node.inputBindings[key] = { kind: 'node', nodeInstanceId: candidates[0].nodeId, fieldPath: candidates[0].path } }
  }
}
async function loadConnections() { connections.value = (await fetchConnections()).items }
async function validate() { const result = await api.validateWorkflowGraph(graph.value); issues.value = result.issues.map(i => i.message); if (result.valid) ElMessage.success('图校验通过'); return result.valid }
async function save() {
  saving.value = true
  try {
    if (!await validate()) return
    if (!workflow.value) { workflow.value = await api.createWorkflow({ name: name.value, description: description.value, templateKey: 'blank', groupId: Number(route.query.groupId) || null }); await router.replace(`/scheduler/workflow/${workflow.value.id}/edit`) }
    const current = workflow.value
    if (current.name !== name.value || current.description !== description.value) await api.updateWorkflow(current.id, { name: name.value, description: description.value })
    const previous = await api.fetchWorkflowRevision(current.id, current.activeRevisionId)
    const resetStateNodeInstanceIds = current.stateNodeInstanceIds.filter(id => { const before = previous.graph.nodes.find(n => n.nodeInstanceId === id); const after = graph.value.nodes.find(n => n.nodeInstanceId === id); return !after || !before || before.nodeType !== after.nodeType || before.nodeVersion !== after.nodeVersion })
    revision.value = await api.saveWorkflowRevision(current.id, { expectedActiveRevisionId: current.activeRevisionId, graph: graph.value, secretChanges: secretChanges.value, resetStateNodeInstanceIds })
    workflow.value = await api.fetchWorkflow(current.id); secretChanges.value = []; baseline.value = snapshot(); ElMessage.success('修订已保存')
  } finally { saving.value = false }
}
async function toggleLifecycle() { if (workflow.value) workflow.value = await api.applyWorkflowLifecycle(workflow.value.id, workflow.value.status === 'active' ? 'deactivate' : 'activate') }
const runVisible = ref(false), revisions = ref<api.WorkflowRevision[]>([])
const runRevisionId = ref(0), runTriggerId = ref(''), runResultNodeId = ref(''), runInput = ref<Record<string, any>>({ data: {} }), runGraph = ref<api.WorkflowGraph>({ schemaVersion: 3, profileRefs: [], nodes: [], edges: [] })
const runTriggers = computed(() => runGraph.value.nodes.filter(n => {
  const desc = definitionFor(n)
  return n.nodeType === 'core.manual' || Boolean(desc?.capabilities.manualTrigger)
}))
const runTrigger = computed(() => runGraph.value.nodes.find(node => node.nodeInstanceId === runTriggerId.value))
const runTriggerDefinition = computed(() => runTrigger.value ? definitionFor(runTrigger.value) : undefined)
const runIsOperation = computed(() => Boolean(runTriggerDefinition.value?.capabilities.frameDriver))
const runResultNodes = computed(() => {
  const framePorts = runTriggerDefinition.value?.frameSourcePorts
  const allowed = framePorts?.length ? new Set(framePorts) : undefined
  const reachable = new Set<string>(), queue = [runTriggerId.value]
  while (queue.length) {
    const current = queue.shift()!
    for (const edge of runGraph.value.edges.filter(item => item.sourceNodeInstanceId === current && (current !== runTriggerId.value || !allowed || allowed.includes(item.sourcePort)))) {
      if (!reachable.has(edge.targetNodeInstanceId)) { reachable.add(edge.targetNodeInstanceId); queue.push(edge.targetNodeInstanceId) }
    }
  }
  return runGraph.value.nodes.filter(node => reachable.has(node.nodeInstanceId) && Boolean(definitionFor(node)?.capabilities.frameResult))
})
watch([runTriggerId, runResultNodes], () => {
  if (!runResultNodes.value.some(node => node.nodeInstanceId === runResultNodeId.value)) runResultNodeId.value = runResultNodes.value[0]?.nodeInstanceId || ''
})
async function loadRunRevision() { if (!workflow.value) return; runGraph.value = (await api.fetchWorkflowRevision(workflow.value.id, runRevisionId.value)).graph; runTriggerId.value = runTriggers.value[0]?.nodeInstanceId || '' }
async function openRun() { if (!workflow.value) return; revisions.value = (await api.fetchWorkflowRevisions(workflow.value.id)).items; runRevisionId.value = revision.value?.id || workflow.value.activeRevisionId; runInput.value = { data: {} }; runResultNodeId.value = ''; await loadRunRevision(); runVisible.value = true }
async function run() {
  if (!workflow.value) return
  running.value = true
  try {
    const result = await api.createWorkflowRun(workflow.value.id, {
      revisionId: runRevisionId.value,
      triggerNodeId: runTriggerId.value,
      operationType: runIsOperation.value ? runTrigger.value?.nodeType : undefined,
      resultNodeIds: runIsOperation.value ? [runResultNodeId.value] : undefined,
      input: runInput.value.data ?? runInput.value
    })
    runVisible.value = false
    await router.push(`/scheduler/execution/${result.id}/detail`)
  } finally { running.value = false }
}
const { width, height } = useElementSize(canvasHost)
watch([width, height], ([w, h]) => { if (w && h) canvas.value?.resize(w, h) })
onMounted(async () => {
  try {
    definitions.value = (await api.fetchWorkflowNodeDefinitions()).items
    await loadConnections()
    const id = Number(route.params.definitionId)
    if (id) { workflow.value = await api.fetchWorkflow(id); revision.value = await api.fetchWorkflowRevision(id, Number(route.query.revisionId) || workflow.value.activeRevisionId); graph.value = revision.value.graph; name.value = workflow.value.name; description.value = workflow.value.description }
    else { const manual = definitions.value.find(d => d.type === 'core.manual'); if (manual) addNode(manual) }
    await nextTick()
    if (!canvasHost.value) return
    canvas.value = new Graph({ container: canvasHost.value, grid: true, panning: true, mousewheel: { enabled: true, minScale: 0.25, maxScale: 2 }, connecting: { allowBlank: false, allowLoop: false, allowNode: false, allowEdge: false, snap: true, validateConnection({ sourcePort, targetPort }) { return Boolean(sourcePort?.startsWith('out:') && targetPort?.startsWith('in:')) } } })
    canvas.value.on('node:click', ({ node }) => selectNode(node.id))
    canvas.value.on('edge:click', ({ edge }) => { selectedId.value = ''; selectedEdgeId.value = edge.id })
    canvas.value.on('blank:click', () => { selectedId.value = ''; selectedEdgeId.value = '' })
    canvas.value.on('node:change:position', ({ node }) => { const item = graph.value.nodes.find(n => n.nodeInstanceId === node.id); if (item) item.position = node.position() })
    canvas.value.on('edge:connected', ({ edge }) => { const item = { edgeId: edge.id, sourceNodeInstanceId: String(edge.getSourceCellId()), sourcePort: String(edge.getSourcePortId()).slice(4), targetNodeInstanceId: String(edge.getTargetCellId()), targetPort: String(edge.getTargetPortId()).slice(3) }; graph.value.edges = [...graph.value.edges.filter(e => e.edgeId !== edge.id), item]; autoBind(item.targetNodeInstanceId) })
    render(); baseline.value = snapshot()
    if (route.query.run === '1' && workflow.value) await openRun()
  } finally { loading.value = false }
})
onBeforeRouteLeave(() => { if (dirty.value && !saving.value) return window.confirm('还有未保存的修改，确定离开？') })
onBeforeUnmount(() => { canvas.value?.dispose(); secretChanges.value = [] })
</script>
<style scoped>
.workflow-editor { height: calc(100vh - 125px); min-height: 600px; display: flex; flex-direction: column; border: 1px solid var(--el-border-color); border-radius: 8px; overflow: hidden; background: var(--el-bg-color); }
.workflow-editor__toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; padding: 12px; border-bottom: 1px solid var(--el-border-color); }
.workflow-editor__toolbar .el-button + .el-button { margin-left: 0; }
.workflow-editor__name { width: 190px; }
.workflow-editor__revision { margin-right: auto; font-size: 12px; color: var(--el-text-color-secondary); }
.workflow-editor__body { display: grid; grid-template-columns: 220px minmax(280px, 1fr) 350px; flex: 1; min-height: 0; }
.workflow-editor__library, .workflow-editor__inspector { padding: 16px; overflow: auto; }
.workflow-editor__library { border-right: 1px solid var(--el-border-color); display: flex; flex-direction: column; gap: 12px; }
.workflow-editor__inspector { border-left: 1px solid var(--el-border-color); }
.workflow-editor h3 { font-size: 14px; margin: 12px 0; }
.workflow-editor h4 { margin: 22px 0 12px; font-size: 13px; }
.workflow-editor__material { display: block; width: 100%; margin: 8px 0; text-align: left; padding: 12px; border: 1px solid var(--el-border-color); border-left: 3px solid; border-radius: 6px; background: var(--el-fill-color-blank); color: var(--el-text-color-primary); cursor: pointer; }
.workflow-editor__material:hover { background: var(--el-fill-color-light); }
.workflow-editor__material small { display: block; margin-top: 5px; color: var(--el-text-color-secondary); line-height: 1.5; }
.workflow-editor__canvas { position: relative; min-height: 0; background: var(--el-fill-color-lighter); }
.workflow-editor__graph { position: absolute; inset: 0; }
.workflow-editor__zoom { position: absolute; bottom: 16px; left: 16px; display: flex; gap: 4px; }
.workflow-editor__empty { position: absolute; left: 20%; top: 40%; max-width: 260px; color: var(--el-text-color-secondary); pointer-events: none; }
.workflow-editor__hint { font-size: 12px; line-height: 1.7; color: var(--el-text-color-secondary); overflow-wrap: anywhere; }
.workflow-editor__panel-title, .workflow-editor__add-input { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.workflow-editor__trigger { display: flex; flex-direction: column; align-items: flex-start; border-bottom: 1px solid var(--el-border-color-lighter); padding: 8px 0; }
.workflow-editor details { margin-top: 24px; }
.workflow-editor summary { cursor: pointer; margin-bottom: 16px; }
@media (max-width: 1100px) { .workflow-editor__body { grid-template-columns: 160px minmax(180px, 1fr) 310px; } }
</style>
