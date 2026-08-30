/**
 * 工作流编辑器辅助模块：useWorkflowStencil。
 *
 * 左侧「节点物料」面板的全部逻辑：注册物料卡片形状、创建 X6 Stencil、把物料装进去、
 * 点击物料往画布中央插一个节点、以及那条自绘的滚动条。
 *
 * 抽成 composable 是因为这些东西自成一体 —— 除了「往哪张画布插节点」之外，
 * 跟画布的选中、连线、右键菜单都没有关系。留在 WorkflowCanvas.vue 里只是让那个文件更长。
 */
import { Graph, Stencil } from '@antv/x6'
import type { CSSProperties, Ref, ShallowRef } from 'vue'
import { createStencilNode } from '../workflow-editor.mapper'
import type { WorkflowMaterialDropPayload, WorkflowMaterialGroup } from '../types'

const STENCIL_GRAPH_WIDTH = 232
const STENCIL_COLUMN_WIDTH = 216
const STENCIL_CARD_WIDTH = 208
const STENCIL_CARD_HEIGHT = 66
const STENCIL_ROW_HEIGHT = 88
const STENCIL_SCROLLBAR_TOP_INSET = 6
const STENCIL_SCROLLBAR_BOTTOM_INSET = 10

let stencilShapeRegistered = false

/** 注册物料面板里那张小卡片的形状（图标 + 标题 + 描述）。 */
export const ensureStencilShapeRegistered = () => {
  if (stencilShapeRegistered) return

  Graph.registerNode(
    'workflow-stencil-card',
    {
      inherit: 'rect',
      width: STENCIL_CARD_WIDTH,
      height: STENCIL_CARD_HEIGHT,
      markup: [
        { tagName: 'rect', selector: 'body' },
        { tagName: 'rect', selector: 'iconRect' },
        { tagName: 'text', selector: 'iconLabel' },
        { tagName: 'text', selector: 'title' },
        { tagName: 'text', selector: 'desc' }
      ],
      attrs: {
        body: { stroke: '#5f95ff', strokeWidth: 1, fill: '#fff', rx: 8, ry: 8 },
        iconRect: { width: 32, height: 32, rx: 8, ry: 8, refX: 12, refY: 17, fill: '#f0f5ff' },
        iconLabel: {
          refX: 28,
          refY: 33,
          textAnchor: 'middle',
          textVerticalAnchor: 'middle',
          fontSize: 12,
          fontWeight: 600,
          fill: '#1d39c4'
        },
        title: {
          refX: 56,
          refY: 24,
          textAnchor: 'start',
          textVerticalAnchor: 'middle',
          fontSize: 14,
          fontWeight: 600,
          fill: '#141414',
          textWrap: { width: 140, height: 20, ellipsis: '…' }
        },
        desc: {
          refX: 56,
          refY: 43,
          textAnchor: 'start',
          textVerticalAnchor: 'middle',
          fontSize: 12,
          fill: 'rgba(0,0,0,0.65)',
          textWrap: { width: 140, height: 28, ellipsis: '…' }
        }
      }
    },
    true
  )

  stencilShapeRegistered = true
}

interface StencilOptions {
  /** 物料面板挂载的容器。 */
  stencilRef: Ref<HTMLDivElement | null>
  /** 目标画布，点击物料时往它中央插节点。 */
  graphInstance: ShallowRef<Graph | null>
  /** 物料分组（后端节点定义映射来的）。 */
  materialGroups: () => WorkflowMaterialGroup[]
  /** 面板当前是否可见，隐藏时不必算滚动条。 */
  materialsVisible: () => boolean
  /** 画布可视区中心的屏幕坐标，用来决定新节点插在哪。 */
  viewportCenter: () => { x: number; y: number }
  /** 点击物料后通知外部插入节点。 */
  onInsert: (payload: WorkflowMaterialDropPayload) => void
}

export function useWorkflowStencil(options: StencilOptions) {
  const stencilInstance = shallowRef<Stencil | null>(null)
  const stencilScrollContainer = shallowRef<HTMLElement | null>(null)
  let detachScrollSync: (() => void) | null = null
  // 连续点击插入时依次错开一点位置，免得新节点完全叠在一起。
  let insertionCount = 0

  const scrollbar = reactive({ visible: false, thumbTop: 0, thumbHeight: 0 })
  const thumbStyle = computed<CSSProperties>(() => ({
    top: `${scrollbar.thumbTop}px`,
    height: `${scrollbar.thumbHeight}px`
  }))
  const materialCount = computed(() =>
    options.materialGroups().reduce((total, group) => total + group.items.length, 0)
  )

  const loadStencil = () => {
    const stencil = stencilInstance.value
    if (!stencil) return
    options.materialGroups().forEach((group) => {
      const nodes = group.items.map((item) => createStencilNode(item))
      stencil.load(nodes as any, group.key)
    })
  }

  /** 按内容/视口比例算出滚动条滑块的位置和高度。 */
  const updateScrollbar = () => {
    const container = stencilScrollContainer.value
    if (!container || !options.materialsVisible()) {
      scrollbar.visible = false
      return
    }

    const viewportHeight = container.clientHeight
    const contentHeight = container.scrollHeight
    const trackHeight = Math.max(
      0,
      viewportHeight - STENCIL_SCROLLBAR_TOP_INSET - STENCIL_SCROLLBAR_BOTTOM_INSET
    )

    if (contentHeight <= viewportHeight + 1 || viewportHeight <= 0 || trackHeight <= 0) {
      scrollbar.visible = false
      return
    }

    const thumbHeight = Math.max(36, Math.round((trackHeight * viewportHeight) / contentHeight))
    const maxThumbOffset = Math.max(0, trackHeight - thumbHeight)
    const maxScrollTop = Math.max(1, contentHeight - viewportHeight)

    scrollbar.visible = true
    scrollbar.thumbHeight = thumbHeight
    scrollbar.thumbTop = Math.round((container.scrollTop / maxScrollTop) * maxThumbOffset)
  }

  const bindScrollbar = () => {
    detachScrollSync?.()
    detachScrollSync = null
    stencilScrollContainer.value = null

    const container = options.stencilRef.value?.querySelector(
      '.x6-widget-stencil-content'
    ) as HTMLElement | null
    if (!container) {
      scrollbar.visible = false
      return
    }
    stencilScrollContainer.value = container

    const sync = () => window.requestAnimationFrame(updateScrollbar)
    const resizeObserver = new ResizeObserver(sync)
    resizeObserver.observe(container)
    if (options.stencilRef.value) {
      resizeObserver.observe(options.stencilRef.value)
    }
    container.addEventListener('scroll', sync, { passive: true })
    window.addEventListener('resize', sync)
    sync()

    detachScrollSync = () => {
      container.removeEventListener('scroll', sync)
      window.removeEventListener('resize', sync)
      resizeObserver.disconnect()
    }
  }

  /** 物料面板里每个分组各是一张小画布，点击它上面的卡片就往主画布插一个节点。 */
  const bindClickInsert = (stencil: Stencil) => {
    const graphs = Object.values(((stencil as any).graphs || {}) as Record<string, Graph>)
    graphs.forEach((groupGraph) => {
      groupGraph.on('node:click', ({ node }) => {
        const data = node.getData() || {}
        const typeCode = String(data.stencilTypeCode || '')
        const graph = options.graphInstance.value
        if (!typeCode || !graph) return
        const centerPoint = graph.clientToLocal(options.viewportCenter()) as {
          x: number
          y: number
        }
        const offset = insertionCount * 24
        insertionCount = (insertionCount + 1) % 6
        options.onInsert({
          typeCode,
          title: String(data.stencilTitle || ''),
          color: String(data.color || ''),
          presetConfig: data.stencilPresetConfig,
          presetSubtitle: String(data.stencilPresetSubtitle || ''),
          source: 'click',
          position: { x: centerPoint.x + offset, y: centerPoint.y + offset }
        })
      })
    })
  }

  const createStencil = () => {
    const groups = options.materialGroups()
    if (!options.graphInstance.value || !options.stencilRef.value || !groups.length) return
    ;(stencilInstance.value as any)?.dispose?.()
    options.stencilRef.value.innerHTML = ''

    const stencil = new Stencil({
      title: '节点物料',
      target: options.graphInstance.value,
      stencilGraphWidth: STENCIL_GRAPH_WIDTH,
      stencilGraphHeight: 480,
      stencilGraphOptions: { async: false, panning: true, interacting: false },
      collapsable: false,
      groups: groups.map((group) => ({
        title: group.title,
        name: group.key,
        collapsable: true,
        collapsed: false,
        graphHeight: Math.max(STENCIL_ROW_HEIGHT, group.items.length * STENCIL_ROW_HEIGHT),
        layoutOptions: { rowHeight: STENCIL_ROW_HEIGHT }
      })),
      layoutOptions: {
        columns: 1,
        columnWidth: STENCIL_COLUMN_WIDTH,
        rowHeight: STENCIL_ROW_HEIGHT,
        dx: 6,
        dy: 0
      }
    })

    options.stencilRef.value.appendChild(stencil.container)
    stencilInstance.value = stencil
    bindClickInsert(stencil)
    loadStencil()
    nextTick(bindScrollbar)
  }

  const destroyStencil = () => {
    detachScrollSync?.()
    detachScrollSync = null
    ;(stencilInstance.value as any)?.dispose?.()
    stencilInstance.value = null
    stencilScrollContainer.value = null
  }

  return {
    stencilInstance,
    scrollbar,
    thumbStyle,
    materialCount,
    createStencil,
    loadStencil,
    updateScrollbar,
    destroyStencil
  }
}
