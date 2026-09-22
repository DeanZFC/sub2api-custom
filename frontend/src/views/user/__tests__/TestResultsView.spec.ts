import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestResultsView from '../TestResultsView.vue'
import TestVoteCard from '@/components/tests/TestVoteCard.vue'

const api = vi.hoisted(() => ({ list: vi.fn(), history: vi.fn(), votes: vi.fn(), vote: vi.fn(), error: vi.fn(), reviews: vi.fn(), decide: vi.fn(), adminResults: vi.fn(), isAdmin: false }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: api.isAdmin }) }))
vi.mock('@/api/admin/tests', () => ({ listReviews: api.reviews, decideResult: api.decide, listResults: api.adminResults, default: { listReviews: api.reviews, decideResult: api.decide } }))
vi.mock('@/api/testResults', () => ({ testResultsAPI: { list: api.list, history: api.history, votes: api.votes, vote: api.vote } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.error }) }))

const baseResult = { plan_id: 10, test_definition_id: 1, test_name: 'Pelican', group_id: 8, group_name: 'Group Eight', account_id: 3, model_id: 'gpt-6-astra', output_kind: 'html', status: 'success', created_at: '2026-09-15T12:00:00Z' }
const mountResults = () => mount(TestResultsView, { global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
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

describe('channel quality result groups', () => {
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

  it('uses admin access for paused account history and filters out other accounts and tests', async () => {
    api.isAdmin = true
    api.list.mockResolvedValue([])
    const result = { ...baseResult, id: 91 }
    api.reviews.mockResolvedValue([{ result, generation: 7, verdict: 'fail', admin_verdict: 'fail', account_paused: true }])
    api.adminResults.mockResolvedValue([result, { ...result, id: 92, account_id: 99 }, { ...result, id: 93, test_definition_id: 5 }, { ...result, id: 94, status: 'failed' }])
    const wrapper = mountResults(); await flushPromises()
    await wrapper.get('[data-test-section] button').trigger('click'); await flushPromises()
    expect(api.adminResults).toHaveBeenCalledWith(10, 21)
    expect(api.history).not.toHaveBeenCalled()
    expect(wrapper.get('[data-dialog]').findAll('[data-output]').map(output => output.attributes('data-result-id'))).toEqual(['91'])
    wrapper.unmount()
  })

  it('shows administrator decisions for current paused results and refreshes after a verdict', async () => {
    api.isAdmin = true
    api.list.mockResolvedValue([])
    const result = { ...baseResult, id: 91, account_name: 'Review Account' }
    const review = { result, generation: 7, verdict: 'pending', admin_verdict: '', account_paused: true }
    api.reviews.mockResolvedValue([review])
    api.decide.mockResolvedValue(undefined)
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.get('[data-account-info]').text()).toContain('Review Account')
    expect(wrapper.get('[data-admin-decision]').text()).toContain('tests.voting.paused')
    api.reviews.mockResolvedValue([{ ...review, verdict: 'pass', admin_verdict: 'pass', account_paused: false }])
    await wrapper.get('[data-admin-pass]').trigger('click'); await flushPromises()
    expect(api.decide).toHaveBeenCalledWith(91, 7, 'pass')
    expect(wrapper.get('[data-admin-pass]').attributes('disabled')).toBeDefined()
    api.reviews.mockResolvedValue([{ ...review, verdict: 'fail', admin_verdict: 'fail' }])
    await wrapper.get('[data-admin-fail]').trigger('click'); await flushPromises()
    expect(api.decide).toHaveBeenLastCalledWith(91, 7, 'fail')
    expect(wrapper.get('[data-admin-fail]').attributes('disabled')).toBeDefined()
    expect(api.vote).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('reloads result projections after an administrator decision', async () => {
    api.isAdmin = true
    const initial = { ...baseResult, id: 101, group_id: 8, group_name: 'Source Group' }
    const moved = { ...initial, group_id: 12, group_name: 'Target Group', protection_decision: { verdict: 'pass', status: 'applied', scheduling: 'resume' } }
    api.list.mockResolvedValueOnce([initial]).mockResolvedValueOnce([moved])
    api.reviews.mockResolvedValueOnce([{ result: initial, generation: 4, verdict: 'pending', admin_verdict: '', account_paused: true }])
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
    await wrapper.get('[data-test-section] button').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-dialog]').findAll('[data-output]').map(output => output.attributes('data-result-id'))).toEqual(['91'])
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
    expect(wrapper.findAll('[data-result-gallery] figure')).toHaveLength(1)
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
    expect(wrapper.get('[data-test-section] .badge').text()).toBe('tests.modelCheck.fail')
    expect(wrapper.get('[data-test-section] .badge').classes()).toContain('badge-danger')
    expect(wrapper.text()).not.toContain('Private account name')
    await wrapper.get('[data-test-section] button').trigger('click'); await flushPromises()
    const badges = wrapper.get('[data-dialog]').findAll('.badge')
    expect(badges.map(badge => badge.text())).toEqual(['tests.modelCheck.fail', 'tests.modelCheck.unknown'])
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

    expect(api.list).toHaveBeenCalledWith(3)
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

  it('preserves document scrolling while polling and leaves model filters accessible without matches', async () => {
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
    await wrapper.get('select').setValue('model-b')
    expect(panel.element.scrollTop).toBe(0)
    expect(panel.text()).toContain('tests.noMatches')
    expect(wrapper.find('select').exists()).toBe(true)
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(panel.findAll('[data-account-result]')).toHaveLength(1)
    wrapper.unmount()
  })
})

describe('channel quality result series', () => {
  it('shows only the latest output and fetches complete history on demand', async () => {
    const results = [1, 2, 3, 4, 5].map(id => ({ ...baseResult, id, created_at: `2026-09-15T${10 + id}:00:00Z` }))
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['5'])
    expect(wrapper.findAll('[data-result-gallery] [data-output]').every(output => output.attributes('data-compact') === '')).toBe(true)
    expect(wrapper.text()).not.toContain('tests.previousResult')
    expect(api.history).not.toHaveBeenCalled()
    await wrapper.get('[data-content-test] button').trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenCalledWith(5, undefined)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['5', '4', '3', '2', '1'])
    wrapper.unmount()
  })

  it('uses stable type IDs and keeps different model and reasoning configurations separate', async () => {
    const results = [
      { ...baseResult, id: 4, test_name: 'Renamed Pelican', reasoning_effort: 'ultra', created_at: '2026-09-15T14:00:00Z' },
      { ...baseResult, id: 3, reasoning_effort: 'ultra', created_at: '2026-09-15T13:00:00Z' },
      { ...baseResult, id: 2, reasoning_effort: 'high' },
      { ...baseResult, id: 1, reasoning_effort: 'ultra', model_id: 'another-model' },
    ]
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results.slice(0, 2) })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    const series = wrapper.findAll('[data-content-test]')
    expect(series).toHaveLength(3)
    const renamed = series.find(item => item.text().includes('Renamed Pelican'))!
    expect(renamed.text()).toContain('gpt-6-astra · tests.reasoningEffort: ultra')
    expect(renamed.findAll('[data-output]').map(output => output.attributes('data-result-id'))).toEqual(['4'])
    await renamed.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] [data-output]')).toHaveLength(2)
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
    expect(wrapper.findAll('figcaption')[0].text()).toContain(new Date('2026-09-15T13:00:00Z').toLocaleString())
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

  it('preserves configured order across statistics, numeric, and HTML sections', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, test_name: 'Pelican', test_order: 0 },
      { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 29, test_order: 20 },
      { ...baseResult, id: 3, test_definition_id: 3, test_name: 'Last hour', output_kind: 'statistics', test_order: 10 },
    ])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-account-tests] > [data-test-section] h4').map(heading => heading.text())).toEqual(['Pelican', 'Last hour', 'Candy'])
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    expect(wrapper.findAll('[data-statistics-test]')).toHaveLength(1)
    expect(wrapper.get('[data-numeric-test] strong').text()).toBe('29')
    wrapper.unmount()
  })

  it('shows the latest statistics snapshot and loads previous snapshots through history', async () => {
    const results = [1, 2, 3].map(id => ({ ...baseResult, id, test_name: 'Last hour', output_kind: 'statistics', latency_ms: 8 }))
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-statistics-test] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['3'])
    expect(wrapper.get('[data-statistics-test]').text()).not.toContain('8ms')
    await wrapper.get('[data-statistics-test] button').trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenCalledWith(3, undefined)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['3', '2', '1'])
    wrapper.unmount()
  })

  it('shows the latest successful output and removes failed records from cards and history', async () => {
    const results = [
      { ...baseResult, id: 3, status: 'failed', error_message: 'private upstream failure', created_at: '2026-09-15T14:00:00Z' },
      { ...baseResult, id: 2, status: 'success' },
      { ...baseResult, id: 1, status: 'passed' },
    ]
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['2'])
    await wrapper.get('[data-content-test] button').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['2', '1'])
    expect(wrapper.text()).not.toContain('private')
    wrapper.unmount()
  })

  it('shows an empty state if every result failed', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, status: 'failed' }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.text()).toContain('tests.empty')
    expect(wrapper.find('[role="tablist"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each(['pending', 'running'])('keeps an initial %s check visible', async status => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, status }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    expect(wrapper.text()).toContain('tests.running')
    expect(wrapper.find('[data-content-test] button').exists()).toBe(false)
    expect(api.history).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it.each(['number', 'text'])('shows only the latest %s result and opens older results through history', async outputKind => {
    const results = [1, 2, 3, 4].map(id => ({ ...baseResult, id, output_kind: outputKind, output_numeric: id }))
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    if (outputKind === 'number') {
      expect(wrapper.findAll('[data-numeric-gallery] strong').map(item => item.text())).toEqual(['4'])
    } else {
      expect(wrapper.findAll('[data-result-gallery] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['4'])
    }
    expect(api.history).not.toHaveBeenCalled()
    await wrapper.get('[data-test-section] button').trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenCalledWith(4, undefined)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['4', '3', '2', '1'])
    wrapper.unmount()
  })

  it('labels completed HTML tests as completed without a manual review state', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1 }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.get('[data-content-test] .badge').text()).toBe('tests.completed')
    expect(wrapper.get('[data-content-test] .badge').classes()).toContain('badge-success')
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
    await wrapper.get('[data-content-test] button').trigger('click')
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
    const buttons = wrapper.findAll('[data-content-test] button')
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
  const voting = { enabled: true, open: true, pass_count: 0, fail_count: 0, reject_above: 0, pass_at_least: 2, account_paused: true, reference_answer: '<b>Expected answer</b>' }

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
    await wrapper.get('[data-test-section] button').trigger('click'); await flushPromises()
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
    await wrapper.get('[data-test-section] button').trigger('click'); await flushPromises()
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
    await wrapper.get('[data-test-section] button').trigger('click'); await flushPromises()
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
    await wrapper.get('[data-test-section] button').trigger('click'); await flushPromises()
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
    api.list.mockResolvedValue([proPlus, pro])
    api.votes.mockResolvedValue([
      { result: { ...pro, group_id: 43, group_name: 'Pro-不降智', group_order: 0, plan_order: 55 }, voting },
      { result: plus, voting },
    ])
    const wrapper = mountResults(); await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Pro-不降智', 'Pro', 'Plus'])
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(wrapper.getComponent(TestVoteCard).props('item').result).toMatchObject({ id: 2, group_id: 2, group_name: 'Pro', group_order: 1, plan_order: 0 })
    await wrapper.findAll('[role="tab"]')[2].trigger('click')
    expect(wrapper.get('[data-vote-result]').attributes('data-vote-result')).toBe('3')
    wrapper.unmount()
  })
})
