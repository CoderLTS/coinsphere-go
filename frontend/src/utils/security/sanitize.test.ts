// @vitest-environment jsdom

import MarkdownIt from 'markdown-it'
import { describe, expect, it } from 'vitest'

import { sanitizeInlineText, sanitizeRichHtml, sanitizeSvg } from './sanitize'

describe('不可信富文本净化', () => {
  it('移除 Markdown、HTML 与 SVG 中的可执行内容和外部资源', () => {
    const markdown = new MarkdownIt({ html: false })
    const richHtml = sanitizeRichHtml(
      markdown.render(`
<script>alert(1)</script>

![tracker](https://evil.example/tracker.png)

[bad](javascript:alert(1))
`)
    )
    const richContainer = document.createElement('div')
    richContainer.innerHTML = richHtml

    expect(richContainer.querySelector('script')).toBeNull()
    expect(richContainer.querySelector('img')?.hasAttribute('src')).toBe(false)
    expect(richContainer.querySelector('a[href^="javascript:"]')).toBeNull()

    const inlineHtml = sanitizeInlineText(
      '<a href="javascript:alert(1)" onclick="alert(1)">通知</a><script>alert(1)</script>'
    )
    expect(inlineHtml).not.toMatch(/<script|onclick|javascript:/i)

    const svg = sanitizeSvg(`
      <svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)">
        <script>alert(1)</script>
        <image href="https://evil.example/tracker.png" />
        <use href="javascript:alert(1)" />
        <circle onclick="alert(1)" style="fill:url(https://evil.example/a.svg)" />
        <style>@import url(https://evil.example/a.css);</style>
        <path style="fill:u\\72l(https://escaped.example/a.svg)" />
        <style>.x { fill: u/**/rl(https://comment.example/a.svg) }</style>
      </svg>
    `)
    expect(svg).not.toMatch(
      /<script|onload|onclick|javascript:|evil\.example|escaped\.example|comment\.example/i
    )
  })
})
