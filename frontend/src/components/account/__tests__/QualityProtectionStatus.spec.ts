import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import QualityProtectionStatus from '../QualityProtectionStatus.vue'
import { getAccountProtection, type QualityProtectionHold } from '@/api/admin/tests'
import type { Account } from '@/types'

vi.mock('@/api/admin/tests', () => ({ getAccountProtection: vi.fn() }))
vi.mock('vue-i18n', async () => {
  const dictionary = { admin: (await import('@/i18n/locales/zh/admin/accounts')).default, common: { close: '关闭', loading: '加载中', retry: '重试' } }
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
    expect(wrapper.element.contains(dialog())).toBe(false)
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
