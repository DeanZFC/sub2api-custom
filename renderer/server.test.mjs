import assert from 'node:assert/strict'
import { test } from 'node:test'
import http from 'node:http'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { createRendererServer, MAX_HTML_BYTES, renderHTML } from './server.mjs'

async function fixture(t, render) {
  const dir = await mkdtemp(path.join(tmpdir(), 'quality-renderer-'))
  const socket = path.join(dir, 'renderer.sock')
  const server = createRendererServer(render)
  await new Promise(resolve => server.listen(socket, resolve))
  t.after(async () => {
    await new Promise(resolve => server.close(resolve))
    await rm(dir, { recursive: true, force: true })
  })
  return (body, method = 'POST', url = '/render') => new Promise((resolve, reject) => {
    const request = http.request({ socketPath: socket, path: url, method, headers: { 'Content-Type': 'application/json' } }, response => {
      const chunks = []
      response.on('data', chunk => chunks.push(chunk))
      response.on('end', () => resolve({ status: response.statusCode, body: JSON.parse(Buffer.concat(chunks).toString()) }))
    })
    request.on('error', reject)
    request.end(typeof body === 'string' ? body : JSON.stringify(body))
  })
}

test('UNIX protocol rejects malformed and oversized input before rendering', async t => {
  let calls = 0
  const request = await fixture(t, async () => { calls++; return { frames: [] } })
  assert.equal((await request('{')).status, 400)
  assert.equal((await request({ html: ' ' })).status, 400)
  assert.equal((await request({ html: 'a'.repeat(MAX_HTML_BYTES + 1) })).status, 413)
  assert.equal(calls, 0)
})

test('renderer keeps one active job and does not leak diagnostics', async t => {
  let release
  let entered
  const begun = new Promise(resolve => { entered = resolve })
  const request = await fixture(t, async html => {
    entered()
    await new Promise(resolve => { release = resolve })
    throw new Error(`private browser path and HTML: ${html}`)
  })
  const first = request({ html: '<svg/>' })
  await begun
  assert.equal((await request({ html: '<svg/>' })).status, 429)
  release()
  const failed = await first
  assert.equal(failed.status, 503)
  assert.equal(failed.body.error.includes('private'), false)
  assert.equal(failed.body.error.includes('<svg'), false)
})

test('health does not launch Chromium and render forwards input', async t => {
  const frame = { at_ms: 0, png_base64: 'fixture' }
  const request = await fixture(t, async html => { assert.equal(html, '<svg/>'); return { frames: [frame] } })
  assert.equal((await request('', 'GET', '/health')).status, 200)
  assert.deepEqual((await request({ html: '<svg/>' })).body, { frames: [frame] })
})

test('browser sandbox, request blocking and fresh frame sampling remain enabled', async () => {
  let closed = false
  let blocked = false
  let websocketBlocked = false
  let options
  let screenshotCount = 0
  let csp = ''
  const page = {
    setDefaultTimeout() {}, on() {},
    setContent: async html => { csp = html },
    screenshot: async opts => { screenshotCount++; assert.equal(opts.animations, 'allow'); return Buffer.from('PNG fixture') },
  }
  const context = {
    route: async (pattern, handler) => { assert.equal(pattern, '**/*'); await handler({ abort: async () => { blocked = true } }) },
    routeWebSocket: async (pattern, handler) => { assert.equal(pattern, '**/*'); handler({ close() { websocketBlocked = true } }) },
    newPage: async () => page, on() {},
  }
  const browser = {
    newContext: async config => { assert.equal(config.serviceWorkers, 'block'); assert.deepEqual(config.viewport, { width: 960, height: 720 }); return context },
    close: async () => { closed = true },
  }
  const result = await renderHTML('<!doctype html><html><script>animation()</script></html>', async opts => { options = opts; return browser })
  assert.equal(options.chromiumSandbox, true)
  assert.equal(options.args.some(arg => arg.includes('no-sandbox')), false)
  assert.equal(blocked, true)
  assert.equal(websocketBlocked, true)
  assert.match(csp, /Content-Security-Policy/)
  assert.match(csp, /connect-src 'none'/)
  assert.match(csp, /^<!doctype html><meta/)
  assert.equal(screenshotCount, 4)
  assert.deepEqual(result.frames.map(frame => frame.at_ms), [0, 700, 1700, 2900])
  assert.equal(closed, true)
})
