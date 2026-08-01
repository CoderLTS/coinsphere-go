/** 状态管理模块：workflow-editor。 */
import { computed, reactive, ref } from 'vue'
import { defineStore } from 'pinia'
import type {
  TaskDefinitionItem,
  WorkflowDefinitionItem,
  WorkflowNodeDefinitionItem
} from '@/api/scheduler'
import { buildEditorSnapshot } from '@/views/scheduler/workflow/editor/workflow-editor.mapper'
import type {
  WorkflowDefinitionRegistryPayload,
  WorkflowDomainGraphModel,
  WorkflowEditorDraftState,
  WorkflowEditorIssue,
  WorkflowEditorMetaForm,
  WorkflowEditorMode,
  WorkflowEditorSelectionState,
  WorkflowMaterialGroup
} from '@/views/scheduler/workflow/editor/types'

const createInitialMetaForm = (): WorkflowEditorMetaForm => ({
  code: '',
  displayName: '',
  description: ''
})

const createInitialGraph = (): WorkflowDomainGraphModel => ({
  nodes: [],
  edges: []
})

const cloneJson = <T>(value: T): T => JSON.parse(JSON.stringify(value))

const createInitialSelection = (): WorkflowEditorSelectionState => ({
  activeCellId: null,
  activeCellType: null
})

const createInitialDraft = (): WorkflowEditorDraftState => ({
  cellId: null,
  cellType: null,
  model: null,
  dirty: false,
  valid: true,
  errors: []
})

export const useWorkflowEditorStore = defineStore('workflowEditorStore', () => {
  // 这一组状态共同构成“编辑器真源”：
  // graph、meta、selection、draft 都以 store 为唯一依据，画布只是渲染层。
  const mode = ref<WorkflowEditorMode>('create')
  const definitionId = ref<number | null>(null)
  const loading = ref(false)
  const saving = ref(false)
  const validating = ref(false)

  const definitionDetail = ref<WorkflowDefinitionItem | null>(null)
  const metaForm = reactive<WorkflowEditorMetaForm>(createInitialMetaForm())
  const taskDefinitions = ref<TaskDefinitionItem[]>([])
  const nodeDefinitions = ref<WorkflowNodeDefinitionItem[]>([])
  const materialGroups = ref<WorkflowMaterialGroup[]>([])
  const domainGraph = ref<WorkflowDomainGraphModel>(createInitialGraph())
  const selection = reactive<WorkflowEditorSelectionState>(createInitialSelection())
  const draftState = reactive<WorkflowEditorDraftState>(createInitialDraft())

  const clientIssues = ref<WorkflowEditorIssue[]>([])
  const serverIssues = ref<WorkflowEditorIssue[]>([])
  const dirtyTrackingEnabled = ref(false)
  const baselineSnapshot = ref('')
  const draftBaselineSnapshot = ref('')
  const baselineMeta = ref<WorkflowEditorMetaForm>(createInitialMetaForm())
  const baselineGraph = ref<WorkflowDomainGraphModel>(createInitialGraph())

  const allIssues = computed(() => [...clientIssues.value, ...serverIssues.value])
  const currentSnapshot = computed(() => buildEditorSnapshot(metaForm, domainGraph.value))
  const dirty = computed(() => {
    // dirty 判断只在显式开启后生效，避免首次加载服务端数据时被误判为已修改。
    if (!dirtyTrackingEnabled.value) return false
    return currentSnapshot.value !== baselineSnapshot.value
  })

  const resetEditorState = () => {
    // 切路由、重开编辑器、首次进入创建页时，都回到统一的初始状态。
    mode.value = 'create'
    definitionId.value = null
    loading.value = false
    saving.value = false
    validating.value = false
    definitionDetail.value = null
    Object.assign(metaForm, createInitialMetaForm())
    taskDefinitions.value = []
    nodeDefinitions.value = []
    materialGroups.value = []
    domainGraph.value = createInitialGraph()
    Object.assign(selection, createInitialSelection())
    Object.assign(draftState, createInitialDraft())
    clientIssues.value = []
    serverIssues.value = []
    dirtyTrackingEnabled.value = false
    baselineSnapshot.value = ''
    draftBaselineSnapshot.value = ''
    baselineMeta.value = createInitialMetaForm()
    baselineGraph.value = createInitialGraph()
  }

  const setMode = (value: WorkflowEditorMode, id?: number | null) => {
    mode.value = value
    definitionId.value = id ?? null
  }

  const setDefinitionDetail = (value: WorkflowDefinitionItem | null) => {
    definitionDetail.value = value
  }

  const setMetaForm = (value: Partial<WorkflowEditorMetaForm>) => {
    Object.assign(metaForm, value)
  }

  const setRegistryPayload = (payload: WorkflowDefinitionRegistryPayload) => {
    // 节点注册表和物料分组来自后端定义，用于编辑器面板和表单映射。
    taskDefinitions.value = payload.taskDefinitions
    nodeDefinitions.value = payload.nodeDefinitions
    materialGroups.value = payload.materialGroups
  }

  const setDomainGraph = (graph: WorkflowDomainGraphModel) => {
    domainGraph.value = graph
  }

  const setSelection = (payload: Partial<WorkflowEditorSelectionState>) => {
    Object.assign(selection, payload)
  }

  const setIssues = (payload: {
    client?: WorkflowEditorIssue[]
    server?: WorkflowEditorIssue[]
  }) => {
    // 前端本地校验和服务端校验分开存，便于界面区分错误来源。
    if (payload.client) clientIssues.value = payload.client
    if (payload.server) serverIssues.value = payload.server
  }

  const seedDraft = (payload: Partial<WorkflowEditorDraftState>) => {
    // 打开节点/边编辑器时记录一份草稿基线，用来判断当前草稿是否改动过。
    Object.assign(draftState, createInitialDraft(), payload)
    draftBaselineSnapshot.value = JSON.stringify(draftState.model || null)
  }

  const updateDraft = (payload: Partial<WorkflowEditorDraftState>) => {
    Object.assign(draftState, payload)
    draftState.dirty = JSON.stringify(draftState.model || null) !== draftBaselineSnapshot.value
  }

  const clearDraft = () => {
    Object.assign(draftState, createInitialDraft())
    draftBaselineSnapshot.value = ''
  }

  const captureBaseline = () => {
    // 保存成功或首次加载完成后刷新基线，后续 dirty 会以这份快照为比较标准。
    baselineSnapshot.value = currentSnapshot.value
    baselineMeta.value = cloneJson(metaForm)
    baselineGraph.value = cloneJson(domainGraph.value)
  }

  const enableDirtyTracking = () => {
    dirtyTrackingEnabled.value = true
  }

  const disableDirtyTracking = () => {
    dirtyTrackingEnabled.value = false
  }

  return {
    mode,
    definitionId,
    loading,
    saving,
    validating,
    definitionDetail,
    metaForm,
    taskDefinitions,
    nodeDefinitions,
    materialGroups,
    domainGraph,
    selection,
    draftState,
    clientIssues,
    serverIssues,
    allIssues,
    dirty,
    dirtyTrackingEnabled,
    baselineSnapshot,
    baselineMeta,
    baselineGraph,
    currentSnapshot,
    resetEditorState,
    setMode,
    setDefinitionDetail,
    setMetaForm,
    setRegistryPayload,
    setDomainGraph,
    setSelection,
    setIssues,
    seedDraft,
    updateDraft,
    clearDraft,
    captureBaseline,
    enableDirtyTracking,
    disableDirtyTracking
  }
})
