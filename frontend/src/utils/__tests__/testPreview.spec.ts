import { describe, expect, it } from 'vitest'
import { buildTestPreviewHTML, freezeTestPreviewViewportValue } from '../testPreview'

describe('test preview viewport sizing', () => {
  it.each([
    ['100vh', '640px'],
    ['min(100svh, 90dvw)', 'min(640px, 864px)'],
    ['calc(100lvh - 2vmin + .5vmax)', 'calc(640px - 12.8px + 4.8px)'],
    ['-20vw 5vb 5vi', '-192px 32px 48px'],
    ['"100vh" url(data:image/svg+xml;50vh) 20vh', '"100vh" url(data:image/svg+xml;50vh) 128px'],
    ['min(90%, 1200px)', 'min(90%, 1200px)'],
    ['var(--panel100vh, 80vh)', 'var(--panel100vh, 512px)'],
  ])('freezes %s to the initial render viewport', (source, expected) => {
    expect(freezeTestPreviewViewportValue(source, 960, 640)).toBe(expected)
  })

  it('propagates the trusted page nonce without relaxing isolation', () => {
    const source = '<script>window.animate = true</script><script src="https://example.com/external.js"></script><svg viewBox="0 0 960 640"></svg>'
    const preview = new DOMParser().parseFromString(buildTestPreviewHTML(source, 'testNonce'), 'text/html')
    expect(preview.querySelectorAll('script')).toHaveLength(2)
    for (const script of preview.querySelectorAll('script')) expect(script.getAttribute('nonce')).toBe('testNonce')
    const policy = preview.querySelector('meta[http-equiv="Content-Security-Policy"]')?.getAttribute('content')
    expect(policy).toContain("script-src 'unsafe-inline'")
    expect(policy).not.toContain('nonce-testNonce')
    expect(policy).toContain("default-src 'none'")
    expect(preview.querySelector('script[src]')).toBeNull()
    expect(preview.querySelector('style')?.textContent).not.toContain('svg {')
  })
})
