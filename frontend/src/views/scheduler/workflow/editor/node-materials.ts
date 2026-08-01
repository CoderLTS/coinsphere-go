/** 工作流编辑器辅助模块：node-materials。 */
import type { WorkflowNodeDefinitionItem } from '@/api/scheduler'
import type { WorkflowMaterialGroup, WorkflowMaterialItem, WorkflowNodeKind } from './types'

const MATERIAL_META: Record<
  string,
  {
    kind: WorkflowNodeKind
    group: string
    description: string
    color: string
    iconText: string
    width?: number
    height?: number
  }
> = {
  'start.manual': {
    kind: 'start',
    group: '开始',
    description: '声明手动触发入口节点',
    color: '#3b82f6',
    iconText: 'S'
  },
  'start.schedule': {
    kind: 'start',
    group: '开始',
    description: '声明定时触发入口节点',
    color: '#2563eb',
    iconText: 'S'
  },
  'start.event': {
    kind: 'start',
    group: '开始',
    description: '声明事件触发入口节点',
    color: '#1d4ed8',
    iconText: 'S'
  },
  'start.webhook': {
    kind: 'start',
    group: '开始',
    description: '声明 Webhook 触发入口节点',
    color: '#1e40af',
    iconText: 'S'
  },
  'task.run': {
    kind: 'task',
    group: '任务',
    description: '执行一个代码注册的任务能力',
    color: '#16a34a',
    iconText: 'T'
  },
  'condition.branch': {
    kind: 'condition',
    group: '控制',
    description: '按条件选择 true 或 false 分支',
    color: '#d97706',
    iconText: 'C'
  },
  foreach: {
    kind: 'foreach',
    group: '控制',
    description: '顺序遍历数组并执行后续节点',
    color: '#ca8a04',
    iconText: 'F'
  },
  notify: {
    kind: 'notify',
    group: '通知',
    description: '按目标和渠道直接发送通知',
    color: '#7c3aed',
    iconText: 'N'
  },
  'event.publish': {
    kind: 'event',
    group: '事件',
    description: '发布领域事件供其他工作流消费',
    color: '#9333ea',
    iconText: 'E'
  },
  'http.request': {
    kind: 'http',
    group: '集成',
    description: '向外部服务发起 HTTP 请求',
    color: '#0f766e',
    iconText: 'H'
  },
  'delay.wait': {
    kind: 'delay',
    group: '控制',
    description: '暂停当前工作流一段时间',
    color: '#475569',
    iconText: 'D'
  },
  end: {
    kind: 'end',
    group: '结束',
    description: '声明当前执行链路结束',
    color: '#dc2626',
    iconText: 'E'
  }
}

const GROUP_ORDER = ['开始', '任务', '控制', '事件', '通知', '集成', '结束']

export function inferNodeKind(typeCode: string): WorkflowNodeKind {
  return MATERIAL_META[typeCode]?.kind || 'task'
}

export function getNodeMaterialMeta(typeCode: string) {
  return MATERIAL_META[typeCode]
}

export function buildWorkflowMaterialGroups(
  nodeDefinitions: WorkflowNodeDefinitionItem[]
): WorkflowMaterialGroup[] {
  const materialItems: WorkflowMaterialItem[] = nodeDefinitions.map((definition) => {
    const meta = MATERIAL_META[definition.typeCode]
    return {
      typeCode: definition.typeCode,
      kind: meta?.kind || 'task',
      group: meta?.group || '其他',
      title: definition.label,
      description: meta?.description || definition.typeCode,
      color: meta?.color || '#64748b',
      iconText: meta?.iconText || definition.label.slice(0, 1).toUpperCase(),
      width: meta?.width || 260,
      height: meta?.height || 96
    }
  })

  return GROUP_ORDER.map((groupTitle) => ({
    key: groupTitle,
    title: groupTitle,
    items: materialItems.filter((item) => item.group === groupTitle)
  })).filter((group) => group.items.length > 0)
}
