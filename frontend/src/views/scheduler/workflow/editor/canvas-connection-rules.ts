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
    if (graphKindOfCell(targetCell) === 'start') return false
    if (graphKindOfCell(sourceCell) === 'terminal') return false

    const graph = getGraph()
    if (wouldIntroduceCycle(graph, sourceCell.id, targetCell.id, edge?.id, getDraftEdgeId()))
      return false

    const allowedPorts = outPortsOfCell(sourceCell)
    if (!allowedPorts.includes(sourcePort)) return false

    // 分支与循环的每个出口只允许接一条边；普通节点的 out 端口可以扇出多条。
    const singleUsePort =
      graphKindOfCell(sourceCell) === 'branch' || graphKindOfCell(sourceCell) === 'loop'
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
  if (connected) return { stroke: '#5f95ff', fill: '#5f95ff', label: '#2563eb' }
  const palette: Record<string, { stroke: string; fill: string; label: string }> = {
    true: { stroke: '#22c55e', fill: '#ffffff', label: '#15803d' },
    false: { stroke: '#ef4444', fill: '#ffffff', label: '#b91c1c' },
    body: { stroke: '#ca8a04', fill: '#ffffff', label: '#a16207' },
    [LOOP_NEXT_BRANCH]: { stroke: '#0ea5e9', fill: '#ffffff', label: '#0369a1' }
  }
  return palette[portId] || { stroke: '#c2c8d5', fill: '#ffffff', label: '#64748b' }
}
