/** 工作流编辑器节点物料的展示元数据。 */
import type { WorkflowNodeDefinitionItem } from '@/api/scheduler'
import type { WorkflowMaterialGroup, WorkflowMaterialItem, WorkflowNodeFormKind } from './types'

type MaterialMeta = {
  kind: WorkflowNodeFormKind
  group: string
  description: string
  color: string
  iconText: string
  width?: number
  height?: number
}

const MATERIAL_META: Record<string, MaterialMeta> = {
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
  'core.constant': {
    kind: 'generic',
    group: '数据',
    description: '输出配置的常量文本',
    color: '#0891b2',
    iconText: 'C'
  },
  'core.human_approval': {
    kind: 'generic',
    group: '控制',
    description: '创建人工审批任务并等待处理',
    color: '#d97706',
    iconText: 'A'
  },
  'core.loop': {
    kind: 'generic',
    group: '控制',
    description: '在限制次数和时间内执行内嵌流程',
    color: '#ca8a04',
    iconText: 'L'
  },
  'official.connector.websocket': {
    kind: 'generic',
    group: '开始',
    description: '从 WebSocket 消息触发工作流',
    color: '#0f766e',
    iconText: 'W'
  },
  'official.connector.http': {
    kind: 'generic',
    group: '集成',
    description: '向外部服务发起受控 HTTP 请求',
    color: '#0f766e',
    iconText: 'H'
  },
  'official.ai.model_call': {
    kind: 'generic',
    group: '智能体',
    description: '调用已配置的 AI 模型',
    color: '#0ea5e9',
    iconText: 'AI'
  },
  'official.quant.binance_candles': {
    kind: 'generic',
    group: '行情',
    description: '采集并发布 Binance 已收盘 K 线',
    color: '#7ec7b7',
    iconText: 'K'
  },
  'official.quant.sync_instruments': {
    kind: 'generic',
    group: '行情',
    description: '同步过滤后的 Binance 币种元数据',
    color: '#0891b2',
    iconText: 'M'
  },
  'official.quant.evaluate': {
    kind: 'generic',
    group: '策略',
    description: '使用已编译策略评估 K 线',
    color: '#9e8cff',
    iconText: 'E'
  },
  'official.quant.volume_spike_condition': {
    kind: 'indicator-condition',
    group: '行情',
    description: '判断当前成交量是否相对近期均量放大',
    color: '#0f766e',
    iconText: '量',
    width: 280,
    height: 112
  },
  'official.quant.price_change_condition': {
    kind: 'indicator-condition',
    group: '行情',
    description: '判断 K 线涨跌幅或区间振幅',
    color: '#047857',
    iconText: '%',
    width: 280,
    height: 112
  },
  'official.quant.macd_condition': {
    kind: 'indicator-condition',
    group: '行情',
    description: '判断 MACD 交叉或 DIF 零轴位置',
    color: '#0e7490',
    iconText: 'M',
    width: 280,
    height: 112
  },
  'official.quant.kdj_condition': {
    kind: 'indicator-condition',
    group: '行情',
    description: '判断 KDJ 交叉或阈值位置',
    color: '#0369a1',
    iconText: 'K',
    width: 280,
    height: 112
  },
  'official.quant.rsi_condition': {
    kind: 'indicator-condition',
    group: '行情',
    description: '判断 RSI 是否越过配置阈值',
    color: '#4338ca',
    iconText: 'R',
    width: 280,
    height: 112
  },
  'official.quant.bollinger_condition': {
    kind: 'indicator-condition',
    group: '行情',
    description: '判断收盘价是否突破布林带',
    color: '#6d28d9',
    iconText: 'B',
    width: 280,
    height: 112
  },
  'official.quant.backtest': {
    kind: 'generic',
    group: '策略',
    description: '基于历史 K 线执行确定性回测',
    color: '#7c6ee6',
    iconText: 'B'
  },
  'official.quant.signal': {
    kind: 'generic',
    group: '策略',
    description: '持久化可替换的量化信号',
    color: '#6d5bd0',
    iconText: 'S'
  },
  'official.quant.paper_execute': {
    kind: 'generic',
    group: '策略',
    description: '执行完整 Paper 风控并模拟成交',
    color: '#5948b8',
    iconText: 'P'
  },
  'official.notification.in_app': {
    kind: 'notify',
    group: '通知',
    description: '向用户或角色发送站内通知',
    color: '#7c3aed',
    iconText: '内'
  },
  'official.notification.dingtalk': {
    kind: 'generic',
    group: '通知',
    description: '通过钉钉机器人发送通知',
    color: '#2563eb',
    iconText: '钉'
  },
  'official.notification.qq': {
    kind: 'generic',
    group: '通知',
    description: '向 QQ 群或频道发送通知',
    color: '#0891b2',
    iconText: 'Q'
  },
  'official.notification.smtp': {
    kind: 'generic',
    group: '通知',
    description: '通过 TLS SMTP 发送邮件通知',
    color: '#15803d',
    iconText: '邮'
  },
  end: {
    kind: 'end',
    group: '结束',
    description: '声明当前执行链路结束',
    color: '#dc2626',
    iconText: 'E'
  }
}

const GROUP_ORDER = ['开始', '行情', '策略', '智能体', '控制', '数据', '通知', '集成', '结束']
const FALLBACK_GROUP = '其他'

export function inferNodeFormKind(typeCode: string): WorkflowNodeFormKind {
  return MATERIAL_META[typeCode]?.kind || 'generic'
}

export function getNodeMaterialMeta(typeCode: string) {
  return MATERIAL_META[typeCode]
}

export function buildWorkflowMaterialGroups(
  nodeDefinitions: WorkflowNodeDefinitionItem[]
): WorkflowMaterialGroup[] {
  const materialItems: WorkflowMaterialItem[] = nodeDefinitions.map((definition) => {
    const meta = MATERIAL_META[definition.typeCode]
    const kind =
      meta?.kind ||
      (definition.kind === 'start' ? 'start' : definition.kind === 'terminal' ? 'end' : 'generic')
    return {
      typeCode: definition.typeCode,
      kind,
      group: meta?.group || (kind === 'start' ? '开始' : kind === 'end' ? '结束' : FALLBACK_GROUP),
      title: definition.label,
      description: meta?.description || definition.description || definition.typeCode,
      color: meta?.color || '#64748b',
      iconText: meta?.iconText || definition.label.slice(0, 1).toUpperCase(),
      width: meta?.width || 260,
      height: meta?.height || 96
    }
  })

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
