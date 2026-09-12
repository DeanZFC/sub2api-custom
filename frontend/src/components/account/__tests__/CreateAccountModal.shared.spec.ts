import { defineComponent } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { http, app } = vi.hoisted(() => ({
  http: { get: vi.fn(), post: vi.fn() },
  app: { showError: vi.fn(), showSuccess: vi.fn(), showWarning: vi.fn() }
}))
vi.mock('@/api/client', () => ({ apiClient: http, default: http }))
vi.mock('@/stores/app', () => ({ useAppStore: () => app }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
import CreateAccountModal from '../CreateAccountModal.vue'

const Dialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' })
function modal() {
  return mount(CreateAccountModal, {
    props: { show: true, sharedPool: true, proxies: [], groups: [] },
    global: { mocks: { $t: (key: string) => key }, stubs: { BaseDialog: Dialog, ConfirmDialog: true, Select: true, Icon: true, PlatformIcon: true, HelpTooltip: true } }
  })
}
function button(wrapper: VueWrapper, text: string) {
  const found = wrapper.findAll('button').find(b => b.text() === text || b.text().endsWith(text))
  if (!found) throw new Error('Missing button: ' + text)
  return found
}
async function basic(wrapper: VueWrapper, platform = 'OpenAI') {
  await button(wrapper, platform).trigger('click')
  await wrapper.get('[data-tour="account-form-name"]').setValue('共享测试')
  await wrapper.get('#shared-account-proxy').setValue('socks5://user:pass@proxy.example.com:1080')
  await wrapper.get('#shared-account-concurrency').setValue(4)
  await wrapper.get('#shared-account-rate').setValue(1.6)
}
async function next(wrapper: VueWrapper) {
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}
function listingPayload() {
  return http.post.mock.calls.find(([url]) => url === '/user/shared-pool/listings')?.[1]
}
function expectOnlyUserAPI() {
  const urls = [...http.get.mock.calls, ...http.post.mock.calls].map(([url]) => url)
  expect(urls.every(url => url.startsWith('/user/shared-pool/'))).toBe(true)
}
beforeEach(() => {
  vi.clearAllMocks()
  http.get.mockResolvedValue({ data: { ai_studio_oauth_enabled: false } })
  http.post.mockImplementation(async (url: string, payload: Record<string, unknown>) => {
    if (url.endsWith('/generate-auth-url')) return { data: { auth_url: 'https://auth.example.com/authorize?state=state-1', session_id: 'session-1', state: 'state-1' } }
    if (url.endsWith('/exchange-code') || url.endsWith('/refresh-token') || url.endsWith('/cookie-auth')) return { data: { access_token: 'access-test', refresh_token: 'refresh-test', expires_at: 2000000000, chatgpt_account_id: 'acct-test' } }
    if (url.endsWith('/parse-session')) return { data: [{ index: 1, name: '共享测试', credentials: String(payload.content).includes('agentIdentity') ? { auth_mode: 'agent_identity', agent_runtime_id: 'runtime', agent_private_key: 'private', chatgpt_account_id: 'team' } : { access_token: 'access-test', refresh_token: 'refresh-test', chatgpt_account_id: 'team' } }] }
    if (url.endsWith('/validate-pat')) return { data: { credentials: { access_token: 'at-test', auth_mode: 'personal_access_token', chatgpt_account_id: 'team' } } }
    if (url.endsWith('/validate-sso')) return { data: { credentials: { access_token: 'grok-access', refresh_token: 'grok-refresh', team_id: 'team' } } }
    if (url.endsWith('/listings')) return { data: { id: 12, account_id: 42, status: 'active' } }
    throw new Error('Unexpected request: ' + url)
  })
})

describe('shared account creation with real authorization UI', () => {
  it('removes optional settings for every platform and makes no admin requests on open/reset', async () => {
    const wrapper = modal()
    for (const platform of ['OpenAI', 'Anthropic', 'Gemini', 'Antigravity', 'Grok']) {
      await button(wrapper, platform).trigger('click')
      await flushPromises()
      for (const label of ['notes', 'priority', 'expiresAt', 'modelRestriction', 'poolMode', 'loadFactor', 'autoPauseOnExpired']) {
        expect(wrapper.find('form').text()).not.toContain('admin.accounts.' + label)
      }
      expect(wrapper.find('#shared-account-rate').exists()).toBe(true)
      expect(wrapper.find('proxy-selector').exists()).toBe(false)
    }
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expectOnlyUserAPI()
    wrapper.unmount()
  })
  it('creates an API key listing using the entered proxy, concurrency and price', async () => {
    const wrapper = modal()
    await basic(wrapper)
    await button(wrapper, 'admin.accounts.types.responsesApi').trigger('click')
    const apiInput = wrapper.findAll('input').find(input => input.attributes('placeholder') === 'sk-proj-...')
    expect(apiInput).toBeDefined()
    await apiInput!.setValue('sk-key')
    await next(wrapper)
    expect(listingPayload()).toMatchObject({ name: '共享测试', platform: 'openai', type: 'apikey', credentials: { api_key: 'sk-key' }, concurrency: 4, sell_rate: 1.6, proxy_url: 'socks5://user:pass@proxy.example.com:1080' })
    expect(listingPayload()).not.toHaveProperty('concurrency_multiplier')
    expect(listingPayload().credentials).not.toHaveProperty('model_mapping')
    expect(wrapper.emitted('created')).toHaveLength(1)
    expectOnlyUserAPI()
    wrapper.unmount()
  })
  it('generates a real auth link and exchanges its callback before publishing', async () => {
    const wrapper = modal()
    await basic(wrapper)
    await next(wrapper)
    expect(wrapper.findAll('input[type="radio"]').map(r => r.attributes('value'))).toEqual(expect.arrayContaining(['manual', 'refresh_token', 'mobile_refresh_token', 'codex_session', 'agent_identity', 'codex_pat']))
    await button(wrapper, 'admin.accounts.oauth.openai.generateAuthUrl').trigger('click')
    await flushPromises()
    expect(http.post).toHaveBeenCalledWith('/user/shared-pool/oauth/openai/generate-auth-url', expect.objectContaining({ proxy_url: 'socks5://user:pass@proxy.example.com:1080' }))
    const textarea = wrapper.findAll('textarea')[0]
    await textarea.setValue('http://localhost:1455/auth/callback?code=code-1&state=state-1')
    await button(wrapper, 'admin.accounts.oauth.completeAuth').trigger('click')
    await flushPromises()
    expect(http.post).toHaveBeenCalledWith('/user/shared-pool/oauth/openai/exchange-code', expect.objectContaining({ code: 'code-1', state: 'state-1', session_id: 'session-1' }))
    expect(listingPayload()).toMatchObject({ type: 'oauth', credentials: { access_token: 'access-test', refresh_token: 'refresh-test' } })
    expectOnlyUserAPI()
    wrapper.unmount()
  })
  it.each(['refresh_token', 'mobile_refresh_token'])('validates %s and publishes to shared pool', async method => {
    const wrapper = modal()
    await basic(wrapper)
    await next(wrapper)
    await wrapper.get('input[value="' + method + '"]').setValue(true)
    await wrapper.get('textarea').setValue('rt-test')
    await button(wrapper, 'admin.accounts.oauth.openai.validateAndCreate').trigger('click')
    await flushPromises()
    expect(listingPayload()).toMatchObject({ platform: 'openai', type: 'oauth', credentials: { access_token: 'access-test' } })
    const refresh = http.post.mock.calls.find(([url]) => url.endsWith('/refresh-token'))
    expect(refresh?.[1]).toMatchObject({ refresh_token: 'rt-test' })
    if (method === 'mobile_refresh_token') expect(refresh?.[1].client_id).toBeTruthy()
    expectOnlyUserAPI()
    wrapper.unmount()
  })
  it.each(['codex_session', 'agent_identity', 'codex_pat'])('imports %s through user endpoints', async method => {
    const wrapper = modal()
    await basic(wrapper)
    await next(wrapper)
    await wrapper.get('input[value="' + method + '"]').setValue(true)
    const value = method === 'agent_identity' ? JSON.stringify({ auth_mode: 'agentIdentity', agent_identity: { agent_runtime_id: 'runtime', agent_private_key: 'private' } }) : method === 'codex_pat' ? 'at-test' : '{"tokens":{"access_token":"access-test","refresh_token":"refresh-test"}}'
    await wrapper.get('textarea').setValue(value)
    await button(wrapper, method === 'codex_pat' ? 'admin.accounts.oauth.openai.codexPatImportAndCreate' : 'admin.accounts.oauth.openai.codexSessionImportAndCreate').trigger('click')
    await flushPromises()
    const payload = listingPayload()
    expect(payload).toMatchObject({ platform: 'openai', type: 'oauth', concurrency: 4, sell_rate: 1.6 })
    if (method === 'agent_identity') {
      expect(payload.credentials).toMatchObject({ agent_runtime_id: 'runtime', agent_private_key: 'private' })
      expect(payload.credentials).not.toHaveProperty('access_token')
    }
    if (method === 'codex_pat') expect(payload.credentials).toMatchObject({ access_token: 'at-test', chatgpt_account_id: 'team' })
    expect(wrapper.emitted('created')).toHaveLength(1)
    expectOnlyUserAPI()
    wrapper.unmount()
  })
  it('imports Grok SSO through user endpoints without exposing the SSO cookie to listing storage', async () => {
    const wrapper = modal()
    await basic(wrapper, 'Grok')
    await next(wrapper)
    await wrapper.get('input[value="sso_cookie"]').setValue(true)
    await wrapper.get('textarea').setValue('sso-test')
    await button(wrapper, 'admin.accounts.oauth.grok.convertSSOAndCreate').trigger('click')
    await flushPromises()
    expect(listingPayload()).toMatchObject({ platform: 'grok', credentials: { access_token: 'grok-access', refresh_token: 'grok-refresh', team_id: 'team' } })
    expect(JSON.stringify(listingPayload())).not.toContain('sso-test')
    expectOnlyUserAPI()
    wrapper.unmount()
  })
  it.each(['Gemini', 'Antigravity', 'Grok'])('generates %s authorization with the user proxy', async platform => {
    const wrapper = modal()
    await basic(wrapper, platform)
    await next(wrapper)
    await button(wrapper, 'admin.accounts.oauth.' + platform.toLowerCase() + '.generateAuthUrl').trigger('click')
    await flushPromises()
    expect(http.post).toHaveBeenCalledWith('/user/shared-pool/oauth/' + platform.toLowerCase() + '/generate-auth-url', expect.objectContaining({ proxy_url: 'socks5://user:pass@proxy.example.com:1080' }))
    expectOnlyUserAPI()
    wrapper.unmount()
  })
  it.each([
    { platform: 'openai', type: 'apikey', selected: 'admin.accounts.types.responsesApi', selectedClass: 'border-purple-500' },
    { platform: 'grok', type: 'apikey', selected: 'admin.accounts.types.responsesApi', selectedClass: 'border-purple-500', platformButton: 'Grok' },
    { platform: 'anthropic', type: 'bedrock', selected: 'admin.accounts.bedrockLabel', selectedClass: 'border-amber-500', platformButton: 'Anthropic' }
  ])('echoes $platform $type when editing a shared listing', async ({ platform, type, selected, selectedClass, platformButton }) => {
    const wrapper = mount(CreateAccountModal, {
      props: {
        show: false,
        sharedPool: true,
        readonlyPlatform: true,
        proxies: [],
        groups: [],
        initialAccount: { name: '已有账号', platform, type, concurrency: 4, rate_multiplier: 1.6 }
      },
      global: { mocks: { $t: (key: string) => key }, stubs: { BaseDialog: Dialog, ConfirmDialog: true, Select: true, Icon: true, PlatformIcon: true, HelpTooltip: true } }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect((wrapper.get('[data-tour="account-form-name"]').element as HTMLInputElement).value).toBe('已有账号')
    if (platformButton) expect(wrapper.text()).toContain(platformButton)
    const selectedButton = wrapper.find('[data-tour="account-form-type"]').findAll('button').find(button => button.text().includes(selected))
    expect(selectedButton?.classes().join(' ')).toContain(selectedClass)
    expect((wrapper.get('#shared-account-concurrency').element as HTMLInputElement).value).toBe('4')
    expect((wrapper.get('#shared-account-rate').element as HTMLInputElement).value).toBe('1.6')
    wrapper.unmount()
  })

})
