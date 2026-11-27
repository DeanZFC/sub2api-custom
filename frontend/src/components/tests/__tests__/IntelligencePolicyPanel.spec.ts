import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import type { AdminGroup, AccountListItem, TestPlan } from '@/types'
import IntelligencePolicyPanel from '../IntelligencePolicyPanel.vue'
import IntelligencePolicyResults from '../IntelligencePolicyResults.vue'

const api = vi.hoisted(() => ({
  candidates: vi.fn(),
  fingerprints: vi.fn(),
  scope: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  overview: vi.fn(),
  art: vi.fn(),
  review: vi.fn(),
  budget: vi.fn(),
  retry: vi.fn(),
  release: vi.fn(),
}))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: { getModelAllowlistCandidates: api.candidates },
    tests: { createPlan: api.create, updatePlan: api.update, retryResult: api.retry },
  },
}))
vi.mock('@/api/admin/intelligence', () => ({
  fingerprintModels: api.fingerprints,
  intelligenceScope: api.scope,
  policyOverview: api.overview,
  policyArtwork: api.art,
  releaseIntelligenceHold: api.release,
  reviewPolicyArtwork: api.review,
}))
vi.mock('@/api/admin/qualityOperations', () => ({ qualityOperationsAPI: { get: api.budget } }))
const i18n = () => createI18n({ legacy: false, locale: 'zh', missingWarn: false, fallbackWarn: false, messages: { zh } })
const groups = [
  { id: 1, name: 'Pro', platform: 'openai', status: 'active', model_allowlist: { enabled: true, models: ['gpt-*'] } },
  { id: 2, name: 'Plus', platform: 'openai', status: 'active' },
  { id: 3, name: 'Other', platform: 'anthropic', status: 'active' },
] as AdminGroup[]
const accounts = [
  { id: 1, name: 'A', group_ids: [1, 3] },
  { id: 2, name: 'B', group_ids: [1, 2] },
  { id: 3, name: 'C', group_ids: [2] },
] as AccountListItem[]
const plan = {
  id: 9,
  name: 'OpenAI',
  enabled: true,
  group_ids: [1],
  model_ids: ['gpt-a'],
  protection: {
    enabled: false,
    rules: [],
    intelligence: { auto_isolate: true, interval_minutes: 60, cooldown_minutes: 30, recovery_passes: 2 },
  },
} as TestPlan
const defaults = {
  results: [
    {
      result_id: 5,
      account_id: 1,
      account_name: 'A',
      model: 'gpt-a',
      verdict: 'pass',
      artwork: 'default',
      reviewed: '',
      evidence: { auto_verdict: 'pass', checks: [{ key: 'http_429', status: 'pass', detail: '0/3' }] },
      finished_at: '2026-11-26T11:00:00Z',
    },
  ],
  states: [
    {
      account_id: 2,
      account_name: 'B',
      model: 'gpt-a',
      blocked: true,
      passes: 1,
      verdict: 'confirmed',
      next_check: '2026-11-26T11:30:00Z',
    },
  ],
}
beforeEach(() => {
  vi.resetAllMocks()
  api.candidates.mockImplementation((id: number) =>
    Promise.resolve(id === 1 ? ['gpt-a', 'gpt-b', 'gpt-pro', 'outside'] : ['gpt-a', 'gpt-b', 'outside']),
  )
  api.fingerprints.mockResolvedValue(['gpt-a'])
  api.scope.mockResolvedValue({ targets: 4, account_ids: [1, 2, 3], group_counts: { 1: 2, 2: 2 } })
  api.create.mockResolvedValue(plan)
  api.budget.mockResolvedValue({ config: { budget_enabled: false } })
  api.overview.mockResolvedValue(defaults)
  api.art.mockResolvedValue('<svg/>')
})
describe('intelligence monitoring policy', () => {
  it('previews the server-supported account-model pairs across multiple groups and saves the selected policy', async () => {
    const w = mount(IntelligencePolicyPanel, {
      props: { groups, accounts, plans: [], catalogReady: true },
      global: { plugins: [i18n()], stubs: { IntelligencePolicyResults: true } },
    })
    await flushPromises()
    for (const id of [1, 2]) await w.get('input[value="' + id + '"]').setValue(true)
    await flushPromises()
    expect(w.find('input[value="gpt-pro"]').exists()).toBe(false)
    expect(w.find('input[value="outside"]').exists()).toBe(false)
    for (const model of ['gpt-a', 'gpt-b']) await w.get('input[value="' + model + '"]').setValue(true)
    await flushPromises()
    expect(api.scope).toHaveBeenLastCalledWith([1, 2], ['gpt-a', 'gpt-b'], [], expect.any(AbortSignal))
    expect(w.text()).toContain('3 个去重账号，2 个所选模型，共 4 个支持')
    expect(w.text()).toContain('1 个跨所选分组共享的账号')
    expect(w.text()).toContain('gpt-b 在 high 档暂无指纹参考')
    api.fingerprints.mockResolvedValue(['gpt-a', 'gpt-b'])
    await w.get('[data-intelligence-effort]').setValue('medium')
    await flushPromises()
    expect(api.fingerprints).toHaveBeenLastCalledWith('medium', expect.any(AbortSignal))
    expect(w.text()).not.toContain('暂无指纹参考')
    await w.get('[role="switch"]').setValue(true)
    expect(w.text()).toContain('也会影响 Other')
    expect(w.get<HTMLInputElement>('[data-pause-on-any]').element.checked).toBe(false)
    await w.get('[data-pause-on-any]').setValue(true)
    expect(w.text()).toContain('原触发模型全部恢复后')
    const save = w.findAll('button').find((b) => b.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeUndefined()
    const interval = w.get('input[type="number"]')
    await interval.setValue('0')
    expect(save.attributes('disabled')).toBeDefined()
    await interval.setValue('120')
    await save.trigger('click')
    await flushPromises()
    expect(api.create).toHaveBeenCalledWith(
      expect.objectContaining({
        group_ids: [1, 2],
        model_ids: ['gpt-a', 'gpt-b'],
        reasoning_effort: 'medium',
        protection: {
          enabled: false,
          rules: [],
          intelligence: {
            auto_isolate: true,
            pause_on_any: true,
            model_default_effort: false,
            interval_minutes: 120,
            cooldown_minutes: 30,
            recovery_passes: 2,
          },
        },
      }),
    )
    expect(w.emitted('changed')).toHaveLength(1)
    w.unmount()
  })
  it('restores and submits an explicitly selected model-default effort', async () => {
    const existing = {
      ...plan,
      reasoning_effort: '',
      protection: { ...plan.protection!, intelligence: { ...plan.protection!.intelligence!, model_default_effort: true } },
    }
    const w = mount(IntelligencePolicyPanel, {
      props: { groups, accounts, plans: [existing], catalogReady: true },
      global: { plugins: [i18n()], stubs: { IntelligencePolicyResults: true } },
    })
    await flushPromises()
    expect(w.get<HTMLSelectElement>('[data-intelligence-effort]').element.value).toBe('default')
    expect(api.fingerprints).toHaveBeenLastCalledWith('default', expect.any(AbortSignal))
    api.update.mockResolvedValue(existing)
    await w
      .findAll('button')
      .find((b) => b.text() === 'common.save')!
      .trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith(
      9,
      expect.objectContaining({
        reasoning_effort: 'default',
        protection: expect.objectContaining({ intelligence: expect.objectContaining({ model_default_effort: true }) }),
      }),
    )
    w.unmount()
  })
  it('requires confirmation to release only the selected account-model hold in this plan', async () => {
    const w = mount(IntelligencePolicyResults, {
      props: { plan, refreshKey: 0 },
      global: {
        plugins: [i18n()],
        stubs: {
          BaseDialog: { props: ['show'], template: '<section v-if="show" role="dialog"><slot/><slot name="footer"/></section>' },
          TestResultOutput: true,
        },
      },
    })
    await flushPromises()
    const action = w.get('li button')
    await action.trigger('click')
    expect(w.get('[role=dialog]').text()).toContain('B 的 gpt-a')
    expect(api.release).not.toHaveBeenCalled()
    await w.get('[role=dialog] .btn-secondary').trigger('click')
    expect(api.release).not.toHaveBeenCalled()
    await action.trigger('click')
    api.overview.mockResolvedValue({ ...defaults, states: [] })
    await w.get('[role=dialog] .btn-primary').trigger('click')
    await flushPromises()
    expect(api.release).toHaveBeenCalledWith(9, 2, 'gpt-a')
    expect(w.find('li button').exists()).toBe(false)
    w.unmount()
  })
  it('retries only the selected missing artwork and reloads the authoritative result', async () => {
    const row = { ...defaults.results[0], artwork: 'missing', verdict: 'unknown', retryable_artwork: true }
    api.overview.mockResolvedValueOnce({ results: [row, { ...row, result_id: 6, retryable_artwork: false }], states: [] })
    const w = mount(IntelligencePolicyResults, {
      props: { plan, refreshKey: 0 },
      global: { plugins: [i18n()], stubs: { BaseDialog: true } },
    })
    await flushPromises()
    expect(w.findAll('tbody button')).toHaveLength(1)
    await w.get('tbody button').trigger('click')
    await flushPromises()
    expect(api.retry).toHaveBeenCalledWith(5)
    expect(api.art).not.toHaveBeenCalled()
    expect(w.get('tbody').text()).toContain('默认通过')
    expect(w.text()).toContain('沿用本轮的四项检测结果')
    w.unmount()
  })
  it('loads artwork only when selected and refreshes the final verdict from the server after a veto', async () => {
    const w = mount(IntelligencePolicyResults, {
      props: { plan, refreshKey: 0 },
      global: {
        plugins: [i18n()],
        stubs: {
          BaseDialog: { props: ['show'], template: '<section v-if="show" role="dialog"><slot/><slot name="footer"/></section>' },
          TestResultOutput: true,
        },
      },
    })
    await flushPromises()
    expect(w.text()).toContain('恢复进度 1/2')
    expect(w.text()).toContain('下次复测')
    expect(api.art).not.toHaveBeenCalled()
    await w.get('tbody button').trigger('click')
    await flushPromises()
    expect(api.art).toHaveBeenCalledWith(5, expect.any(AbortSignal))
    await w.get('textarea').setValue(' 图中没有自行车 ')
    api.overview.mockResolvedValue({
      ...defaults,
      results: [{ ...defaults.results[0], verdict: 'confirmed', reviewed: 'fail', review_reason: '图中没有自行车' }],
    })
    await w
      .findAll('[role=dialog] button')
      .find((b) => b.text() === '否决作品')!
      .trigger('click')
    await flushPromises()
    expect(api.review).toHaveBeenCalledWith(5, 'fail', '图中没有自行车')
    expect(w.find('[role=dialog]').exists()).toBe(false)
    expect(w.get('tbody details summary').text()).toContain('确认降智')
    await w.get('tbody details summary').trigger('click')
    expect(w.get('tbody dl').text()).toContain('连续三轮 HTTP 429 · 通过')
    expect(w.get('tbody details').text()).toContain('上次审批说明 · 图中没有自行车')
    expect(w.get('tbody').text()).toContain('人工否决')
    expect(w.get('tbody').text()).toContain('确认降智')
    w.unmount()
  })
})
