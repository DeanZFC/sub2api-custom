export function freezeTestPreviewViewportValue(value: string, width: number, height: number): string {
  // Work on CSS declaration values, leaving quoted text and URLs untouched.
  return value.replace(/("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|url\((?:\\.|[^)])*\))|(?<![\w-])([+-]?(?:\d+(?:\.\d*)?|\.\d+))([sld]?v(?:min|max|w|h|i|b))\b/gi,
    (match, literal: string | undefined, amount: string, unit: string) => {
      if (literal) return match
      const dimension = unit.toLowerCase().replace(/^[sld]/, '')
      const size = dimension === 'vmin' ? Math.min(width, height)
        : dimension === 'vmax' ? Math.max(width, height)
          : dimension === 'vw' || dimension === 'vi' ? width : height
      return `${Number(amount) * size / 100}px`
    })
}

/** Build an isolated, offline HTML/SVG preview. The iframe also has sandbox="allow-scripts".
 * CSS, SVG/SMIL and inline-script animations run in an opaque origin; external
 * resources and access to application cookies/storage are blocked. Never render a model response directly into the application's DOM.
 */
export function buildTestPreviewHTML(source: string, nonce?: string): string {
  const document = new DOMParser().parseFromString(source, 'text/html')
  document.querySelectorAll('script[src], iframe, frame, frameset, object, embed, base, form, meta[http-equiv]').forEach(node => node.remove())
  for (const node of document.querySelectorAll('*')) {
    for (const attribute of Array.from(node.attributes)) {
      if (attribute.name.toLowerCase().startsWith('on')) node.removeAttribute(attribute.name)
    }
  }
  // Keep internal SVG references while preventing links from navigating the
  // preview to an unrelated page, including SVG anchors.
  document.querySelectorAll('a').forEach(node => {
    for (const name of ['href', 'xlink:href']) {
      if (!(node.getAttribute(name) || '').startsWith('#')) node.removeAttribute(name)
    }
  })
  const csp = document.createElement('meta')
  csp.httpEquiv = 'Content-Security-Policy'
  // The host nonce authorizes inline execution under the inherited policy.
  // This separate policy must still deny external scripts, including scripts
  // dynamically created by the preview with a copied nonce.
  csp.content = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; base-uri 'none'; form-action 'none'"
  document.head.prepend(csp)

  // Let the host iframe size itself to the complete document. Some generated
  // previews include viewport-height or overflow rules that otherwise create
  // a second scrollbar inside the test result card.
  const layoutStyle = document.createElement('style')
  layoutStyle.textContent = 'html, body { height: auto !important; min-height: 0 !important; max-height: none !important; overflow: visible !important; }'
  document.head.appendChild(layoutStyle)

  // The preview is rendered in a sandboxed iframe. Report its rendered height
  // to the host so tall SVG/HTML responses are not clipped by a fixed iframe
  // height. The host validates the message source before applying it.
  const resizeScript = document.createElement('script')
  resizeScript.textContent = `(() => {
    const viewportWidth = window.innerWidth
    const viewportHeight = window.innerHeight
    const freezeViewportValue = ${freezeTestPreviewViewportValue.toString()}
    const freezeStyle = (style) => {
      for (const property of Array.from(style)) {
        const value = style.getPropertyValue(property)
        const frozen = freezeViewportValue(value, viewportWidth, viewportHeight)
        if (frozen !== value) style.setProperty(property, frozen, style.getPropertyPriority(property))
      }
    }
    const freezeRules = (rules) => {
      for (const rule of Array.from(rules)) {
        if (rule.style) freezeStyle(rule.style)
        if (rule.cssRules) freezeRules(rule.cssRules)
      }
    }
    const normalizeLayout = () => {
      for (const sheet of Array.from(document.styleSheets)) {
        try { freezeRules(sheet.cssRules) } catch (_) {}
      }
      for (const node of document.querySelectorAll('[style]')) freezeStyle(node.style)
      for (const node of document.body?.querySelectorAll('*') || []) {
        if (!(node instanceof HTMLElement) || node.closest('svg')) continue
        const style = getComputedStyle(node)
        const expandY = /^(auto|scroll)$/.test(style.overflowY) && node.scrollHeight > node.clientHeight + 1
        const expandX = /^(auto|scroll)$/.test(style.overflowX) && node.scrollWidth > node.clientWidth + 1
        if (!expandY && !expandX) continue
        const height = node.scrollHeight + parseFloat(style.borderTopWidth || 0) + parseFloat(style.borderBottomWidth || 0)
        const width = node.scrollWidth + parseFloat(style.borderLeftWidth || 0) + parseFloat(style.borderRightWidth || 0)
        node.style.setProperty('box-sizing', 'border-box', 'important')
        node.style.setProperty('overflow', 'visible', 'important')
        if (expandY) {
          node.style.setProperty('max-height', 'none', 'important')
          node.style.setProperty('height', height + 'px', 'important')
        }
        if (expandX) {
          node.style.setProperty('max-width', 'none', 'important')
          node.style.setProperty('width', width + 'px', 'important')
        }
      }
    }
    let previousSize = ''
    let needsNormalization = true
    let scheduled = false
    let lastReportAt = 0
    const report = () => {
      scheduled = false
      lastReportAt = performance.now()
      if (needsNormalization) {
        needsNormalization = false
        normalizeLayout()
      }
      const body = document.body
      const root = document.documentElement
      let height = Math.max(body?.scrollHeight || 0, root?.scrollHeight || 0, body?.offsetHeight || 0, root?.offsetHeight || 0, body?.clientHeight || 0, root?.clientHeight || 0)
      let width = Math.max(body?.scrollWidth || 0, root?.scrollWidth || 0, body?.offsetWidth || 0, root?.offsetWidth || 0)
      const clipping = new Map()
      const clipFor = (node) => {
        if (!node || node === body || node === root) return { right: Infinity, bottom: Infinity }
        if (clipping.has(node)) return clipping.get(node)
        const clip = { ...clipFor(node.parentElement) }
        const style = getComputedStyle(node)
        const rect = node.getBoundingClientRect()
        if (/^(hidden|clip|auto|scroll)$/.test(style.overflowX)) clip.right = Math.min(clip.right, rect.right)
        if (/^(hidden|clip|auto|scroll)$/.test(style.overflowY)) clip.bottom = Math.min(clip.bottom, rect.bottom)
        clipping.set(node, clip)
        return clip
      }
      for (const node of body?.querySelectorAll('*') || []) {
        if (!(node instanceof HTMLElement || node instanceof SVGSVGElement) || node.parentElement?.closest('svg')) continue
        const rect = node.getBoundingClientRect()
        const clip = clipFor(node.parentElement)
        if (Number.isFinite(rect.bottom)) height = Math.max(height, Math.min(rect.bottom, clip.bottom) + window.scrollY)
        if (Number.isFinite(rect.right)) width = Math.max(width, Math.min(rect.right, clip.right) + window.scrollX)
      }
      height = Math.ceil(height)
      width = Math.ceil(width)
      const size = width + ':' + height
      if (size === previousSize) return
      previousSize = size
      window.parent.postMessage({ type: 'sub2api-test-preview-size', height, width }, '*')
    }
    const scheduleReport = () => {
      if (scheduled) return
      scheduled = true
      setTimeout(() => requestAnimationFrame(report), Math.max(0, 200 - (performance.now() - lastReportAt)))
    }
    window.addEventListener('load', scheduleReport)
    window.addEventListener('resize', scheduleReport)
    if (window.ResizeObserver) {
      const observer = new ResizeObserver(scheduleReport)
      observer.observe(document.documentElement)
      if (document.body) observer.observe(document.body)
    }
    if (window.MutationObserver) new MutationObserver((records) => {
      if (!records.some(record => record.type !== 'attributes' || record.target instanceof HTMLElement || (record.target instanceof SVGSVGElement && !record.target.parentElement?.closest('svg')))) return
      needsNormalization = true
      scheduleReport()
    }).observe(document.documentElement, { childList: true, subtree: true, attributes: true, characterData: true, attributeFilter: ['style', 'class', 'width', 'height'] })
    report()
    if (document.fonts) document.fonts.ready.then(scheduleReport)
    setTimeout(scheduleReport, 300)
    setTimeout(scheduleReport, 1200)
    setTimeout(scheduleReport, 2000)
  })()`
  document.body.appendChild(resizeScript)
  // srcdoc inherits the host page's CSP. Its meta policy cannot relax that
  // policy, so inline animation and sizing scripts need the host's nonce too.
  for (const script of document.querySelectorAll('script')) {
    if (nonce) script.setAttribute('nonce', nonce)
    else script.removeAttribute('nonce')
  }
  return '<!doctype html>\n' + document.documentElement.outerHTML
}
