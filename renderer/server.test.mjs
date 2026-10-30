import assert from 'node:assert/strict'
import { test } from 'node:test'
import http from 'node:http'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { createRendererServer, MAX_HTML_BYTES, renderHTML } from './server.mjs'

async function fixture(t, render, options = {}) {
  const dir = await mkdtemp(path.join(tmpdir(), 'quality-renderer-'))
  const socket = path.join(dir, 'renderer.sock')
  const server = createRendererServer(render, { probe: async () => {}, logger: () => {}, ...options })
  await new Promise(resolve => server.listen(socket, resolve))
  await new Promise(resolve => setImmediate(resolve))
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
  const malformed = await request('{')
  assert.equal(malformed.status, 400)
  assert.equal(malformed.body.code, 'renderer_invalid_input')
  assert.equal(malformed.body.cause, 'invalid_json')
  assert.equal((await request({ html: ' ' })).status, 400)
  assert.equal((await request({ html: 'a'.repeat(MAX_HTML_BYTES + 1) })).status, 413)
  assert.equal(calls, 0)
})

test('renderer keeps one active job and does not leak diagnostics', async t => {
  let release
  let entered
  const begun = new Promise(resolve => { entered = resolve })
  const logs = []
  const request = await fixture(t, async html => {
    entered()
    await new Promise(resolve => { release = resolve })
    throw new Error(`private browser path and HTML: ${html}`)
  }, { logger: entry => logs.push(entry) })
  const first = request({ html: '<svg/>' })
  await begun
  const busy = await request({ html: '<svg/>' })
  assert.equal(busy.status, 429)
  assert.equal(busy.body.code, 'renderer_busy')
  release()
  const failed = await first
  assert.equal(failed.status, 503)
  assert.equal(failed.body.error.includes('private'), false)
  assert.equal(failed.body.error.includes('<svg'), false)
  assert.equal(failed.body.code, 'renderer_render_failed')
  assert.equal(logs.length, 1)
  assert.equal(logs[0].event, 'renderer_render_failed')
  assert.equal(logs[0].cause, 'operation_failed')
  assert.doesNotMatch(JSON.stringify(logs), /private|<svg|stack/)
})

test('health caches the startup probe and render forwards input', async t => {
  const frame = { at_ms: 0, png_base64: 'fixture' }
  let probes = 0
  const request = await fixture(t, async html => { assert.equal(html, '<svg/>'); return { frames: [frame] } }, { probe: async () => { probes++ } })
  assert.equal((await request('', 'GET', '/health')).status, 200)
  assert.equal((await request('', 'GET', '/health')).status, 200)
  assert.equal(probes, 1)
  assert.deepEqual((await request({ html: '<svg/>' })).body, { frames: [frame] })
})

test('startup readiness uses the render slot and cannot report healthy before Chromium works', async t => {
  let release
  let calls = 0
  const request = await fixture(t, async () => { calls++; return { frames: [] } }, { probe: () => new Promise(resolve => { release = resolve }) })
  const pending = await request('', 'GET', '/health')
  assert.equal(pending.status, 503)
  assert.equal(pending.body.code, 'renderer_unavailable')
  assert.equal((await request({ html: '<svg/>' })).body.code, 'renderer_busy')
  assert.equal(calls, 0)
  release()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal((await request('', 'GET', '/health')).status, 200)
})

test('failed startup probes are safe, throttled and recover without restarting', async t => {
  let clock = 100
  let probes = 0
  const logs = []
  const request = await fixture(t, async () => ({ frames: [] }), {
    now: () => clock,
    logger: entry => logs.push(entry),
    probe: async () => { if (++probes === 1) throw new Error('secret HTML /private/path token=secret') },
  })
  const failed = await request('', 'GET', '/health')
  assert.equal(failed.status, 503)
  assert.equal(failed.body.code, 'renderer_browser_unavailable')
  assert.equal(probes, 1)
  assert.equal((await request('', 'GET', '/health')).status, 503)
  assert.equal(probes, 1)
  assert.doesNotMatch(JSON.stringify([failed, logs]), /secret|private|token|stack/)
  assert.equal(logs[0].event, 'renderer_readiness_failed')
  clock += 30000
  await request('', 'GET', '/health')
  await new Promise(resolve => setImmediate(resolve))
  assert.equal((await request('', 'GET', '/health')).status, 200)
  assert.equal(probes, 2)
})

test('Chromium launch failures expose fixed codes rather than browser diagnostics', async t => {
  const request = await fixture(t, html => renderHTML(html, async () => { throw new Error('sandbox /private/browser api_key=secret') }))
  const failed = await request({ html: '<svg/>' })
  assert.equal(failed.status, 503)
  assert.equal(failed.body.code, 'renderer_browser_unavailable')
  assert.equal(failed.body.stage, 'launch')
  assert.equal(failed.body.cause, 'launch_failed')
  assert.doesNotMatch(JSON.stringify(failed), /private|api_key|secret|stack/)
  assert.equal((await request('', 'GET', '/health')).status, 503, 'runtime browser failure invalidates cached readiness')
})

test('launch timeout stays distinct from content timeout and screenshot failure', async () => {
  await assert.rejects(renderHTML('<svg/>', async () => { throw Object.assign(new Error('private launch'), { name: 'TimeoutError' }) }), error => {
    assert.equal(error.code, 'renderer_browser_unavailable')
    assert.equal(error.causeCode, 'launch_timeout')
    return true
  })
  for (const stage of ['content', 'screenshot']) {
    let closed = false
    const page = {
      setDefaultTimeout() {}, on() {},
      setContent: async () => { if (stage === 'content') throw Object.assign(new Error('private content'), { name: 'TimeoutError' }) },
      screenshot: async () => { throw new Error('private screenshot') },
    }
    const browser = { newContext: async () => ({ route: async () => {}, routeWebSocket: async () => {}, newPage: async () => page, on() {} }), close: async () => { closed = true } }
    await assert.rejects(renderHTML('<svg/>', async () => browser), error => {
      assert.equal(error.stage, stage)
      assert.equal(error.code, stage === 'content' ? 'renderer_timeout' : 'renderer_render_failed')
      assert.doesNotMatch(error.message, /private/)
      return true
    })
    assert.equal(closed, true)
  }
})

test('hung browser cleanup fails readiness without launching overlapping browsers', async () => {
  let release
  const closing = new Promise(resolve => { release = resolve })
  const browser = {
    newContext: async () => { throw new Error('private context failure') },
    close: () => closing,
  }
  try {
    await assert.rejects(renderHTML('<svg/>', async () => browser), error => {
      assert.equal(error.code, 'renderer_browser_unavailable')
      assert.equal(error.stage, 'cleanup')
      assert.equal(error.causeCode, 'cleanup_timeout')
      return true
    })
    let launches = 0
    await assert.rejects(renderHTML('<svg/>', async () => { launches++; return browser }), error => {
      assert.equal(error.causeCode, 'cleanup_pending')
      return true
    })
    assert.equal(launches, 0)
  } finally {
    release()
    await new Promise(resolve => setImmediate(resolve))
  }
  await assert.rejects(renderHTML('<svg/>', async () => { throw new Error('private launch') }), error => {
    assert.equal(error.causeCode, 'launch_failed', 'completed cleanup permits launching again')
    return true
  })
})

test('rejected browser cleanup gates launches until disconnection is confirmed', async () => {
  let connected = true
  const browser = {
    newContext: async () => { throw new Error('private context failure') },
    close: async () => { throw new Error('private cleanup failure') },
    isConnected: () => connected,
  }
  try {
    await assert.rejects(renderHTML('<svg/>', async () => browser), error => {
      assert.equal(error.code, 'renderer_browser_unavailable')
      assert.equal(error.causeCode, 'cleanup_failed')
      return true
    })
    let launches = 0
    await assert.rejects(renderHTML('<svg/>', async () => { launches++; return browser }), error => {
      assert.equal(error.causeCode, 'cleanup_pending')
      return true
    })
    assert.equal(launches, 0)
  } finally {
    connected = false
  }
  await assert.rejects(renderHTML('<svg/>', async () => { throw new Error('private launch') }), error => {
    assert.equal(error.causeCode, 'launch_failed', 'confirmed disconnection permits launching again')
    return true
  })
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
