import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import type { TestAdminReview } from '@/api/admin/tests'
import AdminTestDecision from '../AdminTestDecision.vue'

const api = vi.hoisted(() => ({ decide: vi.fn() }))
vi.mock('@/api/admin/tests', () => ({ decideResult: api.decide }))

const review = (extra: Partial<TestAdminReview> = {}): TestAdminReview => ({
  result: { id: 91, account_id: 3, model_id: 'test-model', output_kind: 'html', status: 'success' },
  generation: 7, verdict: 'pending', admin_verdict: '', account_paused: false, ...extra,
})
const render = (value = review(), compact = true) => mount(AdminTestDecision, {
  props: { review: value, compact },
  global: {
    plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
    stubs: { Icon: true },
  },
})

beforeEach(() => { vi.resetAllMocks(); api.decide.mockResolvedValue(undefined) })

describe('administrator test decision controls', () => {
  it('keeps pending decisions directly available and submits the current review generation once', async () => {
    let finish!: () => void
    api.decide.mockReturnValue(new Promise<void>(resolve => { finish = resolve }))
    const wrapper = render()
    expect(wrapper.find('[data-admin-edit-decision]').exists()).toBe(false)
    expect(wrapper.find('[data-admin-verdict]').exists()).toBe(false)
    expect(wrapper.get('[data-admin-pass]').classes()).not.toContain('btn')
    expect(wrapper.get('[data-admin-pass]').classes()).toContain('hover:text-emerald-700')
    expect(wrapper.get('[data-admin-fail]').classes()).toContain('hover:text-red-700')
    expect(wrapper.get('[data-admin-pass]').element.closest('details')).toBeNull()
    expect(wrapper.get('[data-review-round-hint]').element.closest('details')).not.toBeNull()
    await wrapper.get('[data-admin-pass]').trigger('click')
    expect(api.decide).toHaveBeenCalledOnce()
    expect(api.decide).toHaveBeenCalledWith(91, 7, 'pass')
    expect(wrapper.get('[data-admin-pass]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-admin-fail]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-admin-fail]').trigger('click')
    expect(api.decide).toHaveBeenCalledOnce()
    finish(); await flushPromises()
    expect(wrapper.emitted('decided')).toHaveLength(1)
    expect(wrapper.get('[data-admin-pass]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it.each(['pass', 'fail'] as const)('emphasizes a completed %s decision and folds change controls into one closed section', async verdict => {
    const wrapper = render(review({ admin_verdict: verdict, verdict }))
    const conclusion = wrapper.get('[data-admin-verdict]')
    expect(conclusion.element.tagName).toBe('STRONG')
    expect(conclusion.text()).toBe(`tests.adminReview.${verdict}`)
    expect(conclusion.classes()).toContain(verdict === 'pass' ? 'text-emerald-700' : 'text-red-700')
    const details = wrapper.get('[data-admin-edit-decision]')
    expect(details.element.tagName).toBe('DETAILS')
    expect(details.attributes('open')).toBeUndefined()
    expect(details.get('summary').text()).toBe('tests.adminReview.editDecision')
    expect(details.findAll('[data-review-round-hint]')).toHaveLength(1)
    expect(wrapper.findAll('details')).toHaveLength(1)
    expect(details.get(`[data-admin-${verdict}]`).attributes('disabled')).toBeDefined()
    expect(details.get(`[data-admin-${verdict}]`).attributes('aria-pressed')).toBe('true')
    const next = verdict === 'pass' ? 'fail' : 'pass'
    await details.get(`[data-admin-${next}]`).trigger('click'); await flushPromises()
    expect(api.decide).toHaveBeenCalledOnce()
    expect(api.decide).toHaveBeenCalledWith(91, 7, next)
    expect(wrapper.emitted('decided')).toHaveLength(1)
    wrapper.unmount()
  })

  it('keeps an automatic hold visible after a manual pass and requests refresh after a failed change', async () => {
    api.decide.mockRejectedValue(new Error('stale review generation'))
    const wrapper = render(review({ admin_verdict: 'pass', verdict: 'fail', account_paused: true }))
    expect(wrapper.text()).toContain('tests.adminReview.automaticHold')
    expect(wrapper.get('[data-admin-verdict]').text()).toBe('tests.adminReview.pass')
    await wrapper.get('[data-admin-fail]').trigger('click'); await flushPromises()
    expect(api.decide).toHaveBeenCalledOnce()
    expect(api.decide).toHaveBeenCalledWith(91, 7, 'fail')
    expect(wrapper.get('[role="alert"]').text()).toBeTruthy()
    expect(wrapper.get('[role="alert"]').element.closest('details')).toBeNull()
    expect(wrapper.emitted('decided')).toHaveLength(1)
    expect(wrapper.get('[data-admin-fail]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('tests.adminReview.automaticHold')
    wrapper.unmount()
  })

  it('keeps dialog decisions directly editable with the round explanation visible', () => {
    const wrapper = render(review({ admin_verdict: 'fail', verdict: 'fail' }), false)
    expect(wrapper.find('[data-admin-edit-decision]').exists()).toBe(false)
    expect(wrapper.get('[data-admin-pass]').element.closest('details')).toBeNull()
    expect(wrapper.get('[data-review-round-hint]').element.closest('details')).toBeNull()
    wrapper.unmount()
  })
})
