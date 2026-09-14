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
  return '<!doctype html>\n' + document.documentElement.outerHTML
}
