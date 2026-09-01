/** 节点展示元数据全部来自后端 NodeDescriptor。 */
import type { WorkflowNodeDefinitionItem } from '@/api/scheduler'
import type { WorkflowMaterialGroup, WorkflowMaterialItem, WorkflowNodeFormKind } from './types'

const GROUP_ORDER = ['开始', '行情', '策略', '智能体', '控制', '数据', '通知', '集成', '结束']

const formKind = (definition: WorkflowNodeDefinitionItem): WorkflowNodeFormKind => {
  if (definition.typeCode === 'core.end' || definition.kind === 'terminal') return 'end'
  if (definition.kind === 'start') return 'start'
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
    group: definition.category || (definition.kind === 'start' ? '开始' : '其他'),
    title: definition.label,
    description: definition.description || definition.label,
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
    ...GROUP_ORDER.filter((group) => groups.includes(group)),
    ...groups.filter((group) => !GROUP_ORDER.includes(group))
  ].map((group) => ({
    key: group,
    title: group,
    items: items.filter((item) => item.group === group)
  }))
}
