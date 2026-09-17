import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({
  update: vi.fn(),
  apply: vi.fn(),
  revert: vi.fn(),
  preview: vi.fn(),
  getById: vi.fn(),
  strategies: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess, showInfo: vi.fn() })
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      update: mocks.update,
      applyAntiDegrade: mocks.apply,
      revertAntiDegrade: mocks.revert,
      previewAntiDegrade: mocks.preview,
      getById: mocks.getById,
      listAntiDegradeStrategies: mocks.strategies,
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
const ConfirmDialogStub = defineComponent({
  props: { show: Boolean, title: String },
  emits: ['confirm', 'cancel'],
  template: `<div v-if="show" data-testid="confirmation">
    <span>{{ title }}</span>
    <button data-testid="confirm" @click="$emit('confirm')">confirm</button>
    <button data-testid="cancel" @click="$emit('cancel')">cancel</button>
  </div>`
})

function buildAccount(extra: Record<string, unknown> = {}): Account {
  return {
    id: 71,
    name: 'Existing OAuth account',
    notes: '',
    platform: 'openai',
    type: 'oauth',
    credentials: { access_token: 'test-token' },
    extra,
    proxy_id: null,
    concurrency: 3,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as Account
}

function protectedAccount(mode = 'legacy'): Account {
  return buildAccount({
    anti_degradation: true,
    anti_degrade: { enabled: true, mode, max_concurrency: 3, prev: { concurrency: 3 } },
    protection_scope: mode === 'mode1' ? 'codex_v3' : 'legacy',
    codex_fingerprint_mode: mode === 'mode1' ? 'device' : 'session',
    enable_tls_fingerprint: mode !== 'mode1',
    ...(mode !== 'mode1' ? { tls_fingerprint_builtin: 'nodejs24' } : {})
  })
}

function mountModal(account = buildAccount()) {
  return mount(EditAccountModal, {
    props: { show: true, account, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: ConfirmDialogStub,
        Select: true,
        Icon: true,
        ProxySelector: true,
        GroupSelector: true,
        ModelWhitelistSelector: true
      }
    }
  })
}

enableAutoUnmount(afterEach)

describe('EditAccountModal account protection', () => {
  beforeEach(() => {
    Object.values(mocks).forEach(mock => mock.mockReset())
    mocks.strategies.mockResolvedValue([
      { id: 'generic', name: 'Generic protection', identity_mode: 'account', tls_profile: 'account', apply_supported: true },
      { id: 'legacy', name: 'Legacy policy', identity_mode: 'session', tls_profile: 'nodejs24', apply_supported: true },
      { id: 'mode1', name: 'Compatibility v3', identity_mode: 'device', tls_profile: 'standard', apply_supported: true }
    ])
    mocks.preview.mockResolvedValue({ eligible: true, issues: [], changes: [] })
    mocks.update.mockResolvedValue(buildAccount())
    mocks.getById.mockResolvedValue(buildAccount())
  })

  it('defaults to legacy and waits for the server before showing protection as enabled', async () => {
    let finishApply!: (account: Account) => void
    mocks.apply.mockReturnValue(new Promise<Account>(resolve => { finishApply = resolve }))
    const wrapper = mountModal()
    await flushPromises()

    expect(wrapper.get<HTMLSelectElement>('[data-testid="anti-degrade-mode"]').element.value).toBe('legacy')
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('false')
    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')

    expect(mocks.apply).toHaveBeenCalledWith(71, 'legacy')
    expect(wrapper.find('[data-testid="anti-degrade-status"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('disabled')).toBeDefined()

    finishApply(protectedAccount())
    await flushPromises()
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="anti-degrade-current-mode"]').text()).toContain('Legacy policy')
    expect(mocks.update).not.toHaveBeenCalled()
  })

  it('uses the server-returned active strategy rather than the requested selection', async () => {
    const returned = protectedAccount('mode1')
    mocks.apply.mockResolvedValue(returned)
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')
    await flushPromises()

    expect(mocks.apply).toHaveBeenCalledWith(71, 'legacy')
    expect(wrapper.get('[data-testid="anti-degrade-current-mode"]').text()).toContain('Compatibility v3')
    expect(wrapper.get<HTMLSelectElement>('[data-testid="anti-degrade-mode"]').element.value).toBe('mode1')
    expect(wrapper.emitted('updated')?.[0]).toEqual([returned])
  })

  it('previews a different strategy without changing the active policy until applied', async () => {
    mocks.apply.mockResolvedValue(protectedAccount('mode1'))
    const wrapper = mountModal(protectedAccount())
    await flushPromises()
    await wrapper.get('[data-testid="anti-degrade-mode"]').setValue('mode1')
    await flushPromises()

    expect(mocks.preview).toHaveBeenCalledWith(71, 'mode1')
    expect(mocks.apply).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="anti-degrade-current-mode"]').text()).toContain('Legacy policy')
    await wrapper.get('[data-testid="anti-degrade-apply-strategy"]').trigger('click')
    await flushPromises()
    expect(mocks.apply).toHaveBeenCalledWith(71, 'mode1')
    expect(wrapper.get('[data-testid="anti-degrade-current-mode"]').text()).toContain('Compatibility v3')
  })

  it('keeps protection enabled until disabling is confirmed and persisted', async () => {
    mocks.revert.mockResolvedValue(buildAccount({ anti_degradation: false, protection_scope: 'disabled' }))
    const wrapper = mountModal(protectedAccount())
    await flushPromises()

    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')
    expect(wrapper.get('[data-testid="confirmation"]').text()).toContain('antiDegradeDisableTitle')
    expect(mocks.revert).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[data-testid="cancel"]').trigger('click')
    expect(mocks.revert).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="anti-degrade-revert"]').trigger('click')
    await wrapper.get('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(mocks.revert).toHaveBeenCalledWith(71, true)
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-testid="anti-degrade-current-mode"]').exists()).toBe(false)
  })

  it('does not leave a failed apply visually enabled and reloads the committed state', async () => {
    mocks.apply.mockRejectedValue(new Error('Protection save failed'))
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')
    await flushPromises()

    expect(mocks.showError).toHaveBeenCalled()
    expect(mocks.getById).toHaveBeenCalledWith(71)
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-testid="anti-degrade-status"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('disabled')).toBeUndefined()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
  })

  it('preserves the latest applied policy on ordinary save while retaining unsaved adaptive edits', async () => {
    const initial = buildAccount({ codex_fingerprint_mode: 'off', unrelated: 'keep' })
    const returned = protectedAccount()
    mocks.apply.mockResolvedValue(returned)
    const wrapper = mountModal(initial)
    await flushPromises()

    const adaptive = wrapper.get('[data-testid="account-protection-policy"]')
    await adaptive.get('[role="switch"]').trigger('click')
    await adaptive.get('input[type="checkbox"]').setValue(true)
    await adaptive.findAll('input[type="checkbox"]')[1].setValue(true)
    await adaptive.get('input[type="number"]').setValue(2)
    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')
    await flushPromises()

    // A parent list may immediately echo the committed protection response.
    // That echo must not discard unrelated edits already entered in the form.
    await wrapper.setProps({ account: returned })
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(mocks.update).toHaveBeenCalledTimes(1)
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.extra).toMatchObject(returned.extra)
    expect(payload.extra.account_protection_policy).toMatchObject({
      enabled: true,
      adaptive_concurrency: true,
      adaptive_mode: 'automatic',
      adaptive_min_concurrency: 2
    })
    expect(payload.concurrency).toBe(3)
    expect(mocks.apply).toHaveBeenCalledTimes(1)
    expect(mocks.revert).not.toHaveBeenCalled()
  })

  it('does not implicitly enable adaptive concurrency when applying identity protection', async () => {
    mocks.apply.mockResolvedValue(protectedAccount())
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="account-protection-policy"] [role="switch"]').attributes('aria-checked')).toBe('false')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.extra).toMatchObject(protectedAccount().extra)
    expect(payload.extra).not.toHaveProperty('account_protection_policy')
  })

  it.each([
    ['OpenAI API key', { type: 'apikey', credentials: { api_key: 'test-key', base_url: 'https://example.com' } }],
    ['Gemini OAuth', { platform: 'gemini' }],
    ['shadow account', { parent_account_id: 9 }],
    ['multiple proxies', { proxy_ids: [4, 5] }],
    ['random proxy', { extra: { proxy_mode: 'random' } }]
  ] as const)('offers generic protection for %s without an identity override', async (_name, overrides) => {
    const account = { ...buildAccount(), ...overrides } as Account
    const wrapper = mountModal(account)
    await flushPromises()

    const selector = wrapper.get<HTMLSelectElement>('[data-testid="anti-degrade-mode"]')
    expect(selector.element.value).toBe('generic')
    expect(selector.findAll('option').map(option => option.attributes('value'))).toEqual(['generic'])
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('false')
    expect(mocks.apply).not.toHaveBeenCalled()
  })

  it('retains generic protection during ordinary saves for other providers', async () => {
    const initial = { ...buildAccount({ custom_setting: 'keep' }), platform: 'gemini' } as Account
    const returned = {
      ...initial,
      extra: { ...initial.extra, anti_degradation: true, anti_degrade: { enabled: true, mode: 'generic', prev: { concurrency: 3 } }, protection_scope: 'generic_v1' }
    }
    mocks.apply.mockResolvedValue(returned)
    const wrapper = mountModal(initial)
    await flushPromises()
    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')
    await flushPromises()
    expect(mocks.apply).toHaveBeenCalledWith(71, 'generic')
    expect(wrapper.get('[data-testid="anti-degrade-current-mode"]').text()).toContain('Generic protection')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(mocks.update.mock.calls[0][1].extra).toMatchObject(returned.extra)
  })

  it.each([false, true])('keeps native identity editable after generic protection, disable=%s', async (disable) => {
    const initial = buildAccount({
      codex_fingerprint_mode: 'off',
      anti_degradation: true,
      anti_degrade: { enabled: true, mode: 'generic', prev: { concurrency: 3 } },
      protection_scope: 'generic_v1'
    })
    const wrapper = mountModal(initial)
    await flushPromises()
    const identity = wrapper.getComponent('[data-testid="edit-codex-fingerprint-mode-select"]')
    identity.vm.$emit('update:modelValue', 'session')
    await flushPromises()

    if (disable) {
      mocks.revert.mockResolvedValue(buildAccount({ codex_fingerprint_mode: 'off', anti_degradation: false, protection_scope: 'disabled' }))
      await wrapper.get('[data-testid="anti-degrade-revert"]').trigger('click')
      await wrapper.get('[data-testid="confirm"]').trigger('click')
      await flushPromises()
    }

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    const extra = mocks.update.mock.calls[0][1].extra
    expect(extra.codex_fingerprint_mode).toBe('session')
    expect(extra.anti_degradation).toBe(!disable)
    if (disable) expect(extra).not.toHaveProperty('anti_degrade')
    else expect(extra.anti_degrade.mode).toBe('generic')
  })

  it('does not resurrect identity fields after switching an active identity strategy to generic', async () => {
    const returned = buildAccount({
      codex_fingerprint_mode: 'off',
      anti_degradation: true,
      anti_degrade: { enabled: true, mode: 'generic', prev: { concurrency: 3 } },
      protection_scope: 'generic_v1'
    })
    mocks.apply.mockResolvedValue(returned)
    const wrapper = mountModal(protectedAccount())
    await flushPromises()
    await wrapper.get('[data-testid="anti-degrade-mode"]').setValue('generic')
    await flushPromises()
    await wrapper.get('[data-testid="anti-degrade-apply-strategy"]').trigger('click')
    await flushPromises()
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const extra = mocks.update.mock.calls[0][1].extra
    expect(extra.codex_fingerprint_mode ?? 'off').toBe('off')
    expect(extra).not.toHaveProperty('enable_tls_fingerprint')
    expect(extra).not.toHaveProperty('tls_fingerprint_builtin')
    expect(extra).toMatchObject({ anti_degradation: true, anti_degrade: { mode: 'generic' } })
  })

  it('offers an explicit upgrade for a persisted mode1 v2 account', async () => {
    const initial = protectedAccount('mode1')
    initial.extra!.anti_degrade = { enabled: true, mode: 'mode1', policy_version: 2, max_concurrency: 3 }
    const returned = protectedAccount('mode1')
    returned.extra!.anti_degrade = { enabled: true, mode: 'mode1', policy_version: 3, max_concurrency: 3 }
    mocks.apply.mockResolvedValue(returned)
    const wrapper = mountModal(initial)
    await flushPromises()
    expect(mocks.apply).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="anti-degrade-current-mode"]').text()).toContain('Compatibility v2')
    await wrapper.get('[data-testid="anti-degrade-apply-strategy"]').trigger('click')
    await flushPromises()
    expect(mocks.apply).toHaveBeenCalledWith(71, 'mode1')
    expect(wrapper.find('[data-testid="anti-degrade-apply-strategy"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="anti-degrade-current-mode"]').text()).toContain('Compatibility v3')
  })
})
