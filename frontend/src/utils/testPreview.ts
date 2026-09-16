/** Build an isolated, offline HTML/SVG preview. The iframe also has sandbox="allow-scripts".
 * CSS, SVG/SMIL and inline-script animations run in an opaque origin; external
 * resources and access to application cookies/storage are blocked. Never render a model response directly into the application's DOM.
 */
export function buildTestPreviewHTML(source: string): string {
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
  csp.content = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; base-uri 'none'; form-action 'none'"
  document.head.prepend(csp)

  // Let the host iframe size itself to the complete document. Some generated
  // previews include viewport-height or overflow rules that otherwise create
  // a second scrollbar inside the test result card.
  const layoutStyle = document.createElement('style')
  layoutStyle.textContent = 'html, body { height: auto !important; min-height: 0 !important; max-height: none !important; overflow: visible !important; } svg { max-height: none !important; overflow: visible !important; }'
  document.head.appendChild(layoutStyle)

  // The preview is rendered in a sandboxed iframe. Report its rendered height
  // to the host so tall SVG/HTML responses are not clipped by a fixed iframe
  // height. The host validates the message source before applying it.
  const resizeScript = document.createElement('script')
  resizeScript.textContent = `(() => {
    const report = () => {
      const body = document.body
      const root = document.documentElement
      let height = Math.max(body?.scrollHeight || 0, root?.scrollHeight || 0, body?.offsetHeight || 0, root?.offsetHeight || 0, body?.clientHeight || 0, root?.clientHeight || 0)
      for (const node of document.querySelectorAll('*')) {
        const rect = node.getBoundingClientRect()
        if (Number.isFinite(rect.bottom)) height = Math.max(height, rect.bottom + window.scrollY)
        if (node instanceof SVGGraphicsElement) {
          try {
            const box = node.getBBox()
            if (Number.isFinite(box.bottom)) height = Math.max(height, rect.top + box.bottom + window.scrollY)
          } catch (_) {}
        }
      }
      window.parent.postMessage({ type: 'sub2api-test-preview-size', height }, '*')
    }
    window.addEventListener('load', report)
    if (window.ResizeObserver) {
      const observer = new ResizeObserver(report)
      observer.observe(document.documentElement)
      if (document.body) observer.observe(document.body)
    }
    if (window.MutationObserver) new MutationObserver(report).observe(document.documentElement, { childList: true, subtree: true, attributes: true })
    report()
    setTimeout(report, 0)
    setTimeout(report, 100)
    setTimeout(report, 300)
    setTimeout(report, 700)
    setTimeout(report, 1200)
    setTimeout(report, 2000)
  })()`
  document.body.appendChild(resizeScript)
  return '<!doctype html>\n' + document.documentElement.outerHTML
}
