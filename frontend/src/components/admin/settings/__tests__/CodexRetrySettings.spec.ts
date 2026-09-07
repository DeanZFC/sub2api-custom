import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CodexRetrySettings from '../CodexRetrySettings.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api', () => ({ adminAPI: { settings: { getCodexPreOutputRetrySettings: mocks.get, updateCodexPreOutputRetrySettings: mocks.update } } }))
vi.mock('@/stores', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const defaults = { enabled: false, max_retries: 3, retry_interval_ms: 1000, max_retry_window_seconds: 30, keywords: ['server_is_overloaded'] }
const mountForm = () => mount(CodexRetrySettings, {
  global: { stubs: { Icon: true } },
})

describe('Codex retry settings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ ...defaults })
    mocks.update.mockImplementation(async settings => settings)
  })

  it('loads saved settings, enables retries and saves multiple error keywords', async () => {
    const wrapper = mountForm()
    await flushPromises()
    expect(wrapper.get('fieldset').attributes('disabled')).toBeDefined()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('#codex-retry-count').setValue(5)
    await wrapper.get('#codex-retry-keywords').setValue('server_is_overloaded\n custom_busy \n\n')
    await wrapper.get('[data-testid="codex-retry-save"]').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith({ ...defaults, enabled: true, max_retries: 5, keywords: ['server_is_overloaded', 'custom_busy'] })
    expect(mocks.showSuccess).toHaveBeenCalledOnce()
  })

  it('rejects invalid retry counts and empty enabled keyword lists', async () => {
    const wrapper = mountForm()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('#codex-retry-count').setValue(11)
    expect(wrapper.get('[data-testid="codex-retry-save"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#codex-retry-count').setValue(3)
    await wrapper.get('#codex-retry-keywords').setValue('  \n  ')
    expect(wrapper.get('[data-testid="codex-retry-save"]').attributes('disabled')).toBeDefined()
    expect(mocks.update).not.toHaveBeenCalled()
  })

  it('can disable the policy while preserving saved values', async () => {
    mocks.get.mockResolvedValue({ ...defaults, enabled: true })
    const wrapper = mountForm()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('[data-testid="codex-retry-save"]').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(defaults)
  })

  it('prevents saving defaults after a failed load and allows loading again', async () => {
    mocks.get.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountForm()
    await flushPromises()
    expect(wrapper.find('[data-testid="codex-retry-save"]').exists()).toBe(false)
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="codex-retry-save"]').exists()).toBe(true)
  })

  it('retains edits when saving fails', async () => {
    mocks.update.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountForm()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('#codex-retry-count').setValue(4)
    await wrapper.get('[data-testid="codex-retry-save"]').trigger('click')
    await flushPromises()
    expect(mocks.showError).toHaveBeenCalledOnce()
    expect((wrapper.get('#codex-retry-count').element as HTMLInputElement).value).toBe('4')
    expect(wrapper.get('[data-testid="codex-retry-save"]').attributes('disabled')).toBeUndefined()
  })
})
