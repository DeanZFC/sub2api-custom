import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestResultOutput from '../TestResultOutput.vue'
import { buildTestPreviewHTML } from '@/utils/testPreview'

const global = { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] }

describe('test result output', () => {
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
