/**
 * 工作流编辑器辅助模块：canvas-connection-rules。
 *
 * 画布上「这条线能不能连」的全部规则。原来这些判断散在 WorkflowCanvas.vue 里，
 * 而且把节点类型编码写死了（condition.branch 只认 true/false、foreach 只认 body），
 * 后端加一种分支节点、或者给 foreach 加了 NEXT 出口，画布这边都连不出来。
 *
 * 现在规则按后端下发的图语义（见 node-registry）来判断，与校验器/引擎保持同一套语义：
 *   - 起始节点不能作为终点，终止节点不能作为源；
 *   - 只能连到 in 端口，不能自环，不能成环（当前模型只支持 DAG）；
 *   - 分支节点的每个声明分支各自最多一条出边；
 *   - 循环节点的 BODY / NEXT 各自最多一条出边；
 *   - 普通节点只有一个 out 端口。
 *
 * 注意「多入边（汇聚 join）」是允许的：引擎会等所有活跃分支到齐再把该节点跑一次。
 * 早先这里限制「只有多个开始节点才能汇聚」，导致正常的 join 图在画布上根本画不出来。
 */
import type { Graph } from '@antv/x6'
import { LOOP_NEXT_BRANCH, getNodeBranches, getNodeGraphKind } from './node-registry'

const cellTypeCode = (cell: any) => String(cell?.getData?.()?.typeCode || '')
const MARKET_SIGNAL_NODE_TYPE = 'official.quant.market_signal'
const BACKTEST_START_NODE_TYPE = 'official.quant.backtest_start'
const INDICATOR_CONDITION_TYPES = new Set([
  'official.quant.volume_spike_condition',
  'official.quant.price_change_condition',
  'official.quant.macd_condition',
  'official.quant.kdj_condition',
  'official.quant.rsi_condition',
  'official.quant.bollinger_condition'
])

export const graphKindOfCell = (cell: any) => getNodeGraphKind(cellTypeCode(cell))

/** 某个出口端口是否已经被占用（同一个分支/循环出口只允许一条边）。 */
export const isSourcePortOccupied = (
  graph: Graph | null,
  cellId: string,
  portId: string,
  skipEdgeId?: string | null
) =>
  (graph?.getOutgoingEdges(cellId) || []).some(
    (edge) => edge.id !== skipEdgeId && edge.getSourcePortId() === portId
  )

/**
 * 连上这条线会不会成环。从终点出发沿现有边往下走，能走回起点就说明成环。
 * skipEdgeId / draftEdgeId 用来排除「正在被改的那条边」和「还没落地的草稿边」。
 */
export const wouldIntroduceCycle = (
  graph: Graph | null,
  sourceCellId: string,
  targetCellId: string,
  skipEdgeId?: string | null,
  draftEdgeId?: string | null
) => {
  if (!graph) return false

  const adjacency = new Map<string, string[]>()
  graph.getNodes().forEach((node) => adjacency.set(node.id, []))
  graph.getEdges().forEach((edge) => {
    if (edge.id === skipEdgeId || edge.id === draftEdgeId) return
    const sourceId = edge.getSourceCellId()
    const targetId = edge.getTargetCellId()
    if (!sourceId || !targetId) return
    adjacency.get(sourceId)?.push(targetId)
  })

  const stack = [targetCellId]
  const visited = new Set<string>()
  while (stack.length) {
    const currentId = stack.pop() as string
    if (currentId === sourceCellId) return true
    if (visited.has(currentId)) continue
    visited.add(currentId)
    ;(adjacency.get(currentId) || []).forEach((nextId) => stack.push(nextId))
  }
  return false
}

/** 某种节点合法的出口端口清单。 */
export const outPortsOfCell = (cell: any): string[] => {
  if (cellTypeCode(cell) === BACKTEST_START_NODE_TYPE) return getNodeBranches(cellTypeCode(cell))
  switch (graphKindOfCell(cell)) {
    case 'branch':
      return getNodeBranches(cellTypeCode(cell))
    case 'loop':
      return ['body', LOOP_NEXT_BRANCH]
    case 'terminal':
      return []
    default:
      return ['out']
  }
}

export interface ConnectionArgs {
  sourceCell?: any
  targetCell?: any
  sourcePort?: string | null
  targetPort?: string | null
  edge?: { id?: string } | null
}

export const createConnectionValidator =
  (getGraph: () => Graph | null, getDraftEdgeId: () => string | null | undefined) =>
  (args: ConnectionArgs): boolean => {
    const { sourceCell, targetCell, sourcePort, targetPort, edge } = args
    if (!sourceCell || !targetCell || !sourcePort || !targetPort) return false
    if (sourceCell.id === targetCell.id) return false
    if (targetPort !== 'in') return false
    if (
      graphKindOfCell(targetCell) === 'start' ||
      cellTypeCode(targetCell) === BACKTEST_START_NODE_TYPE
    )
      return false
    if (graphKindOfCell(sourceCell) === 'terminal') return false

    const graph = getGraph()
    if (cellTypeCode(targetCell) === MARKET_SIGNAL_NODE_TYPE) {
      if (!INDICATOR_CONDITION_TYPES.has(cellTypeCode(sourceCell)) || sourcePort !== 'true') {
        return false
      }
      const occupied = (graph?.getIncomingEdges(targetCell.id) || []).some(
        (incoming) => incoming.id !== edge?.id && incoming.id !== getDraftEdgeId()
      )
      if (occupied) return false
    }
    if (wouldIntroduceCycle(graph, sourceCell.id, targetCell.id, edge?.id, getDraftEdgeId()))
      return false

    const allowedPorts = outPortsOfCell(sourceCell)
    if (!allowedPorts.includes(sourcePort)) return false

    // 循环出口只允许接一条边；判断分支和普通节点都允许扇出多条。
    const singleUsePort = graphKindOfCell(sourceCell) === 'loop'
    if (singleUsePort && isSourcePortOccupied(graph, sourceCell.id, sourcePort, edge?.id))
      return false

    return true
  }

export const validateMagnet = ({ magnet }: { magnet: Element | null }) => {
  if (!magnet) return false
  return magnet.getAttribute('magnet') !== 'passive'
}

/** 端口小圆点的配色：已连上统一走主色，未连上按语义给色。 */
export const getPortDisplayColor = (portId: string, connected: boolean) => {
  if (connected)
    return {
      stroke: 'var(--theme-color, #5d87ff)',
      fill: 'var(--theme-color, #5d87ff)',
      label: 'var(--theme-color, #5d87ff)'
    }
  const palette: Record<string, { stroke: string; fill: string; label: string }> = {
    true: {
      stroke: 'var(--el-color-success, #67c23a)',
      fill: 'var(--workflow-panel-bg, #fff)',
      label: 'var(--el-color-success, #67c23a)'
    },
    false: {
      stroke: 'var(--el-color-danger, #f56c6c)',
      fill: 'var(--workflow-panel-bg, #fff)',
      label: 'var(--el-color-danger, #f56c6c)'
    },
    body: {
      stroke: 'var(--el-color-warning, #e6a23c)',
      fill: 'var(--workflow-panel-bg, #fff)',
      label: 'var(--el-color-warning, #e6a23c)'
    },
    [LOOP_NEXT_BRANCH]: {
      stroke: 'var(--el-color-info, #909399)',
      fill: 'var(--workflow-panel-bg, #fff)',
      label: 'var(--el-color-info, #909399)'
    }
  }
  return (
    palette[portId] || {
      stroke: 'var(--workflow-edge-color, #98a4b6)',
      fill: 'var(--workflow-panel-bg, #fff)',
      label: 'var(--workflow-panel-muted, #78859a)'
    }
  )
}
