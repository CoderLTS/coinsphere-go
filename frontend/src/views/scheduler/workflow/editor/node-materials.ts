/** 节点展示元数据全部来自后端 NodeDescriptor。 */
import type { WorkflowNodeDefinitionItem } from '@/api/scheduler'
import type { WorkflowMaterialGroup, WorkflowMaterialItem, WorkflowNodeFormKind } from './types'

export const WORKFLOW_CATEGORY_ORDER = [
  'start',
  'market',
  'strategy',
  'agent',
  'control',
  'data',
  'notification',
  'integration',
  'end'
]

export const WORKFLOW_CATEGORY_LABELS: Record<string, string> = {
  start: '开始',
  market: '行情',
  strategy: '策略',
  agent: '智能体',
  control: '控制',
  data: '数据',
  notification: '通知',
  integration: '集成',
  end: '结束',
  other: '其他'
}

export const workflowCategoryLabel = (key: string) => WORKFLOW_CATEGORY_LABELS[key] || '其他'

const normalizeCategory = (category: string) => {
  const value = category.trim()
  return WORKFLOW_CATEGORY_LABELS[value] ? value : 'other'
}

const BUILTIN_START_FORM_TYPES = new Set([
  'start.manual',
  'start.schedule',
  'start.event',
  'start.webhook'
])

const formKind = (definition: WorkflowNodeDefinitionItem): WorkflowNodeFormKind => {
  if (definition.typeCode === 'core.end' || definition.kind === 'terminal') return 'end'
  if (BUILTIN_START_FORM_TYPES.has(definition.typeCode)) return 'start'
  return 'generic'
}

export function inferNodeFormKind(typeCode: string): WorkflowNodeFormKind {
  void typeCode
  return 'generic'
}

export function buildWorkflowMaterialGroups(
  definitions: WorkflowNodeDefinitionItem[]
): WorkflowMaterialGroup[] {
  const items: WorkflowMaterialItem[] = definitions.map((definition) => ({
    typeCode: definition.typeCode,
    kind: formKind(definition),
    group: normalizeCategory(definition.category),
    title: definition.label,
    description: definition.description || definition.label,
    aliases: definition.aliases || [],
    tags: definition.tags || [],
    color: definition.color || '#64748b',
    iconText:
      definition.icon && Array.from(definition.icon).length <= 2
        ? definition.icon
        : definition.label.slice(0, 1).toUpperCase(),
    width: definition.width || 220,
    height: definition.height || 72
  }))
  const groups = Array.from(new Set(items.map((item) => item.group)))
  return [
    ...WORKFLOW_CATEGORY_ORDER.filter((group) => groups.includes(group)),
    ...(groups.includes('other') ? ['other'] : [])
  ].map((group) => ({
    key: group,
    title: workflowCategoryLabel(group),
    items: items.filter((item) => item.group === group)
  }))
}
