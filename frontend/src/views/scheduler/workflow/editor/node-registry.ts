/**
 * 工作流编辑器辅助模块：node-registry。
 *
 * 后端 nodes.go 的节点注册表在前端的镜像。后端 `GET /node-definitions` 会下发每种节点的
 * typeCode / label / configSchema / kind / branches，其中 kind 与 branches 是「图语义」——
 * 决定这个节点在画布上有几个出口、出口叫什么、校验时按哪套规则查。
 *
 * 有了它，画布、mapper、校验器就不用各自再维护一份「哪些类型是开始节点 / 哪个类型有 true-false 分支」
 * 的硬编码名单：后端加一种节点，前端跟着走。
 *
 * 加载时机：编辑器页与执行详情页拿到 node definitions 后各调一次 syncNodeDefinitions。
 */
import type { WorkflowNodeDefinitionItem, WorkflowNodeGraphKind } from '@/api/scheduler'

let registry = new Map<string, WorkflowNodeDefinitionItem>()

/** 用后端下发的节点定义刷新本地镜像。 */
export function syncNodeDefinitions(definitions: WorkflowNodeDefinitionItem[]) {
  registry = new Map((definitions || []).map((item) => [item.typeCode, item]))
}

export function getNodeDefinition(typeCode: string) {
  return registry.get(typeCode)
}

/**
 * 兜底推断：后端还没下发 kind 时（老版本后端 / 定义没加载完）按类型编码猜一个。
 * 正常路径永远走后端下发的值，这里只是别让画布在数据没到位时崩掉。
 */
const inferGraphKind = (typeCode: string): WorkflowNodeGraphKind => {
  if (typeCode.startsWith('start.')) return 'start'
  if (typeCode === 'end') return 'terminal'
  if (typeCode === 'condition.branch') return 'branch'
  if (typeCode === 'foreach') return 'loop'
  return 'plain'
}

export function getNodeGraphKind(typeCode: string): WorkflowNodeGraphKind {
  return registry.get(typeCode)?.kind || inferGraphKind(typeCode)
}

/**
 * 分支节点该有哪些分支键；非分支节点返回空数组。
 *
 * 静态声明的直接用 branches；声明了 branchesConfigKey 的（多路 switch）从节点自己的
 * config 里那个数组逐项取 key，再补上 extraBranches。解析规则与后端
 * workflowNodeDefinition.resolveBranches 一致 —— 两边都只依赖后端下发的这份声明。
 */
export function getNodeBranches(typeCode: string, config?: Record<string, any>): string[] {
  const definition = registry.get(typeCode)
  const configKey = definition?.branchesConfigKey
  if (configKey) {
    const branches: string[] = []
    const push = (key: unknown) => {
      const text = String(key ?? '').trim()
      if (text && !branches.includes(text)) branches.push(text)
    }
    const items = Array.isArray(config?.[configKey]) ? (config?.[configKey] as any[]) : []
    items.forEach((item) => push(item?.key))
    ;(definition?.extraBranches || []).forEach(push)
    return branches
  }
  if (definition?.branches?.length) return definition.branches
  return getNodeGraphKind(typeCode) === 'branch' ? ['true', 'false'] : []
}

/** 这种节点的分支是否随配置变化（配置改了要重建端口）。 */
export const hasDynamicBranches = (typeCode: string) =>
  Boolean(registry.get(typeCode)?.branchesConfigKey)

export function getNodeConfigSchema(typeCode: string): Record<string, any> {
  return registry.get(typeCode)?.configSchema || {}
}

export function getNodeUISchema(typeCode: string): Record<string, any> {
  return registry.get(typeCode)?.uiSchema || {}
}

export const isStartTypeCode = (typeCode: string) => getNodeGraphKind(typeCode) === 'start'
export const isBranchTypeCode = (typeCode: string) => getNodeGraphKind(typeCode) === 'branch'
export const isLoopTypeCode = (typeCode: string) => getNodeGraphKind(typeCode) === 'loop'
export const isTerminalTypeCode = (typeCode: string) => getNodeGraphKind(typeCode) === 'terminal'

/** 循环节点「跑完之后继续」那条出边的分支键，与后端 loopNextBranch 保持一致。 */
export const LOOP_NEXT_BRANCH = 'next'
