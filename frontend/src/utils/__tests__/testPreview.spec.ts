import { describe, expect, it, vi } from 'vitest'
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

  it.each([[0, 0], [0, 640], [960, 0]])('waits for a valid lazy iframe viewport when initially %s x %s', (width, height) => {
    vi.useFakeTimers()
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { callback(0); return 0 })
    try {
      const preview = new DOMParser().parseFromString(buildTestPreviewHTML('<main style="width:94vw;height:100vh">Preview</main>'), 'text/html')
      const content = preview.querySelector('main')!
      const previewWindow = Object.assign(new EventTarget(), {
        innerWidth: width, innerHeight: height, scrollX: 0, scrollY: 0,
        parent: { postMessage: vi.fn() },
      })
      Object.defineProperties(preview.documentElement, {
        scrollWidth: { get: () => previewWindow.innerWidth },
        scrollHeight: { get: () => previewWindow.innerHeight },
      })
      // Run the generated sizing script as the sandbox would, with an
      // offscreen viewport that only becomes available after a resize event.
      new Function('window', 'document', preview.scripts[0].textContent!)(previewWindow, preview)
      vi.advanceTimersByTime(3000)
      expect(content.style.width).toBe('94vw')
      expect(content.style.height).toBe('100vh')
      expect(previewWindow.parent.postMessage).not.toHaveBeenCalled()

      previewWindow.innerWidth = 960
      previewWindow.innerHeight = 640
      previewWindow.dispatchEvent(new Event('resize'))
      vi.advanceTimersByTime(250)
      expect(content.style.width).toBe('902.4px')
      expect(content.style.height).toBe('640px')
      expect(previewWindow.parent.postMessage).toHaveBeenCalledWith({ type: 'sub2api-test-preview-size', width: 960, height: 640 }, '*')

      previewWindow.innerWidth = 1200
      previewWindow.innerHeight = 900
      previewWindow.dispatchEvent(new Event('resize'))
      vi.advanceTimersByTime(250)
      expect(content.style.width).toBe('902.4px')
      expect(content.style.height).toBe('640px')
    } finally {
      vi.useRealTimers()
      vi.unstubAllGlobals()
    }
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
