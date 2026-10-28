import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import QualityProtectionStatus from '../QualityProtectionStatus.vue'
import { getAccountProtection, type QualityProtectionHold } from '@/api/admin/tests'
import type { Account } from '@/types'

vi.mock('@/api/admin/tests', () => ({ getAccountProtection: vi.fn() }))
vi.mock('vue-i18n', async () => {
  const dictionary = { admin: { ...(await import('@/i18n/locales/zh/admin/accounts')).default, tests: { experience: (await import('@/i18n/locales/zh/admin/qualityExperience')).default } }, common: { close: '关闭', loading: '加载中', retry: '重试' } }
  return { ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string, params: Record<string, unknown> = {}) => {
    const value = key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown>)?.[part], dictionary)
    return typeof value === 'string' ? value.replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? '')) : key
  } }) }
})
const details: QualityProtectionHold[] = [{
  plan_id: 3, plan_name: '策略 A', mode: 'combined', reason: '命中组合规则：不通过', updated_at: '2026-10-13T08:00:00Z', prior_round: false,
  rules: [{ id: 'failure', name: '不通过', current_match: 'pass', condition: { operator: 'any', conditions: [
    { operator: 'test', test_definition_id: 2, verdict: 'fail' },
    { operator: 'all', conditions: [{ operator: 'test', test_definition_id: 3, verdict: 'fail' }] }
  ] } }],
  tests: [
    { test_definition_id: 2, test_name: '缓存检测', result_id: 88, started_at: '2026-10-13T08:00:00Z', current: true, verdict: 'fail', automated_verdict: 'fail', admin_verdict: '', reason: '缓存率低于阈值', automatic_reason: '', thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80, observed: 60 }], pass_count: 0, fail_count: 0 },
    { test_definition_id: 3, test_name: '人工审核', current: true, verdict: 'pass', automated_verdict: 'pass', admin_verdict: 'pass', reason: '', automatic_reason: '', thresholds: [], pass_count: 2, fail_count: 0, vote: { enabled: true, public_enabled: true, reject_above: 3, pass_at_least: 2 } },
  ],
}]
const account = { id: 62, status: 'quality_paused', schedulable: true, extra: { quality_protection_reason: '命中组合规则：不通过' } } as Account
let wrapper: VueWrapper
const dialog = () => document.querySelector('[role="dialog"]')
const trigger = () => wrapper.get('[data-testid="quality-paused-status"]')

beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(getAccountProtection).mockReset().mockResolvedValue(details)
  wrapper = mount(QualityProtectionStatus, { props: { account }, attachTo: document.body })
})
afterEach(() => {
  wrapper.unmount()
  document.body.innerHTML = ''
  vi.useRealTimers()
})

describe('quality pause details', () => {
  it('distinguishes models when one plan has several active holds', async () => {
    vi.mocked(getAccountProtection).mockResolvedValueOnce([
      { ...details[0], model_id: 'model-a' },
      { ...details[0], model_id: 'model-b' },
    ])
    await trigger().trigger('mouseenter'); await flushPromises()
    expect(Array.from(dialog()?.querySelectorAll('[data-hold-model]') || []).map(node => node.textContent)).toEqual(['model-a', 'model-b'])
  })

  it('loads only on hover and displays nested rules, actual values and manual decisions', async () => {
    let resolve!: (value: QualityProtectionHold[]) => void
    vi.mocked(getAccountProtection).mockReturnValueOnce(new Promise(done => { resolve = done }))
    expect(getAccountProtection).not.toHaveBeenCalled()
    await trigger().trigger('mouseenter')
    expect(dialog()?.textContent).toContain('加载中')
    resolve(details)
    await flushPromises()
    expect(getAccountProtection).toHaveBeenCalledWith(62)
    const text = dialog()?.textContent
    for (const value of ['策略 A', '命中规则：不通过', '任一满足', '全部满足', '缓存检测 = 不通过', '60%', '80%', '管理员判定：通过', '通过 2 / 不通过 0']) expect(text).toContain(value)
    expect(dialog()?.querySelector('[data-trigger-missing]')?.textContent).toContain('当前观察不能还原当时的判断依据')
    expect(wrapper.element.contains(dialog())).toBe(false)
  })

  it('keeps the historical trigger evidence and policy version separate from current passing observations', async () => {
    const value = structuredClone(details)
    value[0].tests[0] = { ...value[0].tests[0], verdict: 'pass', automated_verdict: 'pass', reason: '当前缓存已恢复', thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 75, observed: 95 }] }
    value[0].trigger_snapshot = {
      id: 100, occurred_at: '2026-10-12T06:00:00Z', plan_id: 3, plan_name: '触发时策略', test_definition_id: 2,
      result_id: 77, run_id: 'trigger-run-42', model_id: 'historical-model', policy_version: 'version-abc123', policy_updated_at: '2026-10-11T06:00:00Z',
      source: 'scheduled', reason: '当时缓存指标未达标',
      before: { status: 'active', schedulable: true, group_ids: [8] }, after: { status: 'quality_paused', schedulable: true, group_ids: [8] },
      rules: [{ id: 'old-rule', name: '历史缓存规则', current_match: 'fail', condition: { operator: 'test', test_definition_id: 2, verdict: 'fail' } }],
      tests: [{ ...details[0].tests[0], result_id: 77, test_name: '历史缓存检测', started_at: '2026-10-12T06:00:00Z', window_start: '2026-10-12T05:00:00Z', window_end: '2026-10-12T06:00:00Z', min_samples: 20, reason: '历史实测缓存率低', thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80, observed: 12, samples: 25 }] }],
    }
    vi.mocked(getAccountProtection).mockResolvedValueOnce(value)
    await trigger().trigger('mouseenter'); await flushPromises()
    const snapshot = dialog()?.querySelector('[data-trigger-snapshot]')?.textContent
    for (const text of ['触发保护时的依据', 'historical-model', '当时缓存指标未达标', 'version-abc123', 'trigger-run-42', '历史缓存规则', '历史缓存检测', '#77', '12%', '80%', '有效样本 25 / 最低要求 20']) expect(snapshot).toContain(text)
    expect(snapshot).not.toContain('95%')
    expect(snapshot).not.toContain('当前缓存已恢复')
    expect(dialog()?.textContent).toContain('当前观察')
    expect(dialog()?.textContent).toContain('当前缓存已恢复')
    expect(dialog()?.textContent).toContain('95%')
    expect(dialog()?.querySelector('[data-trigger-missing]')).toBeNull()
  })

  it('keeps hover content readable, supports pinning, and closes on Escape', async () => {
    await trigger().trigger('focus')
    await flushPromises()
    await trigger().trigger('mouseleave')
    dialog()?.dispatchEvent(new MouseEvent('mouseenter'))
    await vi.advanceTimersByTimeAsync(200)
    expect(dialog()).not.toBeNull()
    await trigger().trigger('click')
    dialog()?.dispatchEvent(new MouseEvent('mouseleave'))
    await vi.advanceTimersByTimeAsync(200)
    expect(dialog()).not.toBeNull()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(dialog()).toBeNull()
    await trigger().trigger('mouseenter')
    await flushPromises()
    expect(getAccountProtection).toHaveBeenCalledTimes(2)
    document.body.click()
    await nextTick()
    expect(dialog()).toBeNull()
  })

  it('ignores a late response when the account changes', async () => {
    let resolve!: (value: QualityProtectionHold[]) => void
    vi.mocked(getAccountProtection).mockReturnValueOnce(new Promise(done => { resolve = done }))
    await trigger().trigger('mouseenter')
    await wrapper.setProps({ account: { ...account, id: 63 } })
    resolve(details)
    await flushPromises()
    expect(dialog()).toBeNull()
    vi.mocked(getAccountProtection).mockResolvedValueOnce([])
    await trigger().trigger('mouseenter')
    await flushPromises()
    expect(dialog()?.textContent).not.toContain('策略 A')
    expect(dialog()?.textContent).toContain('暂无生效中的暂停明细')
  })

  it('retries failures and distinguishes retained pauses from current pending evidence', async () => {
    vi.mocked(getAccountProtection).mockRejectedValueOnce(new Error('offline'))
    await trigger().trigger('mouseenter')
    await flushPromises()
    expect(dialog()?.textContent).toContain('详情加载失败')
    const stale = structuredClone(details)
    stale[0].prior_round = true
    stale[0].tests[0] = { ...stale[0].tests[0], current: false, verdict: 'pending', reason: '', thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80, observed: null }] }
    stale[0].rules[0].current_match = 'pending'
    stale[0].plan_name = '<script>alert(1)</script>'
    vi.mocked(getAccountProtection).mockResolvedValueOnce(stale)
    ;(dialog()?.querySelector('[role="alert"] button') as HTMLButtonElement).click()
    await flushPromises()
    expect(dialog()?.textContent).toContain('暂停延续自此前轮次')
    expect(dialog()?.textContent).toContain('等待本轮有效结果')
    expect(dialog()?.textContent).toContain('实际：—')
    expect(dialog()?.querySelector('script')).toBeNull()
  })
})
