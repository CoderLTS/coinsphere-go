/**
 * 工作流编辑器辅助模块：workflow-graph-shapes。
 *
 * 画布上的节点卡片是 X6 的 HTML shape：这里只负责把节点数据翻译成一段结构固定的 HTML，
 * 具体长什么样交给 CSS（见 workflow-node-card.scss）。
 *
 * 两条原则：
 *   1. 配色全部走 STATE_STYLE 查表 —— 早先 borderColor / iconBg / statusColor 等六个变量
 *      各自写了一遍同样的八层嵌套三元，改一个状态色要改六处；
 *   2. 只输出 class 和文本，不拼内联样式 —— 内联样式绕开了主题变量，暗色模式对节点卡片无效。
 */
import { Graph, Shape } from '@antv/x6'
import { getNodeGraphKind } from '../node-registry'

let workflowNodeShapeRegistered = false

const escapeHtml = (value: string) =>
  String(value || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')

/**
 * 节点右上角的角标文案。按后端声明的图语义分类，不再逐个类型编码写死；
 * 只有「普通节点」才退回按业务 kind 给个更具体的说法。
 */
const KIND_BADGE: Record<string, string> = {
  start: '触发器',
  terminal: '输出器',
  branch: '分支',
  loop: '循环'
}

const FORM_KIND_BADGE: Record<string, string> = {
  notify: '通知',
  event: '事件',
  http: 'HTTP',
  delay: '延迟',
  task: '任务'
}

const resolveNodeBadgeText = (typeCode: string, formKind: string) =>
  KIND_BADGE[getNodeGraphKind(typeCode)] || FORM_KIND_BADGE[formKind] || '节点'

/**
 * 节点的「视觉状态」。优先级从上到下：异常 > 执行态 > 未保存 > 选中 > 默认。
 * 一个节点在任一时刻只属于一种状态，配色由 STATE_STYLE 一次查出来。
 */
type NodeVisualState =
  | 'issue'
  | 'failed'
  | 'running'
  | 'retryWaiting'
  | 'queued'
  | 'success'
  | 'dirty'
  | 'selected'
  | 'idle'

const resolveVisualState = (flags: {
  hasIssue: boolean
  isDirty: boolean
  isSelected: boolean
  executionState: string
}): NodeVisualState => {
  if (flags.hasIssue) return 'issue'
  if (flags.executionState === 'failed') return 'failed'
  if (flags.executionState === 'running') return 'running'
  if (flags.executionState === 'retry_waiting') return 'retryWaiting'
  if (flags.executionState === 'queued') return 'queued'
  if (flags.executionState === 'success') return 'success'
  if (flags.isDirty) return 'dirty'
  if (flags.isSelected) return 'selected'
  return 'idle'
}

/**
 * 状态 → 展示。text 为空表示状态条不占位（用 CSS 的 visibility 藏起来，保持高度稳定）。
 * 颜色都在 workflow-node-card.scss 里按 data-state 定义，这里只决定「是哪个状态」和「写什么字」。
 */
const STATE_TEXT: Record<NodeVisualState, string> = {
  issue: '异常',
  failed: '执行失败',
  running: '执行中',
  retryWaiting: '等待重试',
  queued: '等待执行',
  success: '',
  dirty: '未保存',
  selected: '',
  idle: ''
}

const successBadgeText = (executionCount: number, isExecutionEntry: boolean) => {
  if (executionCount > 1) return `x${executionCount}`
  return isExecutionEntry ? '入口' : ''
}

const CHECK_ICON = `<svg viewBox="0 0 10 10" fill="none" aria-hidden="true"><path d="M2 5.1L4.15 7.25L8.1 2.9" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>`

const renderStatusMarkup = (
  state: NodeVisualState,
  executionCount: number,
  isExecutionEntry: boolean
) => {
  // 成功态用一个对勾图标代替文字，执行过多次或是本次入口时再补一个小标签。
  if (state === 'success') {
    const extra = successBadgeText(executionCount, isExecutionEntry)
    return `<div class="wf-node__status wf-node__status--ok">
      <span class="wf-node__check">${CHECK_ICON}</span>
      ${extra ? `<span class="wf-node__status-text">${escapeHtml(extra)}</span>` : ''}
    </div>`
  }

  const text = STATE_TEXT[state]
  return `<div class="wf-node__status"${text ? '' : ' data-empty="true"'}>
    ${text ? `<span class="wf-node__status-text">${escapeHtml(text)}</span>` : ''}
  </div>`
}

export const renderWorkflowNodeCardHtml = (cell: any) => {
  const data = cell.getData() || {}
  const typeCode = String(data.typeCode || '')
  const formKind = String(data.kind || '')
  const graphKind = getNodeGraphKind(typeCode)
  const hasIssue = Boolean(data.hasIssue)
  const executionState = String(data.executionState || '')
  const executionCount = Number(data.executionCount || 0)

  const state = resolveVisualState({
    hasIssue,
    isDirty: Boolean(data.isDirty),
    isSelected: Boolean(data.isSelected),
    executionState
  })

  const title = escapeHtml(String(data.title || ''))
  const iconText = escapeHtml(String(data.iconText || 'N'))
  const badgeText = escapeHtml(resolveNodeBadgeText(typeCode, formKind))
  const rawSubtitle = hasIssue ? data.issueSummary || data.subtitle || '' : data.subtitle || ''
  const bodyLabel = graphKind === 'start' || graphKind === 'terminal' ? '节点类型' : '节点说明'
  const bodyText = escapeHtml(
    String(rawSubtitle) ||
      (graphKind === 'start'
        ? '工作流开始节点'
        : graphKind === 'terminal'
          ? '工作流结束节点'
          : '配置当前节点的执行内容')
  )

  // accentColor 是物料给这类节点定的主题色（图标底色、左侧色条），与执行状态无关。
  const accentColor = String(data.color || '#64748b')

  return `
    <div class="wf-node" data-state="${state}" data-kind="${graphKind}"${data.isDimmed ? ' data-dimmed="true"' : ''} style="--wf-node-accent:${accentColor}">
      <div class="wf-node__accent"></div>
      <div class="wf-node__inner">
        <div class="wf-node__head">
          <div class="wf-node__icon">${iconText}</div>
          <div class="wf-node__title" title="${title}">${title}</div>
          <div class="wf-node__badge">${badgeText}</div>
        </div>
        <div class="wf-node__body">
          <span class="wf-node__body-label">${bodyLabel}</span>
          <div class="wf-node__body-text">${bodyText}</div>
        </div>
        <div class="wf-node__foot">
          ${renderStatusMarkup(state, executionCount, Boolean(data.isExecutionEntry))}
        </div>
      </div>
    </div>
  `
}

export const ensureWorkflowNodeShapeRegistered = () => {
  if (workflowNodeShapeRegistered) return

  Shape.HTML.register({
    shape: 'workflow-node-card',
    width: 260,
    height: 96,
    html: renderWorkflowNodeCardHtml,
    attrs: {
      body: {
        fill: 'transparent',
        stroke: 'transparent'
      }
    },
    effect: ['data']
  })

  workflowNodeShapeRegistered = true
}

export const ensureWorkflowGraphEdgeRegistered = () => {
  Graph.registerEdge(
    'workflow-editor-edge',
    {
      inherit: 'edge',
      connector: {
        name: 'smooth'
      },
      attrs: {
        line: {
          stroke: '#5f95ff',
          strokeWidth: 2,
          strokeDasharray: '8 6',
          targetMarker: {
            name: 'block',
            width: 12,
            height: 8
          }
        }
      }
    },
    true
  )
}
