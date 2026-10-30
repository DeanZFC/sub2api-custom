import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import http from 'node:http'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { chromium } from 'playwright'
import { createRendererServer, renderHTML } from './server.mjs'

// In the shipped image Playwright uses its matching bundled Chromium. A local
// test may select an already installed executable without changing production.
const launch = options => chromium.launch({ ...options, ...(process.env.RENDERER_TEST_EXECUTABLE ? { executablePath: process.env.RENDERER_TEST_EXECUTABLE } : {}) })

test('health becomes ready only after a real sandboxed startup render', async t => {
  const dir = await mkdtemp(path.join(tmpdir(), 'renderer-ready-'))
  const socket = path.join(dir, 'renderer.sock')
  const server = createRendererServer(html => renderHTML(html, launch))
  await new Promise(resolve => server.listen(socket, resolve))
  t.after(async () => {
    await new Promise(resolve => server.close(resolve))
    await rm(dir, { recursive: true, force: true })
  })
  const health = () => new Promise((resolve, reject) => {
    const request = http.get({ socketPath: socket, path: '/health' }, response => {
      response.resume()
      response.on('end', () => resolve(response.statusCode))
    })
    request.on('error', reject)
  })
  assert.equal(await health(), 503, 'HTTP listening alone must not mark Chromium ready')
  const deadline = Date.now() + 15000
  let status = 503
  while (Date.now() < deadline && status !== 200) {
    await new Promise(resolve => setTimeout(resolve, 100))
    status = await health()
  }
  assert.equal(status, 200)
  assert.equal(await health(), 200)
})

test('real Chromium renders animation frames and blocks outbound content', async t => {
  let hits = 0
  const trap = http.createServer((_, response) => { hits++; response.end('network must not be used') })
  await new Promise(resolve => trap.listen(0, '127.0.0.1', resolve))
  t.after(() => new Promise(resolve => trap.close(resolve)))
  const target = `http://127.0.0.1:${trap.address().port}/forbidden`
  const html = `<!doctype html><html><body><svg width="960" height="720"><rect width="960" height="720" fill="white"/><circle cx="100" cy="200" r="50" fill="red"><animate attributeName="cx" values="100;800;100" dur="2.3s" repeatCount="indefinite"/></circle></svg><img src="${target}"><script src="${target}"></script><iframe src="${target}"></iframe><script>fetch('${target}').catch(()=>{});try{new WebSocket('${target.replace('http:', 'ws:')}')}catch{}; window.open('${target}')</script></body></html>`
  const { frames } = await renderHTML(html, launch)
  assert.deepEqual(frames.map(frame => frame.at_ms), [0, 700, 1700, 2900])
  assert.equal(hits, 0)
  const hashes = frames.map(frame => createHash('sha256').update(frame.png_base64).digest('hex'))
  assert.ok(new Set(hashes).size > 1, 'animation must advance between sampled frames')
  for (const frame of frames) assert.ok(Buffer.from(frame.png_base64, 'base64').length > 1000)
})
