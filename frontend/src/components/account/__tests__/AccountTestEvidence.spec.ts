import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import AccountTestModal from '../AccountTestModal.vue'
import AdminAccountTestModal from '@/components/admin/account/AccountTestModal.vue'
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAvailableModels: vi.fn().mockResolvedValue([{ id: 'gpt-6-astra', display_name: 'GPT-6' }]) } } }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
const account = { id: 1, name: 'BPS', platform: 'openai', type: 'oauth', credentials: {}, extra: {} } as any
const stubs = { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' }, Select: true, TextArea: true, Icon: true, ExcelAuthorizationPanel: true }
function events(items: object[]) {
  const encoded = new TextEncoder().encode(items.map(event => `data: ${JSON.stringify(event)}\n\n`).join(''))
  let done = false
  return { ok: true, body: { getReader: () => ({ read: async () => { if (done) return { done: true }; done = true; return { done: false, value: encoded } } }) } }
}
beforeEach(() => vi.stubGlobal('fetch', vi.fn()))
afterEach(() => vi.unstubAllGlobals())
for (const [name, component] of [['account', AccountTestModal], ['admin', AdminAccountTestModal]] as const) {
  describe(`${name} BPS diagnostic evidence`, () => {
    it('keeps actual upstream 403 evidence after failure and emits once', async () => {
      vi.mocked(fetch).mockResolvedValue(events([{ type: 'test_start', model: 'gpt-6-astra' }, { type: 'upstream_response', upstream_status: 403, upstream_model: 'gpt-6-astra', request_id: 'request-123', upstream_error_code: 'access_denied' }, { type: 'error', error: 'basispoints_upstream_error' }, { type: 'test_complete', success: false }]) as Response)
      const wrapper = mount(component, { props: { show: true, account }, global: { stubs } })
      ;(wrapper.vm as any).selectedModelId = 'gpt-6-astra'
      await (wrapper.vm as any).startTest()
      for (const text of ['403', 'request-123', 'access_denied', 'testRequestStarted']) expect(wrapper.text()).toContain(text)
      expect(wrapper.text()).not.toContain('connectionEstablished')
      expect(wrapper.emitted('tested')).toHaveLength(1)
      wrapper.unmount()
    })
    it('does not invent upstream evidence after a transport failure', async () => {
      vi.mocked(fetch).mockRejectedValue(new Error('network unreachable'))
      const wrapper = mount(component, { props: { show: true, account }, global: { stubs } })
      ;(wrapper.vm as any).selectedModelId = 'gpt-6-astra'
      await (wrapper.vm as any).startTest()
      expect((wrapper.vm as any).upstreamEvidence).toBeNull()
      expect(wrapper.emitted('tested')).toHaveLength(1)
      wrapper.unmount()
    })
  })
}
