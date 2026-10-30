import { mount } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import { nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import TestResultOutput from '../TestResultOutput.vue'
import { buildTestPreviewHTML } from '@/utils/testPreview'

const global = { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] }

describe('test result output', () => {
  it('shows pending and failed automatic review safely without leaking judge account metadata', async () => {
    const execution = {
      input_tokens: null, output_tokens: null, reasoning_tokens: null, cache_read_tokens: null, cache_creation_tokens: null, attempts: [],
      auto_review: { verdict: 'pending' as const, reason: '<script>untrusted judge text</script>', judge_account_id: 999123, judge_model: 'vision-a', attempts: 1, reviewed_at: '2026-10-27T10:00:00Z' },
    }
    const wrapper = mount(TestResultOutput, { global, props: { result: { id: 51, status: 'success', output_kind: 'html', output_html: '<svg />', execution_info: execution } } })
    expect(wrapper.get('[data-auto-review-verdict]').classes()).toContain('badge-warning')
    expect(wrapper.find('[data-auto-review-pending]').exists()).toBe(true)
    expect(wrapper.get('[data-auto-review-reason]').text()).toBe('<script>untrusted judge text</script>')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('999123')
    await wrapper.setProps({ result: { ...wrapper.props('result'), execution_info: { ...execution, auto_review: {
      ...execution.auto_review, verdict: 'fail', reason: 'Motion failed', criteria: [{ key: 'motion', passed: false, reason: 'No cycling movement' }],
    } } } })
    expect(wrapper.get('[data-auto-review-verdict]').classes()).toContain('badge-danger')
    expect(wrapper.find('[data-auto-review-pending]').exists()).toBe(false)
    expect(wrapper.get('[data-auto-review-criteria]').text()).toContain('No cycling movement')
    await wrapper.setProps({ compact: true })
    expect(wrapper.find('[data-auto-review-criteria]').exists()).toBe(false)
    expect(wrapper.get('[data-auto-review-reason]').text()).toBe('Motion failed')
    await wrapper.setProps({ thumbnail: true })
    expect(wrapper.find('[data-auto-review-verdict]').exists()).toBe(false)
    expect(wrapper.find('[data-auto-review-reason]').exists()).toBe(false)
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
    wrapper.unmount()
  })

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

  it.each([undefined, null, '', ' \n '])('collapses the raw response when a failed HTML result has no preview (%s)', outputHTML => {
    const response = '<!DOCTYPE html>\n<html lang="zh-CN"><head><style>body { margin: 0; }'
    const wrapper = mount(TestResultOutput, { global, props: { compact: true, result: { id: 2296, status: 'failed', output_kind: 'html', output_html: outputHTML, response_text: response } } })
    expect(wrapper.get('[data-html-preview-unavailable]').text()).toBe('tests.htmlPreviewUnavailable')
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.find('pre:not(details pre)').exists()).toBe(false)
    expect(wrapper.get('details').attributes('open')).toBeUndefined()
    expect(wrapper.get('details pre').text()).toBe(response)
    expect(wrapper.props('result').status).toBe('failed')
    wrapper.unmount()
  })

  it('keeps a failed HTML result previewable when a complete document is available', () => {
    const wrapper = mount(TestResultOutput, { global, props: { compact: true, result: { id: 9, status: 'failed', output_kind: 'html', output_html: '<svg><circle r="20" /></svg>', response_text: 'Original response' } } })
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('<circle')
    expect(wrapper.find('[data-html-preview-unavailable]').exists()).toBe(false)
    expect(wrapper.find('details').exists()).toBe(false)
    expect(wrapper.props('result').status).toBe('failed')
    wrapper.unmount()
  })

  it.each(['running', 'pending'])('keeps %s HTML output in progress while retaining collapsed diagnostics', status => {
    const wrapper = mount(TestResultOutput, { global, props: { compact: true, result: { id: 10, status, output_kind: 'html', response_text: '<html><body>Still generating' } } })
    expect(wrapper.text()).toContain('tests.running')
    expect(wrapper.find('[data-html-preview-unavailable]').exists()).toBe(false)
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.get('details').attributes('open')).toBeUndefined()
    expect(wrapper.find('pre:not(details pre)').exists()).toBe(false)
    wrapper.unmount()
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

  it('fits the full artwork within a thumbnail height and removes the limit for an expanded view', async () => {
    const measure = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 300 } as DOMRect)
    const wrapper = mount(TestResultOutput, { global, props: { compact: true, maxPreviewHeight: 180, result: { id: 4, status: 'success', output_kind: 'html', output_html: '<div style="width:1200px;height:1600px">Tall artwork</div>' } } })
    const frame = wrapper.get('iframe')
    window.dispatchEvent(new MessageEvent('message', { source: frame.element.contentWindow, data: { type: 'sub2api-test-preview-size', height: 1600, width: 1200 } }))
    await nextTick()
    expect(frame.element.style.width).toBe('1200px')
    expect(frame.element.style.height).toBe('1600px')
    expect(frame.element.style.transform).toBe('scale(0.1125)')
    expect(frame.element.style.left).toBe('82.5px')
    expect(frame.element.parentElement?.style.height).toBe('180px')
    await wrapper.setProps({ maxPreviewHeight: undefined })
    expect(frame.element.style.transform).toBe('scale(0.25)')
    expect(frame.element.parentElement?.style.height).toBe('400px')
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
