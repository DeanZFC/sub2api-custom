import http from 'node:http'
import fs from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
import { chromium } from 'playwright'

export const MAX_HTML_BYTES = 4 * 1024 * 1024
// JSON escaping can expand each HTML byte to six bytes (for example \u003c).
const MAX_REQUEST_BYTES = MAX_HTML_BYTES * 6 + 1024
const MAX_IMAGE_BYTES = 2 * 1024 * 1024
const RENDER_TIMEOUT_MS = 20000
// Unequal spacing avoids sampling the same phase of a one-second animation.
const FRAME_TIMES = [0, 700, 1700, 2900]
const CSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src 'none'; connect-src 'none'; worker-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"
const CSP_META = `<meta http-equiv="Content-Security-Policy" content="${CSP}">`

class RenderError extends Error {
  constructor(message, status = 422) { super(message); this.status = status }
}

// The browser runs in a separate network_mode:none container with its user
// namespace sandbox enabled. CSP and request interception are additional bounds,
// not substitutes for that operating-system isolation.
export async function renderHTML(html, launch = chromium.launch.bind(chromium)) {
  const browser = await launch({
    chromiumSandbox: true,
    headless: true,
    timeout: 10000,
    args: ['--disable-dev-shm-usage', '--disable-background-networking', '--disable-extensions', '--disable-sync'],
  })
  let expired = false
  const timer = setTimeout(() => { expired = true; void browser.close().catch(() => {}) }, RENDER_TIMEOUT_MS)
  timer.unref()
  try {
    const context = await browser.newContext({
      viewport: { width: 960, height: 720 },
      deviceScaleFactor: 1,
      acceptDownloads: false,
      serviceWorkers: 'block',
      javaScriptEnabled: true,
      reducedMotion: 'no-preference',
    })
    await context.route('**/*', route => route.abort('blockedbyclient'))
    await context.routeWebSocket('**/*', ws => ws.close())
    const page = await context.newPage()
    page.setDefaultTimeout(5000)
    let navigated = false
    context.on('page', popup => { if (popup !== page) void popup.close().catch(() => {}) })
    page.on('dialog', dialog => { void dialog.dismiss().catch(() => {}) })
    page.on('download', download => { void download.cancel().catch(() => {}) })
    page.on('framenavigated', frame => {
      if (frame === page.mainFrame() && frame.url() !== 'about:blank') {
        navigated = true
        void context.close().catch(() => {})
      }
    })
    // setContent starts on about:blank. The first CSP applies even when the
    // submitted document includes its own doctype/head or a weaker CSP.
    // Preserve an authored doctype before injecting the policy, so valid
    // generated documents retain standards mode instead of switching to quirks.
    const doctype = html.match(/^\s*<!doctype[^>]*>/i)
    const document = doctype
      ? `${doctype[0]}${CSP_META}${html.slice(doctype[0].length)}`
      : `${CSP_META}${html}`
    await page.setContent(document, { waitUntil: 'load', timeout: 5000 })
    const frames = []
    const started = Date.now()
    for (const atMS of FRAME_TIMES) {
      const delay = atMS - (Date.now() - started)
      if (delay > 0) await new Promise(resolve => setTimeout(resolve, delay))
      const png = await page.screenshot({ type: 'png', fullPage: false, timeout: 5000, animations: 'allow' })
      if (png.length > MAX_IMAGE_BYTES) throw new RenderError('rendered frame exceeds image limit')
      frames.push({ at_ms: atMS, png_base64: png.toString('base64') })
    }
    if (navigated) throw new RenderError('generated content attempted navigation')
    return { frames }
  } catch (error) {
    if (expired) throw new RenderError('rendering deadline exceeded', 504)
    throw error
  } finally {
    clearTimeout(timer)
    await browser.close().catch(() => {})
  }
}

async function readBody(request) {
  const chunks = []
  let size = 0
  for await (const chunk of request) {
    size += chunk.length
    if (size > MAX_REQUEST_BYTES) throw new RenderError('request exceeds HTML limit', 413)
    chunks.push(chunk)
  }
  let body
  try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) } catch { throw new RenderError('invalid JSON request', 400) }
  if (!body || typeof body.html !== 'string' || body.html.trim() === '') throw new RenderError('html is required', 400)
  if (Buffer.byteLength(body.html, 'utf8') > MAX_HTML_BYTES) throw new RenderError('HTML exceeds 4 MiB', 413)
  return body.html
}

export function createRendererServer(render = renderHTML) {
  let active = false
  const server = http.createServer(async (request, response) => {
    response.setHeader('Content-Type', 'application/json')
    response.setHeader('Cache-Control', 'no-store')
    const reply = (status, value) => { response.writeHead(status); response.end(JSON.stringify(value)) }
    if (request.method === 'GET' && request.url === '/health') { reply(200, { status: 'ok' }); return }
    if (request.method !== 'POST' || request.url !== '/render') { reply(404, { error: 'not found' }); return }
    if (active) { request.resume(); reply(429, { error: 'renderer is busy' }); return }
    active = true
    try {
      const html = await readBody(request)
      const result = await render(html)
      if (!response.destroyed) reply(200, result)
    } catch (error) {
      // Do not return browser diagnostics, HTML, stack traces or local paths.
      const safe = error instanceof RenderError
      if (!response.destroyed) reply(safe ? error.status : 503, { error: safe ? error.message : 'renderer unavailable or generated content could not be rendered' })
    } finally { active = false }
  })
  server.requestTimeout = 25000
  server.headersTimeout = 5000
  server.keepAliveTimeout = 1000
  server.maxConnections = 8
  return server
}

async function main() {
  const socket = process.env.RENDERER_SOCKET || '/review-renderer/renderer.sock'
  try { await fs.unlink(socket) } catch (error) { if (error.code !== 'ENOENT') throw error }
  const server = createRendererServer()
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(socket, resolve) })
  await fs.chmod(socket, 0o600)
  const stop = () => { server.close(() => process.exit(0)); setTimeout(() => process.exit(1), 25000).unref() }
  process.on('SIGTERM', stop)
  process.on('SIGINT', stop)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(() => { console.error('renderer failed to start'); process.exit(1) })
}
