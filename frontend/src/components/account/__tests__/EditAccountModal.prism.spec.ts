import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({ update: vi.fn(), showError: vi.fn() }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: vi.fn(), showInfo: vi.fn() })
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      update: mocks.update,
      listAntiDegradeStrategies: vi.fn().mockResolvedValue([]),
      previewAntiDegrade: vi.fn().mockResolvedValue({ eligible: true, issues: [], changes: [] }),
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false })
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) }
  }
}))
vi.mock('@/api/admin/accounts', () => ({ getAntigravityDefaultModelMapping: vi.fn() }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  props: { show: Boolean },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

function buildAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: 71,
    name: 'Prism test account',
    notes: '',
    platform: 'openai',
    type: 'oauth',
    credentials: { access_token: 'test-access-token' },
    extra: {},
    proxy_id: null,
    concurrency: 3,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false,
    ...overrides
  } as Account
}

function mountModal(account = buildAccount()) {
  return mount(EditAccountModal, {
    props: { show: true, account, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        Select: true,
        Icon: true,
        ProxySelector: true,
        GroupSelector: true,
        ModelWhitelistSelector: true
      }
    }
  })
}

async function save(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.get('form#edit-account-form').trigger('submit.prevent')
  await flushPromises()
}

enableAutoUnmount(afterEach)

describe('EditAccountModal Prism settings', () => {
  beforeEach(() => {
    mocks.update.mockReset().mockResolvedValue(buildAccount())
    mocks.showError.mockReset()
  })

  it('starts off and ordinary saves do not add Prism fields', async () => {
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.get('[data-testid="prism-enabled"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-testid="prism-cookie"]').exists()).toBe(false)
    await save(wrapper)

    expect(mocks.update).toHaveBeenCalledTimes(1)
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.extra).not.toHaveProperty('prism')
    expect(payload.credentials).not.toHaveProperty('prism_cookie')
    expect(payload.credentials).not.toHaveProperty('prism_cookie_configured')
  })

  it.each(['oauth', 'setup-token'] as const)('defaults a new %s Prism channel to account authentication without a Cookie', async (type) => {
    const wrapper = mountModal(buildAccount({ type }))
    await flushPromises()
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    expect(wrapper.get<HTMLSelectElement>('[data-testid="prism-auth-mode"]').element.value).toBe('account')
    expect(wrapper.find('[data-testid="prism-cookie"]').exists()).toBe(false)
    await save(wrapper)

    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(mocks.update.mock.calls[0][1].extra.prism).toEqual({ enabled: true, version: 1, auth_mode: 'account', timeout_seconds: 180 })
    expect(mocks.update.mock.calls[0][1].credentials).toMatchObject({ access_token: 'test-access-token' })
    expect(mocks.update.mock.calls[0][1].credentials).not.toHaveProperty('prism_cookie')
  })

  it.each(['oauth', 'setup-token'] as const)('enables %s with a write-only manual Cookie and the configured timeout', async (type) => {
    const wrapper = mountModal(buildAccount({ type }))
    await flushPromises()
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await wrapper.get('[data-testid="prism-auth-mode"]').setValue('cookie')
    expect(wrapper.get<HTMLInputElement>('[data-testid="prism-timeout"]').element.value).toBe('180')
    expect(wrapper.get('[data-testid="prism-cookie"]').attributes('type')).toBe('password')
    await wrapper.get('[data-testid="prism-cookie"]').setValue(' test-cookie-value ')
    await wrapper.get('[data-testid="prism-timeout"]').setValue(240)
    await wrapper.get('[data-testid="prism-action-id"]').setValue('A'.repeat(42))
    await save(wrapper)

    expect(mocks.update).toHaveBeenCalledTimes(1)
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.extra.prism).toEqual({ enabled: true, version: 1, auth_mode: 'cookie', timeout_seconds: 240, conversation_action_id: 'a'.repeat(42) })
    expect(payload.credentials.prism_cookie).toBe('test-cookie-value')
    expect(payload.credentials.access_token).toBe('test-access-token')
  })

  it('leaves Prism absent when an unsaved enable is switched off again', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await wrapper.get('[data-testid="prism-auth-mode"]').setValue('cookie')
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await save(wrapper)
    expect(mocks.update.mock.calls[0][1].extra).not.toHaveProperty('prism')
    expect(mocks.update.mock.calls[0][1].credentials).not.toHaveProperty('prism_cookie')
  })

  it('clears an unsaved Cookie on close and reloads persisted settings on reopen', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await wrapper.get('[data-testid="prism-auth-mode"]').setValue('cookie')
    await wrapper.get('[data-testid="prism-cookie"]').setValue('discarded-test-cookie')
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.get<HTMLInputElement>('[data-testid="prism-cookie"]').element.value).toBe('')
    expect(mocks.update).not.toHaveBeenCalled()

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.get('[data-testid="prism-enabled"]').attributes('aria-checked')).toBe('false')
    await save(wrapper)
    expect(mocks.update.mock.calls[0][1].extra).not.toHaveProperty('prism')
    expect(mocks.update.mock.calls[0][1].credentials).not.toHaveProperty('prism_cookie')
  })

  it('keeps the saved Cookie and complete Prism configuration on an ordinary edit', async () => {
    const prism = { enabled: true, version: 1, conversation_action_id: 'b'.repeat(42), future_option: 'preserved' }
    const wrapper = mountModal(buildAccount({
      extra: { prism },
      credentials: { access_token: 'test-access-token', prism_cookie_configured: true, prism_cookie: 'old-server-value' }
    }))
    await flushPromises()
    expect(wrapper.get<HTMLSelectElement>('[data-testid="prism-auth-mode"]').element.value).toBe('cookie')
    const cookie = wrapper.get<HTMLInputElement>('[data-testid="prism-cookie"]')
    expect(cookie.element.value).toBe('')
    expect(cookie.attributes('placeholder')).toContain('cookieKeepPlaceholder')
    await wrapper.get('[data-tour="edit-account-form-name"]').setValue('Renamed account')
    await save(wrapper)

    expect(mocks.update).toHaveBeenCalledTimes(1)
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.name).toBe('Renamed account')
    expect(payload.extra.prism).toEqual(prism)
    expect(payload.credentials).not.toHaveProperty('prism_cookie')
    expect(payload.credentials).not.toHaveProperty('prism_cookie_configured')
  })

  it('can re-enable a saved Cookie while using the built-in action', async () => {
    const wrapper = mountModal(buildAccount({ extra: { prism: { enabled: false } }, credentials: { prism_cookie_configured: true } }))
    await flushPromises()
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await save(wrapper)
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.extra.prism).toEqual({ enabled: true, version: 1, auth_mode: 'cookie', timeout_seconds: 180 })
    expect(payload.credentials).not.toHaveProperty('prism_cookie')
  })

  it('preserves an account-auth configuration on an ordinary edit', async () => {
    const prism = { enabled: true, auth_mode: 'account', version: 1, timeout_seconds: 300, future_option: 'preserved' }
    const wrapper = mountModal(buildAccount({ extra: { prism } }))
    await flushPromises()
    expect(wrapper.get<HTMLSelectElement>('[data-testid="prism-auth-mode"]').element.value).toBe('account')
    expect(wrapper.find('[data-testid="prism-cookie"]').exists()).toBe(false)
    await save(wrapper)
    expect(mocks.update.mock.calls[0][1].extra.prism).toEqual(prism)
  })

  it('switches a legacy channel to account authentication without echoing or clearing its saved Cookie', async () => {
    const prism = { enabled: true, version: 1, timeout_seconds: 300, future_option: 'preserved' }
    const wrapper = mountModal(buildAccount({
      extra: { prism },
      credentials: { access_token: 'test-access-token', prism_cookie_configured: true, prism_cookie: 'old-server-value' }
    }))
    await flushPromises()
    await wrapper.get('[data-testid="prism-cookie"]').setValue('unsaved-cookie')
    await wrapper.get('[data-testid="prism-auth-mode"]').setValue('account')
    expect(wrapper.find('[data-testid="prism-cookie"]').exists()).toBe(false)
    await save(wrapper)
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.extra.prism).toEqual({ ...prism, auth_mode: 'account' })
    expect(payload.credentials).toMatchObject({ access_token: 'test-access-token' })
    expect(payload.credentials).not.toHaveProperty('prism_cookie')
    expect(payload.credentials).not.toHaveProperty('prism_cookie_configured')
  })

  it('allows switching account authentication to a previously saved manual Cookie', async () => {
    const prism = { enabled: true, auth_mode: 'account', version: 1, timeout_seconds: 300 }
    const wrapper = mountModal(buildAccount({
      extra: { prism },
      credentials: { access_token: 'test-access-token', prism_cookie_configured: true }
    }))
    await flushPromises()
    await wrapper.get('[data-testid="prism-auth-mode"]').setValue('cookie')
    await save(wrapper)
    expect(mocks.update.mock.calls[0][1].extra.prism).toEqual({ ...prism, auth_mode: 'cookie' })
    expect(mocks.update.mock.calls[0][1].credentials).not.toHaveProperty('prism_cookie')
  })

  it('preserves the stored authentication mode if an edited channel is disabled before saving', async () => {
    const prism = { enabled: true, auth_mode: 'account', version: 1, timeout_seconds: 300 }
    const wrapper = mountModal(buildAccount({ extra: { prism } }))
    await flushPromises()
    await wrapper.get('[data-testid="prism-auth-mode"]').setValue('cookie')
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await save(wrapper)
    expect(mocks.update.mock.calls[0][1].extra.prism).toEqual({ ...prism, enabled: false })
  })

  it('disables Prism without changing identity, proxy, concurrency, mappings, or other settings', async () => {
    const prism = { enabled: true, version: 1, timeout_seconds: 300, conversation_action_id: 'c'.repeat(42) }
    const account = buildAccount({
      extra: { prism, codex_fingerprint_mode: 'session', enable_tls_fingerprint: true, tls_fingerprint_builtin: 'nodejs24', unrelated_setting: 'keep' },
      credentials: { access_token: 'test-access-token', prism_cookie_configured: true, model_mapping: { 'request-model': 'mapped-model' } },
      proxy_id: 7,
      proxy_ids: [7],
      concurrency: 5,
      rate_multiplier: 1.5
    })
    const wrapper = mountModal(account)
    await flushPromises()
    await save(wrapper)
    const before = mocks.update.mock.calls[0][1]
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await save(wrapper)
    const after = mocks.update.mock.calls[1][1]

    expect(after).toEqual({ ...before, extra: { ...before.extra, prism: { ...prism, enabled: false } } })
    expect(after).toMatchObject({ proxy_id: 7, proxy_ids: [7], concurrency: 5, rate_multiplier: 1.5 })
    expect(after.extra).toMatchObject({ codex_fingerprint_mode: 'session', enable_tls_fingerprint: true, tls_fingerprint_builtin: 'nodejs24', unrelated_setting: 'keep' })
    expect(after.credentials.model_mapping).toEqual({ 'request-model': 'mapped-model' })
  })

  it('does not enable manual mode without a new or saved Cookie', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await wrapper.get('[data-testid="prism-auth-mode"]').setValue('cookie')
    await save(wrapper)
    expect(mocks.update).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('admin.accounts.openai.prism.cookieRequired')
  })

  it.each([29, 601, 30.5])('rejects invalid total timeout %s', async (timeout) => {
    const wrapper = mountModal(buildAccount({ credentials: { prism_cookie_configured: true } }))
    await flushPromises()
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await wrapper.get('[data-testid="prism-timeout"]').setValue(timeout)
    await save(wrapper)
    expect(mocks.update).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('admin.accounts.openai.prism.timeoutInvalid')
  })

  it.each(['invalid-action', 'a'.repeat(40), 'a'.repeat(43), 'g'.repeat(42)])('rejects malformed conversation action ID %s', async (actionId) => {
    const wrapper = mountModal(buildAccount({ credentials: { prism_cookie_configured: true } }))
    await flushPromises()
    await wrapper.get('[data-testid="prism-enabled"]').trigger('click')
    await wrapper.get('[data-testid="prism-action-id"]').setValue(actionId)
    await save(wrapper)
    expect(mocks.update).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('admin.accounts.openai.prism.actionIdInvalid')
  })

  it.each([
    { type: 'apikey' },
    { platform: 'gemini' },
    { parent_account_id: 9 }
  ] as Partial<Account>[])('hides Prism for an ineligible account %o', async (overrides) => {
    const wrapper = mountModal(buildAccount(overrides))
    await flushPromises()
    expect(wrapper.find('[data-testid="prism-settings"]').exists()).toBe(false)
  })

  it('hides Prism when a shared pool administrator edits a shared listing', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ sharedPool: true, sharedPoolAdmin: true })
    await flushPromises()
    expect(wrapper.find('[data-testid="prism-settings"]').exists()).toBe(false)
  })
})
