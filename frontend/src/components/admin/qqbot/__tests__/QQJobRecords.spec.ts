import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import QQJobRecords from '../QQJobRecords.vue'

const api = vi.hoisted(() => ({ jobs: vi.fn(), members: vi.fn().mockResolvedValue({ app_id: 'app', items: [], total: 0, page: 1, page_size: 20 }) }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))

describe('image intent receipts', () => {
  it.each(['zh', 'en'])('renders review, execution and scoped references in %s', async locale => {
    api.jobs.mockResolvedValue({ items: [{
      id: 'job-id', group_id: 'group', member_id: 'member', kind: 'chat', status: 'completed',
      error: '', input_tokens: 22, output_tokens: 14, created_at: '2026-10-23T08:00:00Z',
      image_decision: { tool: 'generate_image', review: 'text', basis: 'none', reason: 'not_image_request',
        execution: 'not_started', message_ref: 'message-hash', plan_ref: 'plan-hash' }
    }, { id: 'legacy', group_id: 'group', member_id: 'member', kind: 'chat', status: 'completed', error: '',
      input_tokens: 11, output_tokens: 7, created_at: '2026-10-23T07:00:00Z', image_decision: {} }], total: 2, page: 1, page_size: 20 })
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const w = mount(QQJobRecords, { global: { plugins: [i18n] } })
    await flushPromises()
    expect(w.findAll('article')).toHaveLength(2)
    expect(w.text()).toContain(i18n.global.t('qqbot.workspace.imageDecision'))
    expect(w.text()).toContain('generate_image → text → not_started')
    expect(w.text()).toContain('not_image_request')
    expect(w.text()).toContain('message-hash')
    expect(w.text()).toContain('plan-hash')
    expect(w.findAll('article')[1].text()).not.toContain(i18n.global.t('qqbot.workspace.imageDecision'))
    w.unmount()
  })
  it.each(['zh', 'en'])('filters spam report reviews and displays decisions, tokens and execution caveats in %s', async locale => {
    api.jobs.mockResolvedValue({ items: [{
      id: 'report-job', group_id: 'group', member_id: 'reporter', kind: 'spam_report', status: 'completed',
      trigger_reason: 'quoted_message_report', decision_reason: 'spam_phishing', error: '',
      input_tokens: 220, output_tokens: 40, created_at: '2026-10-24T01:00:00Z'
    }], total: 1, page: 1, page_size: 20 })
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const w = mount(QQJobRecords, { global: { plugins: [i18n] } })
    await flushPromises()
    const filter = w.get('[data-testid="qq-job-kind"]')
    expect(filter.get('option[value="spam_report"]').text()).toBe(i18n.global.t('qqbot.workspace.jobKinds.spam_report'))
    await filter.setValue('spam_report')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.jobs).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'spam_report', page: 1 }))
    const record = w.get('article')
    expect(record.text()).toContain(i18n.global.t('qqbot.workspace.jobKinds.spam_report'))
    expect(record.text()).toContain('quoted_message_report → spam_phishing')
    expect(record.text()).toContain('Tokens: 220 / 40')
    expect(record.text()).toContain(i18n.global.t('qqbot.workspace.reportReviewHelp'))
    w.unmount()
  })
})
