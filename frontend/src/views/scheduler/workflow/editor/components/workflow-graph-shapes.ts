/** 工作流编辑器辅助模块：workflow-graph-shapes。 */
import { Graph, Shape } from '@antv/x6'

let workflowNodeShapeRegistered = false

const escapeHtml = (value: string) =>
  String(value || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')

const resolveNodeBadgeText = (typeCode: string, kind: string) => {
  if (typeCode.startsWith('start.')) return '触发器'
  if (typeCode === 'end') return '输出器'
  if (typeCode === 'condition.branch') return '分支'
  if (typeCode === 'foreach') return '循环'
  if (kind === 'notify') return '通知'
  if (kind === 'event') return '事件'
  if (kind === 'http') return 'HTTP'
  if (kind === 'delay') return '延迟'
  return '节点'
}

const resolveStatusText = (options: {
  hasIssue: boolean
  isDirty: boolean
  executionState: string
  executionCount: number
  isExecutionEntry: boolean
}) => {
  const { hasIssue, isDirty, executionState, executionCount, isExecutionEntry } = options
  if (hasIssue) return '异常'
  if (executionState === 'failed') return '执行失败'
  if (executionState === 'running') return '执行中'
  if (executionState === 'retry_waiting') return '等待重试'
  if (executionState === 'queued') return '等待执行'
  if (isDirty) return '未保存'
  if (executionState === 'success') {
    if (isExecutionEntry) {
      return executionCount > 1 ? `入口执行 ${executionCount} 次` : '本次入口'
    }
    return executionCount > 1 ? `已执行 ${executionCount} 次` : '已执行'
  }
  return ''
}

const renderStatusMarkup = (options: {
  hasIssue: boolean
  isDirty: boolean
  executionState: string
  executionCount: number
  isExecutionEntry: boolean
  statusBg: string
  statusColor: string
}) => {
  const { hasIssue, isDirty, executionState, executionCount, isExecutionEntry, statusBg, statusColor } = options

  if (executionState === 'success' && !hasIssue && !isDirty) {
    const extraText = executionCount > 1 ? `x${executionCount}` : isExecutionEntry ? '入口' : ''
    return `
      <div style="display:flex;align-items:center;gap:6px;">
        <span style="display:inline-flex;align-items:center;justify-content:center;width:18px;height:18px;border-radius:999px;background:#22c55e;color:#ffffff;transform:translateY(-1px);">
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
            <path d="M2 5.1L4.15 7.25L8.1 2.9" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </span>
        ${
          extraText
            ? `<span style="font-size:11px;line-height:16px;font-weight:700;color:${statusColor};background:${statusBg};border-radius:8px;padding:1px 8px;">${extraText}</span>`
            : ''
        }
      </div>
    `
  }

  const statusText = resolveStatusText({
    hasIssue,
    isDirty,
    executionState,
    executionCount,
    isExecutionEntry
  })

  return `<div style="visibility:${statusText ? 'visible' : 'hidden'};font-size:11px;line-height:16px;font-weight:600;color:${statusColor};background:${statusBg};border-radius:8px;padding:1px 8px;">${statusText}</div>`
}

export const renderWorkflowNodeCardHtml = (cell: any) => {
  const data = cell.getData() || {}
  const typeCode = String(data.typeCode || '')
  const kind = String(data.kind || '')
  const color = String(data.color || '#64748b')
  const title = escapeHtml(String(data.title || ''))
  const subtitle = escapeHtml(String(data.hasIssue ? data.issueSummary || data.subtitle || '' : data.subtitle || ''))
  const iconText = escapeHtml(String(data.iconText || 'N'))
  const badgeText = escapeHtml(resolveNodeBadgeText(typeCode, kind))
  const hasIssue = Boolean(data.hasIssue)
  const isDirty = Boolean(data.isDirty)
  const isSelected = Boolean(data.isSelected)
  const executionState = String(data.executionState || '')
  const executionCount = Number(data.executionCount || 0)
  const isDimmed = Boolean(data.isDimmed)
  const isExecutionEntry = Boolean(data.isExecutionEntry)
  const isStart = typeCode.startsWith('start.')
  const isEnd = typeCode === 'end'

  const borderColor = hasIssue
    ? '#ef4444'
    : executionState === 'failed'
      ? '#ef4444'
      : executionState === 'running'
        ? '#f59e0b'
        : executionState === 'retry_waiting'
          ? '#fb923c'
          : executionState === 'queued'
          ? '#60a5fa'
          : isSelected
            ? '#2563eb'
            : isDirty
              ? '#5f95ff'
              : executionState === 'success'
                ? '#22c55e'
                : isEnd
                  ? '#ffb4b0'
                  : '#bcd0ff'

  const iconBg =
    executionState === 'failed'
      ? '#fff1f0'
      : executionState === 'running'
        ? '#fff7ed'
        : executionState === 'retry_waiting'
          ? '#fff7ed'
          : executionState === 'queued'
          ? '#eff6ff'
          : executionState === 'success'
            ? '#ecfdf5'
            : isEnd
              ? '#fff1f0'
              : isStart
                ? '#eef2ff'
                : `${color}16`

  const iconColor =
    hasIssue || executionState === 'failed'
      ? '#ef4444'
      : executionState === 'running'
        ? '#d97706'
        : executionState === 'retry_waiting'
          ? '#c2410c'
          : executionState === 'queued'
          ? '#2563eb'
          : executionState === 'success'
            ? '#16a34a'
            : isEnd
              ? '#ff7875'
              : isStart
                ? '#5f95ff'
                : color

  const badgeBg = isEnd ? '#fff1f0' : '#f5f7ff'
  const badgeColor = isEnd ? '#ff7875' : '#5f95ff'
  const bodyLabel = isStart || isEnd ? '节点类型' : '节点说明'
  const bodyText =
    subtitle || (isStart ? '工作流开始节点' : isEnd ? '工作流结束节点' : '配置当前节点的执行内容')
  const bodyColor = hasIssue || executionState === 'failed' ? '#dc2626' : '#595959'

  const statusBg = hasIssue
    ? '#fff1f0'
    : executionState === 'failed'
      ? '#fff1f0'
      : executionState === 'running'
        ? '#fff7ed'
        : executionState === 'retry_waiting'
          ? '#fff7ed'
          : executionState === 'queued'
          ? '#eff6ff'
          : isDirty
            ? '#eff6ff'
            : executionState === 'success'
              ? '#ecfdf5'
              : 'transparent'

  const statusColor = hasIssue
    ? '#ef4444'
    : executionState === 'failed'
      ? '#ef4444'
      : executionState === 'running'
        ? '#b45309'
        : executionState === 'retry_waiting'
          ? '#c2410c'
          : executionState === 'queued'
          ? '#2563eb'
          : isDirty
            ? '#2563eb'
            : executionState === 'success'
              ? '#15803d'
              : '#2563eb'

  const statusMarkup = renderStatusMarkup({
    hasIssue,
    isDirty,
    executionState,
    executionCount,
    isExecutionEntry,
    statusBg,
    statusColor
  })

  const nodeShadow = hasIssue
    ? '0 0 0 1px rgba(239,68,68,.08)'
    : executionState === 'failed'
      ? '0 0 0 2px rgba(239,68,68,.14), 0 14px 28px rgba(239,68,68,.14)'
      : executionState === 'running'
        ? '0 0 0 2px rgba(245,158,11,.12), 0 14px 28px rgba(245,158,11,.12)'
        : executionState === 'retry_waiting'
          ? '0 0 0 2px rgba(251,146,60,.12), 0 14px 28px rgba(251,146,60,.12)'
          : executionState === 'queued'
          ? '0 0 0 2px rgba(96,165,250,.12), 0 14px 28px rgba(96,165,250,.12)'
          : isSelected
            ? '0 0 0 3px rgba(37,99,235,.22), 0 16px 32px rgba(37,99,235,.16)'
            : executionState === 'success'
              ? '0 0 0 2px rgba(34,197,94,.12), 0 14px 28px rgba(34,197,94,.12)'
              : isDirty
                ? '0 0 0 1px rgba(95,149,255,.08)'
                : 'none'

  return `
    <div class="workflow-node-card-root" style="position:relative;width:260px;height:96px;box-sizing:border-box;border:1.5px solid ${borderColor};border-radius:12px;background:#ffffff;overflow:hidden;font-family:Inter,'PingFang SC','Microsoft YaHei',sans-serif;box-shadow:${nodeShadow};transition:border-color .18s ease, box-shadow .18s ease, opacity .18s ease;transform:translateY(0);opacity:${isDimmed ? '0.82' : '1'};">
      <div style="display:flex;flex-direction:column;gap:8px;padding:12px;box-sizing:border-box;width:100%;height:100%;">
        <div style="display:flex;align-items:center;gap:12px;min-width:0;">
          <div style="flex:0 0 auto;width:32px;height:32px;border-radius:8px;background:${iconBg};color:${iconColor};display:flex;align-items:center;justify-content:center;font-size:12px;font-weight:700;line-height:1;">${iconText}</div>
          <div style="min-width:0;flex:1;font-size:16px;line-height:20px;font-weight:600;color:#141414;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">${title}</div>
          <div style="flex:0 0 auto;font-size:12px;line-height:16px;color:${badgeColor};background:${badgeBg};border-radius:8px;padding:2px 8px;white-space:nowrap;">${badgeText}</div>
        </div>
        <div style="display:flex;align-items:flex-start;gap:8px;min-width:0;flex:1;">
          <span style="flex:0 0 auto;font-size:12px;line-height:18px;color:#8c8c8c;white-space:nowrap;">${bodyLabel}</span>
          <div style="min-width:0;flex:1;font-size:13px;line-height:18px;color:${bodyColor};display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;">${bodyText}</div>
        </div>
        <div style="display:flex;justify-content:flex-end;min-height:18px;">
          ${statusMarkup}
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
