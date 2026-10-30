import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestResultsView from '../TestResultsView.vue'
import TestVoteCard from '@/components/tests/TestVoteCard.vue'
import TestRecentPassRate from '@/components/tests/TestRecentPassRate.vue'
import type { TestPassRateSummary } from '@/types'

const api = vi.hoisted(() => ({ list: vi.fn(), history: vi.fn(), votes: vi.fn(), vote: vi.fn(), error: vi.fn(), reviews: vi.fn(), decide: vi.fn(), adminResults: vi.fn(), isAdmin: false }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: api.isAdmin }) }))
vi.mock('@/api/admin/tests', () => ({ listReviews: api.reviews, decideResult: api.decide, listResults: api.adminResults, default: { listReviews: api.reviews, decideResult: api.decide } }))
vi.mock('@/api/testResults', () => ({ testResultsAPI: { list: api.list, history: api.history, votes: api.votes, vote: api.vote } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.error }) }))

const baseResult = { plan_id: 10, test_definition_id: 1, test_name: 'Pelican', group_id: 8, group_name: 'Group Eight', account_id: 3, model_id: 'gpt-6-astra', output_kind: 'html', status: 'success', created_at: '2026-09-15T12:00:00Z' }
const passRate = (samples: number, passed: number, overrides: Partial<TestPassRateSummary> = {}): TestPassRateSummary => ({
  samples, passed, pass_rate: samples ? passed / samples * 100 : null, window_size: 10, window_seconds: 604800,
  pause_below: 80, recover_at_least: 90, recover_consecutive: 2, consecutive_passes: 1,
  blocked: false, ready: samples >= 10, stale: false, reason: samples >= 10 ? 'healthy' : 'insufficient_samples',
  latest_verdict: 'pass', rule_version: 'v1', model: 'gpt-6-astra', ...overrides,
})
const mountResults = () => mount(TestResultsView, { global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: { tests: { protection: { passRate: {
    recentCounts: 'Within {window}, last {count} checks · {passed} passed · {rate}',
    recentWindowDays: '{count} days', recentWindowHours: '{count} hours', recentWindowMinutes: '{count} minutes', recentWindowSeconds: '{count} seconds',
    recentProgress: 'Collecting {samples}/{count}',
    recentUnavailable: 'Recent pass-rate statistics unavailable',
  }, allPass: {
    consecutiveProgress: 'Consecutive successful rounds: {current}/{required}',
    progressStates: { pending: 'Current round pending', failed: 'Current round failed', collecting: 'More successful rounds required', ready: 'Consecutive pass requirement met' },
  } } } }, tests: {} } } })],
  stubs: {
    AppLayout: { template: '<main><slot /></main>' }, Icon: true,
    TestResultOutput: { props: ['result', 'compact'], template: '<div data-output :data-result-id="result.id" :data-compact="compact" />' },
    BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><button data-close @click="$emit(\'close\')">Close</button><slot /></section>' },
  },
} })

beforeEach(() => {
  vi.resetAllMocks()
  api.isAdmin = false
  api.reviews.mockResolvedValue([])
  api.adminResults.mockResolvedValue([])
  api.history.mockResolvedValue({ items: [] })
  api.votes.mockResolvedValue([])
})
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

describe('public recent check counts', () => {
  it('shows each check\'s own counts without requesting administrator APIs', async () => {
    const artworkEvidence = passRate(10, 8)
    const candyEvidence = passRate(20, 19, { window_size: 20 })
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, pass_rate: artworkEvidence },
      { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 29, pass_rate: candyEvidence },
      { ...baseResult, id: 3, test_definition_id: 3, test_name: 'Retired statistics', output_kind: 'statistics', pass_rate: passRate(10, 10) },
    ])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-content-test] [data-recent-pass-rate-counts]').text()).toBe('Within 7 days, last 10 checks · 8 passed · 80%')
    expect(wrapper.get('[data-numeric-test] [data-recent-pass-rate-counts]').text()).toBe('Within 7 days, last 20 checks · 19 passed · 95%')
    expect(wrapper.text()).not.toContain('Retired statistics')
    expect(wrapper.findAllComponents(TestRecentPassRate).map(component => component.props('evidence'))).toEqual([candyEvidence, artworkEvidence])
    expect(api.reviews).not.toHaveBeenCalled()
    expect(api.adminResults).not.toHaveBeenCalled()
    expect(wrapper.find('[data-admin-decision]').exists()).toBe(false)
    expect(wrapper.find('[data-quality-score]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows current whole-round streaks for each selected model even without individual rate evidence', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 11, model_id: 'model-a', model_order: 0, all_pass_progress: { required: 3, consecutive_passes: 1, round_verdict: 'pass', blocked: true } },
      { ...baseResult, id: 12, model_id: 'model-b', model_order: 1, all_pass_progress: { required: 3, consecutive_passes: 0, round_verdict: 'fail', blocked: true } },
    ])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-all-pass-progress]').text()).toContain('1/3')
    expect(wrapper.get('[data-all-pass-progress]').text()).toContain('More successful rounds required')
    expect(wrapper.find('[data-recent-pass-rate-unavailable]').exists()).toBe(false)
    await wrapper.get('[data-model-selector]').setValue('model-b')
    expect(wrapper.get('[data-all-pass-progress]').text()).toContain('0/3')
    expect(wrapper.get('[data-all-pass-progress]').text()).toContain('Current round failed')
    wrapper.unmount()
  })

  it('keeps missing statistics and insufficient evidence unknown instead of deriving passes from transport success', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1 },
      { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 29,
        pass_rate: passRate(2, 1, { pass_rate: null }) },
    ])
    const wrapper = mountResults(); await flushPromises()
    const artwork = wrapper.get('[data-content-test]')
    expect(artwork.get('[data-recent-pass-rate-unavailable]').text()).toBe('Recent pass-rate statistics unavailable')
    expect(artwork.find('[data-recent-pass-rate-counts]').exists()).toBe(false)
    expect(wrapper.get('[data-numeric-test] [data-recent-pass-rate-counts]').text()).toBe('Within 7 days, last 10 checks · 1 passed · —')
    expect(wrapper.get('[data-numeric-test] [data-recent-pass-rate-state]').text()).toBe('Collecting 2/10')
    wrapper.unmount()
  })

  it('switches every account check count from the single top-level model selector', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, model_id: 'model-a', model_order: 0, pass_rate: passRate(10, 9, { model: 'model-a' }) },
      { ...baseResult, id: 2, model_id: 'model-b', model_order: 1, pass_rate: passRate(10, 4, { model: 'model-b' }) },
      { ...baseResult, id: 3, account_id: 4, model_id: 'model-a', model_order: 0, pass_rate: passRate(10, 7, { model: 'model-a' }) },
      { ...baseResult, id: 4, account_id: 4, model_id: 'model-b', model_order: 1, pass_rate: passRate(10, 6, { model: 'model-b' }) },
    ])
    const wrapper = mountResults(); await flushPromises()
    const accounts = wrapper.findAll('[data-account-result]')
    expect(wrapper.findAll('[data-model-selector]')).toHaveLength(1)
    expect(wrapper.find('[data-account-model-selector]').exists()).toBe(false)
    expect(wrapper.get('[data-model-selector]').element.closest('[data-account-result]')).toBeNull()
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-a')
    await wrapper.get('[data-model-selector]').setValue('model-b')
    expect(accounts[0].get('[data-recent-pass-rate-counts]').text()).toBe('Within 7 days, last 10 checks · 4 passed · 40%')
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-b')
    expect(accounts[1].get('[data-recent-pass-rate-counts]').text()).toBe('Within 7 days, last 10 checks · 6 passed · 60%')
    wrapper.unmount()
  })

  it('hides accounts without the chosen model instead of showing another model as a fallback', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, model_id: 'model-a', model_order: 0 },
      { ...baseResult, id: 2, account_id: 4, model_id: 'model-b', model_order: 1 },
    ])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[data-account-info] h3').map(node => node.text())).toEqual(['tests.account #3'])
    await wrapper.get('[data-model-selector]').setValue('model-b')
    expect(wrapper.findAll('[data-account-info] h3').map(node => node.text())).toEqual(['tests.account #4'])
    expect(wrapper.get('[data-output]').attributes('data-result-id')).toBe('2')
    await wrapper.get('[data-model-selector]').setValue('model-a')
    expect(wrapper.get('[data-output]').attributes('data-result-id')).toBe('1')
    wrapper.unmount()
  })

  it('refreshes rolling pass-rate evidence', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const result = { ...baseResult, id: 1 }
    api.list.mockResolvedValueOnce([{ ...result, pass_rate: passRate(10, 8) }])
      .mockResolvedValue([{ ...result, pass_rate: passRate(10, 9) }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-recent-pass-rate-counts]').text()).toBe('Within 7 days, last 10 checks · 8 passed · 80%')
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-recent-pass-rate-counts]').text()).toBe('Within 7 days, last 10 checks · 9 passed · 90%')
    wrapper.unmount()
  })
})

describe('quality verdict presentation and result actions', () => {
  it('shows a saved numeric pass while successful executions without a quality decision remain pending', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, output_kind: 'number', output_numeric: 29, quality_verdict: 'pass' },
      { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Unreviewed numeric', output_kind: 'number', output_numeric: 29 },
    ])
    const wrapper = mountResults(); await flushPromises()
    const cards = wrapper.findAll('[data-numeric-test]')
    expect(cards.map(card => card.get('[data-current-verdict]').text())).toEqual(['tests.verdicts.pass', 'tests.verdicts.pending'])
    expect(cards.map(card => card.get('[data-numeric-gallery] strong').text())).toEqual(['29', '29'])
    expect(cards[0].get('[data-current-verdict]').classes()).toContain('badge-success')
    expect(cards[1].get('[data-current-verdict]').classes()).toContain('badge-warning')
    wrapper.unmount()
  })

  it('keeps visual success pending and preserves a final duration failure despite a manual pass', async () => {
    const execution = { input_tokens: null, output_tokens: null, reasoning_tokens: null, cache_read_tokens: null, cache_creation_tokens: null, attempts: [] }
    const visualPass = { ...baseResult, id: 1, account_id: 1, execution_info: {
      ...execution, auto_review: { verdict: 'pass', reason: 'visual match', judge_account_id: 999, judge_model: 'vision', attempts: 1, reviewed_at: '2026-10-28T10:00:00Z' },
    } }
    const durationFail = { ...visualPass, id: 2, account_id: 2, quality_verdict: 'fail', latency_ms: 120000, protection_decision: { verdict: 'fail', reason: 'duration_exceeded', status: 'applied' } }
    api.isAdmin = true
    api.list.mockResolvedValue([visualPass, durationFail])
    api.reviews.mockResolvedValue([{ result: durationFail, generation: 9, verdict: 'pass', admin_verdict: 'pass', account_paused: true }])
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-show-all-reviews]').setValue(true)
    const accounts = wrapper.findAll('[data-account-result]')
    const visual = accounts.find(account => account.get('[data-output]').attributes('data-result-id') === '1')!
    const duration = accounts.find(account => account.get('[data-output]').attributes('data-result-id') === '2')!
    expect(visual.get('[data-current-verdict]').text()).toBe('tests.verdicts.pending')
    expect(duration.get('[data-current-verdict]').text()).toBe('tests.verdicts.fail')
    expect(duration.get('[data-current-verdict]').classes()).toContain('badge-danger')
    expect(duration.get('[data-admin-verdict]').text()).toBe('tests.adminReview.pass')
    expect(duration.get('[data-scheduling-status]').text()).toContain('tests.schedulingPaused')
    wrapper.unmount()
  })

  it('stacks short checks beside artwork and keeps timestamp, history and preview actions in the card footer', async () => {
    const artwork = { ...baseResult, id: 1, output_html: '<svg />' }
    api.list.mockResolvedValue([artwork, { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 29 }])
    api.history.mockResolvedValue({ items: [artwork] })
    const wrapper = mountResults(); await flushPromises()
    const content = wrapper.get('[data-content-test]')
    const numeric = wrapper.get('[data-numeric-test]')
    expect(wrapper.findAll('[data-short-test-stack] [data-test-section]')).toHaveLength(1)
    expect(wrapper.findAll('[data-preview-test-stack] [data-test-section]')).toHaveLength(1)
    expect(numeric.element.closest('[data-short-test-stack]')).not.toBeNull()
    expect(content.element.closest('[data-preview-test-stack]')).not.toBeNull()
    expect(content.get('[data-test-footer] time').attributes('datetime')).toBe(artwork.created_at)
    expect(numeric.get('[data-test-footer] [data-view-history]').text()).toBe('tests.viewHistory')
    expect(numeric.find('[data-expand-preview]').exists()).toBe(false)
    await content.get('[data-test-footer] [data-expand-preview]').trigger('click')
    expect(wrapper.get('[data-dialog] [data-output]').attributes('data-result-id')).toBe('1')
    expect(api.history).not.toHaveBeenCalled()
    await wrapper.get('[data-dialog] [data-close]').trigger('click')
    await content.get('[data-test-footer] [data-view-history]').trigger('click'); await flushPromises()
    expect(api.history).toHaveBeenCalledWith(1, undefined)
    expect(wrapper.get('[data-dialog] [data-output]').attributes('data-result-id')).toBe('1')
    wrapper.unmount()
  })
})

describe('channel quality result groups', () => {
  it.each(['pass', 'fail'] as const)('keeps artwork visible after an administrator %s decision and refresh', async verdict => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.isAdmin = true
    const result = { ...baseResult, id: 91, output_html: '<svg />', execution_info: {
      input_tokens: null, output_tokens: null, reasoning_tokens: null, cache_read_tokens: null, cache_creation_tokens: null, attempts: [],
      auto_review: { verdict: 'pending', reason: 'awaiting review', judge_account_id: 999, judge_model: 'vision', attempts: 1, reviewed_at: '2026-10-27T10:00:00Z' },
    } }
    const review = { result, generation: 7, verdict: 'pending', admin_verdict: '', account_paused: false }
    api.list.mockResolvedValue([{ ...baseResult, id: 91, output_html: '<svg />' },
      { ...baseResult, id: 92, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 21 }])
    api.reviews.mockResolvedValueOnce([review]).mockResolvedValue([{ ...review, verdict, admin_verdict: verdict }])
    api.decide.mockResolvedValue(undefined)
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get(`[data-content-test] [data-admin-${verdict}]`).trigger('click'); await flushPromises()
    expect(api.decide).toHaveBeenCalledWith(91, 7, verdict)
    expect(wrapper.get('[data-content-test] [data-output]').attributes('data-result-id')).toBe('91')
    expect(wrapper.get('[data-content-test] [data-current-verdict]').text()).toBe(`tests.verdicts.${verdict}`)
    expect(wrapper.get('[data-content-test] [data-admin-verdict]').text()).toBe(`tests.adminReview.${verdict}`)
    expect(wrapper.find('[data-numeric-test]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-content-test] [data-output]').attributes('data-result-id')).toBe('91')
    await wrapper.get('[data-expand-preview]').trigger('click')
    expect(wrapper.get('[data-dialog] [data-output]').attributes('data-result-id')).toBe('91')
    wrapper.unmount()
  })

  it.each(['pass', 'fail'] as const)('ignores historical artwork after the latest %s verdict when filtering models and keeps voting-only entries', async verdict => {
    api.isAdmin = true
    const execution = { input_tokens: null, output_tokens: null, reasoning_tokens: null, cache_read_tokens: null, cache_creation_tokens: null, attempts: [] }
    const reviewed = { ...baseResult, id: 91, model_id: 'model-a', model_order: 0, output_html: '<svg />', execution_info: {
      ...execution, auto_review: { verdict, reason: 'reviewed', judge_account_id: 999, judge_model: 'vision', attempts: 1, reviewed_at: '2026-10-27T10:00:00Z' },
    } }
    const pending = { ...baseResult, id: 92, model_id: 'model-b', model_order: 1, execution_info: {
      ...execution, auto_review: { verdict: 'pending', reason: 'awaiting review', judge_account_id: 999, judge_model: 'vision', attempts: 1, reviewed_at: '2026-10-27T10:00:00Z' },
    } }
    const historical = { ...baseResult, id: 90, model_id: 'model-a', model_order: 0, output_html: '<svg />', created_at: '2026-09-14T12:00:00Z' }
    const votingOnly = { ...baseResult, id: 94, model_id: 'model-c', model_order: 2 }
    api.list.mockResolvedValue([historical, { ...reviewed, execution_info: execution }, { ...pending, execution_info: execution }])
    api.reviews.mockResolvedValue([reviewed, pending].map(result => ({ result, generation: 7, verdict: result.execution_info.auto_review.verdict, admin_verdict: '', account_paused: false })))
    api.votes.mockResolvedValue([{ result: votingOnly, voting: { enabled: true, open: true, pass_count: 0, fail_count: 0, reject_above: 0, pass_at_least: 2, account_paused: false } }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-show-all-reviews]').element.checked).toBe(true)
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-a')
    expect(wrapper.get('[data-model-selector]').findAll('option').map(option => option.attributes('value'))).toEqual(['model-a', 'model-b', 'model-c'])
    expect(wrapper.get('[data-latest-preview] [data-output]').attributes('data-result-id')).toBe('91')
    expect(wrapper.get('[data-previous-preview]').attributes('data-previous-preview')).toBe('90')
    await wrapper.get('[data-show-all-reviews]').setValue(false)
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-b')
    expect(wrapper.get('[data-model-selector]').findAll('option').map(option => option.attributes('value'))).toEqual(['model-b', 'model-c'])
    expect(wrapper.get('[data-output]').attributes('data-result-id')).toBe('92')
    await wrapper.get('[data-model-selector]').setValue('model-c')
    expect(wrapper.get('[data-vote-result]').attributes('data-vote-result')).toBe('94')
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    await wrapper.get('[data-show-all-reviews]').setValue(true)
    expect(wrapper.get('[data-model-selector]').findAll('option').map(option => option.attributes('value'))).toEqual(['model-a', 'model-b', 'model-c'])
    wrapper.unmount()
  })

  it('shows all artwork by default and retains an optional unresolved filter and manual override', async () => {
    api.isAdmin = true
    const execution = { input_tokens: null, output_tokens: null, reasoning_tokens: null, cache_read_tokens: null, cache_creation_tokens: null, attempts: [] }
    const autoResult = (id: number, verdict: 'pass' | 'fail' | 'pending') => ({ ...baseResult, id, account_id: id, execution_info: {
      ...execution, auto_review: { verdict, reason: verdict, judge_account_id: 999, judge_model: 'vision-a', attempts: 1, reviewed_at: '2026-10-27T10:00:00Z' },
    } })
    const passed = autoResult(91, 'pass')
    const failed = autoResult(92, 'fail')
    const pending = autoResult(93, 'pending')
    const manuallyHandled = autoResult(94, 'pending')
    const manualOnly = { ...baseResult, id: 95, account_id: 95 }
    api.reviews.mockResolvedValue([passed, failed, pending, manuallyHandled, manualOnly].map(result => ({
      result, generation: 7, verdict: 'pending', admin_verdict: result.id === 94 ? 'pass' : '', account_paused: false,
    })))
    // Public results intentionally omit private review information. Their group
    // projection remains authoritative without discarding the private verdict.
    api.list.mockResolvedValue([{ ...passed, group_name: 'Current Group', execution_info: execution }, { ...baseResult, id: 90, account_id: 93, created_at: '2026-09-14T12:00:00Z' }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-show-all-reviews]').element.checked).toBe(true)
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id')).sort()).toEqual(['91', '92', '93', '94', '95'])
    expect(wrapper.findAll('[data-admin-decision]')).toHaveLength(5)
    const passedOutput = wrapper.findAll('[data-content-test]').find(section => section.find('[data-output]').attributes('data-result-id') === '91')!
    expect(passedOutput.get('.badge').text()).toBe('tests.verdicts.pending')
    expect(passedOutput.get('.badge').classes()).toContain('badge-warning')
    await wrapper.get('[data-show-all-reviews]').setValue(false)
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id')).sort()).toEqual(['93', '95'])
    expect(wrapper.get('[data-content-test] .badge').text()).toBe('tests.verdicts.pending')
    wrapper.unmount()
  })

  it('does not resurrect an older pending automatic result after the latest result passed', async () => {
    api.isAdmin = true
    const autoReview = (verdict: 'pass' | 'pending') => ({ input_tokens: null, output_tokens: null, reasoning_tokens: null, cache_read_tokens: null, cache_creation_tokens: null, attempts: [], auto_review: { verdict, reason: verdict, judge_account_id: 999, judge_model: 'vision-a', attempts: 1, reviewed_at: '2026-10-27T10:00:00Z' } })
    const latest = { ...baseResult, id: 2, created_at: '2026-09-16T12:00:00Z', execution_info: autoReview('pass') }
    api.list.mockResolvedValue([{ ...baseResult, id: 1, execution_info: autoReview('pending') }])
    api.reviews.mockResolvedValue([{ result: latest, generation: 3, verdict: 'pass', admin_verdict: '', account_paused: false }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-result-gallery] [data-output]').attributes('data-result-id')).toBe('2')
    await wrapper.get('[data-show-all-reviews]').setValue(false)
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    await wrapper.get('[data-show-all-reviews]').setValue(true)
    expect(wrapper.get('[data-result-gallery] [data-output]').attributes('data-result-id')).toBe('2')
    wrapper.unmount()
  })

  it('shows current pass-rate evidence in administrator review cards without changing the pending decision', async () => {
    api.isAdmin = true
    api.list.mockResolvedValue([])
    const pass_rate = {
      samples: 10, passed: 7, pass_rate: 70, window_size: 10, window_seconds: 604800,
      pause_below: 80, recover_at_least: 90, recover_consecutive: 2, consecutive_passes: 1,
      blocked: true, ready: true, stale: false, reason: 'recovery_pending',
      latest_verdict: 'pass', rule_version: 'v1', model: 'gpt-6-astra',
    }
    const all_pass_progress = { required: 3, consecutive_passes: 1, round_verdict: 'pending', blocked: true }
    const result = { ...baseResult, id: 91, pass_rate, all_pass_progress }
    api.reviews.mockResolvedValue([{ result, generation: 7, verdict: 'pending', admin_verdict: '', account_paused: true }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.getComponent(TestRecentPassRate).props('evidence')).toEqual(pass_rate)
    expect(wrapper.getComponent(TestRecentPassRate).props('allPassProgress')).toEqual(all_pass_progress)
    expect(wrapper.get('[data-all-pass-progress]').text()).toContain('Current round pending')
    expect(wrapper.get('[data-recent-pass-rate] [data-recent-pass-rate-state]').text()).toContain('states.blocked')
    expect(wrapper.find('[data-admin-decision] [data-pass-rate-evidence]').exists()).toBe(false)
    expect(wrapper.get('[data-admin-pass]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-admin-fail]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('shows a skipped badge when a later check was not executed', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 11, status: 'failed', error_message: '检测跳过：前一项检测未满足条件' }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-test-section] .badge').text()).toBe('tests.skipped')
    expect(wrapper.get('[data-test-section] .badge').classes()).toContain('badge-gray')
    wrapper.unmount()
  })

  it('explains direct group replacement and sends an immediate admin decision for a strategy review', async () => {
    api.isAdmin = true
    api.list.mockResolvedValue([])
    const result = { ...baseResult, id: 91 }
    api.reviews.mockResolvedValue([{ result, generation: 7, verdict: 'pending', admin_verdict: '', account_paused: false }])
    api.decide.mockResolvedValue(undefined)
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-review-round-hint]').text()).toContain('tests.adminReview.roundOverrideHint')
    await wrapper.get('[data-admin-pass]').trigger('click'); await flushPromises()
    expect(api.decide).toHaveBeenCalledWith(91, 7, 'pass')
    expect(api.vote).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps paused accounts and their results in the shared quality page', async () => {
    api.isAdmin = true
    const result = { ...baseResult, id: 91, group_id: 9, group_name: 'Paused Group', model_id: 'paused-model' }
    const other = { ...baseResult, id: 92, account_id: 99 }
    api.list.mockResolvedValue([result, { ...result, id: 93, test_definition_id: 5 }, other])
    api.reviews.mockResolvedValue([{ result, generation: 7, verdict: 'fail', admin_verdict: 'fail', account_paused: true }])
    api.votes.mockResolvedValue([{ result, voting: { enabled: true, open: true } }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    expect(wrapper.get('[data-account-result] h3').text()).toContain('#99')
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(wrapper.get('[data-account-info]').text()).toContain('#3')
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingPaused')
    expect(wrapper.findAll('[data-test-section]')).toHaveLength(2)
    expect(wrapper.find('[data-voting-section]').exists()).toBe(false)
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Group Eight', 'Paused Group'])
    expect(wrapper.get('[data-account-tests]').text()).toContain('paused-model')
    wrapper.unmount()
  })

  it('keeps administrator decisions available for paused results', async () => {
    api.isAdmin = true
    api.list.mockResolvedValue([])
    const result = { ...baseResult, id: 91, account_name: 'Review Account' }
    const review = { result, generation: 7, verdict: 'pending', admin_verdict: '', account_paused: true }
    api.reviews.mockResolvedValue([review])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.find('[data-account-result]').exists()).toBe(true)
    expect(wrapper.find('[data-admin-decision]').exists()).toBe(true)
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingPaused')
    wrapper.unmount()
  })

  it('updates scheduling without removing the account after an administrator pauses it', async () => {
    api.isAdmin = true
    const result = { ...baseResult, id: 91, account_name: 'Review Account' }
    const review = { result, generation: 7, verdict: 'pending', admin_verdict: '', account_paused: false }
    // A public result can still be in flight while the decision pauses its account.
    api.list.mockResolvedValue([result])
    api.reviews.mockResolvedValueOnce([review])
      .mockResolvedValue([{ ...review, verdict: 'fail', admin_verdict: 'fail', account_paused: true }])
    api.decide.mockResolvedValue(undefined)
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-account-info]').text()).toContain('Review Account')
    await wrapper.get('[data-admin-fail]').trigger('click'); await flushPromises()
    expect(api.decide).toHaveBeenCalledWith(91, 7, 'fail')
    expect(wrapper.find('[data-account-result]').exists()).toBe(true)
    expect(wrapper.find('[data-admin-decision]').exists()).toBe(true)
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingPaused')
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(1)
    expect(api.vote).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps paused account history open and refreshes scheduling after recovery', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.isAdmin = true
    const result = { ...baseResult, id: 91 }
    const review = { result, generation: 7, verdict: 'pending', admin_verdict: '', account_paused: false }
    api.list.mockResolvedValue([result])
    api.reviews.mockResolvedValue([review])
    let resolveHistory!: (value: unknown) => void
    api.adminResults.mockReturnValue(new Promise(resolve => { resolveHistory = resolve }))
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-test-section] [data-view-history]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(true)
    api.reviews.mockResolvedValue([{ ...review, account_paused: true }])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(true)
    expect(wrapper.find('[data-account-result]').exists()).toBe(true)
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingPaused')
    resolveHistory([result]); await flushPromises()
    expect(wrapper.find('[data-dialog] [data-output]').exists()).toBe(true)
    api.reviews.mockResolvedValue([review])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-result-gallery] [data-output]').attributes('data-result-id')).toBe('91')
    wrapper.unmount()
  })

  it('reloads result projections after an administrator decision', async () => {
    api.isAdmin = true
    const initial = { ...baseResult, id: 101, group_id: 8, group_name: 'Source Group' }
    const moved = { ...initial, group_id: 12, group_name: 'Target Group', protection_decision: { verdict: 'pass', status: 'applied', scheduling: 'resume' } }
    api.list.mockResolvedValueOnce([initial]).mockResolvedValueOnce([moved])
    api.reviews.mockResolvedValueOnce([{ result: initial, generation: 4, verdict: 'pending', admin_verdict: '', account_paused: false }])
      .mockResolvedValueOnce([{ result: moved, generation: 4, verdict: 'pass', admin_verdict: 'pass', account_paused: false }])
    api.decide.mockResolvedValue(undefined)
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Source Group'])
    await wrapper.get('[data-admin-pass]').trigger('click'); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Target Group'])
    wrapper.unmount()
  })

  it('preserves the admin account name and history after moving the display group', async () => {
    api.isAdmin = true
    const stored = { ...baseResult, id: 91, account_name: 'Review Account' }
    const moved = { ...baseResult, id: 91, group_id: 12, group_name: 'Target Group' }
    api.list.mockResolvedValue([moved])
    api.reviews.mockResolvedValue([{ result: stored, generation: 7, verdict: 'pass', admin_verdict: 'pass', account_paused: false }])
    api.adminResults.mockResolvedValue([stored, { ...stored, id: 92, account_id: 99 }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Target Group'])
    expect(wrapper.get('[data-account-info]').text()).toContain('Review Account')
    await wrapper.get('[data-test-section] [data-view-history]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-dialog]').findAll('[data-output]').map(output => output.attributes('data-result-id'))).toEqual(['91'])
    wrapper.unmount()
  })

  it.each([
    ['number', 'passed'],
    ['html', 'success'],
    ['model_check', 'failed'],
    ['text', 'error'],
  ])('shows administrator account names for %s results without review records', async (output_kind, status) => {
    api.isAdmin = true
    api.list.mockResolvedValue([{ ...baseResult, id: 91, output_kind, status, account_name: 'GPT-SN-1' }])
    const wrapper = mountResults(); await flushPromises()
    expect(api.reviews).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-account-info]').text()).toContain('#3 · GPT-SN-1')
    expect(wrapper.find('[data-admin-decision]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each(['', 'Previous account name'])('prefers the current administrator account name over a review name of %s', async account_name => {
    api.isAdmin = true
    const current = { ...baseResult, id: 91, account_name: 'Current account name' }
    api.list.mockResolvedValue([current])
    api.reviews.mockResolvedValue([{ result: { ...current, account_name }, generation: 7, verdict: 'pass', admin_verdict: 'pass', account_paused: false }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-account-info]').text()).toContain('Current account name')
    expect(wrapper.get('[data-account-info]').text()).not.toContain('Previous account name')
    wrapper.unmount()
  })

  it('keeps administrator account names after review records disappear on refresh', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.isAdmin = true
    const result = { ...baseResult, id: 91, account_name: 'GPT-SN-1' }
    api.list.mockResolvedValue([result])
    api.reviews.mockResolvedValueOnce([{ result, generation: 7, verdict: 'pass', admin_verdict: 'pass', account_paused: false }]).mockResolvedValue([])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-account-info]').text()).toContain('GPT-SN-1')
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(api.reviews).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-account-info]').text()).toContain('GPT-SN-1')
    wrapper.unmount()
  })

  it('shows administrator account names when the review request fails', async () => {
    api.isAdmin = true
    api.list.mockResolvedValue([{ ...baseResult, id: 91, account_name: 'GPT-SN-1' }])
    api.reviews.mockRejectedValue(new Error('Review service unavailable'))
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-account-info]').text()).toContain('GPT-SN-1')
    expect(wrapper.get('[role="alert"]').text()).toBeTruthy()
    wrapper.unmount()
  })

  it('never exposes administrator review controls or requests to ordinary users', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1 }])
    const wrapper = mountResults(); await flushPromises()
    expect(api.reviews).not.toHaveBeenCalled()
    expect(wrapper.find('[data-admin-decision]').exists()).toBe(false)
    expect(api.votes).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('does not offer decisions for superseded historical results', async () => {
    api.isAdmin = true
    const latest = { ...baseResult, id: 2, created_at: '2026-09-16T12:00:00Z' }
    api.list.mockResolvedValue([latest, { ...baseResult, id: 1 }])
    api.reviews.mockResolvedValue([{ result: latest, generation: 3, verdict: 'pending', admin_verdict: '', account_paused: false }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[data-admin-decision]')).toHaveLength(1)
    expect(wrapper.findAll('figure[data-result-gallery]')).toHaveLength(1)
    expect(wrapper.get('[data-result-gallery] [data-output]').attributes('data-result-id')).toBe('2')
    wrapper.unmount()
  })

  it('reports rejected decisions and reloads the current review generation', async () => {
    api.isAdmin = true
    const result = { ...baseResult, id: 2 }
    api.list.mockResolvedValue([result])
    api.reviews.mockResolvedValue([{ result, generation: 3, verdict: 'pending', admin_verdict: '', account_paused: false }])
    api.decide.mockRejectedValue(new Error('stale round'))
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-admin-fail]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-admin-decision] [role="alert"]').text()).toBeTruthy()
    expect(api.reviews).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-admin-fail]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('labels completed model checks by evidence and displays their history without account names', async () => {
    const check = { requested_model: 'alias', upstream_model: 'expected', returned_models: ['different'], match_mode: 'exact', verdict: 'fail', reason: 'mismatch' }
    const latest = { ...baseResult, id: 1, output_kind: 'model_check', test_name: 'Model consistency', output_model_check: check, account_name: 'Private account name' }
    api.list.mockResolvedValue([latest])
    api.history.mockResolvedValue({ items: [latest, { ...latest, id: 2, created_at: '2026-09-15T11:00:00Z', output_model_check: { ...check, verdict: 'unknown', reason: 'missing_model', returned_models: [] } }] })
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-test-section] .badge').text()).toBe('tests.verdicts.fail')
    expect(wrapper.get('[data-test-section] .badge').classes()).toContain('badge-danger')
    expect(wrapper.text()).not.toContain('Private account name')
    await wrapper.get('[data-test-section] [data-view-history]').trigger('click'); await flushPromises()
    const badges = wrapper.get('[data-dialog]').findAll('.badge')
    expect(badges.map(badge => badge.text())).toEqual(['tests.verdicts.fail', 'tests.verdicts.pending'])
    expect(badges[1].classes()).toContain('badge-warning')
    expect(wrapper.text()).not.toContain('Private account name')
    wrapper.unmount()
  })

  it('uses ordered group tabs and combines multiple test types under one account', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, plan_order: 10 },
      { ...baseResult, id: 2, plan_id: 20, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 29, plan_order: 10 },
      { ...baseResult, id: 3, group_id: 9, group_name: 'Next Group', account_id: 4, plan_order: 20 },
    ])
    const wrapper = mountResults()
    await flushPromises()

    expect(api.list).toHaveBeenCalledWith(3, 4)
    expect(api.history).not.toHaveBeenCalled()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Group Eight', 'Next Group'])
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    expect(wrapper.get('[data-account-result] h3').text()).toBe('tests.account #3')
    expect(wrapper.get('[data-numeric-test]').text()).toContain('Candy')
    expect(wrapper.get('[data-numeric-test] strong').text()).toBe('29')
    expect(wrapper.get('[data-content-test]').text()).toContain('Pelican')
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(wrapper.get('[data-account-result] h3').text()).toBe('tests.account #4')
    expect(wrapper.find('[data-numeric-test]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('hides account identifiers for group-only checks even when a legacy response has an account ID', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, target_mode: 'group', account_id: 99 }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.get('[data-account-result] h3').text()).toBe('tests.groupCheck')
    expect(wrapper.text()).not.toContain('#99')
    wrapper.unmount()
  })

  it('keeps groups stable while polling and supports keyboard group navigation', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const first = { ...baseResult, id: 1, group_name: 'Alpha', group_id: 1, plan_order: 0 }
    const second = { ...baseResult, id: 2, group_name: 'Beta', group_id: 2, plan_order: 0 }
    api.list.mockResolvedValueOnce([second, first]).mockResolvedValueOnce([{ ...first, id: 4 }, { ...second, id: 3 }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Alpha', 'Beta'])
    await wrapper.findAll('[role="tab"]')[0].trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.findAll('[role="tab"]')[1].attributes('aria-selected')).toBe('true')
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Alpha', 'Beta'])
    expect(wrapper.findAll('[role="tab"]')[1].attributes('aria-selected')).toBe('true')
    wrapper.unmount()
  })

  it('preserves document scrolling while polling and selects an available model when the group changes', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, group_name: 'A', model_id: 'model-a' },
      { ...baseResult, id: 2, group_id: 9, group_name: 'B', model_id: 'model-b' },
    ])
    const wrapper = mountResults()
    await flushPromises()
    const panel = wrapper.get<HTMLElement>('[role="tabpanel"]')
    expect(panel.classes()).not.toContain('overflow-y-auto')
    panel.element.scrollTop = 150
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(panel.element.scrollTop).toBe(150)
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-a')
    expect(wrapper.find('[data-account-model-selector]').exists()).toBe(false)
    expect(panel.text()).toContain('model-a')
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(panel.element.scrollTop).toBe(0)
    expect(panel.findAll('[data-account-result]')).toHaveLength(1)
    expect(panel.text()).toContain('model-b')
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-b')
    wrapper.unmount()
  })
})

describe('channel quality result series', () => {
  it('expands the latest artwork locally and retains its administrator review controls', async () => {
    api.isAdmin = true
    const result = { ...baseResult, id: 91, output_html: '<svg />' }
    api.list.mockResolvedValue([result])
    api.reviews.mockResolvedValue([{ result, generation: 7, verdict: 'pending', admin_verdict: '', account_paused: false }])
    api.decide.mockResolvedValue(undefined)
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-expand-preview]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    expect(dialog.get('[data-output]').attributes('data-result-id')).toBe('91')
    expect(dialog.get('[data-output]').attributes('data-compact')).toBeUndefined()
    expect(api.history).not.toHaveBeenCalled()
    expect(api.adminResults).not.toHaveBeenCalled()
    await dialog.get('[data-admin-fail]').trigger('click'); await flushPromises()
    expect(api.decide).toHaveBeenCalledWith(91, 7, 'fail')
    await dialog.get('[data-close]').trigger('click')
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('closes an expanded preview if the result is no longer visible after refresh', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.list.mockResolvedValueOnce([{ ...baseResult, id: 1, output_html: '<svg />' }]).mockResolvedValue([])
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-expand-preview]').trigger('click')
    expect(wrapper.find('[data-dialog]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows the latest artwork with three recent thumbnails and fetches complete history on demand', async () => {
    const results = [1, 2, 3, 4, 5].map(id => ({ ...baseResult, id, output_html: `<svg><text>${id}</text></svg>`, created_at: `2026-09-15T${10 + id}:00:00Z` }))
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    expect(api.list).toHaveBeenCalledWith(3, 4)
    expect(wrapper.get('[data-latest-preview] [data-output]').attributes('data-result-id')).toBe('5')
    expect(wrapper.findAll('[data-previous-preview]').map(output => output.attributes('data-previous-preview'))).toEqual(['4', '3', '2'])
    expect(wrapper.get('[data-previous-preview="4"] time').attributes('datetime')).toBe(results[3].created_at)
    expect(wrapper.findAll('[data-result-gallery] [data-output]').every(output => output.attributes('data-compact') === '')).toBe(true)
    expect(api.history).not.toHaveBeenCalled()
    await wrapper.get('[data-previous-preview="4"]').trigger('click')
    expect(wrapper.get('[data-dialog] [data-output]').attributes('data-result-id')).toBe('4')
    expect(wrapper.get('[data-latest-preview] [data-output]').attributes('data-result-id')).toBe('5')
    await wrapper.get('[data-dialog] [data-close]').trigger('click')
    await wrapper.get('[data-content-test] [data-view-history]').trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenCalledWith(5, undefined)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['5', '4', '3', '2', '1'])
    wrapper.unmount()
  })

  it('keeps a failed latest execution in the main preview and skips failed or empty historical artwork', async () => {
    const results = [
      { ...baseResult, id: 5, status: 'failed', created_at: '2026-09-15T15:00:00Z' },
      { ...baseResult, id: 4, status: 'failed', output_html: '<svg />', created_at: '2026-09-15T14:00:00Z' },
      { ...baseResult, id: 3, output_html: ' ', created_at: '2026-09-15T13:00:00Z' },
      { ...baseResult, id: 2, output_html: '<svg />', created_at: '2026-09-15T12:00:00Z' },
    ]
    api.list.mockResolvedValue(results)
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-latest-preview] [data-output]').attributes('data-result-id')).toBe('5')
    expect(wrapper.get('[data-current-verdict]').text()).toBe('tests.executionFailed')
    expect(wrapper.findAll('[data-previous-preview]').map(output => output.attributes('data-previous-preview'))).toEqual(['2'])
    wrapper.unmount()
  })

  it('advances the main preview on refresh while preserving a still-visible historical preview', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const results = [1, 2, 3, 4].map(id => ({ ...baseResult, id, output_html: '<svg />', created_at: `2026-09-15T${10 + id}:00:00Z` }))
    api.list.mockResolvedValueOnce(results.slice(0, 3)).mockResolvedValue(results.slice(1))
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-previous-preview="2"]').trigger('click')
    expect(wrapper.get('[data-dialog] [data-output]').attributes('data-result-id')).toBe('2')
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-latest-preview] [data-output]').attributes('data-result-id')).toBe('4')
    expect(wrapper.findAll('[data-previous-preview]').map(output => output.attributes('data-previous-preview'))).toEqual(['3', '2'])
    expect(wrapper.get('[data-dialog] [data-output]').attributes('data-result-id')).toBe('2')
    api.list.mockResolvedValue([])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    expect(wrapper.find('[data-previous-preview]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('uses stable type IDs and keeps different model and reasoning configurations separate', async () => {
    const results = [
      { ...baseResult, id: 4, model_order: 0, test_name: 'Renamed Pelican', reasoning_effort: 'ultra', created_at: '2026-09-15T14:00:00Z' },
      { ...baseResult, id: 3, model_order: 0, reasoning_effort: 'ultra', created_at: '2026-09-15T13:00:00Z' },
      { ...baseResult, id: 2, model_order: 0, reasoning_effort: 'high' },
      { ...baseResult, id: 1, model_order: 1, reasoning_effort: 'ultra', model_id: 'another-model' },
    ]
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results.slice(0, 2) })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    const series = wrapper.findAll('[data-content-test]')
    expect(series).toHaveLength(2)
    const renamed = series.find(item => item.text().includes('Renamed Pelican'))!
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('gpt-6-astra')
    expect(renamed.find('[data-account-model-selector]').exists()).toBe(false)
    expect(renamed.text()).toContain('gpt-6-astra')
    expect(renamed.text()).toContain('tests.reasoningEffort: ultra')
    expect(renamed.findAll('[data-output]').map(output => output.attributes('data-result-id'))).toEqual(['4'])
    await renamed.get('[data-view-history]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] [data-output]')).toHaveLength(2)
    wrapper.unmount()
  })

  it('switches between models from one plan and loads only the selected model history', async () => {
    const modelA = { ...baseResult, id: 101, model_id: 'model-a', model_order: 0 }
    const modelB = { ...baseResult, id: 102, model_id: 'model-b', model_order: 1 }
    api.list.mockResolvedValue([modelA, modelB])
    api.history.mockResolvedValue({ items: [modelB, { ...modelB, id: 100, created_at: '2026-09-14T12:00:00Z' }] })
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[data-content-test]')).toHaveLength(1)
    expect(wrapper.get('[data-result-gallery] [data-output]').attributes('data-result-id')).toBe('101')
    await wrapper.get('[data-model-selector]').setValue('model-b')
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['102'])
    await wrapper.get('[data-content-test] [data-view-history]').trigger('click'); await flushPromises()
    expect(api.history).toHaveBeenCalledWith(102, undefined)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['102', '100'])
    await wrapper.get('[data-model-selector]').setValue('model-a')
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    expect(wrapper.get('[data-result-gallery] [data-output]').attributes('data-result-id')).toBe('101')
    expect(wrapper.findAll('[data-content-test]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('switches all accounts and active checks together using the configured model order', async () => {
    const primary = { ...baseResult, id: 101, model_id: 'model-z', model_order: 0, created_at: '2026-09-14T12:00:00Z', pass_rate: passRate(10, 9, { model: 'model-z' }) }
    const secondary = { ...baseResult, id: 102, model_id: 'model-a', model_order: 1, created_at: '2026-09-16T12:00:00Z', pass_rate: passRate(10, 4, { model: 'model-a' }) }
    const otherPrimary = { ...baseResult, id: 201, account_id: 4, model_id: 'model-z', model_order: 0 }
    const otherSecondary = { ...baseResult, id: 202, account_id: 4, model_id: 'model-a', model_order: 1 }
    api.list.mockResolvedValue([
      secondary, otherSecondary,
      { ...secondary, id: 104, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 30 },
      { ...secondary, id: 106, test_definition_id: 3, test_name: 'Text', output_kind: 'text' },
      primary, otherPrimary,
      { ...primary, id: 103, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 29 },
      { ...primary, id: 105, test_definition_id: 3, test_name: 'Text', output_kind: 'text' },
    ])
    const wrapper = mountResults(); await flushPromises()
    const accounts = wrapper.findAll('[data-account-result]')
    const outputIDs = (account: typeof accounts[number]) => account.findAll('[data-output]').map(output => output.attributes('data-result-id')).sort()
    expect(accounts).toHaveLength(2)
    expect(outputIDs(accounts[0])).toEqual(['101', '105'])
    expect(accounts[0].get('[data-numeric-gallery] strong').text()).toBe('29')
    expect(accounts[0].findAll('[data-recent-pass-rate-counts]').map(counts => counts.text())).toEqual([
      'Within 7 days, last 10 checks · 9 passed · 90%', 'Within 7 days, last 10 checks · 9 passed · 90%', 'Within 7 days, last 10 checks · 9 passed · 90%',
    ])
    expect(outputIDs(accounts[1])).toEqual(['201'])
    expect(wrapper.findAll('[data-model-selector]')).toHaveLength(1)
    expect(wrapper.find('[data-account-model-selector]').exists()).toBe(false)
    expect(accounts[0].find('[data-account-tests] select').exists()).toBe(false)
    expect(wrapper.get('[data-model-selector]').attributes('aria-label')).toBe('tests.modelFilter')
    expect(wrapper.get('[data-model-selector]').findAll('option').map(option => option.attributes('value'))).toEqual(['model-z', 'model-a'])
    await wrapper.get('[data-model-selector]').setValue('model-a')
    expect(outputIDs(accounts[0])).toEqual(['102', '106'])
    expect(accounts[0].get('[data-numeric-gallery] strong').text()).toBe('30')
    expect(accounts[0].findAll('[data-recent-pass-rate-counts]').map(counts => counts.text())).toEqual([
      'Within 7 days, last 10 checks · 4 passed · 40%', 'Within 7 days, last 10 checks · 4 passed · 40%', 'Within 7 days, last 10 checks · 4 passed · 40%',
    ])
    expect(accounts[0].findAll('[data-test-section]').every(card => card.text().includes('model-a'))).toBe(true)
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-a')
    expect(outputIDs(accounts[1])).toEqual(['202'])
    await wrapper.get('[data-model-selector]').setValue('model-z')
    expect(outputIDs(accounts[1])).toEqual(['201'])
    expect(outputIDs(accounts[0])).toEqual(['101', '105'])
    wrapper.unmount()
  })

  it('retains the global model through polling and compatible groups and falls back when it disappears', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const primary = { ...baseResult, id: 101, model_id: 'model-z', model_order: 0, output_html: '<svg />' }
    const secondary = { ...baseResult, id: 102, model_id: 'model-a', model_order: 1, output_html: '<svg />' }
    const otherGroup = { ...baseResult, id: 201, group_id: 9, group_name: 'Other group', model_id: 'model-a' }
    api.list.mockResolvedValueOnce([secondary, primary, otherGroup])
      .mockResolvedValueOnce([primary, secondary, otherGroup])
      .mockResolvedValue([primary, otherGroup])
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-expand-preview]').trigger('click')
    expect(wrapper.find('[data-dialog]').exists()).toBe(true)
    await wrapper.get('[data-model-selector]').setValue('model-a')
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-a')
    expect(wrapper.get('[data-output]').attributes('data-result-id')).toBe('102')
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(wrapper.get('[data-output]').attributes('data-result-id')).toBe('201')
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-a')
    await wrapper.findAll('[role="tab"]')[0].trigger('click')
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-a')
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-output]').attributes('data-result-id')).toBe('101')
    expect(wrapper.get<HTMLSelectElement>('[data-model-selector]').element.value).toBe('model-z')
    wrapper.unmount()
  })

  it('uses new execution time when a previous record is retried', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 2, started_at: '2026-09-15T12:00:00Z' },
      { ...baseResult, id: 1, created_at: '2026-09-15T10:00:00Z', started_at: '2026-09-15T13:00:00Z' },
    ])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['1'])
    expect(wrapper.get('[data-test-footer] time').text()).toContain(new Date('2026-09-15T13:00:00Z').toLocaleString())
    wrapper.unmount()
  })

  it('orders each account test type using its configured order', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, test_definition_id: 1, test_name: 'Alpha', test_order: 20 },
      { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Beta', test_order: 0 },
    ])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-content-test] h4').map(heading => heading.text())).toEqual(['Beta', 'Alpha'])
    wrapper.unmount()
  })

  it('preserves configured order across text, numeric, and HTML sections', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, test_name: 'Pelican', test_order: 0 },
      { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 29, test_order: 20 },
      { ...baseResult, id: 3, test_definition_id: 3, test_name: 'Text', output_kind: 'text', test_order: 10 },
    ])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-short-test-stack] [data-test-section] h4').map(heading => heading.text())).toEqual(['Text', 'Candy'])
    expect(wrapper.findAll('[data-preview-test-stack] [data-test-section] h4').map(heading => heading.text())).toEqual(['Pelican'])
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    expect(wrapper.findAll('[data-content-test]')).toHaveLength(2)
    expect(wrapper.get('[data-numeric-test] strong').text()).toBe('29')
    wrapper.unmount()
  })


  it('shows the latest failed output and retains successful history', async () => {
    const results = [
      { ...baseResult, id: 3, status: 'failed', error_message: 'private upstream failure', created_at: '2026-09-15T14:00:00Z' },
      { ...baseResult, id: 2, status: 'success' },
      { ...baseResult, id: 1, status: 'passed' },
    ]
    api.list.mockResolvedValue([results[0]])
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['3'])
    expect(wrapper.get('[data-content-test] .badge').text()).toBe('tests.executionFailed')
    expect(wrapper.get('[data-content-test] .badge').classes()).toContain('badge-danger')
    await wrapper.get('[data-content-test] [data-view-history]').trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenCalledWith(3, undefined)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['2', '1'])
    expect(wrapper.text()).not.toContain('private')
    wrapper.unmount()
  })

  it('keeps the result list visible if every result failed', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, status: 'failed' }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.text()).toContain('tests.executionFailed')
    expect(wrapper.find('[role="tablist"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    wrapper.unmount()
  })

  it.each(['pending', 'running'])('keeps an initial %s check visible', async status => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, status }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    expect(wrapper.text()).toContain('tests.running')
    expect(wrapper.find('[data-content-test] [data-view-history]').exists()).toBe(false)
    expect(api.history).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it.each(['number', 'text'])('shows recent %s results and opens complete history on demand', async outputKind => {
    const results = [1, 2, 3, 4].map(id => ({ ...baseResult, id, output_kind: outputKind, output_numeric: id }))
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    if (outputKind === 'number') {
      expect(wrapper.findAll('[data-numeric-gallery] strong').map(item => item.text())).toEqual(['4'])
      expect(wrapper.findAll('[data-previous-numeric-result]').map(item => item.text())).toEqual(['3', '2', '1'])
      expect(wrapper.findAll('[data-previous-preview]').map(item => item.attributes('data-previous-preview'))).toEqual(['3', '2', '1'])
      await wrapper.get('[data-previous-preview="3"]').trigger('click')
      expect(wrapper.get('[data-dialog] [data-output]').attributes('data-result-id')).toBe('3')
      expect(wrapper.get('[data-numeric-gallery] strong').text()).toBe('4')
      await wrapper.get('[data-dialog] [data-close]').trigger('click')
    } else {
      expect(wrapper.findAll('[data-result-gallery] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['4'])
      expect(wrapper.find('[data-previous-preview]').exists()).toBe(false)
    }
    expect(api.history).not.toHaveBeenCalled()
    await wrapper.get('[data-test-section] [data-view-history]').trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenCalledWith(4, undefined)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['4', '3', '2', '1'])
    wrapper.unmount()
  })

  it('limits candy history to three valid numbers, includes zero and keeps a failed current result visible', async () => {
    const results = [1, 2, 3, 4, 5, 6, 7].map(id => ({ ...baseResult, id, output_kind: 'number', output_numeric: id === 4 ? 0 : id,
      created_at: `2026-09-15T${10 + id}:00:00Z` }))
    api.list.mockResolvedValue(results.map(result => result.id === 7 || result.id === 6 ? { ...result, status: 'failed' }
      : result.id === 5 ? { ...result, output_numeric: null } : result))
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-numeric-gallery] strong').text()).toBe('7')
    expect(wrapper.get('[data-current-verdict]').text()).toBe('tests.executionFailed')
    expect(wrapper.findAll('[data-previous-preview]').map(item => item.attributes('data-previous-preview'))).toEqual(['4', '3', '2'])
    expect(wrapper.findAll('[data-previous-numeric-result]').map(item => item.text())).toEqual(['0', '3', '2'])
    expect(wrapper.get('[data-previous-preview="4"]').attributes('aria-label')).toContain('0 · ')
    expect(wrapper.get('[data-previous-preview="4"] time').attributes('datetime')).toBe(results[3].created_at)
    wrapper.unmount()
  })

  it('keeps successful HTML executions pending without a final quality decision', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1 }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.get('[data-content-test] .badge').text()).toBe('tests.verdicts.pending')
    expect(wrapper.get('[data-content-test] .badge').classes()).toContain('badge-warning')
    expect(wrapper.text()).not.toContain('tests.awaitingReview')
    wrapper.unmount()
  })

  it('pages history and retries a failed page without discarding earlier results', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 5 }])
    api.history.mockResolvedValueOnce({ items: [{ ...baseResult, id: 5 }], next_before_id: 5 })
      .mockRejectedValueOnce(new Error('history unavailable'))
      .mockResolvedValueOnce({ items: [{ ...baseResult, id: 4 }, { ...baseResult, id: 3 }] })
    const wrapper = mountResults()
    await flushPromises()
    await wrapper.get('[data-content-test] [data-view-history]').trigger('click')
    await flushPromises()
    await wrapper.findAll('[data-dialog] button').find(button => button.text() === 'tests.loadMore')!.trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenLastCalledWith(5, 5)
    expect(wrapper.find('[data-dialog] [role="alert"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-dialog] [data-output]')).toHaveLength(1)
    await wrapper.findAll('[data-dialog] button').find(button => button.text() === 'common.retry')!.trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenLastCalledWith(5, 5)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['5', '4', '3'])
    expect(wrapper.find('[data-dialog] [role="alert"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-dialog] button').some(button => button.text() === 'tests.loadMore')).toBe(false)
    wrapper.unmount()
  })

  it('ignores a slow history response after opening a different test', async () => {
    const first = { ...baseResult, id: 1, test_name: 'Alpha' }
    const second = { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Beta' }
    api.list.mockResolvedValue([first, second])
    let resolveFirst!: (value: unknown) => void
    api.history.mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
      .mockResolvedValueOnce({ items: [second] })
    const wrapper = mountResults()
    await flushPromises()
    const buttons = wrapper.findAll('[data-content-test] [data-view-history]')
    await buttons[0].trigger('click')
    expect(wrapper.find('[data-dialog] [role="status"]').exists()).toBe(true)
    await wrapper.get('[data-close]').trigger('click')
    await buttons[1].trigger('click')
    await flushPromises()
    resolveFirst({ items: [first], next_before_id: 1 })
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['2'])
    expect(wrapper.find('[data-dialog] [role="status"]').exists()).toBe(false)
    wrapper.unmount()
  })
})


describe('public quality voting follows the configured entries', () => {
  const voting = { enabled: true, open: true, pass_count: 0, fail_count: 0, reject_above: 0, pass_at_least: 2, account_paused: false, reference_answer: '<b>Expected answer</b>' }

  it('includes voting-only models in the top selector and filters voting and account results together', async () => {
    const modelA = { ...baseResult, id: 44, model_id: 'model-a', model_order: 0 }
    const modelB = { ...baseResult, id: 45, account_id: 4, model_id: 'model-b', model_order: 1 }
    api.list.mockResolvedValue([modelA])
    api.votes.mockResolvedValue([{ result: modelA, voting }, { result: modelB, voting }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-model-selector]').findAll('option').map(option => option.attributes('value'))).toEqual(['model-a', 'model-b'])
    expect(wrapper.findAll('[data-vote-result]').map(card => card.attributes('data-vote-result'))).toEqual(['44'])
    expect(wrapper.get('[data-account-result] h3').text()).toBe('tests.account #3')
    await wrapper.get('[data-model-selector]').setValue('model-b')
    expect(wrapper.findAll('[data-vote-result]').map(card => card.attributes('data-vote-result'))).toEqual(['45'])
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    expect(wrapper.find('[data-model-selector]').exists()).toBe(true)
    await wrapper.get('[data-vote-pass]').trigger('click'); await flushPromises()
    expect(api.vote).toHaveBeenCalledWith(45, 'pass')
    wrapper.unmount()
  })

  it('keeps regular paused results visible without enabling paused votes', async () => {
    const result = { ...baseResult, id: 44 }
    api.list.mockResolvedValue([result, { ...result, id: 45, test_definition_id: 2, output_kind: 'number', output_numeric: 21 }])
    api.votes.mockResolvedValue([
      { result, voting: { ...voting, account_paused: true } },
      { result: { ...result, id: 46, test_definition_id: 3 }, voting },
      { result: { ...result, id: 47, account_id: 99, group_id: 9, group_name: 'Voting only' }, voting: { ...voting, account_paused: true } },
    ])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.find('[data-account-result]').exists()).toBe(true)
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingPaused')
    expect(wrapper.find('[data-voting-section]').exists()).toBe(false)
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(1)
    expect(wrapper.findAll('[data-test-section]')).toHaveLength(2)
    expect(api.reviews).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps the voting area hidden when no entries are enabled', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1 }])
    const wrapper = mountResults(); await flushPromises()
    expect(api.votes).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-account-result]').exists()).toBe(true)
    expect(wrapper.find('[data-voting-section]').exists()).toBe(false)
    expect(wrapper.find('[data-vote-result]').exists()).toBe(false)
    expect(api.vote).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('offers only entries returned by the voting endpoint while keeping other results visible', async () => {
    const enabled = { ...baseResult, id: 1 }
    const disabled = { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Private review' }
    api.list.mockResolvedValue([enabled, disabled])
    api.votes.mockResolvedValue([{ result: enabled, voting }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[data-test-section]')).toHaveLength(2)
    expect(wrapper.findAll('[data-vote-result]').map(card => card.attributes('data-vote-result'))).toEqual(['1'])
    expect(wrapper.get('[data-reference-answer]').text()).toBe('<b>Expected answer</b>')
    expect(wrapper.get('[data-reference-answer]').find('b').exists()).toBe(false)
    expect(api.reviews).not.toHaveBeenCalled()
    expect(wrapper.find('[data-admin-decision]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows voting-only results without exposing the account name', async () => {
    api.list.mockResolvedValue([])
    const result = { ...baseResult, id: 44, target_mode: 'account', account_name: 'Must stay private' }
    api.votes.mockResolvedValue([{ result, voting }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Group Eight'])
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    expect(wrapper.find('[data-voting-section]').exists()).toBe(true)
    expect(wrapper.get('[data-vote-result]').attributes('data-vote-result')).toBe('44')
    expect(wrapper.text()).not.toContain('Must stay private')
    wrapper.unmount()
  })

  it('submits once while busy and refreshes the vote counts and selected state', async () => {
    const result = { ...baseResult, id: 44 }
    api.list.mockResolvedValue([result])
    api.votes.mockResolvedValueOnce([{ result, voting }]).mockResolvedValue([{ result, voting: { ...voting, pass_count: 1, my_vote: 'pass' } }])
    let resolveVote!: () => void
    api.vote.mockReturnValue(new Promise<void>(resolve => { resolveVote = resolve }))
    const wrapper = mountResults(); await flushPromises()
    const pass = wrapper.get('[data-vote-pass]')
    await pass.trigger('click')
    await pass.trigger('click')
    expect(api.vote).toHaveBeenCalledOnce()
    expect(api.vote).toHaveBeenCalledWith(44, 'pass')
    expect(pass.attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-vote-fail]').attributes('disabled')).toBeDefined()
    resolveVote(); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(api.votes).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-vote-pass]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-vote-pass]').text()).toContain('1')
    expect(wrapper.get('[data-vote-pass]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('fully refreshes after a vote moves an account out of the user scope', async () => {
    const result = { ...baseResult, id: 44 }
    api.list.mockResolvedValueOnce([result]).mockResolvedValue([])
    api.votes.mockResolvedValueOnce([{ result, voting }]).mockResolvedValue([])
    api.vote.mockResolvedValue({ result, voting: { ...voting, my_vote: 'fail' } })
    api.history.mockResolvedValue({ items: [result] })
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-test-section] [data-view-history]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(true)
    await wrapper.get('[data-vote-fail]').trigger('click'); await flushPromises()
    expect(api.vote).toHaveBeenCalledWith(44, 'fail')
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(api.votes).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    expect(wrapper.find('[data-voting-section]').exists()).toBe(false)
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(0)
    wrapper.unmount()
  })

  it('reports a rejected stale vote and removes revoked entries after refresh', async () => {
    const result = { ...baseResult, id: 44 }
    api.list.mockResolvedValueOnce([result]).mockResolvedValue([])
    api.votes.mockResolvedValueOnce([{ result, voting }]).mockResolvedValue([])
    api.vote.mockRejectedValue(new Error('This round has closed'))
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-vote-pass]').trigger('click'); await flushPromises()
    expect(api.error).toHaveBeenCalledOnce()
    expect(api.votes).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-voting-section]').exists()).toBe(false)
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not allow votes for closed entries or show disabled and failed entries', async () => {
    api.list.mockResolvedValue([])
    api.votes.mockResolvedValue([
      { result: { ...baseResult, id: 44 }, voting: { ...voting, open: false } },
      { result: { ...baseResult, id: 45 }, voting: { ...voting, enabled: false } },
      { result: { ...baseResult, id: 46, status: 'failed' }, voting },
    ])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[data-vote-result]').map(card => card.attributes('data-vote-result'))).toEqual(['44'])
    expect(wrapper.get('[data-vote-pass]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-vote-pass]').trigger('click'); await flushPromises()
    expect(api.vote).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('clears stale entries on voting refresh errors while regular results keep refreshing', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const result = { ...baseResult, id: 1 }
    api.list.mockResolvedValue([result])
    api.votes.mockResolvedValueOnce([{ result, voting }]).mockRejectedValue(new Error('unavailable'))
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.find('[data-voting-section]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(api.votes).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-account-result]').exists()).toBe(true)
    expect(wrapper.find('[data-voting-section]').exists()).toBe(false)
    expect(wrapper.find('[data-votes-error]').exists()).toBe(true)
    expect(api.error).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('ignores a poll started before a vote and reloads permissions after it finishes', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const result = { ...baseResult, id: 44 }
    let resolvePoll!: (value: unknown) => void
    api.list.mockResolvedValue([result])
    api.votes.mockResolvedValueOnce([{ result, voting }])
      .mockReturnValueOnce(new Promise(resolve => { resolvePoll = resolve }))
      .mockResolvedValue([])
    api.vote.mockResolvedValue({ result, voting: { ...voting, my_vote: 'fail' } })
    const wrapper = mountResults(); await flushPromises()
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    api.list.mockResolvedValue([])
    await wrapper.get('[data-vote-fail]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-voting-section]').exists()).toBe(false)
    resolvePoll([{ result, voting }]); await flushPromises()
    expect(api.votes).toHaveBeenCalledTimes(3)
    expect(wrapper.find('[data-voting-section]').exists()).toBe(false)
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    wrapper.unmount()
  })
})


describe('current group refresh and access changes', () => {
  const initial = { ...baseResult, id: 44, group_name: 'Original Group' }
  const moved = { ...initial, group_id: 12, group_name: 'Current Group' }
  const voting = { enabled: true, open: true, pass_count: 0, fail_count: 0, reject_above: 0, pass_at_least: 2, account_paused: false }

  it('updates group tabs on the next five-second visible-page poll', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.list.mockResolvedValueOnce([initial]).mockResolvedValue([moved])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Original Group'])
    await vi.advanceTimersByTimeAsync(4999); await flushPromises()
    expect(api.list).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(1); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Current Group'])
    expect(wrapper.get('[data-account-info]').text()).toContain('Current Group')
    wrapper.unmount()
  })

  it('refreshes immediately after an admin decision even with an older poll still pending', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.isAdmin = true
    const review = { result: initial, generation: 1, verdict: 'pending', admin_verdict: '', account_paused: false }
    let resolveOldResults!: (value: unknown) => void
    let resolveOldReviews!: (value: unknown) => void
    api.list.mockResolvedValueOnce([initial])
      .mockReturnValueOnce(new Promise(resolve => { resolveOldResults = resolve }))
      .mockResolvedValue([moved])
    api.reviews.mockResolvedValueOnce([review])
      .mockReturnValueOnce(new Promise(resolve => { resolveOldReviews = resolve }))
      .mockResolvedValue([{ ...review, result: moved, verdict: 'pass', admin_verdict: 'pass' }])
    api.decide.mockResolvedValue(undefined)
    const wrapper = mountResults(); await flushPromises()
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(2)
    await wrapper.get('[data-admin-pass]').trigger('click'); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(3)
    expect(api.reviews).toHaveBeenCalledTimes(3)
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Current Group'])
    resolveOldResults([initial]); resolveOldReviews([review]); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Current Group'])
    expect(wrapper.get('[data-admin-pass]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('publishes all result projections together without mixing a new group and old reviews', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.isAdmin = true
    const review = { result: { ...initial, id: 45 }, generation: 1, verdict: 'pending', admin_verdict: '', account_paused: false }
    let resolveReviews!: (value: unknown) => void
    api.list.mockResolvedValueOnce([initial]).mockResolvedValue([moved])
    api.reviews.mockResolvedValueOnce([review]).mockReturnValueOnce(new Promise(resolve => { resolveReviews = resolve }))
    const wrapper = mountResults(); await flushPromises()
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Original Group'])
    resolveReviews([{ ...review, result: { ...moved, id: 45 } }]); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Current Group'])
    wrapper.unmount()
  })

  it('uses the current result group for its voting card when review projection is older', async () => {
    api.list.mockResolvedValue([moved])
    api.votes.mockResolvedValue([{ result: initial, voting }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Current Group'])
    expect(wrapper.get('[data-vote-result]').attributes('data-vote-result')).toBe('44')
    wrapper.unmount()
  })

  it('closes cached history when access disappears and ignores its delayed response', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let resolveHistory!: (value: unknown) => void
    api.list.mockResolvedValueOnce([initial]).mockResolvedValue([])
    api.history.mockReturnValue(new Promise(resolve => { resolveHistory = resolve }))
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-test-section] [data-view-history]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    resolveHistory({ items: [initial] }); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    expect(wrapper.find('[data-output]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps unchanged history open but closes it when the account moves', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.list.mockResolvedValue([initial])
    api.history.mockResolvedValue({ items: [initial] })
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-test-section] [data-view-history]').trigger('click'); await flushPromises()
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(true)
    api.list.mockResolvedValue([moved])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Current Group'])
    wrapper.unmount()
  })

  it.each([403, 404])('clears old history and reloads permissions after a top-level %s error', async status => {
    api.list.mockResolvedValueOnce([initial]).mockResolvedValue([])
    api.history.mockResolvedValueOnce({ items: [initial], next_before_id: 44 })
      .mockRejectedValueOnce({ status, message: 'Access is no longer available' })
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-test-section] [data-view-history]').trigger('click'); await flushPromises()
    expect(wrapper.findAll('[data-dialog] [data-output]')).toHaveLength(1)
    await wrapper.findAll('[data-dialog] button').find(button => button.text() === 'tests.loadMore')!.trigger('click'); await flushPromises()
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-account-result]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('refreshes when the page becomes visible or focused and removes both listeners on unmount', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    api.list.mockResolvedValueOnce([initial]).mockResolvedValue([moved])
    const wrapper = mountResults(); await flushPromises()
    hidden.mockReturnValue(true)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(api.list).toHaveBeenCalledOnce()
    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange')); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Current Group'])
    window.dispatchEvent(new Event('focus')); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(3)
    wrapper.unmount()
    document.dispatchEvent(new Event('visibilitychange'))
    window.dispatchEvent(new Event('focus')); await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(3)
  })
})


describe('current group display order', () => {
  const proPlus = { ...baseResult, id: 1, group_id: 43, group_name: 'Pro-不降智', group_order: 0, plan_order: 99 }
  // This result was produced by Pro-不降智's plan before its account moved.
  const pro = { ...baseResult, id: 2, account_id: 4, group_id: 2, group_name: 'Pro', group_order: 1, plan_order: 0 }
  const plus = { ...baseResult, id: 3, account_id: 5, group_id: 3, group_name: 'Plus', group_order: 2, plan_order: 0 }

  it('uses current group order including zero, independent of source plan and API row order', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const unconfigured = { ...baseResult, id: 4, group_id: 4, group_name: 'No group plan', group_order: 2147483647, plan_order: 0 }
    api.list.mockResolvedValueOnce([pro, unconfigured, plus, proPlus]).mockResolvedValue([plus, proPlus, unconfigured, pro])
    const wrapper = mountResults(); await flushPromises()
    const expected = ['Pro-不降智', 'Pro', 'Plus', 'No group plan']
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(expected)
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(expected)
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(wrapper.get('[data-account-info]').text()).toContain('tests.account #4')
    expect(wrapper.get('[data-account-info]').text()).toContain('Pro')
    wrapper.unmount()
  })

  it('applies newly configured group orders without new test results and preserves the selected group', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.list.mockResolvedValueOnce([proPlus, pro, plus])
      .mockResolvedValue([{ ...proPlus, group_order: 2 }, pro, { ...plus, group_order: 0 }])
    const wrapper = mountResults(); await flushPromises()
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Plus', 'Pro', 'Pro-不降智'])
    expect(wrapper.findAll('[role="tab"]').filter(tab => tab.attributes('aria-selected') === 'true').map(tab => tab.text())).toEqual(['Pro'])
    expect(wrapper.get('[data-output]').attributes('data-result-id')).toBe('2')
    expect(api.list).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('sorts voting-only groups by current order and copies current projection order into existing voting cards', async () => {
    const voting = { enabled: true, open: true, pass_count: 0, fail_count: 0, reject_above: 0, pass_at_least: 1, account_paused: false }
    api.list.mockResolvedValue([proPlus, { ...pro, account_schedulable: true, model_schedulable: false }])
    api.votes.mockResolvedValue([
      { result: { ...pro, group_id: 43, group_name: 'Pro-不降智', group_order: 0, plan_order: 55, model_schedulable: true }, voting },
      { result: plus, voting },
    ])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Pro-不降智', 'Pro', 'Plus'])
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(wrapper.getComponent(TestVoteCard).props('item').result).toMatchObject({ id: 2, group_id: 2, group_name: 'Pro', group_order: 1, plan_order: 0, account_schedulable: true, model_schedulable: false })
    expect(wrapper.get('[data-vote-result] [data-model-scheduling-status]').text()).toBe('tests.modelSchedulingPaused')
    await wrapper.findAll('[role="tab"]')[2].trigger('click')
    expect(wrapper.get('[data-vote-result]').attributes('data-vote-result')).toBe('3')
    wrapper.unmount()
  })
})


describe('current scheduling and passing result order', () => {
  it('shows one paused model without pausing the account or its other model', async () => {
    const modelA = { ...baseResult, id: 1, model_id: 'model-a', model_order: 0, account_schedulable: true, model_schedulable: false }
    const modelB = { ...baseResult, id: 2, model_id: 'model-b', model_order: 1, account_schedulable: true, model_schedulable: true }
    api.list.mockResolvedValue([modelA, modelB])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingActive')
    expect(wrapper.findAll('[data-test-section] [data-model-scheduling-status]').map(node => node.text())).toEqual(['tests.modelSchedulingPaused'])
    expect(wrapper.get('[data-selected-model-status]').text()).toContain('tests.modelSchedulingPaused')
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingActive')
    await wrapper.get('[data-model-selector]').setValue('model-b')
    expect(wrapper.get('[data-selected-model-status]').text()).toContain('tests.modelSchedulingActive')
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingActive')
    wrapper.unmount()
  })

  it('uses the account projection when all monitored models pause and recover', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const modelA = { ...baseResult, id: 1, model_id: 'model-a', model_order: 0, account_schedulable: false, model_schedulable: false }
    const modelB = { ...baseResult, id: 2, model_id: 'model-b', model_order: 1, account_schedulable: false, model_schedulable: false }
    api.list.mockResolvedValueOnce([modelA, modelB]).mockResolvedValue([
      { ...modelA, account_schedulable: true },
      { ...modelB, account_schedulable: true, model_schedulable: true },
    ])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingPaused')
    await wrapper.get('[data-model-selector]').setValue('model-b')
    expect(wrapper.get('[data-selected-model-status]').text()).toContain('tests.modelSchedulingPaused')
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingActive')
    expect(wrapper.get('[data-selected-model-status]').text()).toContain('tests.modelSchedulingActive')
    wrapper.unmount()
  })

  it('never infers whole-account unavailability from model-only evidence', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, model_schedulable: false }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.find('[data-scheduling-status]').exists()).toBe(false)
    expect(wrapper.get('[data-model-scheduling-status]').text()).toContain('tests.modelSchedulingPaused')
    wrapper.unmount()
  })

  it('places passing active accounts first and reorders after scheduling changes', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const paused = { ...baseResult, id: 1, account_id: 1, account_schedulable: false, quality_verdict: 'pass' }
    const failed = { ...baseResult, id: 2, account_id: 2, account_schedulable: true, status: 'failed' }
    const passed = { ...baseResult, id: 3, account_id: 3, account_schedulable: true, quality_verdict: 'pass' }
    const mismatch = { ...baseResult, id: 4, account_id: 4, account_schedulable: true, output_kind: 'model_check', output_model_check: { verdict: 'fail' } }
    api.list.mockResolvedValueOnce([paused, failed, mismatch, passed])
      .mockResolvedValue([{ ...paused, account_schedulable: true }, failed, mismatch, { ...passed, account_schedulable: false }])
    const wrapper = mountResults(); await flushPromises()
    const order = () => wrapper.findAll('[data-account-info] h3').map(node => node.text())
    expect(order()).toEqual(['tests.account #3', 'tests.account #2', 'tests.account #4', 'tests.account #1'])
    const statuses = wrapper.findAll('[data-scheduling-status]')
    expect(statuses[0].text()).toContain('tests.schedulingActive')
    expect(statuses[0].get('.text-emerald-600').text()).toContain('tests.schedulingActive')
    expect(statuses[3].text()).toContain('tests.schedulingPaused')
    expect(statuses[3].get('.text-red-600').text()).toContain('tests.schedulingPaused')
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(order()).toEqual(['tests.account #1', 'tests.account #2', 'tests.account #4', 'tests.account #3'])
    wrapper.unmount()
  })

  it('uses the live scheduling switch instead of inferring it from execution success or old reviews', async () => {
    api.isAdmin = true
    const result = { ...baseResult, id: 1, account_schedulable: false }
    api.list.mockResolvedValue([result])
    api.reviews.mockResolvedValue([{ result, generation: 1, verdict: 'pass', admin_verdict: 'pass', account_paused: false }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-scheduling-status]').text()).toContain('tests.schedulingPaused')
    expect(wrapper.get('[data-test-section] .badge').text()).toBe('tests.verdicts.pass')
    wrapper.unmount()
  })

  it('does not invent an account scheduling state for group checks', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, account_id: null, target_mode: 'group', output_kind: 'text' }])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.find('[data-scheduling-status]').exists()).toBe(false)
    wrapper.unmount()
  })
})

it('reports capability failure even when the upstream request succeeded', async () => {
 api.list.mockResolvedValue([{ ...baseResult, id: 1, output_kind: 'capability', execution_info: { attempts: [], capability: { verdict: 'fail' } } }])
 const w = mountResults(); await flushPromises()
 expect(w.get('[data-test-section] .badge').text()).toBe('tests.verdicts.fail')
 expect(w.get('[data-test-section] .badge').classes()).toContain('badge-danger')
 w.unmount()
})
