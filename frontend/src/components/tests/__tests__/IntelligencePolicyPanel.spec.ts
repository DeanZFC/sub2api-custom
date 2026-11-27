import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import type { AdminGroup, AccountListItem, TestPlan } from '@/types'
import IntelligencePolicyPanel from '../IntelligencePolicyPanel.vue'
import IntelligencePolicyResults from '../IntelligencePolicyResults.vue'

const api = vi.hoisted(() => ({
  candidates: vi.fn(),
  scope: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  run: vi.fn(),
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
    tests: { createPlan: api.create, updatePlan: api.update, runPlan: api.run, retryResult: api.retry },
  },
}))
vi.mock('@/api/admin/intelligence', () => ({
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
  { id: 4, name: 'Image Only', group_ids: [1, 2] },
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
function mountPanel(plans: TestPlan[] = [], initialPlanId?: number, catalogReady = true) {
  return mount(IntelligencePolicyPanel, {
    attachTo: document.body,
    props: { groups, accounts, plans, initialPlanId, catalogReady },
    global: {
      plugins: [i18n()],
      stubs: {
        IntelligencePolicyResults: true,
        BaseDialog: {
          props: ['show', 'title'],
          template: `<div v-if="show" role="dialog"><h3>{{ title }}</h3><button data-close-dialog @click="$emit('close')">Close</button><slot/><slot name="footer"/></div>`,
        },
      },
    },
  })
}
beforeEach(() => {
  vi.resetAllMocks()
  api.candidates.mockImplementation((id: number) =>
    Promise.resolve(id === 1 ? ['gpt-a', 'gpt-b', 'gpt-pro', 'outside'] : ['gpt-a', 'gpt-b', 'outside']),
  )
  api.scope.mockResolvedValue({ targets: 4, account_ids: [1, 2, 3], group_counts: { 1: 2, 2: 2 } })
  api.create.mockResolvedValue(plan)
  api.budget.mockResolvedValue({ config: { budget_enabled: false } })
  api.overview.mockResolvedValue(defaults)
  api.art.mockResolvedValue('<svg/>')
})
describe('intelligence monitoring policy', () => {
  it('previews the server-supported account-model pairs across multiple groups and saves the selected policy', async () => {
    const w = mountPanel()
    await flushPromises()
    expect(w.findAll('[data-policy-id]')).toHaveLength(0)
    expect(w.find('[data-save-policy]').exists()).toBe(false)
    expect(api.scope).not.toHaveBeenCalled()
    await w.get('[data-new-policy]').trigger('click')
    for (const id of [1, 2]) await w.get('input[value="' + id + '"]').setValue(true)
    await flushPromises()
    expect(w.find('input[value="gpt-pro"]').exists()).toBe(false)
    expect(w.find('input[value="outside"]').exists()).toBe(false)
    expect(w.find('input[type=search], [data-plan-exclusions]').exists()).toBe(false)
    for (const model of ['gpt-a', 'gpt-b']) await w.get('input[value="' + model + '"]').setValue(true)
    await flushPromises()
    expect(api.scope).toHaveBeenLastCalledWith([1, 2], ['gpt-a', 'gpt-b'], [], expect.any(AbortSignal))
    expect(w.text()).toContain('3 个去重账号，2 个所选模型，共 4 个支持')
    expect(w.text()).toContain('1 个跨所选分组共享的账号')
    expect(w.text()).toContain('预计每天约 192 次请求')
    expect(w.get('aside ul').findAll('li')).toHaveLength(3)
    expect(w.get('aside ul').text()).not.toContain('Image Only')
    await w.get('[data-intelligence-effort]').setValue('medium')
    await flushPromises()
    expect(w.text()).toContain('答错一次或连续三轮返回 HTTP 429')
    expect(w.text()).not.toContain('行为指纹')
    expect(w.text()).not.toContain('Codex 状态')
    await w.get('[role="switch"]').trigger('click')
    expect(w.text()).toContain('也会影响 Other')
    expect(w.get<HTMLInputElement>('[data-pause-on-any]').element.checked).toBe(false)
    await w.get('[data-pause-on-any]').setValue(true)
    expect(w.text()).toContain('原触发模型全部恢复后')
    const save = w.get('[data-save-policy]')
    expect(save.attributes('disabled')).toBeUndefined()
    const interval = w.get('input[type="number"]')
    await interval.setValue('0')
    expect(save.attributes('disabled')).toBeDefined()
    await interval.setValue('120')
    expect(w.text()).toContain('预计每天约 96 次请求')
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
    expect(api.create.mock.calls[0][0]).not.toHaveProperty('excluded_account_ids')
    expect(w.emitted('changed')).toHaveLength(1)
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    w.unmount()
  })
  it('keeps a failed edit in the dialog and updates the list only after a successful retry', async () => {
    const w = mountPanel([plan])
    await w.get('[data-edit-policy]').trigger('click')
    await flushPromises()
    await w.get('[data-policy-name]').setValue('Nightly')
    api.update.mockRejectedValueOnce(new Error('Save failed'))
    await w.get('[data-save-policy]').trigger('click')
    await flushPromises()
    expect(w.get('[role="dialog"] [role="alert"]').text()).toBe('Save failed')
    expect(w.get<HTMLInputElement>('[data-policy-name]').element.value).toBe('Nightly')
    expect(w.get('[data-policy-id="9"]').text()).toContain('OpenAI')

    api.update.mockResolvedValueOnce({ ...plan, name: 'Nightly' })
    await w.get('[data-save-policy]').trigger('click')
    await flushPromises()
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    expect(w.get('[data-policy-id="9"]').text()).toContain('Nightly')
    w.unmount()
  })
  it('restores and submits an explicitly selected model-default effort', async () => {
    const existing = {
      ...plan,
      reasoning_effort: '',
      protection: { ...plan.protection!, intelligence: { ...plan.protection!.intelligence!, model_default_effort: true } },
    }
    const w = mountPanel([existing])
    await w.get('[data-policy-id="9"] [data-edit-policy]').trigger('click')
    expect(w.get('[role="dialog"] h3').text()).toContain('OpenAI')
    await flushPromises()
    expect(w.get<HTMLSelectElement>('[data-intelligence-effort]').element.value).toBe('default')
    api.update.mockResolvedValue(existing)
    await w.get('[data-save-policy]').trigger('click')
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
  it('adds a second automatic-scope policy independently of an existing policy and discards cancelled drafts', async () => {
    const existing = { ...plan, excluded_account_ids: [1] }
    const w = mountPanel([existing])
    await w.get('[data-new-policy]').trigger('click')
    expect(w.get('[role="dialog"] h3').text()).toBe('新增策略')
    expect(w.get('[data-policy-id="9"]').isVisible()).toBe(true)
    await w.get('input[value="1"]').setValue(true)
    await flushPromises()
    let finishCancelled!: () => void
    api.scope.mockImplementationOnce(() => new Promise((resolve) => {
      finishCancelled = () => resolve({ targets: 1, account_ids: [1], group_counts: { 1: 1 } })
    }))
    await w.get('input[value="gpt-a"]').setValue(true)
    await w.get('[data-cancel-policy]').trigger('click')
    expect(api.create).not.toHaveBeenCalled()
    expect(api.update).not.toHaveBeenCalled()

    await w.get('[data-new-policy]').trigger('click')
    expect(w.get<HTMLInputElement>('input[value="1"]').element.checked).toBe(false)
    await w.get('[data-policy-name]').setValue('Plus Nightly')
    await w.get('input[value="2"]').setValue(true)
    await flushPromises()
    api.scope.mockResolvedValueOnce({ targets: 2, account_ids: [2, 3], group_counts: { 2: 2 } })
    await w.get('input[value="gpt-a"]').setValue(true)
    await flushPromises()
    finishCancelled()
    await flushPromises()
    expect(api.scope).toHaveBeenLastCalledWith([2], ['gpt-a'], [], expect.any(AbortSignal))
    expect(w.text()).toContain('2 个去重账号，1 个所选模型，共 2 个支持')
    api.create.mockImplementationOnce(async (payload) => ({ ...payload, id: 15 }))
    await w.get('[data-save-policy]').trigger('click')
    await flushPromises()
    expect(api.create).toHaveBeenCalledWith(expect.objectContaining({ name: 'Plus Nightly', group_ids: [2], model_ids: ['gpt-a'] }))
    expect(api.create.mock.calls[0][0]).not.toHaveProperty('excluded_account_ids')
    expect(api.update).not.toHaveBeenCalled()
    expect(w.findAll('[data-policy-id]')).toHaveLength(2)
    expect(w.get('[data-policy-id="15"]').text()).toContain('Plus Nightly')
    w.unmount()
  })
  it('honors saved legacy exceptions and only clears them when the administrator saves that change', async () => {
    const existing = { ...plan, group_ids: [1, 2], excluded_account_ids: [2] }
    const w = mountPanel([existing])
    api.scope.mockImplementation(async (_groups, _models, excluded: number[]) => excluded.includes(2)
      ? { targets: 1, account_ids: [1], group_counts: { 1: 1, 2: 0 } }
      : { targets: 2, account_ids: [1, 2], group_counts: { 1: 2, 2: 1 } })
    api.update.mockImplementation(async (id, payload) => ({ ...existing, ...payload, id }))
    await w.get('[data-policy-id="9"] [data-edit-policy]').trigger('click')
    await flushPromises()
    expect(w.find('[data-plan-exclusions]').exists()).toBe(false)
    expect(api.scope).toHaveBeenLastCalledWith([1, 2], ['gpt-a'], [2], expect.any(AbortSignal))
    expect(w.text()).toContain('1 个去重账号')
    await w.get('[data-save-policy]').trigger('click')
    await flushPromises()
    expect(api.update.mock.calls[0][1]).not.toHaveProperty('excluded_account_ids')

    await w.get('[data-policy-id="9"] [data-edit-policy]').trigger('click')
    await w.get('[data-clear-legacy-exclusions]').setValue(true)
    await flushPromises()
    expect(api.scope).toHaveBeenLastCalledWith([1, 2], ['gpt-a'], [], expect.any(AbortSignal))
    expect(w.text()).toContain('2 个去重账号')
    expect(w.text()).toContain('1 个跨所选分组共享的账号')
    await w.get('[data-cancel-policy]').trigger('click')
    expect(api.update).toHaveBeenCalledTimes(1)

    await w.get('[data-policy-id="9"] [data-edit-policy]').trigger('click')
    await flushPromises()
    expect(api.scope).toHaveBeenLastCalledWith([1, 2], ['gpt-a'], [2], expect.any(AbortSignal))
    await w.get('[data-clear-legacy-exclusions]').setValue(true)
    await flushPromises()
    await w.get('[data-save-policy]').trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenLastCalledWith(9, expect.objectContaining({ excluded_account_ids: [] }))
    await w.get('[data-policy-id="9"] [data-edit-policy]').trigger('click')
    expect(w.find('[data-clear-legacy-exclusions]').exists()).toBe(false)
    w.unmount()
  })
  it('dispatches run, enable, disable and results for the selected row', async () => {
    const second = { ...plan, id: 15, name: 'Second', enabled: false }
    const legacy = { ...plan, id: 20, name: 'Legacy', protection: { enabled: false, rules: [] } }
    const w = mountPanel([plan, second, legacy])
    expect(w.findAll('[data-policy-id]')).toHaveLength(2)
    expect(w.find('[data-policy-id="20"]').exists()).toBe(false)
    expect(w.findComponent(IntelligencePolicyResults).exists()).toBe(false)
    const row = w.get('[data-policy-id="15"]')
    api.update.mockImplementation(async (id, payload) => ({ ...second, ...payload, id }))
    await row.get('[data-toggle-policy]').trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenLastCalledWith(15, { enabled: true })
    await row.get('[data-toggle-policy]').trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenLastCalledWith(15, { enabled: false })
    await w.get('[data-policy-id="15"] [data-policy-results]').trigger('click')
    expect(w.getComponent(IntelligencePolicyResults).props('plan').id).toBe(15)
    expect(w.get('[role=dialog] h3').text()).toContain('检测结果 · Second')
    expect(w.findAll('[data-policy-id]')).toHaveLength(2)
    await w.get('[data-close-dialog]').trigger('click')
    await w.get('[data-policy-id="9"] [data-run-policy]').trigger('click')
    await flushPromises()
    expect(api.run).toHaveBeenCalledWith(9)
    w.unmount()
  })
  it('keeps the latest scope and budget when a previous model selection resolves late', async () => {
    const w = mountPanel([{ ...plan, group_ids: [1, 2] }])
    await w.get('[data-policy-id="9"] [data-edit-policy]').trigger('click')
    await flushPromises()
    const stale = { targets: 4, account_ids: [1, 2, 3], group_counts: { 1: 2, 2: 2 } }
    const latest = { targets: 2, account_ids: [2, 3], group_counts: { 1: 1, 2: 2 } }
    let finishStale!: () => void
    api.scope.mockImplementationOnce(() => new Promise((resolve) => { finishStale = () => resolve(stale) }))
    await w.get('input[value="gpt-b"]').setValue(true)
    expect(api.scope).toHaveBeenLastCalledWith([1, 2], ['gpt-a', 'gpt-b'], [], expect.any(AbortSignal))
    api.scope.mockResolvedValueOnce(latest)
    await w.get('input[value="gpt-a"]').setValue(false)
    await flushPromises()
    finishStale()
    await flushPromises()
    expect(api.scope).toHaveBeenLastCalledWith([1, 2], ['gpt-b'], [], expect.any(AbortSignal))
    expect(w.text()).toContain('2 个去重账号，1 个所选模型，共 2 个支持')
    expect(w.text()).toContain('预计每天约 96 次请求')
    expect(w.get('aside ul').findAll('li')).toHaveLength(2)
    w.unmount()
  })
  it('opens results for the linked policy after the first catalog load', async () => {
    const second = { ...plan, id: 15, name: 'Second' }
    const w = mountPanel([], 15, false)
    await w.setProps({ plans: [plan, second], catalogReady: true })
    await flushPromises()
    expect(w.getComponent(IntelligencePolicyResults).props('plan').id).toBe(15)
    expect(w.get('[role=dialog] h3').text()).toContain('检测结果 · Second')
    expect(w.findAll('[data-policy-id]')).toHaveLength(2)
    await w.get('[data-close-dialog]').trigger('click')
    expect(w.findAll('[data-policy-id]')).toHaveLength(2)
    expect(w.findComponent(IntelligencePolicyResults).exists()).toBe(false)
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
    const action = w.get('ul li button')
    await action.trigger('click')
    expect(w.get('[role=dialog]').text()).toContain('B #2 的 gpt-a')
    expect(api.release).not.toHaveBeenCalled()
    await w.get('[role=dialog] .btn-secondary').trigger('click')
    expect(api.release).not.toHaveBeenCalled()
    await action.trigger('click')
    api.overview.mockResolvedValue({ ...defaults, states: [] })
    await w.get('[role=dialog] .btn-primary').trigger('click')
    await flushPromises()
    expect(api.release).toHaveBeenCalledWith(9, 2, 'gpt-a')
    expect(w.find('ul li button').exists()).toBe(false)
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
    expect(w.findAll('[data-result-id] button')).toHaveLength(1)
    await w.get('[data-result-id] button').trigger('click')
    await flushPromises()
    expect(api.retry).toHaveBeenCalledWith(5)
    expect(api.art).not.toHaveBeenCalled()
    expect(w.get('[data-result-id]').text()).toContain('默认通过')
    expect(w.text()).toContain('沿用本轮已记录的检测结果')
    w.unmount()
  })
  it('shows only the checks saved for each round when current and historical results appear together', async () => {
    const row = defaults.results[0]
    const current = [
      { key: 'candy', status: 'pass', detail: '21' },
      { key: 'http_429', status: 'pass', detail: '0/3' },
    ]
    const historical = [
      { key: 'fingerprint', status: 'unknown', detail: '' },
      { key: 'codex_state', status: 'unknown', detail: '' },
      ...current,
    ]
    api.overview.mockResolvedValue({
      states: [],
      results: [
        { ...row, evidence: { auto_verdict: 'pass', checks: current } },
        { ...row, result_id: 4, evidence: { auto_verdict: 'unknown', checks: historical } },
      ],
    })
    const w = mount(IntelligencePolicyResults, {
      props: { plan, refreshKey: 0 },
      global: { plugins: [i18n()], stubs: { BaseDialog: true } },
    })
    await flushPromises()
    const checks = w.findAll('[data-result-id]').map(r => r.findAll('dt').map(check => check.text()))
    expect(checks[0]).toEqual(['糖果题 · 通过', '连续三轮 HTTP 429 · 通过'])
    expect(checks[1]).toEqual(['行为指纹 · 未能判定', 'Codex 状态探针 · 未能判定', ...checks[0]])
    w.unmount()
  })
  it('shows failed checks immediately and keeps inconclusive rounds when filtering by issues', async () => {
    const row = defaults.results[0]
    api.overview.mockResolvedValue({ states: [], results: [
      { ...row, result_id: 12, account_id: 8, account_name: 'Same name', model: 'model-A', verdict: 'confirmed',
        evidence: { auto_verdict: 'confirmed', checks: [
          { key: 'candy', status: 'fail', detail: '回答 19，参考答案 21' },
          { key: 'http_429', status: 'pass', detail: '0/3' },
        ] } },
      { ...row, result_id: 11, account_id: 9, account_name: 'Same name', model: 'model-B', verdict: 'unknown',
        error_message: '请求中断', evidence: null },
      ...Array.from({ length: 10 }, (_, i) => ({ ...row, result_id: 10 - i })),
    ] })
    const w = mount(IntelligencePolicyResults, {
      props: { plan, refreshKey: 0 }, global: { plugins: [i18n()], stubs: { BaseDialog: true } },
    })
    await flushPromises()
    expect(w.findAll('[data-result-id]')).toHaveLength(10)
    const failed = w.get('[data-result-id="12"]')
    for (const text of ['Same name', '#8', 'model-A', '糖果题 · 异常', '回答 19，参考答案 21']) expect(failed.text()).toContain(text)
    expect(failed.get('dt').element.closest('details')).toBeNull()
    await w.get('nav button:last-child').trigger('click')
    expect(w.find('[data-result-id="12"]').exists()).toBe(false)
    await w.get('input[type=checkbox]').setValue(true)
    expect(w.findAll('[data-result-id]')).toHaveLength(2)
    expect(w.get('[data-result-id="11"]').text()).toContain('请求中断')
    expect(w.get('[data-result-id="11"]').text()).toContain('该轮未保留分项明细')
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
    await w.get('[data-result-id] button').trigger('click')
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
    expect(w.get('[data-result-id] details').text()).toContain('连续三轮 HTTP 429 · 通过')
    expect(w.get('[data-result-id]').text()).toContain('图中没有自行车')
    expect(w.get('[data-result-id]').text()).toContain('人工否决')
    expect(w.get('[data-result-id]').text()).toContain('确认降智')
    w.unmount()
  })
})
