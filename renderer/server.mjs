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
const READINESS_HTML = '<!doctype html><html><body><svg width="960" height="720"><rect width="960" height="720" fill="white"/></svg></body></html>'
const READINESS_RETRY_MS = 30000
const ERROR_CODES = {
  renderer_busy: { status: 429, message: 'renderer is busy' },
  renderer_unavailable: { status: 503, message: 'renderer is not ready' },
  renderer_browser_unavailable: { status: 503, message: 'sandboxed browser could not be started' },
  renderer_timeout: { status: 504, message: 'rendering deadline exceeded' },
  renderer_render_failed: { status: 503, message: 'generated content could not be rendered' },
  renderer_invalid_input: { status: 400, message: 'invalid renderer input' },
}

class RenderError extends Error {
  constructor(code, stage, cause, status = ERROR_CODES[code].status) {
    super(ERROR_CODES[code].message)
    this.code = code
    this.stage = stage
    this.causeCode = cause
    this.status = status
  }
}

function diagnostic(error, stage = 'render') {
  // Only locally constructed errors may contribute fields. Never serialize an
  // arbitrary browser exception, its message, stack, HTML or filesystem paths.
  const safe = error instanceof RenderError
    ? error
    : new RenderError('renderer_render_failed', stage, 'operation_failed')
  return { status: safe.status, body: { error: ERROR_CODES[safe.code].message, code: safe.code, stage: safe.stage, cause: safe.causeCode } }
}

// Preserve the one-browser boundary even if Playwright cannot close promptly:
// callers fail safely until the previous cleanup actually completes.
let pendingBrowserCleanup = null
function browserDisconnected(browser) {
  try { return typeof browser.isConnected === 'function' && !browser.isConnected() } catch { return false }
}
async function closeBrowser(browser) {
  let timer
  const pending = { browser }
  pendingBrowserCleanup = pending
  const cleanup = Promise.resolve().then(() => browser.close()).then(
    () => { if (pendingBrowserCleanup === pending) pendingBrowserCleanup = null; return 'closed' },
    () => {
      if (!browserDisconnected(browser)) return 'failed'
      if (pendingBrowserCleanup === pending) pendingBrowserCleanup = null
      return 'closed'
    },
  )
  try {
    const outcome = await Promise.race([
      cleanup,
      new Promise(resolve => { timer = setTimeout(() => resolve('timeout'), 3000) }),
    ])
    if (outcome !== 'closed') throw new RenderError('renderer_browser_unavailable', 'cleanup', outcome === 'failed' ? 'cleanup_failed' : 'cleanup_timeout')
  } finally { clearTimeout(timer) }
}

// The browser runs in a separate network_mode:none container with its user
// namespace sandbox enabled. CSP and request interception are additional bounds,
// not substitutes for that operating-system isolation.
export async function renderHTML(html, launch = chromium.launch.bind(chromium)) {
  if (pendingBrowserCleanup && browserDisconnected(pendingBrowserCleanup.browser)) pendingBrowserCleanup = null
  if (pendingBrowserCleanup) throw new RenderError('renderer_browser_unavailable', 'cleanup', 'cleanup_pending')
  let browser
  try {
    browser = await launch({
      chromiumSandbox: true,
      headless: true,
      timeout: 10000,
      args: ['--disable-dev-shm-usage', '--disable-background-networking', '--disable-extensions', '--disable-sync'],
    })
  } catch (error) {
    throw new RenderError('renderer_browser_unavailable', 'launch', error?.name === 'TimeoutError' ? 'launch_timeout' : 'launch_failed')
  }
  let expired = false
  let stage = 'context'
  let navigated = false
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
    stage = 'content'
    await page.setContent(document, { waitUntil: 'load', timeout: 5000 })
    const frames = []
    const started = Date.now()
    for (const atMS of FRAME_TIMES) {
      const delay = atMS - (Date.now() - started)
      if (delay > 0) await new Promise(resolve => setTimeout(resolve, delay))
      stage = 'screenshot'
      const png = await page.screenshot({ type: 'png', fullPage: false, timeout: 5000, animations: 'allow' })
      if (png.length > MAX_IMAGE_BYTES) throw new RenderError('renderer_render_failed', stage, 'image_too_large', 422)
      frames.push({ at_ms: atMS, png_base64: png.toString('base64') })
    }
    if (navigated) throw new RenderError('renderer_render_failed', stage, 'navigation_blocked', 422)
    return { frames }
  } catch (error) {
    if (expired) throw new RenderError('renderer_timeout', stage, 'deadline_exceeded')
    if (error instanceof RenderError) throw error
    if (navigated) throw new RenderError('renderer_render_failed', stage, 'navigation_blocked', 422)
    if (error?.name === 'TimeoutError') throw new RenderError('renderer_timeout', stage, 'operation_timeout')
    throw new RenderError('renderer_render_failed', stage, 'operation_failed')
  } finally {
    clearTimeout(timer)
    await closeBrowser(browser)
  }
}

async function readBody(request) {
  const chunks = []
  let size = 0
  for await (const chunk of request) {
    size += chunk.length
    if (size > MAX_REQUEST_BYTES) throw new RenderError('renderer_invalid_input', 'input', 'request_too_large', 413)
    chunks.push(chunk)
  }
  let body
  try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')) } catch { throw new RenderError('renderer_invalid_input', 'input', 'invalid_json') }
  if (!body || typeof body.html !== 'string' || body.html.trim() === '') throw new RenderError('renderer_invalid_input', 'input', 'missing_html')
  if (Buffer.byteLength(body.html, 'utf8') > MAX_HTML_BYTES) throw new RenderError('renderer_invalid_input', 'input', 'html_too_large', 413)
  return body.html
}

export function createRendererServer(render = renderHTML, { probe = () => render(READINESS_HTML), logger = entry => console.error(JSON.stringify(entry)), now = Date.now } = {}) {
  let active = false
  let ready = false
  let lastProbeAt
  let readinessError = new RenderError('renderer_unavailable', 'readiness', 'probe_pending')
  const log = (event, failure, started) => logger({ event, ...failure.body, status: failure.status, elapsed_ms: Math.max(0, now() - started) })
  const probeReadiness = async () => {
    if (active) return
    active = true
    lastProbeAt = now()
    try {
      // This is one real sandboxed render at startup, shared with the render
      // concurrency slot. Successful health checks never launch a browser.
      await probe()
      ready = true
      readinessError = null
    } catch (error) {
      ready = false
      readinessError = error instanceof RenderError ? error : new RenderError('renderer_browser_unavailable', 'readiness', 'probe_failed')
      log('renderer_readiness_failed', diagnostic(readinessError), lastProbeAt)
    } finally { active = false }
  }
  const server = http.createServer(async (request, response) => {
    response.setHeader('Content-Type', 'application/json')
    response.setHeader('Cache-Control', 'no-store')
    const reply = (status, value) => { response.writeHead(status); response.end(JSON.stringify(value)) }
    if (request.method === 'GET' && request.url === '/health') {
      if (!ready && !active && (lastProbeAt === undefined || now() - lastProbeAt >= READINESS_RETRY_MS)) void probeReadiness()
      if (ready) reply(200, { status: 'ok' })
      else { const failure = diagnostic(readinessError); reply(failure.status, failure.body) }
      return
    }
    if (request.method !== 'POST' || request.url !== '/render') { reply(404, { error: 'not found' }); return }
    if (active) {
      request.resume()
      response.setHeader('Retry-After', '2')
      const failure = diagnostic(new RenderError('renderer_busy', 'render', 'busy'))
      reply(failure.status, failure.body)
      return
    }
    active = true
    const started = now()
    try {
      const html = await readBody(request)
      const result = await render(html)
      ready = true
      readinessError = null
      if (!response.destroyed) reply(200, result)
    } catch (error) {
      const failure = diagnostic(error)
      if (failure.body.code === 'renderer_browser_unavailable') {
        ready = false
        readinessError = error
        lastProbeAt = now()
      }
      log('renderer_render_failed', failure, started)
      if (!response.destroyed) reply(failure.status, failure.body)
    } finally { active = false }
  })
  server.requestTimeout = 25000
  server.headersTimeout = 5000
  server.keepAliveTimeout = 1000
  server.maxConnections = 8
  server.once('listening', () => { void probeReadiness() })
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
