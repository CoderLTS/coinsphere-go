/** 工作流编辑器辅助模块：types。 */
import type {
  WorkflowDefinitionValidationIssue,
  WorkflowEdgeItem,
  WorkflowNodeDefinitionItem,
  WorkflowNodeItem
} from '@/api/scheduler'

export type WorkflowEditorMode = 'create' | 'edit'

/**
 * 节点的「表单形态」：决定属性面板用哪套编辑界面，纯前端概念。
 *
 * 注意别和后端下发的 WorkflowNodeGraphKind（plain/start/branch/loop/terminal）搞混：
 * 那个是「图语义」，决定节点在画布上有几个出口、校验时按哪套规则查，来源是后端注册表（见 node-registry.ts）。
 * 一个节点两者都有，比如 condition.branch 的 formKind 是 'condition'、graphKind 是 'branch'。
 */
export type WorkflowNodeFormKind =
  | 'start'
  | 'agent'
  | 'condition'
  | 'foreach'
  | 'notify'
  | 'event'
  | 'http'
  | 'delay'
  | 'end'
  /** 没有定制表单的节点：属性面板按后端下发的 configSchema 自动渲染。 */
  | 'generic'

export type WorkflowActiveCellType = 'node' | 'edge' | null

export interface WorkflowEditorMetaForm {
  code: string
  displayName: string
  description: string
}

export interface WorkflowMaterialItem {
  typeCode: string
  kind: WorkflowNodeFormKind
  group: string
  title: string
  description: string
  color: string
  iconText: string
  width: number
  height: number
  presetConfig?: Record<string, any>
  presetSubtitle?: string
}

export interface WorkflowMaterialGroup {
  key: string
  title: string
  items: WorkflowMaterialItem[]
}

export interface WorkflowNotifyTargetOption {
  value: number
  label: string
}

export interface WorkflowDomainPort {
  id: string
  group: string
  role: 'in' | 'out'
  label?: string
}

export interface WorkflowDomainNodeData {
  typeCode: string
  kind: WorkflowNodeFormKind
  title: string
  subtitle: string
  color: string
  iconText: string
  config: Record<string, any>
}

export interface WorkflowDomainNode {
  id: string
  position: {
    x: number
    y: number
  }
  size: {
    width: number
    height: number
  }
  ports: WorkflowDomainPort[]
  data: WorkflowDomainNodeData
}

export interface WorkflowDomainEdgeData {
  branch: string
  label: string
}

export interface WorkflowDomainEdge {
  id: string
  source: string
  target: string
  sourcePort?: string
  targetPort?: string
  data: WorkflowDomainEdgeData
}

export interface WorkflowDomainGraphModel {
  nodes: WorkflowDomainNode[]
  edges: WorkflowDomainEdge[]
}

export interface WorkflowX6NodeCell {
  id: string
  shape: string
  x?: number
  y?: number
  width?: number
  height?: number
  position?: {
    x: number
    y: number
  }
  size?: {
    width: number
    height: number
  }
  attrs: Record<string, any>
  ports: {
    groups: Record<string, any>
    items: Array<Record<string, any>>
  }
  data: Record<string, any>
  zIndex?: number
}

export interface WorkflowX6EdgeCell {
  id: string
  shape: 'edge'
  source: {
    cell: string
    port?: string
  }
  target: {
    cell: string
    port?: string
  }
  router?: Record<string, any>
  connector?: Record<string, any>
  attrs: Record<string, any>
  labels?: Array<Record<string, any>>
  data: Record<string, any>
  zIndex?: number
}

export interface WorkflowX6GraphJson {
  cells: Array<WorkflowX6NodeCell | WorkflowX6EdgeCell>
}

export interface WorkflowEditorIssue extends WorkflowDefinitionValidationIssue {
  id: string
  source: 'client' | 'server'
}

export interface WorkflowEditorSelectionState {
  activeCellId: string | null
  activeCellType: WorkflowActiveCellType
}

export interface WorkflowNodeFormModel {
  id: string
  label: string
  typeCode: string
  kind: WorkflowNodeFormKind
  config: Record<string, any>
}

export interface WorkflowEdgeFormModel {
  id: string
  source: string
  target: string
  sourcePort: string
  targetPort: string
  branch: string
  label: string
}

export interface WorkflowEditorDraftState {
  cellId: string | null
  cellType: WorkflowActiveCellType
  model: WorkflowNodeFormModel | WorkflowEdgeFormModel | null
  dirty: boolean
  valid: boolean
  errors: string[]
}

export interface WorkflowMaterialDropPayload {
  typeCode: string
  title?: string
  color?: string
  presetConfig?: Record<string, any>
  presetSubtitle?: string
  source: 'click' | 'drag'
  position: {
    x: number
    y: number
  }
}

export interface WorkflowNodeContextActionPayload {
  cellId: string
  action: 'edit' | 'delete'
}

export interface WorkflowDraftValidationResult {
  valid: boolean
  errors: string[]
}

export interface WorkflowGraphCommitPayload {
  graph: WorkflowDomainGraphModel
  reason:
    | 'drag-end'
    | 'connect-end'
    | 'delete-end'
    | 'undo'
    | 'redo'
    | 'external-drop'
    | 'node-draft-commit'
    | 'edge-draft-commit'
    | 'meta-commit'
    | 'init'
}

export interface WorkflowDefinitionRegistryPayload {
  nodeDefinitions: WorkflowNodeDefinitionItem[]
  materialGroups: WorkflowMaterialGroup[]
}

export type WorkflowServerNode = WorkflowNodeItem
export type WorkflowServerEdge = WorkflowEdgeItem
