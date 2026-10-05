<template>
  <section class="graph-editor">
    <aside class="graph-library" aria-label="节点库">
      <ElInput v-model="search" placeholder="搜索节点" aria-label="搜索节点" clearable />
      <button
        v-for="definition in materials"
        :key="definition.type"
        class="material"
        @click="commit(addNode(graph, definition))"
      >
        <strong>{{ definition.title }}</strong
        ><small>{{ definition.description }}</small>
      </button>
      <ElDivider>入口</ElDivider>
      <div v-for="(id, name) in graph.entryPoints" :key="name" class="entry-row">
        <span>{{ name }}</span
        ><ElSelect :model-value="id" @update:model-value="setEntry(String(name), $event)"
          ><ElOption
            v-for="node in graph.nodes"
            :key="node.nodeInstanceId"
            :value="node.nodeInstanceId"
            :label="node.nodeInstanceId"
        /></ElSelect>
        <ElButton link type="danger" @click="removeEntry(String(name))">删除</ElButton>
      </div>
      <ElInput v-model="entryName" placeholder="新入口名称" aria-label="新入口名称" /><ElButton
        @click="addEntry"
        >添加入口</ElButton
      >
      <ElDivider>节点与连线</ElDivider>
      <ElButton
        v-for="node in graph.nodes"
        :key="node.nodeInstanceId"
        link
        @click="select(node.nodeInstanceId, 'node')"
        >{{ node.nodeInstanceId }}</ElButton
      >
      <ElButton
        v-for="edge in graph.edges"
        :key="edge.edgeId"
        link
        @click="select(edge.edgeId, 'edge')"
        >{{ edge.sourceNodeInstanceId }} → {{ edge.targetNodeInstanceId }}</ElButton
      >
    </aside>
    <WorkflowGraphCanvas
      :graph="graph"
      :definitions="definitions"
      :readonly="readOnly"
      @update:graph="commit"
      @select="select"
    />
    <ElDrawer
      v-model="drawer"
      :title="selectedNode ? definition?.title || selectedNode.nodeType : '连线条件'"
      size="min(600px, 100vw)"
    >
      <template v-if="selectedNode">
        <p>{{ selectedNode.nodeInstanceId }} · {{ selectedNode.nodeVersion }}</p>
        <ElTabs v-model="tab">
          <ElTabPane label="配置" name="config">
            <ElAlert v-if="componentError" :title="componentError" type="error" :closable="false" />
            <ElForm label-position="top"
              ><component
                :is="nodeEditor || WorkflowSchemaFields"
                :schema="configSchema"
                :ui-schema="definition?.uiSchema"
                :config="selectedNode.config"
                @update="setConfig"
            /></ElForm>
            <ElButton v-if="selectedNode.nodeType === 'core.loop'" @click="loopVisible = true"
              >编辑循环子图</ElButton
            >
          </ElTabPane>
          <ElTabPane label="输入绑定" name="bindings">
            <ElForm label-position="top">
              <ElFormItem
                v-for="field in inputFields"
                :key="field"
                :label="
                  String((definition?.inputSchema as any)?.properties?.[field]?.title || field)
                "
              >
                <div class="binding-row">
                  <ElSelect
                    :model-value="selectedNode.inputBindings?.[field]?.kind || ''"
                    placeholder="未绑定"
                    @update:model-value="setBindingKind(field, $event)"
                    ><ElOption label="未绑定" value="" /><ElOption
                      label="固定值"
                      value="literal" /><ElOption label="节点输出" value="field" /><ElOption
                      label="入口输入"
                      value="input" /><ElOption label="表达式" value="cel"
                  /></ElSelect>
                  <template
                    v-if="
                      ['field', 'input'].includes(selectedNode.inputBindings?.[field]?.kind || '')
                    "
                  >
                    <ElSelect
                      v-if="selectedNode.inputBindings[field].kind === 'field'"
                      :model-value="selectedNode.inputBindings[field].nodeInstanceId"
                      placeholder="来源节点"
                      @update:model-value="patchBinding(field, { nodeInstanceId: $event })"
                      ><ElOption
                        v-for="node in graph.nodes.filter(
                          (node) => node.nodeInstanceId !== selectedNode?.nodeInstanceId
                        )"
                        :key="node.nodeInstanceId"
                        :label="node.nodeInstanceId"
                        :value="node.nodeInstanceId"
                    /></ElSelect>
                    <ElInput
                      :model-value="selectedNode.inputBindings[field].fieldPath?.join('.')"
                      placeholder="输出字段路径"
                      @change="
                        patchBinding(field, {
                          fieldPath: String($event).split('.').filter(Boolean)
                        })
                      "
                    />
                  </template>
                  <ElInput
                    v-if="selectedNode.inputBindings?.[field]?.kind === 'cel'"
                    :model-value="selectedNode.inputBindings[field].expression"
                    type="textarea"
                    placeholder="nodes['节点ID'].字段 / input.字段"
                    @update:model-value="patchBinding(field, { expression: $event })"
                  />
                  <ElInput
                    v-if="selectedNode.inputBindings?.[field]?.kind === 'literal'"
                    :model-value="JSON.stringify(selectedNode.inputBindings[field].value)"
                    placeholder="JSON 值；文本使用双引号"
                    @change="setLiteral(field, $event)"
                  />
                </div>
              </ElFormItem>
            </ElForm>
            <ElInput v-model="bindingName" placeholder="其他输入字段" /><ElButton
              @click="addBinding"
              >添加绑定</ElButton
            >
          </ElTabPane>
          <ElTabPane label="凭据" name="secrets">
            <ElEmpty v-if="!definition?.secretFields.length" description="此节点不需要凭据" />
            <ElForm label-position="top"
              ><ElFormItem
                v-for="field in definition?.secretFields"
                :key="field.name"
                :label="field.title || field.name"
              >
                <ElTag>{{ secretFields?.[qualifiedID]?.[field.name] ? '已配置' : '未配置' }}</ElTag>
                <ElInput
                  :model-value="
                    secretChanges.find(
                      (change) =>
                        change.nodeInstanceId === qualifiedID && change.field === field.name
                    )?.value || ''
                  "
                  type="password"
                  autocomplete="new-password"
                  placeholder="输入新值以替换；留空保持原值"
                  :disabled="!canManageSecrets"
                  @update:model-value="setSecret(field.name, $event)"
                />
                <ElButton
                  :disabled="!canManageSecrets"
                  link
                  type="danger"
                  @click="setSecret(field.name, '', true)"
                  >移除</ElButton
                >
              </ElFormItem></ElForm
            >
          </ElTabPane>
        </ElTabs>
        <ElButton
          type="danger"
          plain
          @click="
            commit(removeNode(graph, selectedNode.nodeInstanceId))
            drawer = false
          "
          >删除节点</ElButton
        >
      </template>
      <ElForm v-else-if="selectedEdge" label-position="top"
        ><ElFormItem label="来源端口"
          ><ElInput
            :model-value="selectedEdge.sourcePort"
            @update:model-value="patchEdge({ sourcePort: $event })" /></ElFormItem
        ><ElFormItem label="目标端口"
          ><ElInput
            :model-value="selectedEdge.targetPort"
            @update:model-value="patchEdge({ targetPort: $event })" /></ElFormItem
        ><ElFormItem label="执行条件"
          ><ElInput
            :model-value="selectedEdge.condition || ''"
            type="textarea"
            placeholder="留空表示无条件执行"
            @update:model-value="patchEdge({ condition: $event })" /></ElFormItem
        ><ElButton type="danger" @click="deleteEdge">删除连线</ElButton></ElForm
      >
    </ElDrawer>
    <ElDialog v-model="loopVisible" title="循环子图" width="95%" destroy-on-close>
      <WorkflowGraphEditor
        v-if="selectedNode && loopVisible"
        :graph="selectedNode.config.body as WorkflowGraph"
        :definitions="definitions"
        :secret-fields="secretFields"
        :secret-changes="secretChanges"
        :can-manage-secrets="canManageSecrets"
        :prefix="`${qualifiedID}.`"
        :read-only="readOnly"
        body
        @update:graph="setConfig('body', $event)"
        @update:secret-changes="emit('update:secretChanges', $event)"
      />
    </ElDialog>
  </section>
</template>
<script setup lang="ts">
  import { ElMessage } from 'element-plus'
  import type { Component } from 'vue'
  import type {
    WorkflowGraph,
    WorkflowGraphEdge,
    WorkflowGraphNode,
    WorkflowInputBinding,
    WorkflowNodeDefinition,
    WorkflowSecretChange
  } from '@/api/workflows'
  import { loadPluginNodeEditor } from '@/plugins'
  import WorkflowGraphCanvas from './WorkflowGraphCanvas.vue'
  import WorkflowSchemaFields from './WorkflowSchemaFields.vue'
  import { addNode, cloneGraph, removeNode } from './graph'
  const props = withDefaults(
    defineProps<{
      graph: WorkflowGraph
      definitions: WorkflowNodeDefinition[]
      secretFields?: Record<string, Record<string, boolean>>
      secretChanges: WorkflowSecretChange[]
      canManageSecrets: boolean
      readOnly?: boolean
      prefix?: string
      body?: boolean
    }>(),
    { prefix: '' }
  )
  const emit = defineEmits<{
    (event: 'update:graph', graph: WorkflowGraph): void
    (event: 'update:secretChanges', changes: WorkflowSecretChange[]): void
  }>()
  const search = ref(''),
    entryName = ref(''),
    bindingName = ref(''),
    selectedID = ref(''),
    selectedKind = ref('node'),
    tab = ref('config')
  const drawer = ref(false),
    loopVisible = ref(false),
    componentError = ref('')
  const nodeEditor = shallowRef<Component>()
  const materials = computed(() =>
    props.definitions.filter(
      (item) =>
        (props.body
          ? item.available &&
            item.kind !== 'trigger' &&
            !['core.loop', 'core.end', 'core.human_approval'].includes(item.type)
          : item.available) &&
        `${item.title} ${item.type}`.toLowerCase().includes(search.value.toLowerCase())
    )
  )
  const selectedNode = computed(() =>
    selectedKind.value === 'node'
      ? props.graph.nodes.find((node) => node.nodeInstanceId === selectedID.value)
      : undefined
  )
  const selectedEdge = computed(() =>
    selectedKind.value === 'edge'
      ? props.graph.edges.find((edge) => edge.edgeId === selectedID.value)
      : undefined
  )
  const definition = computed(() =>
    props.definitions.find((item) => item.type === selectedNode.value?.nodeType)
  )
  const qualifiedID = computed(() => props.prefix + (selectedNode.value?.nodeInstanceId || ''))
  const configSchema = computed(() => {
    const schema = JSON.parse(JSON.stringify(definition.value?.configSchema || {}))
    const properties = schema.properties as Record<string, unknown> | undefined
    if (properties) {
      delete properties.body
      for (const field of definition.value?.secretFields || []) delete properties[field.name]
    }
    return schema
  })
  const inputFields = computed(() => [
    ...new Set([
      ...Object.keys((definition.value?.inputSchema as any)?.properties || {}),
      ...Object.keys(selectedNode.value?.inputBindings || {})
    ])
  ])
  const commit = (graph: WorkflowGraph) => {
    if (!props.readOnly) emit('update:graph', graph)
  }
  const updateNode = (update: (node: WorkflowGraphNode) => void) => {
    const graph = cloneGraph(props.graph)
    const node = graph.nodes.find((node) => node.nodeInstanceId === selectedID.value)
    if (node) {
      update(node)
      commit(graph)
    }
  }
  const select = (id: string, kind: 'node' | 'edge') => {
    selectedID.value = id
    selectedKind.value = kind
    drawer.value = true
  }
  let componentRequest = 0
  watch(
    () => definition.value,
    async (current) => {
      const request = ++componentRequest
      nodeEditor.value = undefined
      componentError.value = ''
      if (!current?.pluginId || !current.editorKey) return
      try {
        const component = await loadPluginNodeEditor(current.editorKey, current.pluginId)
        if (request === componentRequest) nodeEditor.value = component
      } catch (error: any) {
        if (request === componentRequest) componentError.value = error.message
      }
    }
  )
  const setConfig = (key: string, value: unknown) =>
    updateNode((node) => {
      node.config[key] = value
    })
  const patchBinding = (field: string, patch: Partial<WorkflowInputBinding>) =>
    updateNode((node) => {
      node.inputBindings ??= {}
      node.inputBindings[field] = { ...node.inputBindings[field], ...patch }
    })
  const setBindingKind = (field: string, kind: WorkflowInputBinding['kind'] | '') =>
    updateNode((node) => {
      node.inputBindings ??= {}
      if (!kind) delete node.inputBindings[field]
      else
        node.inputBindings[field] =
          kind === 'literal'
            ? { kind, value: '' }
            : kind === 'field' || kind === 'input'
              ? { kind, ...(kind === 'field' ? { nodeInstanceId: '' } : {}), fieldPath: [] }
              : { kind, expression: '' }
    })
  const setLiteral = (field: string, value: string) => {
    try {
      patchBinding(field, { value: JSON.parse(value) })
    } catch {
      ElMessage.error('请输入有效的 JSON 值')
    }
  }
  const addBinding = () => {
    if (bindingName.value.trim()) setBindingKind(bindingName.value.trim(), 'literal')
    bindingName.value = ''
  }
  const setSecret = (field: string, value: string, remove = false) => {
    if (!props.canManageSecrets || props.readOnly) return
    const changes = props.secretChanges.filter(
      (change) => change.nodeInstanceId !== qualifiedID.value || change.field !== field
    )
    if (value || remove)
      changes.push({
        nodeInstanceId: qualifiedID.value,
        field,
        ...(remove ? { remove: true } : { value })
      })
    emit('update:secretChanges', changes)
  }
  const patchEdge = (patch: Partial<WorkflowGraphEdge>) => {
    const graph = cloneGraph(props.graph)
    const edge = graph.edges.find((edge) => edge.edgeId === selectedID.value)
    if (edge) {
      Object.assign(edge, patch)
      commit(graph)
    }
  }
  const deleteEdge = () => {
    const graph = cloneGraph(props.graph)
    graph.edges = graph.edges.filter((edge) => edge.edgeId !== selectedID.value)
    commit(graph)
    drawer.value = false
  }
  const setEntry = (name: string, id: string) => {
    const graph = cloneGraph(props.graph)
    graph.entryPoints[name] = id
    commit(graph)
  }
  const removeEntry = (name: string) => {
    const graph = cloneGraph(props.graph)
    delete graph.entryPoints[name]
    commit(graph)
  }
  const addEntry = () => {
    const name = entryName.value.trim()
    if (!/^[a-zA-Z][a-zA-Z0-9_-]*$/.test(name) || !props.graph.nodes.length) {
      ElMessage.warning('请输入有效入口名称并先添加节点')
      return
    }
    setEntry(name, props.graph.nodes[0].nodeInstanceId)
    entryName.value = ''
  }
</script>
<style scoped>
  .graph-editor {
    display: grid;
    grid-template-columns: 244px minmax(0, 1fr);
    height: 65vh;
    min-height: 460px;
    border: 1px solid var(--el-border-color);
  }
  .graph-library {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 8px;
    padding: 16px;
    overflow: auto;
    background: var(--el-bg-color);
    border-right: 1px solid var(--el-border-color);
  }
  .material {
    padding: 10px;
    text-align: left;
    cursor: pointer;
    border: 1px solid var(--el-border-color-lighter);
    border-radius: 5px;
    background: var(--el-fill-color-lighter);
    color: var(--el-text-color-primary);
  }
  .material strong,
  .material small {
    display: block;
  }
  .material small {
    margin-top: 5px;
    color: var(--el-text-color-secondary);
  }
  .material:focus-visible {
    outline: 2px solid var(--el-color-primary);
    outline-offset: 2px;
  }
  .entry-row,
  .binding-row {
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: 100%;
  }
  @media (max-width: 760px) {
    .graph-editor {
      grid-template-columns: 1fr;
      height: auto;
    }
    .graph-library {
      max-height: 230px;
      border-right: 0;
      border-bottom: 1px solid var(--el-border-color);
    }
  }
</style>
