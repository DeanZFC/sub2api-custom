import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CodexTicketProxyTest from '../CodexTicketProxyTest.vue'

const testProxy = vi.hoisted(() => vi.fn())
vi.mock('@/api', () => ({ adminAPI: { settings: { testCodexTicketProxy: testProxy } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({
  t: (key: string, params?: Record<string, unknown>) => `${key} ${Object.values(params || {}).join(' ')}`.trim(),
}) }))
const pool = 'http://user:***@proxy1.invalid:8080\nsocks5h://user:***@proxy2.invalid:1080'

function mountTest() {
  return mount(CodexTicketProxyTest, {
    props: { proxyPool: pool, savedProxyPool: pool, savedProxyCount: 2 },
    global: { stubs: { Icon: true } },
  })
}

describe('CodexTicketProxyTest', () => {
  beforeEach(() => { testProxy.mockReset().mockResolvedValue({ proxy_index: 2, success: true, exit_ip: '198.51.100.4', latency_ms: 90 }) })

  it('tests one selected saved proxy by index without submitting masked credentials', async () => {
    const wrapper = mountTest()
    expect(testProxy).not.toHaveBeenCalled()
    await wrapper.get('select').setValue('2')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(testProxy).toHaveBeenCalledWith(2, expect.any(AbortSignal))
    expect(wrapper.text()).toContain('198.51.100.4')
    expect(wrapper.html()).not.toContain('user:***')
  })

  it('disables testing for unsaved changes and enables after the saved snapshot updates', async () => {
    const wrapper = mountTest()
    await wrapper.setProps({ proxyPool: pool + '\nhttp://third.invalid:8080' })
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('saveFirst')
    await wrapper.get('button').trigger('click')
    expect(testProxy).not.toHaveBeenCalled()
    await wrapper.setProps({ savedProxyPool: pool + '\nhttp://third.invalid:8080', savedProxyCount: 3 })
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
    expect(wrapper.findAll('option')).toHaveLength(3)
    await wrapper.setProps({ savedProxyPool: '', proxyPool: '', savedProxyCount: 0 })
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
  })

  it('aborts pending tests after proxy edits and never lets stale results replace newer results', async () => {
    let resolve!: (result: unknown) => void
    testProxy.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = mountTest()
    await wrapper.get('button').trigger('click')
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    const previousSignal = testProxy.mock.calls[0][1] as AbortSignal
    await wrapper.setProps({ proxyPool: pool + '\nhttp://third.invalid:8080' })
    expect(previousSignal.aborted).toBe(true)
    await wrapper.setProps({ proxyPool: pool })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('198.51.100.4')
    resolve({ proxy_index: 1, success: true, exit_ip: '198.51.100.5', latency_ms: 90 })
    await flushPromises()
    expect(wrapper.text()).not.toContain('198.51.100.5')
    expect(wrapper.text()).toContain('198.51.100.4')
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
  })

  it('uses the server count even when masked proxy rows are identical', async () => {
    const wrapper = mountTest()
    const maskedPool = 'http://user:***@same.invalid:8080\nhttp://user:***@same.invalid:8080'
    await wrapper.setProps({ proxyPool: maskedPool, savedProxyPool: maskedPool, savedProxyCount: 2 })
    expect(wrapper.findAll('option')).toHaveLength(2)
    await wrapper.setProps({ savedProxyCount: 1 })
    expect(wrapper.findAll('option')).toHaveLength(1)
  })

  it('maps unsuccessful HTTP 200 results and structured API errors to fixed messages', async () => {
    testProxy.mockResolvedValueOnce({ proxy_index: 1, success: false, latency_ms: 40, error_code: 'proxy_auth' })
    const wrapper = mountTest()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('errors.proxy_auth')
    testProxy.mockRejectedValueOnce({ status: 400, code: 400, error: 'invalid_proxy_index', message: 'user:password@proxy.invalid' })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('errors.invalid_proxy_index')
    expect(wrapper.html()).not.toContain('password')
    testProxy.mockRejectedValueOnce({ code: 'secret-token', message: 'raw secret' })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('errors.unknown')
    expect(wrapper.html()).not.toContain('secret-token')
  })
})
