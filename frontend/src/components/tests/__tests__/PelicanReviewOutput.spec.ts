import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import type { TestAutoReview } from '@/types'
import en from '@/i18n/locales/en/admin/resources'
import zh from '@/i18n/locales/zh/admin/resources'
import PelicanReviewOutput from '../PelicanReviewOutput.vue'
import TestResultOutput from '../TestResultOutput.vue'

function options(review: Partial<TestAutoReview> = {}, locale = 'en') {
  return {
    global: { plugins: [createI18n({ legacy: false, locale, messages: { en: { admin: en, tests: { htmlResult: 'HTML result' } }, zh: { admin: zh, tests: { htmlResult: 'HTML 作品' } } } })] },
    props: { review: {
      verdict: 'pending' as const, reason: 'Legacy reason', judge_account_id: 999123, judge_model: 'private-model',
      attempts: 0, reviewed_at: '2026-10-30T02:00:00Z', ...review,
    } },
  }
}

describe('Pelican automatic review status', () => {
  it('separates a completed server capture failure from a generated browser preview', () => {
    const config = options({ failure_stage: 'render', error_code: 'renderer_timeout', render_attempts: 3, render_retryable: true })
    const wrapper = mount(TestResultOutput, {
      global: config.global,
      props: { result: { id: 1, status: 'success', output_kind: 'html', output_html: '<svg><circle r="20" /></svg>', execution_info: {
        attempts: [], input_tokens: null, output_tokens: null, reasoning_tokens: null, cache_read_tokens: null, cache_creation_tokens: null,
        auto_review: config.props.review,
      } } },
    })
    expect(wrapper.get('[data-auto-review-verdict]').text()).toBe('Automatic review capture failed')
    expect(wrapper.get('[data-auto-review-reason]').text()).toBe(en.tests.autoReview.renderErrors.renderer_timeout)
    expect(wrapper.get('[data-auto-review-render-hint]').text()).toContain('artwork was generated')
    expect(wrapper.get('[data-auto-review-judge-not-called]').text()).toContain('no judge review requests were used')
    expect(wrapper.get('[data-auto-review-render-attempts]').text()).toBe('This round completed 3 capture attempts.')
    expect(wrapper.get('[data-auto-review-render-complete]').text()).toContain('have ended')
    expect(wrapper.get('[data-auto-review-pending]').text()).toContain('do not enter the capability pass rate')
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('<circle')
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
    expect(wrapper.text()).not.toContain('999123')
    expect(wrapper.text()).not.toContain('private-model')
    wrapper.unmount()
  })

  it.each(Object.keys(en.tests.autoReview.renderErrors).filter(code => code !== 'unknown'))('translates the safe capture error %s', code => {
    const wrapper = mount(PelicanReviewOutput, options({ failure_stage: 'render', error_code: code }, 'zh'))
    expect(wrapper.get('[data-auto-review-reason]').text()).toBe(zh.tests.autoReview.renderErrors[code as keyof typeof zh.tests.autoReview.renderErrors])
    expect(wrapper.get('[data-auto-review-verdict]').text()).toBe('自动评审截图失败')
    wrapper.unmount()
  })

  it('uses a safe fallback for unknown errors without displaying raw diagnostics', () => {
    const wrapper = mount(PelicanReviewOutput, options({
      failure_stage: 'render', error_code: '<script>private-path-secret</script>', reason: 'Private transport and credential diagnostics', render_attempts: 2,
    }))
    expect(wrapper.get('[data-auto-review-reason]').text()).toBe(en.tests.autoReview.renderErrors.unknown)
    expect(wrapper.text()).not.toContain('private-path-secret')
    expect(wrapper.text()).not.toContain('credential diagnostics')
    expect(wrapper.find('script').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each([undefined, 0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY])('omits unavailable or invalid completed attempt counts: %s', count => {
    const wrapper = mount(PelicanReviewOutput, options({ failure_stage: 'render', render_attempts: count }))
    expect(wrapper.find('[data-auto-review-render-attempts]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('preserves legacy records and normal judge verdicts, including escaped text and manual override', async () => {
    const config = options({ reason: '<script>Legacy judge text</script>', attempts: 1 })
    const wrapper = mount(PelicanReviewOutput, config)
    expect(wrapper.get('[data-auto-review-verdict]').text()).toBe('Awaiting review')
    expect(wrapper.get('[data-auto-review-reason]').text()).toBe('<script>Legacy judge text</script>')
    expect(wrapper.find('[data-auto-review-render-hint]').exists()).toBe(false)
    expect(wrapper.find('[data-auto-review-judge-not-called]').exists()).toBe(false)
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.text()).toContain(en.tests.autoReview.overrideHint)
    await wrapper.setProps({ review: { ...config.props.review, verdict: 'pass', failure_stage: 'render', error_code: 'renderer_timeout' } })
    expect(wrapper.get('[data-auto-review-verdict]').text()).toBe('Automatically passed')
    expect(wrapper.find('[data-auto-review-render-hint]').exists()).toBe(false)
    expect(wrapper.find('[data-auto-review-pending]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not incorrectly claim the judge was unused when a review call is recorded', () => {
    const wrapper = mount(PelicanReviewOutput, options({ failure_stage: 'render', attempts: 1 }))
    expect(wrapper.find('[data-auto-review-judge-not-called]').exists()).toBe(false)
    wrapper.unmount()
  })
})
