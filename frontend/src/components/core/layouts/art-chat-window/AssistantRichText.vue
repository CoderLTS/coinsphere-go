<!-- 通用组件：art-chat-window/AssistantRichText。 -->
<template>
  <div
    ref="containerRef"
    class="assistant-rich-text markdown-body"
    v-html="renderedHtml"
    @click="handleActionClick"
  ></div>
</template>

<script setup lang="ts">
  import { computed, nextTick, ref, watch } from 'vue'
  import { ElMessage } from 'element-plus'
  import MarkdownIt from 'markdown-it'
  import hljs from 'highlight.js'
  import mermaid from 'mermaid'
  import '@styles/core/md.scss'

  defineOptions({ name: 'AssistantRichText' })

  interface MarkdownRenderResult {
    html: string
    mermaidBlocks: string[]
  }

  const props = withDefaults(
    defineProps<{
      content?: string
      streaming?: boolean
    }>(),
    {
      content: '',
      streaming: false
    }
  )

  const containerRef = ref<HTMLElement | null>(null)
  const renderState = ref<MarkdownRenderResult>({ html: '', mermaidBlocks: [] })
  const componentUid = `assistant-rt-${Math.random().toString(36).slice(2, 10)}`

  let renderSequence = 0
  let mermaidParseHandlerBound = false

  const escapeHtml = (value: string) =>
    value
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#39;')

  const buildMermaidToolbar = (blockIndex: number, allowExport: boolean) =>
    [
      '<div class="assistant-mermaid__toolbar">',
      allowExport
        ? `<button type="button" class="assistant-mermaid__action" data-mermaid-action="export" data-mermaid-index="${blockIndex}">\u5bfc\u51fa\u56fe\u7247</button>`
        : '',
      `<button type="button" class="assistant-mermaid__action" data-mermaid-action="copy" data-mermaid-index="${blockIndex}">\u590d\u5236\u4ee3\u7801</button>`,
      '</div>'
    ].join('')

  const buildMermaidLoading = (label = '\u6b63\u5728\u751f\u6210 Mermaid \u56fe...') =>
    [
      '<div class="assistant-mermaid__loading">',
      '<div class="assistant-mermaid__loading-dots">',
      '<span></span>',
      '<span></span>',
      '<span></span>',
      '</div>',
      `<em>${label}</em>`,
      '</div>'
    ].join('')

  const buildMermaidFallback = (source: string) =>
    [
      '<div class="assistant-mermaid__fallback">',
      '<div class="assistant-mermaid__fallback-tip">Mermaid \u6e32\u67d3\u5931\u8d25\uff0c\u5df2\u5207\u6362\u4e3a\u6e90\u7801\u5c55\u793a\u3002</div>',
      `<pre><code class="language-mermaid">${escapeHtml(source)}</code></pre>`,
      '</div>'
    ].join('')

  const createMermaidPanel = (blockIndex: number, allowExport: boolean) => {
    const panel = document.createElement('div')
    panel.className = 'assistant-mermaid__panel'
    panel.dataset.mermaidIndex = String(blockIndex)
    panel.innerHTML = `${buildMermaidToolbar(blockIndex, allowExport)}<div class="assistant-mermaid__canvas"></div>`

    const canvas = panel.querySelector<HTMLElement>('.assistant-mermaid__canvas')
    if (!canvas) {
      throw new Error('Mermaid canvas not created')
    }

    return { panel, canvas }
  }

  const SVG_NAMESPACE = 'http://www.w3.org/2000/svg'

  const injectMermaidThemeStyle = (svgElement: SVGSVGElement) => {
    const style = document.createElementNS(SVG_NAMESPACE, 'style')
    style.textContent = `
      svg { background: #ffffff !important; }
      .rect,
      rect.rect {
        fill: #ffffff !important;
        stroke: transparent !important;
      }
      text, tspan, foreignObject, .messageText, .labelText, .loopText, .noteText, .actor > text {
        fill: #0f172a !important;
        color: #0f172a !important;
        stroke: none !important;
      }
      .actor, .actor-top, .actor-bottom, .labelBox, .labelBox rect, .sequenceNumber {
        fill: #ffffff !important;
        stroke: #94a3b8 !important;
      }
      .note, .note rect {
        fill: #fff7ed !important;
        stroke: #fdba74 !important;
      }
      .activation0, .activation1, .activation2 {
        fill: #e2e8f0 !important;
        stroke: #94a3b8 !important;
      }
      .actor-line, .messageLine0, .messageLine1, .loopLine, .signalLine0, .signalLine1, line {
        stroke: #475569 !important;
      }
      polygon, marker path {
        fill: #475569 !important;
        stroke: #475569 !important;
      }
      .labelBox, .labelBox rect, .actor {
        rx: 12px !important;
        ry: 12px !important;
      }
    `
    svgElement.insertBefore(style, svgElement.firstChild)
  }

  const decorateMermaidSvg = (svgElement: SVGSVGElement) => {
    svgElement.classList.add('assistant-mermaid__svg')
    svgElement.removeAttribute('width')
    svgElement.removeAttribute('height')
    svgElement.style.background = '#ffffff'
    svgElement.style.color = '#0f172a'
    injectMermaidThemeStyle(svgElement)

    Array.from(svgElement.querySelectorAll<SVGGraphicsElement>('.rect')).forEach((node) => {
      node.setAttribute('fill', '#ffffff')
      node.setAttribute('stroke', 'transparent')
    })
  }

  const getMermaidConfig = () => ({
    startOnLoad: false,
    securityLevel: 'loose' as const,
    theme: 'base' as const,
    suppressErrorRendering: true,
    fontFamily: 'inherit',
    themeVariables: {
      background: '#ffffff',
      textColor: '#0f172a',
      lineColor: '#475569',
      primaryColor: '#eef4ff',
      primaryTextColor: '#0f172a',
      primaryBorderColor: '#7c9cff',
      secondaryColor: '#f8fafc',
      secondaryTextColor: '#0f172a',
      secondaryBorderColor: '#cbd5e1',
      tertiaryColor: '#f8fafc',
      tertiaryTextColor: '#0f172a',
      tertiaryBorderColor: '#cbd5e1',
      mainBkg: '#eef4ff',
      secondBkg: '#f8fafc',
      tertiaryBkg: '#ffffff',
      mainContrastColor: '#0f172a',
      clusterBkg: '#f8fafc',
      clusterBorder: '#cbd5e1',
      actorBkg: '#eef4ff',
      actorBorder: '#93c5fd',
      actorTextColor: '#0f172a',
      labelBoxBkgColor: '#ffffff',
      labelBoxBorderColor: '#cbd5e1',
      labelTextColor: '#0f172a',
      signalColor: '#475569',
      signalTextColor: '#0f172a',
      sequenceNumberColor: '#0f172a',
      noteBkgColor: '#fff7ed',
      noteBorderColor: '#fdba74',
      noteTextColor: '#0f172a',
      activationBorderColor: '#94a3b8',
      activationBkgColor: '#e2e8f0',
      sequenceNumberBkgColor: '#ffffff',
      actorLineColor: '#94a3b8'
    }
  })

  const maskIncompleteMermaidFence = (source: string) => {
    if (!props.streaming || !source.includes('```mermaid')) {
      return source
    }

    const startIndex = source.lastIndexOf('```mermaid')
    if (startIndex < 0) {
      return source
    }

    const endIndex = source.indexOf('```', startIndex + '```mermaid'.length)
    if (endIndex >= 0) {
      return source
    }

    return `${source.slice(0, startIndex)}\n\n\`\`\`mermaid\n%% streaming\n\`\`\``
  }

  const renderMarkdown = (source: string): MarkdownRenderResult => {
    const mermaidBlocks: string[] = []
    const markdown = new MarkdownIt({
      html: false,
      linkify: true,
      breaks: true,
      highlight(code: string, language: string) {
        const normalizedLanguage = language && hljs.getLanguage(language) ? language : ''
        const highlightedCode = normalizedLanguage
          ? hljs.highlight(code, { language: normalizedLanguage, ignoreIllegals: true }).value
          : hljs.highlightAuto(code).value
        const className = normalizedLanguage
          ? `hljs language-${normalizedLanguage}`
          : 'hljs language-plaintext'
        return `<pre class="assistant-rich-text__code"><code class="${className}">${highlightedCode}</code></pre>`
      }
    })

    const defaultLinkOpen =
      markdown.renderer.rules.link_open ??
      ((tokens: any[], index: number, options: any, _env: any, self: any) =>
        self.renderToken(tokens, index, options))

    markdown.renderer.rules.link_open = (
      tokens: any[],
      index: number,
      options: any,
      env: any,
      self: any
    ) => {
      tokens[index].attrSet('target', '_blank')
      tokens[index].attrSet('rel', 'noopener noreferrer')
      return defaultLinkOpen(tokens, index, options, env, self)
    }

    const defaultFence = markdown.renderer.rules.fence
    markdown.renderer.rules.fence = (
      tokens: any[],
      index: number,
      options: any,
      env: any,
      self: any
    ) => {
      const token = tokens[index]
      const language = token.info.trim().split(/\s+/)[0]

      if (language === 'mermaid') {
        if (props.streaming) {
          return `<div class="assistant-mermaid assistant-mermaid--pending"><div class="assistant-mermaid__panel"><div class="assistant-mermaid__canvas">${buildMermaidLoading()}</div></div></div>`
        }

        const blockIndex = mermaidBlocks.push(token.content.trim()) - 1
        return `<div class="assistant-mermaid" data-mermaid-index="${blockIndex}"></div>`
      }

      return defaultFence
        ? defaultFence(tokens, index, options, env, self)
        : self.renderToken(tokens, index, options)
    }

    return {
      html: markdown.render(maskIncompleteMermaidFence(source || '')),
      mermaidBlocks
    }
  }

  const renderMermaidBlocks = async () => {
    const container = containerRef.value
    if (!container || props.streaming) {
      return
    }

    mermaid.initialize(getMermaidConfig())
    if (!mermaidParseHandlerBound) {
      mermaid.setParseErrorHandler(() => {})
      mermaidParseHandlerBound = true
    }

    const currentSequence = ++renderSequence
    const blocks = Array.from(container.querySelectorAll<HTMLElement>('.assistant-mermaid[data-mermaid-index]'))

    for (const block of blocks) {
      const blockIndex = Number(block.dataset.mermaidIndex ?? '-1')
      const source = renderState.value.mermaidBlocks[blockIndex]
      if (!source) {
        continue
      }

      const loadingPanel = createMermaidPanel(blockIndex, false)
      loadingPanel.canvas.innerHTML = buildMermaidLoading('\u6b63\u5728\u6e32\u67d3 Mermaid \u56fe...')
      block.replaceChildren(loadingPanel.panel)

      try {
        const parseResult = await mermaid.parse(source, { suppressErrors: true })
        if (currentSequence !== renderSequence) {
          return
        }

        if (parseResult === false) {
          const fallbackPanel = createMermaidPanel(blockIndex, false)
          fallbackPanel.canvas.innerHTML = buildMermaidFallback(source)
          block.replaceChildren(fallbackPanel.panel)
          continue
        }

        const successPanel = createMermaidPanel(blockIndex, true)
        block.replaceChildren(successPanel.panel)
        const rendered = await mermaid.render(
          `assistant-mermaid-${componentUid}-${currentSequence}-${blockIndex}`,
          source
        )
        successPanel.canvas.innerHTML = rendered.svg
        rendered.bindFunctions?.(successPanel.canvas)

        if (currentSequence !== renderSequence) {
          return
        }

        const svgElement = successPanel.canvas.querySelector<SVGSVGElement>('svg')
        if (!svgElement) {
          throw new Error('Mermaid rendered without SVG output')
        }

        decorateMermaidSvg(svgElement)
      } catch (error) {
        if (currentSequence !== renderSequence) {
          return
        }

        const fallbackPanel = createMermaidPanel(blockIndex, false)
        fallbackPanel.canvas.innerHTML = buildMermaidFallback(source)
        block.replaceChildren(fallbackPanel.panel)
        console.warn('Mermaid render failed and was replaced with source code.', error)
      }
    }
  }

  const copyMermaidSource = async (blockIndex: number) => {
    const source = renderState.value.mermaidBlocks[blockIndex]
    if (!source) {
      return
    }

    try {
      await navigator.clipboard.writeText(source)
      ElMessage.success('\u004d\u0065\u0072\u006d\u0061\u0069\u0064\u0020\u4ee3\u7801\u5df2\u590d\u5236')
    } catch (error) {
      console.error('Copy Mermaid code failed.', error)
      ElMessage.error('\u590d\u5236\u0020\u004d\u0065\u0072\u006d\u0061\u0069\u0064\u0020\u4ee3\u7801\u5931\u8d25')
    }
  }

  const exportMermaidImage = async (panel: HTMLElement, blockIndex: number) => {
    const svg = panel.querySelector<SVGSVGElement>('svg')
    if (!svg) {
      ElMessage.warning('\u5f53\u524d\u0020\u004d\u0065\u0072\u006d\u0061\u0069\u0064\u0020\u56fe\u6682\u4e0d\u53ef\u5bfc\u51fa')
      return
    }

    const rect = svg.getBoundingClientRect()
    const viewBox = svg.viewBox.baseVal
    const width = Math.max(Math.ceil(rect.width), Math.ceil(viewBox?.width || 0), 320)
    const height = Math.max(Math.ceil(rect.height), Math.ceil(viewBox?.height || 0), 180)

    const clonedSvg = svg.cloneNode(true) as SVGSVGElement
    clonedSvg.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
    clonedSvg.setAttribute('xmlns:xlink', 'http://www.w3.org/1999/xlink')
    clonedSvg.setAttribute('width', `${width}`)
    clonedSvg.setAttribute('height', `${height}`)

    const svgMarkup = new XMLSerializer().serializeToString(clonedSvg)
    const blob = new Blob([svgMarkup], { type: 'image/svg+xml;charset=utf-8' })
    const url = URL.createObjectURL(blob)

    try {
      const image = await new Promise<HTMLImageElement>((resolve, reject) => {
        const img = new Image()
        img.onload = () => resolve(img)
        img.onerror = reject
        img.src = url
      })

      const canvas = document.createElement('canvas')
      const scale = window.devicePixelRatio > 1 ? 2 : 1
      canvas.width = width * scale
      canvas.height = height * scale

      const context = canvas.getContext('2d')
      if (!context) {
        throw new Error('Canvas context unavailable')
      }

      context.scale(scale, scale)
      context.fillStyle = '#ffffff'
      context.fillRect(0, 0, width, height)
      context.drawImage(image, 0, 0, width, height)

      const pngBlob = await new Promise<Blob | null>((resolve) => {
        canvas.toBlob((value) => resolve(value), 'image/png')
      })

      if (!pngBlob) {
        throw new Error('PNG blob unavailable')
      }

      const downloadUrl = URL.createObjectURL(pngBlob)
      const link = document.createElement('a')
      link.href = downloadUrl
      link.download = `mermaid-${blockIndex + 1}.png`
      link.click()
      URL.revokeObjectURL(downloadUrl)
      ElMessage.success('\u004d\u0065\u0072\u006d\u0061\u0069\u0064\u0020\u56fe\u7247\u5df2\u5bfc\u51fa')
    } catch (error) {
      console.error('Export Mermaid image failed.', error)
      ElMessage.error('\u5bfc\u51fa\u0020\u004d\u0065\u0072\u006d\u0061\u0069\u0064\u0020\u56fe\u7247\u5931\u8d25')
    } finally {
      URL.revokeObjectURL(url)
    }
  }

  const handleActionClick = async (event: MouseEvent) => {
    const target = event.target as HTMLElement | null
    const actionButton = target?.closest<HTMLElement>('[data-mermaid-action]')
    if (!actionButton) {
      return
    }

    event.preventDefault()
    event.stopPropagation()

    const blockIndex = Number(actionButton.dataset.mermaidIndex ?? '-1')
    if (blockIndex < 0) {
      return
    }

    const action = actionButton.dataset.mermaidAction
    if (action === 'copy') {
      await copyMermaidSource(blockIndex)
      return
    }

    if (action === 'export') {
      const panel = actionButton.closest<HTMLElement>('.assistant-mermaid__panel')
      if (panel) {
        await exportMermaidImage(panel, blockIndex)
      }
    }
  }

  const renderedHtml = computed(() => renderState.value.html)

  watch(
    () => [props.content, props.streaming] as const,
    ([value]) => {
      renderState.value = renderMarkdown(value || '')
    },
    {
      immediate: true
    }
  )

  watch(
    renderedHtml,
    () => {
      nextTick(() => {
        void renderMermaidBlocks()
      })
    },
    {
      immediate: true
    }
  )
</script>

<style scoped lang="scss">
  .assistant-rich-text {
    color: inherit;
    word-break: break-word;

    &:deep(p) {
      margin: 0 0 12px;
    }

    &:deep(p:last-child) {
      margin-bottom: 0;
    }

    &:deep(pre) {
      margin: 12px 0;
      overflow: auto;
      border-radius: 14px;
    }

    &:deep(code) {
      word-break: normal;
      white-space: pre-wrap;
    }

    &:deep(pre code) {
      white-space: pre;
    }

    &:deep(table) {
      display: block;
      overflow-x: auto;
      white-space: nowrap;
    }
  }

  .assistant-rich-text:deep(.assistant-mermaid) {
    margin: 12px 0;
  }

  .assistant-rich-text:deep(.assistant-mermaid__panel) {
    position: relative;
    overflow: hidden;
    border-radius: 18px;
    background: linear-gradient(180deg, #f8fbff 0%, #eef5ff 100%);
    border: 1px solid rgba(148, 163, 184, 0.14);
    box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.8);
  }

  .assistant-rich-text:deep(.assistant-mermaid__toolbar) {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 10px 10px 0;
  }

  .assistant-rich-text:deep(.assistant-mermaid__action) {
    cursor: pointer;
    border: 0;
    border-radius: 999px;
    padding: 4px 10px;
    background: rgba(15, 23, 42, 0.08);
    color: var(--el-text-color-secondary);
    font-size: 12px;
    transition: all 0.18s ease;
  }

  .assistant-rich-text:deep(.assistant-mermaid__action:hover) {
    background: rgba(77, 140, 255, 0.14);
    color: #3b82f6;
  }

  .assistant-rich-text:deep(.assistant-mermaid__canvas) {
    overflow: auto;
    margin: 8px 12px 12px;
    padding: 14px;
    border-radius: 16px;
    background: #ffffff;
    box-shadow: inset 0 0 0 1px rgba(148, 163, 184, 0.14);
  }

  .assistant-rich-text:deep(.assistant-mermaid__svg) {
    display: block;
    width: 100%;
    min-width: 280px;
    height: auto;
    margin: 0 auto;
    background: #ffffff;
    color: #0f172a;
    border: 0 !important;
    outline: 0 !important;
    box-shadow: none !important;
  }

  .assistant-rich-text:deep(.assistant-mermaid__svg .rect),
  .assistant-rich-text:deep(.assistant-mermaid__svg rect.rect) {
    fill: #ffffff !important;
    stroke: transparent !important;
  }

  .assistant-rich-text:deep(.assistant-mermaid__svg svg),
  .assistant-rich-text:deep(.assistant-mermaid__svg *[style*='stroke:#000']),
  .assistant-rich-text:deep(.assistant-mermaid__svg *[style*='stroke: #000']) {
    border: 0 !important;
    outline: 0 !important;
  }

  .assistant-rich-text:deep(.assistant-mermaid__loading) {
    display: flex;
    min-height: 200px;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    color: var(--el-text-color-secondary);
    font-size: 13px;

    em {
      font-style: normal;
    }
  }

  .assistant-rich-text:deep(.assistant-mermaid__loading-dots) {
    display: inline-flex;
    align-items: center;
    gap: 6px;

    span {
      width: 8px;
      height: 8px;
      border-radius: 999px;
      background: rgba(77, 140, 255, 0.72);
      animation: assistant-mermaid-bounce 1.1s infinite ease-in-out;
    }

    span:nth-child(2) {
      animation-delay: 0.15s;
    }

    span:nth-child(3) {
      animation-delay: 0.3s;
    }
  }

  .assistant-rich-text:deep(.assistant-mermaid__fallback) {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .assistant-rich-text:deep(.assistant-mermaid__fallback-tip) {
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .assistant-rich-text:deep(.assistant-mermaid__fallback pre) {
    margin: 0;
  }

  @keyframes assistant-mermaid-bounce {
    0%,
    80%,
    100% {
      opacity: 0.35;
      transform: scale(0.82);
    }

    40% {
      opacity: 1;
      transform: scale(1);
    }
  }
</style>
