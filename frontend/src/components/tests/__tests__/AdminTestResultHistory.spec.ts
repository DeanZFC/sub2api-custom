import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AdminTestResultHistory from '../AdminTestResultHistory.vue'
import type { AccountListItem, TestResult, TestType } from '@/types'
import resources from '@/i18n/locales/en/admin/resources'

const messages = {
  admin: resources, common: { delete: 'Delete', loading: 'Loading', noData: 'No data', status: 'Status' },
  tests: { running: 'Test is running', noOutput: 'No output' },
}
vi.mock('vue-i18n', () => ({ useI18n: () => ({
  t: (key: string, params: Record<string, unknown> = {}) => {
    const value = key.split('.').reduce<unknown>((current, part) => current && typeof current === 'object' ? (current as Record<string, unknown>)[part] : undefined, messages)
    return typeof value === 'string' ? value.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? '')) : key
  },
}) }))

const result = (id: number, extra: Partial<TestResult> = {}): TestResult => ({
  id, plan_id: 1, account_id: 10, test_definition_id: 1, test_name: 'Animation',
  model_id: 'gpt-6-astra', reasoning_effort: 'high', status: 'success', output_kind: 'html',
  output_html: '<p>complete content</p>', latency_ms: 2300, started_at: '2026-09-21T10:00:00Z', ...extra,
})
const accounts = [{ id: 10, name: 'Pro Alpha' }, { id: 20, name: 'Team Beta' }] as AccountListItem[]
const types = [{ id: 1, name: 'Animation' }, { id: 2, name: 'Candy' }] as TestType[]
const mountHistory = (results: TestResult[], extra = {}) => mount(AdminTestResultHistory, {
  props: { results, accounts, types, now: Date.parse('2026-09-21T10:01:05Z'), history: true, loading: false, retryingResultId: null, deletingResultId: null, ...extra },
  global: {
    stubs: {
      Icon: true,
      TestResultOutput: { props: ['result'], template: '<div data-output :data-output-id="result.id">{{ result.output_numeric ?? result.response_text ?? result.output_html }}</div>' },
      Pagination: { props: ['total', 'page', 'pageSize'], template: '<div data-pagination :data-total="total"><button data-next-page @click="$emit(\'update:page\', page + 1)">Next</button></div>' },
    },
  },
})

describe('admin result account history', () => {
  it('groups by account, shows names and IDs, and orders each execution independently', async () => {
    const wrapper = mountHistory([
      result(8, { account_id: 20 }),
      result(1, { started_at: '2026-09-21T10:00:01Z', output_kind: 'number', output_numeric: 29, test_name: 'Candy', test_definition_id: 2 }),
      result(3, { status: 'failed', error_message: 'upstream unavailable' }),
      result(2, { status: 'running' }),
    ])
    await flushPromises()
    const accounts = wrapper.findAll('[data-account-key]')
    expect(accounts.map(item => item.text())).toEqual([expect.stringContaining('Pro Alpha'), expect.stringContaining('Team Beta')])
    expect(accounts[0].text()).toContain('#10')
    expect(accounts[0].text()).toContain('3 results')
    expect(accounts[0].text()).toContain('1 failed')
    expect(accounts[0].text()).toContain('1 running')
    expect(wrapper.findAll('[data-result-id]').map(item => item.attributes('data-result-id'))).toEqual(['1', '3', '2'])
    expect(wrapper.get('[data-result-id="1"] [data-output]').text()).toBe('29')
    expect(wrapper.find('[data-result-id="1"] [data-result-summary]').exists()).toBe(false)
    expect(wrapper.get('[data-result-id="3"] [data-result-summary]').text()).toBe('upstream unavailable')
    expect(wrapper.get('[data-result-id="2"]').text()).toContain('Elapsed 01:05')
    expect(wrapper.get('[data-result-id="1"]').text()).toContain('gpt-6-astra · Reasoning effort: high')
    expect(wrapper.findAll('[data-output]').map(item => item.attributes('data-output-id'))).toEqual(['1'])
    await accounts[1].trigger('click')
    expect(wrapper.get('[data-selected-account] h3').text()).toBe('Team Beta #20')
    expect(wrapper.findAll('[data-output]').map(item => item.attributes('data-output-id'))).toEqual(['8'])
  })

  it('uses names from historical records when an account is not in the active account list', async () => {
    const wrapper = mountHistory([result(1, { account_id: 99, account_name: 'Archived Pro' })])
    await flushPromises()
    expect(wrapper.get('[data-account-key="account:99"]').text()).toContain('Archived Pro')
    expect(wrapper.get('[data-selected-account] h3').text()).toBe('Archived Pro #99')
  })

  it('searches by name or ID and keeps mobile selection separate from desktop account navigation', async () => {
    const wrapper = mountHistory([result(1), result(2, { account_id: 20 })])
    await flushPromises()
    await wrapper.get('[data-account-search]').setValue('beta')
    expect(wrapper.findAll('[data-account-key]')).toHaveLength(1)
    expect(wrapper.get('[data-selected-account] h3').text()).toBe('Team Beta #20')
    await wrapper.get('[data-account-search]').setValue('#10')
    expect(wrapper.get('[data-selected-account] h3').text()).toBe('Pro Alpha #10')
    await wrapper.get('[data-account-search]').setValue('')
    await wrapper.get('[data-account-select]').setValue('account:20')
    expect(wrapper.get('[data-selected-account] h3').text()).toBe('Team Beta #20')
    await wrapper.get('[data-account-search]').setValue('no matching account')
    expect(wrapper.find('[data-selected-account]').exists()).toBe(false)
    expect(wrapper.text()).toContain('No matching results')
  })

  it('keeps group checks and unassigned placeholders separate from direct account checks', async () => {
    const wrapper = mountHistory([
      result(2), result(3, { target_mode: 'group', account_id: 20 }),
      result(-1, { account_id: null, target_mode: 'all_accounts', status: 'pending' }),
    ])
    await flushPromises()
    expect(wrapper.findAll('[data-account-key]').map(item => item.attributes('data-account-key'))).toEqual(['group', 'unassigned', 'account:10'])
    expect(wrapper.get('[data-selected-account] h3').text()).toBe('Group check')
    expect(wrapper.get('[data-result-id="3"]').text()).toContain('Execution account: Team Beta #20')
    await wrapper.get('[data-account-key="unassigned"]').trigger('click')
    expect(wrapper.get('[data-selected-account] h3').text()).toBe('Waiting for an account')
    expect(wrapper.find('[data-delete-result]').exists()).toBe(false)
    expect(wrapper.find('[data-retry-result]').exists()).toBe(false)
  })

  it('filters type and status while preserving distinct execution records', async () => {
    const wrapper = mountHistory([
      result(1, { status: 'passed' }), result(2, { status: 'failed' }),
      result(3, { test_definition_id: 2, test_name: 'Candy', status: 'running' }),
      result(4, { test_definition_id: 2, test_name: 'Candy', status: 'pending' }),
    ])
    await flushPromises()
    await wrapper.get('[data-type-filter]').setValue('2')
    await wrapper.get('[data-status-filter]').setValue('running')
    expect(wrapper.findAll('[data-result-id]').map(item => item.attributes('data-result-id'))).toEqual(['4', '3'])
    await wrapper.get('[data-status-filter]').setValue('failed')
    expect(wrapper.text()).toContain('No matching results')
    await wrapper.get('[data-type-filter]').setValue('')
    expect(wrapper.findAll('[data-result-id]').map(item => item.attributes('data-result-id'))).toEqual(['2'])
    await wrapper.get('[data-status-filter]').setValue('success')
    expect(wrapper.findAll('[data-result-id]').map(item => item.attributes('data-result-id'))).toEqual(['1'])
  })

  it('allows multiple expanded outputs and exposes retry and delete as independent commands', async () => {
    const failed = result(1, { status: 'failed', error_message: 'full error details' })
    const wrapper = mountHistory([failed, result(2)])
    await flushPromises()
    expect(wrapper.findAll('[data-output]')).toHaveLength(1)
    await wrapper.get('[data-result-id="1"] [data-expand-result]').trigger('click')
    expect(wrapper.findAll('[data-output]')).toHaveLength(2)
    expect(wrapper.get('[role="alert"]').text()).toBe('full error details')
    await wrapper.get('[data-retry-result]').trigger('click')
    expect(wrapper.emitted('retry')?.[0]).toEqual([failed])
    await wrapper.get('[data-result-id="1"] [data-delete-result]').trigger('click')
    expect(wrapper.emitted('delete')?.[0]).toEqual([failed])
    expect(wrapper.get('[data-retry-result]').attributes('aria-label')).toBe('Retry')
    expect(wrapper.get('[data-retry-result]').attributes('title')).toBe('Retry')
    await wrapper.setProps({ retryingResultId: 1 })
    expect(wrapper.get('[data-retry-result]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-result-id="1"] [data-delete-result]').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('[data-output]')).toHaveLength(2)
  })

  it('pages long histories locally and preserves account selection through refresh', async () => {
    const results = Array.from({ length: 17 }, (_, index) => result(index + 1))
    const wrapper = mountHistory([...results, result(20, { account_id: 20 })])
    await flushPromises()
    expect(wrapper.findAll('[data-result-id]')).toHaveLength(15)
    expect(wrapper.get('[data-pagination]').attributes('data-total')).toBe('17')
    await wrapper.get('[data-next-page]').trigger('click')
    expect(wrapper.findAll('[data-result-id]').map(item => item.attributes('data-result-id'))).toEqual(['2', '1'])
    expect(wrapper.get('[data-output]').attributes('data-output-id')).toBe('2')
    await wrapper.get('[data-account-key="account:20"]').trigger('click')
    await wrapper.setProps({ results: [result(30), result(20, { account_id: 20 })] })
    expect(wrapper.get('[data-selected-account] h3').text()).toBe('Team Beta #20')
    expect(wrapper.find('[data-pagination]').exists()).toBe(false)
    expect(wrapper.get('[data-output]').attributes('data-output-id')).toBe('20')
  })

  it('summarizes text without replacing full expanded content', async () => {
    const text = 'A long result '.repeat(30)
    const wrapper = mountHistory([result(1, { output_kind: 'text', output_html: null, response_text: text })])
    await flushPromises()
    expect(wrapper.get('[data-output]').text()).toBe(text.trim())
    await wrapper.get('[data-expand-result]').trigger('click')
    expect(wrapper.get('[data-result-summary]').text().length).toBeLessThan(text.length)
  })

  it('shows loading or empty status before any executions exist', async () => {
    const wrapper = mountHistory([], { loading: true })
    expect(wrapper.get('[role="status"]').text()).toBe('Loading')
    await wrapper.setProps({ loading: false })
    expect(wrapper.get('[role="status"]').text()).toBe('No data')
  })

  it('summarizes statistics without raw JSON or a misleading model latency', async () => {
    const wrapper = mountHistory([result(1, {
      output_kind: 'statistics', latency_ms: 12, response_text: '{"internal":"not public"}',
      output_statistics: { window_start: '2026-09-21T09:00:00Z', window_end: '2026-09-21T10:00:00Z', total_requests: 0, success_requests: 0, failed_requests: 0, success_rate: null, cache_rate: null, avg_first_token_ms: null, first_token_samples: 0, cache_read_tokens: 0, cache_input_tokens: 0 },
    })])
    await flushPromises()
    await wrapper.get('[data-expand-result]').trigger('click')
    expect(wrapper.get('[data-result-summary]').text()).toBe('Last-hour statistics')
    expect(wrapper.text()).not.toContain('12ms')
    expect(wrapper.text()).not.toContain('internal')
  })

  it('keeps the newest result expanded after a placeholder is persisted, unless manually collapsed', async () => {
    const wrapper = mountHistory([result(-1, { status: 'running' })])
    await flushPromises()
    expect(wrapper.get('[data-output]').attributes('data-output-id')).toBe('-1')
    await wrapper.setProps({ results: [result(101)] })
    expect(wrapper.get('[data-output]').attributes('data-output-id')).toBe('101')
    await wrapper.get('[data-expand-result]').trigger('click')
    await wrapper.setProps({ results: [result(102)] })
    expect(wrapper.find('[data-output]').exists()).toBe(false)
  })
})
