import DOMPurify, { type Config } from 'dompurify'

const MARKDOWN_TAGS = [
  'a',
  'blockquote',
  'br',
  'code',
  'del',
  'div',
  'em',
  'h1',
  'h2',
  'h3',
  'h4',
  'h5',
  'h6',
  'hr',
  'img',
  'li',
  'ol',
  'p',
  'pre',
  'span',
  'strong',
  'table',
  'tbody',
  'td',
  'th',
  'thead',
  'tr',
  'ul'
]

const serialize = (fragment: DocumentFragment) => {
  const template = document.createElement('template')
  template.content.append(fragment)
  return template.innerHTML
}

const sanitizeFragment = (value: string, config: Config): DocumentFragment =>
  DOMPurify.sanitize(value, { ...config, RETURN_DOM_FRAGMENT: true })

const isLocalImageSource = (value: string) => {
  const source = value.trim()
  if (/^(?:blob:|data:image\/(?:avif|gif|jpeg|png|webp);base64,)/i.test(source)) {
    return true
  }

  try {
    const url = new URL(source, document.baseURI)
    return (url.protocol === 'http:' || url.protocol === 'https:') && url.origin === location.origin
  } catch {
    return false
  }
}

/** 助手输出只保留 Markdown 渲染所需标签，并阻止内容静默加载第三方资源。 */
export const sanitizeRichHtml = (value: string) => {
  const fragment = sanitizeFragment(value, {
    ALLOWED_TAGS: MARKDOWN_TAGS,
    ALLOWED_ATTR: [
      'alt',
      'class',
      'data-mermaid-index',
      'href',
      'rel',
      'src',
      'start',
      'target',
      'title'
    ]
  })

  fragment.querySelectorAll<HTMLImageElement>('img[src]').forEach((image) => {
    if (!isLocalImageSource(image.getAttribute('src') ?? '')) {
      image.removeAttribute('src')
    }
  })

  return serialize(fragment)
}

/** 跑马灯文本只需要基础强调和链接，不接受图片、样式或可执行属性。 */
export const sanitizeInlineText = (value: string) =>
  DOMPurify.sanitize(value, {
    ALLOWED_TAGS: ['a', 'b', 'br', 'em', 'i', 'span', 'strong'],
    ALLOWED_ATTR: ['href', 'title']
  })

const LOCAL_SVG_REFERENCE = /url\(\s*(['"]?)#[\w:.-]+\1\s*\)/gi
const CSS_COMMENT = /\/\*[\s\S]*?\*\//g
const CSS_ESCAPE = /\\([0-9a-f]{1,6})(?:\r\n|[ \t\r\n\f])?|\\(.)/gis

const normalizeCssReferences = (value: string) =>
  value.replace(CSS_COMMENT, '').replace(CSS_ESCAPE, (_match, hex: string, escaped: string) => {
    if (!hex) return escaped ?? ''
    const codePoint = Number.parseInt(hex, 16)
    return codePoint === 0 || codePoint > 0x10ffff ? '\uFFFD' : String.fromCodePoint(codePoint)
  })

const hasExternalCssReference = (value: string) =>
  /url\s*\(|@import/i.test(normalizeCssReferences(value).replace(LOCAL_SVG_REFERENCE, ''))

/** SVG 只允许片段内引用，避免内联图形借 href 或 CSS 发起外部请求。 */
export const sanitizeSvg = (value: string) => {
  const fragment = sanitizeFragment(value, {
    USE_PROFILES: { svg: true, svgFilters: true },
    FORBID_TAGS: ['foreignObject', 'script']
  })

  fragment.querySelectorAll('*').forEach((element) => {
    Array.from(element.attributes).forEach((attribute) => {
      const name = attribute.name.toLowerCase()
      if (
        (name === 'href' || name === 'xlink:href' || name === 'src') &&
        !attribute.value.trim().startsWith('#')
      ) {
        element.removeAttributeNode(attribute)
        return
      }
      if (hasExternalCssReference(attribute.value)) {
        element.removeAttributeNode(attribute)
      }
    })

    if (element.localName === 'style' && hasExternalCssReference(element.textContent ?? '')) {
      element.remove()
    }
  })

  return serialize(fragment)
}
