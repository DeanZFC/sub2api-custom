import { mount } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import { nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import TestResultOutput from '../TestResultOutput.vue'
import { buildTestPreviewHTML } from '@/utils/testPreview'

const global = { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] }

describe('test result output', () => {
  it('renders model metadata and its verdict without exposing raw response text', async () => {
    const check = { requested_model: 'public-alias', upstream_model: 'gpt-5.6-sol', returned_models: ['gpt-5.6-sol-2026-09-21'], match_mode: 'snapshot' as const, verdict: 'pass' as const, reason: 'match' as const }
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 50, status: 'success', output_kind: 'model_check', output_model_check: check, response_text: '{"private":"not for display"}' } } })
    expect(wrapper.get('[data-model-check-verdict]').text()).toBe('tests.modelCheck.pass')
    expect(wrapper.get('[data-model-check-requested]').text()).toBe('public-alias')
    expect(wrapper.get('[data-model-check-upstream]').text()).toBe('gpt-5.6-sol')
    expect(wrapper.get('[data-model-check-returned]').text()).toBe('gpt-5.6-sol-2026-09-21')
    expect(wrapper.find('[data-model-check-reason]').exists()).toBe(false)
    expect(wrapper.find('pre').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('private')
    await wrapper.setProps({ result: { ...wrapper.props('result'), output_model_check: { ...check, verdict: 'fail', reason: 'mismatch', returned_models: ['gpt-5.6-sol', '<script>not-a-model</script>'] } } })
    expect(wrapper.get('[data-model-check-verdict]').classes()).toContain('badge-danger')
    expect(wrapper.get('[data-model-check-reason]').text()).toBe('tests.modelCheck.reasons.mismatch')
    expect(wrapper.get('[data-model-check-returned]').text()).toContain('<script>not-a-model</script>')
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.setProps({ result: { ...wrapper.props('result'), output_model_check: { ...check, verdict: 'unknown', reason: 'missing_model', returned_models: [] } } })
    expect(wrapper.get('[data-model-check-verdict]').classes()).toContain('badge-warning')
    expect(wrapper.get('[data-model-check-returned]').text()).toBe('-')
    expect(wrapper.get('[data-model-check-reason]').text()).toBe('tests.modelCheck.reasons.missing_model')
    await wrapper.setProps({ result: { ...wrapper.props('result'), output_model_check: null } })
    expect(wrapper.find('[data-model-check-output]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('private')
    wrapper.unmount()
  })

  it('shows recent request colors with the newest on the right and no hover or click details', async () => {
    const recent = [
      { success: false, created_at: '2026-09-21T10:59:00Z' },
      { success: true, created_at: '2026-09-21T10:58:00Z' },
      { success: false, created_at: '2026-09-21T10:57:00Z' },
    ]
    const statistics = { window_start: '2026-09-21T10:00:00Z', window_end: '2026-09-21T11:00:00Z', total_requests: 3, success_requests: 1, failed_requests: 2, success_rate: 1 / 3, cache_rate: null, avg_first_token_ms: null, first_token_samples: 0, cache_read_tokens: 0, cache_input_tokens: 0, recent_requests: recent }
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 42, status: 'success', output_kind: 'statistics', output_statistics: statistics } } })
    const region = wrapper.get('[data-statistics-recent-requests]')
    expect(region.get('time').attributes('datetime')).toBe(recent[0].created_at)
    expect(region.findAll('[data-request-outcome]').map(bar => bar.classes().includes('bg-emerald-500'))).toEqual([false, true, false])
    for (const bar of region.findAll('[data-request-outcome]')) {
      expect(bar.element.closest('[title]')).toBeNull()
      expect(bar.attributes('tabindex')).toBeUndefined()
      await bar.trigger('mouseenter')
      await bar.trigger('click')
    }
    expect(region.find('button').exists()).toBe(false)
    expect(wrapper.find('[role="dialog"], [role="tooltip"]').exists()).toBe(false)
    expect(document.body.querySelector('[role="dialog"], [role="tooltip"]')).toBeNull()
    await wrapper.setProps({ result: { ...wrapper.props('result'), output_statistics: { ...statistics, recent_requests: [{ ...recent[0], success: true }, ...Array.from({ length: 12 }, () => ({ ...recent[1], success: false }))] } } })
    expect(region.findAll('[data-request-outcome]')).toHaveLength(10)
    expect(region.findAll('[data-request-outcome]').at(-1)!.classes()).toContain('bg-emerald-500')
    await wrapper.setProps({ result: { ...wrapper.props('result'), output_statistics: { ...statistics, recent_requests: undefined } } })
    expect(region.find('[data-request-outcome]').exists()).toBe(false)
    expect(region.text()).toContain('—')
    wrapper.unmount()
  })

  it('renders structured statistics and never falls back to raw statistics JSON', async () => {
    const wrapper = mount(TestResultOutput, { global, props: { result: {
      id: 42, status: 'success', output_kind: 'statistics', response_text: '{"private":"do not render"}',
      output_statistics: { window_start: '2026-09-21T10:00:00Z', window_end: '2026-09-21T11:00:00Z', total_requests: 20, success_requests: 19, failed_requests: 1, success_rate: 0.95, cache_rate: 0.625, avg_first_token_ms: 432.6, first_token_samples: 19, cache_read_tokens: 625, cache_input_tokens: 1000 },
    } } })
    expect(wrapper.get('[data-success-rate]').text()).toBe('95.0%')
    expect(wrapper.get('[data-cache-rate]').text()).toBe('62.5%')
    expect(wrapper.get('[data-first-token]').text()).toBe('433ms')
    expect(wrapper.find('pre').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('private')
    await wrapper.setProps({ result: { ...wrapper.props('result'), output_statistics: null } })
    expect(wrapper.find('[data-statistics-output]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('private')
    wrapper.unmount()
  })

  it('shows no-data metrics as dashes while retaining valid zero percentages', async () => {
    const statistics = { window_start: 'invalid', window_end: 'invalid', total_requests: 0, success_requests: 0, failed_requests: 0, success_rate: null, cache_rate: null, avg_first_token_ms: null, first_token_samples: 0, cache_read_tokens: 0, cache_input_tokens: 0 }
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 42, status: 'success', output_kind: 'statistics', output_statistics: statistics } } })
    for (const selector of ['[data-success-rate]', '[data-cache-rate]', '[data-first-token]']) expect(wrapper.get(selector).text()).toBe('—')
    await wrapper.setProps({ result: { ...wrapper.props('result'), output_statistics: { ...statistics, total_requests: 10, failed_requests: 10, success_rate: 0, cache_input_tokens: 100, cache_rate: 0, first_token_samples: 1, avg_first_token_ms: 0 } } })
    expect(wrapper.get('[data-success-rate]').text()).toBe('0.0%')
    expect(wrapper.get('[data-cache-rate]').text()).toBe('0.0%')
    expect(wrapper.get('[data-first-token]').text()).toBe('0ms')
    await wrapper.setProps({ result: { ...wrapper.props('result'), output_statistics: { ...statistics, total_requests: 0, success_rate: 1, cache_rate: 0, avg_first_token_ms: 0 } } })
    for (const selector of ['[data-success-rate]', '[data-cache-rate]', '[data-first-token]']) expect(wrapper.get(selector).text()).toBe('—')
    wrapper.unmount()
  })
  it('shows zero as a numeric result and keeps the original response available', () => {
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 1, status: 'success', output_kind: 'number', output_numeric: 0, response_text: 'The answer is 0.' } } })
    expect(wrapper.text()).toContain('0')
    expect(wrapper.find('details').text()).toContain('The answer is 0.')
    expect(wrapper.find('iframe').exists()).toBe(false)
  })

  it('renders an opaque sandbox result immediately and preserves SVG animation', () => {
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 2, status: 'success', output_kind: 'html', output_html: '<svg xmlns="http://www.w3.org/2000/svg"><circle r="20"><animate attributeName="r" values="10;20;10" dur="1s" repeatCount="indefinite" /></circle></svg>' } } })
    const frame = wrapper.get('iframe')
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.attributes('srcdoc')).toContain('<animate')
    expect(frame.attributes('srcdoc')).toContain("default-src 'none'")
  })

  it('renders unknown future output kinds as escaped text', () => {
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 3, status: 'success', output_kind: 'future-json', response_text: '<img src=x onerror=alert(1)>' } } })
    expect(wrapper.get('pre').text()).toBe('<img src=x onerror=alert(1)>')
    expect(wrapper.find('img').exists()).toBe(false)
  })

  it('scales the full HTML document in a compact gallery without clipping tall or wide content', async () => {
    const measure = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 240 } as DOMRect)
    const wrapper = mount(TestResultOutput, { global, props: { compact: true, result: { id: 4, status: 'success', output_kind: 'html', output_html: '<div style="width:1200px;height:1600px">Full artwork</div>', response_text: 'raw content' } } })
    const frame = wrapper.get('iframe')
    window.dispatchEvent(new MessageEvent('message', { source: frame.element.contentWindow, data: { type: 'sub2api-test-preview-size', height: 1600, width: 1200 } }))
    await nextTick()
    expect(frame.attributes('style')).toContain('width: 1200px')
    expect(frame.attributes('style')).toContain('height: 1600px')
    expect(frame.attributes('style')).toContain('scale(0.2)')
    expect(frame.element.parentElement?.style.height).toBe('320px')
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(wrapper.find('details').exists()).toBe(false)
    wrapper.unmount()
    measure.mockRestore()
  })

  it('ignores preview sizing messages sent by another iframe', async () => {
    const wrapper = mount(TestResultOutput, { global, props: { compact: true, result: { id: 5, status: 'success', output_kind: 'html', output_html: '<p>Preview</p>' } } })
    window.dispatchEvent(new MessageEvent('message', { source: window, data: { type: 'sub2api-test-preview-size', height: 9000, width: 9000 } }))
    await nextTick()
    expect(wrapper.get('iframe').attributes('style')).toContain('height: 640px')
    wrapper.unmount()
  })

  it('fits wide history output and keeps measurements received before load', async () => {
    const measure = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 300 } as DOMRect)
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 6, status: 'success', output_kind: 'html', output_html: '<div style="width:1200px;height:1600px">Full history</div>' } } })
    const frame = wrapper.get('iframe')
    window.dispatchEvent(new MessageEvent('message', { source: frame.element.contentWindow, data: { type: 'sub2api-test-preview-size', height: 1600, width: 1200 } }))
    await nextTick()
    await frame.trigger('load')
    expect(frame.attributes('style')).toContain('width: 1200px')
    expect(frame.attributes('style')).toContain('scale(0.25)')
    expect(frame.element.parentElement?.style.height).toBe('400px')

    await wrapper.setProps({ result: { ...wrapper.props('result') } })
    expect(wrapper.get('iframe').element).toBe(frame.element)
    expect(wrapper.get('iframe').element.parentElement?.style.height).toBe('400px')

    await wrapper.setProps({ result: { id: 7, status: 'success', output_kind: 'html', output_html: '<p>Next result</p>' } })
    const nextFrame = wrapper.get('iframe')
    expect(nextFrame.element).not.toBe(frame.element)
    expect(nextFrame.attributes('style')).toContain('height: 640px')
    wrapper.unmount()
    measure.mockRestore()
  })

  it('authorizes sandbox scripts with the host nonce without weakening isolation', () => {
    const hostScript = document.createElement('script')
    hostScript.nonce = 'test-preview-nonce'
    document.head.appendChild(hostScript)
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 8, status: 'success', output_kind: 'html', output_html: '<script nonce="untrusted">document.body.dataset.animated="yes"</script>' } } })
    const frame = wrapper.get('iframe')
    const preview = new DOMParser().parseFromString(frame.attributes('srcdoc'), 'text/html')
    expect(Array.from(preview.scripts).every(script => script.nonce === 'test-preview-nonce')).toBe(true)
    const policy = preview.querySelector('meta[http-equiv]')?.getAttribute('content')
    expect(policy).toContain("script-src 'unsafe-inline'")
    expect(policy).not.toContain("'nonce-test-preview-nonce'")
    expect(policy).toContain("default-src 'none'")
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(preview.querySelector('[nonce="untrusted"]')).toBeNull()
    wrapper.unmount()
    hostScript.remove()
  })

  it('blocks external embeds while allowing inline animation in the opaque sandbox', () => {
    const source = '<html><head><meta http-equiv="refresh" content="0;url=https://bad.test"><base href="https://bad.test"></head><body onload="alert(1)"><script>document.body.dataset.animated="yes"</script><script src="https://bad.test/script.js"></script><iframe src="https://bad.test"></iframe><a href="https://bad.test">click</a><svg><use href="#shape"/></svg></body></html>'
    const preview = buildTestPreviewHTML(source)
    const document = new DOMParser().parseFromString(preview, 'text/html')
    expect(document.querySelector('script[src], iframe, base, [onload]')).toBeNull()
    expect(document.head.firstElementChild?.getAttribute('http-equiv')).toBe('Content-Security-Policy')
    expect(document.querySelector('a')?.hasAttribute('href')).toBe(false)
    expect(document.querySelector('use')?.getAttribute('href')).toBe('#shape')
    expect(document.querySelector('script')?.textContent).toContain('dataset.animated')
  })
})
