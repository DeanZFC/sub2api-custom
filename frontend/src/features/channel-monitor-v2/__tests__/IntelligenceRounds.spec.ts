import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import IntelligenceRounds from '../IntelligenceRounds.vue'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import * as api from '@/api/intelligence'
vi.mock('@/api/intelligence', () => ({
  intelligenceRounds: vi.fn(),
  intelligenceRound: vi.fn(),
  intelligenceSummary: vi.fn(),
  intelligenceArtwork: vi.fn(),
}))

const counts = { pass: 1, suspect: 0, degraded: 0, unknown: 0 }
const round: api.IntelligenceRound = {
  id: '9',
  started_at: '2026-10-25T12:00:00Z',
  finished_at: '2026-10-25T12:01:00Z',
  accounts: 1,
  targets: 1,
  passed_targets: 1,
  counts,
  verdict: 'pass',
}
const result: api.IntelligenceModel = {
  model: 'm1',
  effort: 'high',
  verdict: 'pass',
  finished_at: round.finished_at,
  checks: [{ key: 'candy', status: 'pass', detail: '21' }],
  artwork_status: 'default',
  artwork_id: 20,
}
const account = { alias: 'A', targets: 1, passed_targets: 1, verdict: 'pass' as const, models: [result] }
const detail: api.RoundDetail = { round, accounts: [account], page: 1, page_size: 20, total: 1 }
const group: api.IntelligenceGroup = { id: 7, name: 'Pro', models: ['m1', 'm2'] }
function render() {
  return mount(IntelligenceRounds, {
    props: { group },
    attachTo: document.body,
    global: {
      stubs: { TestResultOutput: true },
      plugins: [createI18n({ legacy: false, locale: 'zh', missingWarn: false, fallbackWarn: false, messages: { zh } })],
    },
  })
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.intelligenceRound).mockResolvedValue(detail)
  vi.mocked(api.intelligenceRounds).mockResolvedValue([round])
  vi.mocked(api.intelligenceArtwork).mockResolvedValue('<svg/>')
  vi.mocked(api.intelligenceSummary).mockResolvedValue({
    rounds: 1,
    accounts: 1,
    participations: 1,
    counts,
    targets: 1,
    passed_targets: 1,
    items: [
      {
        alias: 'A',
        counts,
        participations: 1,
        models: [{ model: 'm1', effort: 'high', counts, last_tested_at: round.finished_at }],
      },
    ],
    total: 1,
    page: 1,
    page_size: 20,
  })
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true
  }
  HTMLDialogElement.prototype.close = function () {
    this.open = false
    this.dispatchEvent(new Event('close'))
  }
})
afterEach(() => {
  document.body.innerHTML = ''
})
describe('intelligence history', () => {
  it('loads the selected artwork on demand and restores the same expanded detail and focus', async () => {
    const w = render()
    await flushPromises()
    expect(api.intelligenceArtwork).not.toHaveBeenCalled()
    expect(w.find('dialog').exists()).toBe(false)
    await w.get('.round-cell').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('糖果题')
    const row = w.get('details').element as HTMLDetailsElement
    row.open = true
    const button = w.get('button[aria-haspopup=dialog].underline')
    await button.trigger('click')
    await flushPromises()
    expect(api.intelligenceArtwork).toHaveBeenCalledWith(7, 20, expect.any(AbortSignal))
    expect(w.get('dialog').text()).toContain('默认通过')
    await w.get('dialog button').trigger('click')
    await flushPromises()
    expect(w.find('dialog').exists()).toBe(false)
    expect(w.get('details').element).toBe(row)
    expect(row.open).toBe(true)
    expect(document.activeElement).toBe(button.element)
    w.unmount()
  })
  it('recomputes the last twenty completed rounds for the selected model, ignoring stale responses', async () => {
    const w = render()
    await flushPromises()
    let resolve!: (rows: api.IntelligenceRound[]) => void
    vi.mocked(api.intelligenceRounds)
      .mockImplementationOnce(
        () =>
          new Promise((done) => {
            resolve = done
          }),
      )
      .mockResolvedValueOnce([])
    await w.setProps({ model: 'm1' })
    await w.setProps({ model: 'm2' })
    await flushPromises()
    resolve([round])
    await flushPromises()
    expect(w.find('.round-cell').exists()).toBe(false)
    expect(w.text()).toContain('暂无已完成')
    expect(api.intelligenceRounds).toHaveBeenLastCalledWith(7, 'm2', expect.any(AbortSignal))
    w.unmount()
  })
  it('keeps account-round counts and per-model summaries distinct from the unique account count', async () => {
    const w = render()
    await flushPromises()
    await w.get('button[aria-haspopup=dialog]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('1 个去重账号')
    expect(w.text()).toContain('1 个账号轮次')
    expect(w.get('summary').text()).toContain('1/1 轮全项通过')
    expect(api.intelligenceArtwork).not.toHaveBeenCalled()
    w.unmount()
  })
})
