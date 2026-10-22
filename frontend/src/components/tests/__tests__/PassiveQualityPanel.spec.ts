import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import PassiveQualityPanel from '../PassiveQualityPanel.vue'
import { defaultPassivePolicy } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'
import type { AdminGroup, AccountListItem } from '@/types'
const api = vi.hoisted(() => ({ policies: vi.fn(), states: vi.fn(), events: vi.fn(), save: vi.fn(), remove: vi.fn(), act: vi.fn(), models: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/admin/passiveQuality', async original => ({ ...await original<object>(), passiveQualityAPI: api }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getModelAllowlistCandidates: api.models } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.success, showError: api.error }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
const group = { id: 8, name: 'Group', platform: 'openai', status: 'active' } as AdminGroup
const account = { id: 62, name: 'Account A', group_ids: [8] } as AccountListItem
const makeWrapper = (tab = 'passive') => mount(PassiveQualityPanel, { props: { tab, groups: [group], accounts: [account] }, global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })],
  stubs: { BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><h2>{{ title }}</h2><slot/><slot name="footer"/></section>' } },
} })
beforeEach(() => { vi.resetAllMocks(); api.policies.mockResolvedValue([]); api.states.mockResolvedValue([]); api.events.mockResolvedValue([]); api.models.mockResolvedValue(['model']); api.save.mockResolvedValue({}) })
describe('passive quality management', () => {
  it('saves all models by default without requiring a model', async () => {
    const wrapper = makeWrapper(); await flushPromises(); await wrapper.get('[data-passive-create]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    await dialog.findAll('input:not([type]), input[type="text"]')[0].setValue('Account protection')
    await dialog.findAll('label').find(l => l.text() === 'Group')!.get('input').setValue(true)
    const model = dialog.get('input[list="passive-models"]')
    expect(model.attributes('required')).toBeUndefined()
    expect(model.attributes('placeholder')).toBe('All models')
    const scope = dialog.findAll('select').find(s => s.find('option[value="account"]').exists())!
    expect((scope.element as HTMLSelectElement).value).toBe('account')
    expect(scope.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ name: 'Account protection', model: '', scope: 'account', mode: 'observe' }))
    wrapper.unmount()
  })
  it('preserves an existing model scope until the model filter is cleared', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, version: 2, name: 'Policy', model: 'model', scope: 'model', group_ids: [8] }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const scope = dialog.findAll('select').find(s => s.find('option[value="account"]').exists())!
    expect((scope.element as HTMLSelectElement).value).toBe('model')
    expect(scope.attributes('disabled')).toBeUndefined()
    await dialog.get('input[list="passive-models"]').setValue('  ')
    expect((scope.element as HTMLSelectElement).value).toBe('account')
    expect(scope.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ id: 1, version: 2, model: '', scope: 'account' }))
    wrapper.unmount()
  })
  it('starts in observe mode and saves a bounded recovery policy', async () => {
    const wrapper = makeWrapper(); await flushPromises(); await wrapper.get('[data-passive-create]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const textInputs = dialog.findAll('input:not([type]), input[type="text"]')
    await textInputs[0].setValue('Protect provider'); await dialog.get('input[list="passive-models"]').setValue('model')
    const groupLabel = dialog.findAll('label').find(l => l.text() === 'Group')!
    await groupLabel.get('input').setValue(true); await flushPromises()
    expect(wrapper.get('[data-passive-save]').attributes('disabled')).toBeUndefined()
    expect(dialog.text()).toContain('Observation window')
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ name: 'Protect provider', mode: 'observe', group_ids: [8], model: 'model', trial_concurrency: 1, error_rate: { enabled: true, pause: 20, recover: 5 } }))
    wrapper.unmount()
  })
  it('rejects impossible recovery sample budgets before saving', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, version: 2, name: 'Policy', model: 'model', group_ids: [8] }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    const label = wrapper.get('[data-dialog]').findAll('label').find(l => l.text() === 'Minimum recovery samples')!
    await label.get('input').setValue(200)
    expect(wrapper.get('[data-passive-save]').attributes('disabled')).toBeDefined(); expect(api.save).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('shows missing metrics and keeps verify distinct from temporary exemption', async () => {
    const state = { policy_id: 1, policy_name: 'Policy', account_id: 62, model: '', probe_model: 'observed-model', scope: 'account', mode: 'enforce', phase: 'paused', reason: 'insufficient_metric_samples', used: 0, inflight: 0, other_hold: true, evidence: { samples: 10, failures: 2, error_rate: 20, ttft: null, ttft_samples: 0, cache_rate: null, cache_samples: 0, complete: true } }
    api.states.mockResolvedValue([state]);const wrapper = makeWrapper('records');await flushPromises()
    expect(wrapper.text()).toContain('Another manual, active quality or rate limit hold exists');expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).toContain('All models')
    expect(wrapper.text()).toContain('Recovery probe model: observed-model')
    await wrapper.findAll('button').find(b => b.text() === 'Verify now')!.trigger('click');await flushPromises()
    expect(api.act).toHaveBeenCalledWith(state, 'verify', 0)
    await wrapper.findAll('button').find(b => b.text() === 'Temporary exemption')!.trigger('click')
    await wrapper.get('[data-dialog] input[type="number"]').setValue(15)
    await wrapper.get('[data-dialog]').findAll('button').find(b => b.text() === 'common.confirm')!.trigger('click');await flushPromises()
    expect(api.act).toHaveBeenLastCalledWith(state, 'exempt', 900);wrapper.unmount()
  })
  it('keeps request errors visible and allows retry', async () => {
    api.policies.mockRejectedValueOnce(new Error('offline'))
    const wrapper = makeWrapper();await flushPromises();expect(wrapper.get('[role="alert"]').exists()).toBe(true)
    await wrapper.get('[role="alert"] button').trigger('click');await flushPromises();expect(wrapper.find('[role="alert"]').exists()).toBe(false);wrapper.unmount()
  })
})
