/** 工作流编辑器辅助模块：node-materials。 */
import type { WorkflowNodeDefinitionItem } from '@/api/scheduler'
import type { WorkflowMaterialGroup, WorkflowMaterialItem, WorkflowNodeFormKind } from './types'

const MATERIAL_META: Record<
  string,
  {
    kind: WorkflowNodeFormKind
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
  'market.metadata.sync': {
    kind: 'generic',
    group: '行情',
    description: '按全局范围同步 Binance 币种元数据',
    color: '#c7f46b',
    iconText: 'M'
  },
  'market.candles.subscribe': {
    kind: 'generic',
    group: '行情',
    description: '声明工作流需要持续接收的 K 线',
    color: '#7ec7b7',
    iconText: 'K'
  },
  'market.candles.backfill': {
    kind: 'generic',
    group: '行情',
    description: '补齐指定 UTC 区间的历史 K 线',
    color: '#5eaa74',
    iconText: 'B'
  },
  'strategy.evaluate': {
    kind: 'generic',
    group: '策略',
    description: '同步等待或异步提交一个策略实例',
    color: '#9e8cff',
    iconText: 'Σ'
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
  'condition.switch': {
    kind: 'generic',
    group: '控制',
    description: '按值路由到任意多个分支，都不命中走 default',
    color: '#f59e0b',
    iconText: 'S'
  },
  'state.set': {
    kind: 'generic',
    group: '数据',
    description: '给共享状态赋值，支持 {{路径}} 模板',
    color: '#0891b2',
    iconText: 'V'
  },
  'state.append': {
    kind: 'generic',
    group: '数据',
    description: '往数组变量追加一项，用于汇总 foreach 每轮的结果',
    color: '#0e7490',
    iconText: '+'
  },
  'array.filter': {
    kind: 'generic',
    group: '数据',
    description: '按条件过滤数组',
    color: '#155e75',
    iconText: 'F'
  },
  'log.message': {
    kind: 'generic',
    group: '控制',
    description: '打一条执行日志，方便排查流程',
    color: '#64748b',
    iconText: 'L'
  },
  'workflow.call': {
    kind: 'generic',
    group: '集成',
    description: '调用另一个已激活的工作流',
    color: '#4f46e5',
    iconText: 'W'
  },
  'assistant.agent': {
    kind: 'agent',
    group: '智能体',
    description: '调用一个智能体处理内容，结果写入共享状态',
    color: '#0ea5e9',
    iconText: 'A'
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

/** 已知分组的展示顺序；不在这张表里的分组（新节点带来的）排在后面，不会被丢掉。 */
const GROUP_ORDER = [
  '开始',
  '行情',
  '策略',
  '任务',
  '智能体',
  '控制',
  '数据',
  '事件',
  '通知',
  '集成',
  '结束'
]
const FALLBACK_GROUP = '其他'

export function inferNodeFormKind(typeCode: string): WorkflowNodeFormKind {
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
      group: meta?.group || FALLBACK_GROUP,
      title: definition.label,
      description: meta?.description || definition.typeCode,
      color: meta?.color || '#64748b',
      iconText: meta?.iconText || definition.label.slice(0, 1).toUpperCase(),
      width: meta?.width || 260,
      height: meta?.height || 96
    }
  })

  // 分组顺序 = 已知顺序 + 数据里实际出现过的其它分组。
  // 早先这里直接 map(GROUP_ORDER)，后端新注册的节点因为落进 '其他' 分组，会被整个过滤掉、
  // 在物料面板里凭空消失且不报错 —— 所以改成按实际数据补齐分组。
  const seenGroups = Array.from(new Set(materialItems.map((item) => item.group)))
  const orderedGroups = [
    ...GROUP_ORDER.filter((group) => seenGroups.includes(group)),
    ...seenGroups.filter((group) => !GROUP_ORDER.includes(group))
  ]

  return orderedGroups.map((groupTitle) => ({
    key: groupTitle,
    title: groupTitle,
    items: materialItems.filter((item) => item.group === groupTitle)
  }))
}
