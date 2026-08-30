/**
 * 工作流编辑器辅助模块：useCanvasContextMenu。
 *
 * 画布上节点与连线的右键菜单：菜单项、显示/隐藏、点空白处收起、以及把选中的动作抛给外部。
 *
 * 抽成 composable 的理由和物料面板一样 —— 它只依赖「哪个 cell 被右键了」，
 * 和画布的连线校验、缩放、选中高亮都无关，混在 2000 行的组件里只是噪音。
 */
import type { CSSProperties, Ref } from 'vue'
import type ArtMenuRight from '@/components/core/others/art-menu-right/index.vue'
import type { MenuItemType } from '@/components/core/others/art-menu-right/index.vue'
import type { WorkflowDomainNode, WorkflowNodeContextActionPayload } from '../types'

type MenuRef = Ref<InstanceType<typeof ArtMenuRight> | null>

interface ContextMenuOptions {
  nodeMenuRef: MenuRef
  edgeMenuRef: MenuRef
  /** 画布外壳，用来把菜单位置夹在可视区内。 */
  shellRef: Ref<HTMLElement | null>
  /** 当前图上的节点，用来显示被右键那个节点的名字。 */
  nodes: () => WorkflowDomainNode[]
  onNodeAction: (payload: WorkflowNodeContextActionPayload) => void
  onEdgeEdit: (cellId: string) => void
  onActivateCell: (cellId: string, cellType: 'edge') => void
}

export function useCanvasContextMenu(options: ContextMenuOptions) {
  const nodeMenu = reactive({
    visible: false,
    cellId: null as string | null,
    x: 0,
    y: 0
  })
  const edgeMenuCellId = ref<string | null>(null)

  const nodeMenuItems = computed<MenuItemType[]>(() => [
    { key: 'edit', label: '编辑属性', icon: 'ri:edit-line' },
    { key: 'delete', label: '删除节点', icon: 'ri:delete-bin-line' }
  ])
  const edgeMenuItems = computed<MenuItemType[]>(() => [
    { key: 'edit', label: '编辑属性', icon: 'ri:edit-line' }
  ])

  const nodeMenuNode = computed<WorkflowDomainNode | null>(
    () => options.nodes().find((node) => node.id === nodeMenu.cellId) || null
  )

  /** 菜单位置夹在画布可视区里，避免贴边时被裁掉一半。 */
  const nodeMenuStyle = computed<CSSProperties | null>(() => {
    if (!nodeMenu.visible || !options.shellRef.value) return null
    const menuWidth = 196
    const menuHeight = 118
    const shellWidth = options.shellRef.value.clientWidth
    const shellHeight = options.shellRef.value.clientHeight
    return {
      left: `${Math.max(12, Math.min(nodeMenu.x, shellWidth - menuWidth - 12))}px`,
      top: `${Math.max(12, Math.min(nodeMenu.y, shellHeight - menuHeight - 12))}px`
    }
  })

  const hideNodeMenu = () => {
    nodeMenu.visible = false
    nodeMenu.cellId = null
    options.nodeMenuRef.value?.hide()
  }

  const hideEdgeMenu = () => {
    edgeMenuCellId.value = null
    options.edgeMenuRef.value?.hide()
  }

  const hideAll = () => {
    hideNodeMenu()
    hideEdgeMenu()
  }

  const boundEventToCanvas = (event: MouseEvent, menuHeight: number) => {
    const rect = options.shellRef.value?.getBoundingClientRect()
    if (!rect) return event
    const margin = 8
    return new MouseEvent('contextmenu', {
      clientX: Math.max(rect.left + margin, Math.min(event.clientX, rect.right - 144 - margin)),
      clientY: Math.max(rect.top + margin, Math.min(event.clientY, rect.bottom - menuHeight - margin))
    })
  }

  const openNodeMenu = (cellId: string, event: MouseEvent) => {
    nodeMenu.visible = false
    nodeMenu.cellId = cellId
    options.nodeMenuRef.value?.show(boundEventToCanvas(event, 74))
  }

  const openEdgeMenu = (cellId: string, event: MouseEvent) => {
    edgeMenuCellId.value = cellId
    options.onActivateCell(cellId, 'edge')
    options.edgeMenuRef.value?.show(boundEventToCanvas(event, 42))
  }

  const emitNodeAction = (action: WorkflowNodeContextActionPayload['action']) => {
    if (!nodeMenu.cellId) return
    options.onNodeAction({ cellId: nodeMenu.cellId, action })
    hideNodeMenu()
  }

  /** 点到菜单以外的地方就收起（点菜单自己不算）。 */
  const handleWindowPointerDown = (event: PointerEvent) => {
    const target = event.target as HTMLElement | null
    if (target?.closest('.context-menu')) return
    hideAll()
  }

  const handleNodeMenuSelect = (item: MenuItemType) => {
    if (item.key === 'edit' || item.key === 'delete') emitNodeAction(item.key)
  }

  const handleEdgeMenuSelect = (item: MenuItemType) => {
    if (item.key !== 'edit' || !edgeMenuCellId.value) return
    options.onEdgeEdit(edgeMenuCellId.value)
    hideEdgeMenu()
  }

  return {
    nodeMenu,
    nodeMenuItems,
    edgeMenuItems,
    nodeMenuNode,
    nodeMenuStyle,
    openNodeMenu,
    openEdgeMenu,
    hideNodeMenu,
    hideEdgeMenu,
    hideAll,
    emitNodeAction,
    handleWindowPointerDown,
    handleNodeMenuSelect,
    handleEdgeMenuSelect
  }
}
