import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestProtectionEditor from '../TestProtectionEditor.vue'
import type { AdminGroup, TestOutcomeAction, TestProtectionConfig, TestProtectionRecovery, TestType } from '@/types'
import { canEnableCacheRecovery, copyTestProtection, defaultTestProtectionRecovery, validTestProtection } from '@/utils/testProtection'
import resources from '@/i18n/locales/en/admin/resources'

const types: TestType[] = [
  { id: 1, name: 'Statistics', key: 'stats', output_kind: 'statistics', prompt: '', enabled: true },
  { id: 2, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
  { id: 3, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true },
]
const groups = [{ id: 8, name: 'Basic', platform: 'openai' }, { id: 9, name: 'Premium', platform: 'openai' }, { id: 10, name: 'Priority', platform: 'openai' }] as AdminGroup[]
const recoveryMessages = Object.fromEntries(Object.entries(resources.tests.protection.recovery).map(([key, value]) => [
  key, ({ named }: { named: (key: string) => unknown }) => value.replace(/\{(\w+)\}/g, (_, name: string) => String(named(name))),
]))
const mountEditor = (config: TestProtectionConfig = { enabled: false, rules: [] }) => {
  const wrapper = mount(TestProtectionEditor, {
    props: { modelValue: config, types, groups, 'onUpdate:modelValue': (value: TestProtectionConfig) => wrapper.setProps({ modelValue: value }) },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: { tests: { protection: { recovery: recoveryMessages } } } } } })] },
  })
  return wrapper
}

describe('quality protection settings', () => {
  it('shows execution failure as mandatory when a fail action is configured', async () => {
    const wrapper = mountEditor({ enabled: true, rules: [{
      test_definition_id: 3, priority: 0, required_pass: false, pause_on_failure: false,
      expected_answer: '7', on_fail: { scheduling: 'pause', group_mode: 'keep' },
    }] })
    const numeric = wrapper.get('[data-protection-type="3"]')
    const failure = numeric.get('[data-rule-failure]')
    expect((failure.element as HTMLInputElement).checked).toBe(true)
    expect(failure.attributes('disabled')).toBeDefined()
    expect(numeric.find('[data-rule-failure-action-hint]').exists()).toBe(true)
    expect(wrapper.props('modelValue').rules[0].pause_on_failure).toBe(false)

    await wrapper.setProps({ modelValue: { enabled: true, rules: [{
      test_definition_id: 3, priority: 0, required_pass: false, pause_on_failure: false, expected_answer: '7',
    }] } })
    expect((failure.element as HTMLInputElement).checked).toBe(false)
    expect(failure.attributes('disabled')).toBeUndefined()
    expect(numeric.find('[data-rule-failure-action-hint]').exists()).toBe(false)
    await failure.setValue(true)
    expect(wrapper.props('modelValue').rules[0].pause_on_failure).toBe(true)
    wrapper.unmount()
  })

  it('provides model metadata matching without requiring answers, votes or thresholds', async () => {
    const modelType: TestType = { id: 4, name: 'Model consistency', key: 'model-consistency', output_kind: 'model_check', prompt: '', enabled: true }
    const wrapper = mountEditor()
    await wrapper.setProps({ types: [modelType] })
    await wrapper.get('[data-protection-enabled]').setValue(true)
    const section = wrapper.get('[data-protection-type="4"]')
    expect(section.find('[data-rule-answer]').exists()).toBe(false)
    expect(section.find('[data-rule-vote]').exists()).toBe(false)
    expect(wrapper.props('modelValue').rules[0].model_match).toBe('exact')
    expect(wrapper.props('modelValue').rules[0].answer_match).toBeUndefined()
    const metadataOnly = copyTestProtection(wrapper.props('modelValue'))
    delete metadataOnly.rules[0].on_fail
    await wrapper.setProps({ modelValue: metadataOnly })
    await section.get('[data-rule-failure]').setValue(false)
    expect(validTestProtection(wrapper.props('modelValue'), [modelType], groups)).toBe(true)
    await section.get('[data-rule-model-match]').setValue('snapshot')
    expect(wrapper.props('modelValue').rules[0].model_match).toBe('snapshot')
    await section.get('[data-add-threshold]').trigger('click')
    expect(section.findAll('[data-threshold] select')[0].findAll('option').map(option => option.attributes('value'))).toEqual(['latency_ms'])
    for (const patch of [{ model_match: 'fuzzy' }, { expected_answer: 'OK' }, { vote: { enabled: true, reject_above: 0, pass_at_least: 1 } }, { thresholds: [{ metric: 'output_numeric', operator: 'gt', value: 1 }] }]) {
      const config = copyTestProtection(wrapper.props('modelValue'))
      Object.assign(config.rules[0], patch)
      expect(validTestProtection(config, [modelType], groups)).toBe(false)
    }
    expect(validTestProtection({ enabled: true, rules: [{ test_definition_id: 3, priority: 0, required_pass: false, pause_on_failure: true, model_match: 'snapshot' }] }, types)).toBe(false)
    wrapper.unmount()
  })

  it('defaults to disabled and initializes independent type rules', async () => {
    const wrapper = mountEditor()
    expect(wrapper.find('[data-protection-type]').exists()).toBe(false)
    await wrapper.get('[data-protection-enabled]').setValue(true)
    expect(wrapper.findAll('[data-protection-type]')).toHaveLength(3)
    expect(wrapper.props('modelValue').rules.map(rule => rule.test_definition_id)).toEqual([1, 2, 3])
    expect(wrapper.props('modelValue').rules.every(rule => rule.pause_on_failure)).toBe(true)
    expect(wrapper.props('modelValue').rules.every(rule => rule.on_pass?.scheduling === 'resume' && rule.on_fail?.scheduling === 'pause')).toBe(true)
  })

  it('provides compatible metrics, samples, answer modes and voting controls', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-protection-enabled]').setValue(true)
    const stats = wrapper.get('[data-protection-type="1"]')
    expect(stats.find('[data-rule-vote]').exists()).toBe(false)
    expect(stats.find('[data-rule-answer]').exists()).toBe(false)
    await stats.get('[data-add-threshold]').trigger('click')
    expect(stats.findAll('[data-threshold] select')[0].findAll('option').map(option => option.attributes('value'))).toEqual(['success_rate', 'cache_rate', 'avg_first_token_ms'])
    await stats.get('[data-rule-samples]').setValue(25)
    const html = wrapper.get('[data-protection-type="2"]')
    await html.get('[data-rule-vote]').setValue(true)
    expect((html.get('[data-rule-public-vote]').element as HTMLInputElement).checked).toBe(false)
    expect(html.find('[data-vote-reject]').exists()).toBe(false)
    await html.get('[data-rule-public-vote]').setValue(true)
    await html.get('[data-rule-answer]').setValue('A pelican on a bicycle')
    await html.get('[data-vote-reject]').setValue(0)
    expect(html.find('[data-rule-answer-match]').exists()).toBe(false)
    expect(wrapper.props('modelValue').rules[1]).toMatchObject({ expected_answer: 'A pelican on a bicycle', vote: { enabled: true, reject_above: 0, pass_at_least: 3 } })
    expect(wrapper.props('modelValue').rules[0].min_samples).toBe(25)
    expect(validTestProtection(wrapper.props('modelValue'), types)).toBe(true)
  })

  it('requires at least one judgement rule and validates percentage and voting limits', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-protection-enabled]').setValue(true)
    for (const section of wrapper.findAll('[data-protection-type]')) await section.get('[data-rule-enabled]').setValue(false)
    expect(validTestProtection(wrapper.props('modelValue'), types)).toBe(false)
    await wrapper.get('[data-protection-type="1"] [data-rule-enabled]').setValue(true)
    const stats = wrapper.get('[data-protection-type="1"]')
    expect(stats.get('[data-rule-failure]').attributes('disabled')).toBeDefined()
    expect(validTestProtection(wrapper.props('modelValue'), types)).toBe(true)
    const legacy = copyTestProtection(wrapper.props('modelValue'))
    delete legacy.rules[0].on_fail
    legacy.rules[0].pause_on_failure = false
    expect(validTestProtection(legacy, types)).toBe(false)
    await stats.get('[data-add-threshold]').trigger('click')
    await stats.get('[data-threshold] input').setValue(101)
    expect(validTestProtection(wrapper.props('modelValue'), types)).toBe(false)
    await stats.get('[data-threshold] input').setValue(99)
    expect(validTestProtection(wrapper.props('modelValue'), types)).toBe(true)
    await wrapper.get('[data-protection-type="2"] [data-rule-enabled]').setValue(true)
    await wrapper.get('[data-protection-type="2"] [data-rule-vote]').setValue(true)
    await wrapper.get('[data-protection-type="2"] [data-rule-public-vote]').setValue(true)
    await wrapper.get('[data-vote-pass]').setValue(0)
    expect(validTestProtection(wrapper.props('modelValue'), types)).toBe(false)
  })
  it('keeps legacy review private and allows public voting to be closed without disabling admin review', async () => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 2, priority: 0, required_pass: false, pause_on_failure: true, vote: { enabled: true, reject_above: 5, pass_at_least: 3 } }] }
    const wrapper = mountEditor(config)
    const html = wrapper.get('[data-protection-type="2"]')
    expect((html.get('[data-rule-vote]').element as HTMLInputElement).checked).toBe(true)
    expect((html.get('[data-rule-public-vote]').element as HTMLInputElement).checked).toBe(false)
    expect(html.find('[data-vote-pass]').exists()).toBe(false)
    await html.get('[data-rule-public-vote]').setValue(true)
    expect(wrapper.props('modelValue').rules[0].vote).toEqual({ enabled: true, public_enabled: true, reject_above: 5, pass_at_least: 3 })
    const copied = copyTestProtection(wrapper.props('modelValue'))
    copied.rules[0].vote!.public_enabled = false
    expect(wrapper.props('modelValue').rules[0].vote!.public_enabled).toBe(true)
    await html.get('[data-rule-public-vote]').setValue(false)
    expect(wrapper.props('modelValue').rules[0].vote).toEqual({ enabled: true, public_enabled: false, reject_above: 5, pass_at_least: 3 })
    expect(validTestProtection(wrapper.props('modelValue'), types)).toBe(true)
    await html.get('[data-rule-public-vote]').setValue(true)
    await html.get('[data-rule-vote]').setValue(false)
    expect(wrapper.props('modelValue').rules[0].vote).toMatchObject({ enabled: false, public_enabled: false })
    expect(config.rules[0].vote!.public_enabled).toBeUndefined()
    wrapper.unmount()
  })
  it('keeps disabled type settings readable but prevents protection activation and saving', async () => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 3, priority: 0, required_pass: false, pause_on_failure: true, expected_answer: '29', answer_match: 'numeric' }] }
    const wrapper = mountEditor(config)
    const disabledTypes = types.map(type => type.id === 3 ? { ...type, enabled: false } : type)
    await wrapper.setProps({ types: disabledTypes })
    const section = wrapper.get('[data-protection-type="3"]')
    expect(section.find('[data-disabled-type-hint]').exists()).toBe(true)
    expect((section.get('[data-rule-answer]').element as HTMLTextAreaElement).value).toBe('29')
    expect(section.get('fieldset').attributes('disabled')).toBeDefined()
    expect(validTestProtection(wrapper.props('modelValue'), disabledTypes)).toBe(false)
    await section.get('[data-rule-enabled]').setValue(false)
    expect(section.get('[data-rule-enabled]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-protection-enabled]').setValue(false)
    await wrapper.get('[data-protection-enabled]').setValue(true)
    expect(wrapper.props('modelValue').rules.map(rule => rule.test_definition_id)).toEqual([1, 2])
    expect(validTestProtection(wrapper.props('modelValue'), disabledTypes)).toBe(true)
  })

  it('configures independent pass and fail group actions without changing other types', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-protection-enabled]').setValue(true)
    const stats = wrapper.get('[data-protection-type="1"]')
    const pass = stats.get('[data-outcome="pass"]')
    const fail = stats.get('[data-outcome="fail"]')
    await pass.get('[data-action-scheduling]').setValue('keep')
    await fail.get('[data-action-scheduling]').setValue('keep')
    await pass.get('[data-action-group-mode]').setValue('assign')
    await pass.get('[data-action-group="9"]').setValue(true)
    await pass.get('[data-action-group="10"]').setValue(true)
    await fail.get('[data-action-group-mode]').setValue('assign')
    await fail.get('[data-action-group="8"]').setValue(true)
    expect(wrapper.props('modelValue').rules[0]).toMatchObject({
      on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9, 10] },
      on_fail: { scheduling: 'keep', group_mode: 'assign', group_ids: [8] },
    })
    expect(wrapper.props('modelValue').rules[1].on_fail).toEqual({ scheduling: 'pause', group_mode: 'keep' })
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    await pass.get('[data-group-search]').setValue('premium')
    expect(pass.find('[data-action-group="9"]').exists()).toBe(true)
    expect(pass.find('[data-action-group="10"]').exists()).toBe(false)
    expect(pass.get('[data-selected-groups]').text()).toContain('Priority')
    await pass.get('[data-group-search]').setValue('10')
    expect(pass.find('[data-action-group="10"]').exists()).toBe(true)
    await pass.get('[data-action-group-mode]').setValue('keep')
    expect(wrapper.props('modelValue').rules[0].on_pass?.group_ids).toBeUndefined()
  })

  it('displays missing actions as keep without adding implicit scheduling changes', async () => {
    const legacy: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 2, priority: 0, required_pass: false, pause_on_failure: true }] }
    const wrapper = mountEditor(legacy)
    const html = wrapper.get('[data-protection-type="2"]')
    expect((html.get('[data-outcome="pass"] [data-action-scheduling]').element as HTMLSelectElement).value).toBe('keep')
    expect((html.get('[data-outcome="fail"] [data-action-scheduling]').element as HTMLSelectElement).value).toBe('keep')
    expect((html.get('[data-outcome="pass"] [data-action-group-mode]').element as HTMLSelectElement).value).toBe('keep')
    expect((html.get('[data-outcome="fail"] [data-action-group-mode]').element as HTMLSelectElement).value).toBe('keep')
    await html.get('[data-rule-vote]').setValue(true)
    expect(wrapper.props('modelValue').rules[0].on_pass).toBeUndefined()
    expect(wrapper.props('modelValue').rules[0].on_fail).toBeUndefined()
    await html.get('[data-outcome="fail"] [data-action-group-mode]').setValue('assign')
    await html.get('[data-outcome="fail"] [data-action-group="8"]').setValue(true)
    expect(wrapper.props('modelValue').rules[0].on_fail).toEqual({ scheduling: 'keep', group_mode: 'assign', group_ids: [8] })
    expect(legacy.rules[0].on_fail).toBeUndefined()
  })

  it.each(['on_pass', 'on_fail', 'both'] as const)('requires explicit pause and resume actions for cache recovery when %s is missing', (missing) => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 1, priority: 0, required_pass: false, thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80 }], on_pass: { scheduling: 'resume', group_mode: 'keep' }, on_fail: { scheduling: 'pause', group_mode: 'keep' }, recovery: { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 85 } }] }
    const rule = config.rules[0]
    if (missing === 'both' || missing === 'on_pass') delete rule.on_pass
    if (missing === 'both' || missing === 'on_fail') delete rule.on_fail
    expect(canEnableCacheRecovery(rule, 'statistics')).toBe(false)
    expect(validTestProtection(config, types, groups)).toBe(false)
    const wrapper = mountEditor(config)
    const stats = wrapper.get('[data-protection-type="1"]')
    expect(stats.get('[data-cache-recovery] fieldset').attributes('disabled')).toBeDefined()
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('requires a nonempty strategy group selection for every assign action', () => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 3, priority: 0, required_pass: false, expected_answer: '29', answer_match: 'numeric', on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9] }, on_fail: { scheduling: 'keep', group_mode: 'assign', group_ids: [8] } }] }
    expect(validTestProtection(config, types, groups)).toBe(true)
    for (const ids of [[], [9, 9], [0], [-1], [1.5], [99], Array.from({ length: 101 }, (_, index) => index + 1)]) {
      const next = copyTestProtection(config)
      next.rules[0].on_pass!.group_ids = ids
      expect(validTestProtection(next, types, groups)).toBe(false)
    }
    for (const patch of [{ group_mode: 'keep' }, { scheduling: 'invalid' }, { group_mode: 'invalid' }]) {
      const next = copyTestProtection(config)
      Object.assign(next.rules[0].on_pass!, patch as Partial<TestOutcomeAction>)
      expect(validTestProtection(next, types, groups)).toBe(false)
    }
  })

  it('deep copies actions and visibly rejects removed or incompatible groups without losing IDs', async () => {
    const original: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 1, priority: 0, required_pass: false, pause_on_failure: true, on_pass: { scheduling: 'resume', group_mode: 'assign', group_ids: [9] }, on_fail: { scheduling: 'pause', group_mode: 'assign', group_ids: [8] } }] }
    const copied = copyTestProtection(original)
    copied.rules[0].on_pass!.group_ids!.push(10)
    copied.rules[0].on_fail!.group_ids!.splice(0, 1)
    expect(original.rules[0].on_pass!.group_ids).toEqual([9])
    expect(original.rules[0].on_fail!.group_ids).toEqual([8])
    const wrapper = mountEditor(original)
    await wrapper.setProps({ groups: groups.filter(group => group.id !== 9) })
    expect(wrapper.find('[data-unavailable-groups]').exists()).toBe(true)
    expect(wrapper.props('modelValue').rules[0].on_pass!.group_ids).toEqual([9])
    expect(validTestProtection(original, types, groups.filter(group => group.id !== 9))).toBe(false)
  })

  it('keeps cache recovery off for old rules and enables it only after a cache pause threshold exists', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-protection-enabled]').setValue(true)
    const stats = wrapper.get('[data-protection-type="1"]')
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeDefined()
    expect(wrapper.props('modelValue').rules.every(rule => rule.recovery === undefined)).toBe(true)
    expect(wrapper.get('[data-protection-type="2"]').find('[data-cache-recovery]').exists()).toBe(false)
    await stats.get('[data-add-threshold]').trigger('click')
    await stats.get('[data-threshold] select').setValue('cache_rate')
    await stats.get('[data-threshold] input').setValue(80)
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeUndefined()
    expect(stats.find('[data-recovery-cooldown]').exists()).toBe(false)
    await stats.get('[data-recovery-enabled]').setValue(true)
    expect(wrapper.props('modelValue').rules[0].recovery).toEqual({ enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 85 })
    expect(stats.text()).toContain('once per minute')
    expect(stats.text()).toContain('at most 20 normal request attempts')
    expect(stats.text()).toContain('Synchronous and failed requests consume this allowance')
    expect(stats.text()).toContain('after the trial starts')
    expect(stats.text()).toContain('never count as recovery')
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    wrapper.unmount()
  })

  it('requires a trial group when the fail action assigns groups and allows disabling an incompatible rule', async () => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 1, priority: 0, required_pass: false, thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80 }], on_pass: { scheduling: 'resume', group_mode: 'assign', group_ids: [9] }, on_fail: { scheduling: 'pause', group_mode: 'assign', group_ids: [] } }] }
    const wrapper = mountEditor(config)
    const stats = wrapper.get('[data-protection-type="1"]')
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeDefined()
    expect(stats.get('[data-recovery-eligibility]').text()).toContain('retain at least one group')
    await stats.get('[data-outcome="fail"] [data-action-group="8"]').setValue(true)
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeUndefined()
    await stats.get('[data-recovery-enabled]').setValue(true)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    await stats.get('[data-outcome="fail"] [data-action-group="8"]').setValue(false)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(false)
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeUndefined()
    await stats.get('[data-recovery-enabled]').setValue(false)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(false)
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeDefined()
    await stats.get('[data-outcome="fail"] [data-action-group-mode]').setValue('keep')
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('edits independent recovery settings without mutating the saved rule or other check types', async () => {
    const original: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 1, priority: 0, required_pass: false, thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80 }], on_pass: { scheduling: 'resume', group_mode: 'keep' }, on_fail: { scheduling: 'pause', group_mode: 'keep' }, recovery: { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 85 } }, { test_definition_id: 3, priority: 0, required_pass: false, pause_on_failure: true }] }
    const wrapper = mountEditor(copyTestProtection(original))
    const stats = wrapper.get('[data-protection-type="1"]')
    await stats.get('[data-recovery-cooldown]').setValue(600)
    await stats.get('[data-recovery-trial]').setValue(180)
    await stats.get('[data-recovery-max-requests]').setValue(30)
    await stats.get('[data-recovery-min-samples]').setValue(15)
    await stats.get('[data-recovery-rate]').setValue(92.5)
    expect(wrapper.props('modelValue').rules[0].recovery).toEqual({ enabled: true, cooldown_seconds: 600, trial_seconds: 180, max_requests: 30, min_samples: 15, recover_rate: 92.5 })
    expect(original.rules[0].recovery).toEqual({ enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 85 })
    expect(wrapper.props('modelValue').rules[1].recovery).toBeUndefined()
    const copied = copyTestProtection(original)
    copied.rules[0].recovery!.max_requests = 99
    expect(original.rules[0].recovery!.max_requests).toBe(20)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    wrapper.unmount()
  })

  it('retains incompatible saved recovery settings and lets the administrator turn them off', async () => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 1, priority: 0, required_pass: false, thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80 }], on_pass: { scheduling: 'resume', group_mode: 'keep' }, on_fail: { scheduling: 'pause', group_mode: 'keep' }, recovery: { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 85 } }] }
    const wrapper = mountEditor(config)
    const stats = wrapper.get('[data-protection-type="1"]')
    await stats.get('[data-outcome="fail"] [data-action-scheduling]').setValue('keep')
    expect(stats.get('[data-recovery-eligibility]').text()).toContain('fail action must pause')
    expect(wrapper.props('modelValue').rules[0].recovery?.enabled).toBe(true)
    expect(stats.get('[data-cache-recovery] fieldset').attributes('disabled')).toBeDefined()
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeUndefined()
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(false)
    await stats.get('[data-recovery-enabled]').setValue(false)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    expect(wrapper.props('modelValue').rules[0].recovery?.recover_rate).toBe(85)
    expect(stats.get('[data-recovery-enabled]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('validates recovery boundaries, all pause thresholds and fresh-sample allowance independently', () => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 1, priority: 0, required_pass: false, thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 70 }, { metric: 'cache_rate', operator: 'lt', value: 80 }, { metric: 'success_rate', operator: 'lt', value: 95 }], on_pass: { scheduling: 'resume', group_mode: 'keep' }, on_fail: { scheduling: 'pause', group_mode: 'keep' }, recovery: { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 85 } }] }
    const valid = (patch: Partial<TestProtectionRecovery>) => {
      const next = copyTestProtection(config)
      Object.assign(next.rules[0].recovery!, patch)
      return validTestProtection(next, types, groups)
    }
    for (const field of ['cooldown_seconds', 'trial_seconds', 'max_requests', 'min_samples'] as const) {
      for (const value of [0, -1, 1.5, NaN, Infinity]) expect(valid({ [field]: value }), `${field}=${value}`).toBe(false)
    }
    for (const patch of [{ cooldown_seconds: 59 }, { cooldown_seconds: 86401 }, { trial_seconds: 59 }, { trial_seconds: 3601 }, { max_requests: 1001 }, { min_samples: 21 }, { recover_rate: 79.9 }, { recover_rate: 101 }, { recover_rate: NaN }, { recover_rate: Infinity }]) expect(valid(patch)).toBe(false)
    expect(valid({ cooldown_seconds: 60, trial_seconds: 60, max_requests: 1, min_samples: 1, recover_rate: 80 })).toBe(true)
    expect(valid({ cooldown_seconds: 86400, trial_seconds: 3600, max_requests: 1000, min_samples: 1000, recover_rate: 100 })).toBe(true)
    expect(defaultTestProtectionRecovery(config.rules[0]).recover_rate).toBe(85)
    expect(defaultTestProtectionRecovery({ test_definition_id: 1, priority: 0, required_pass: false, thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 99 }] }).recover_rate).toBe(100)
    const zero = copyTestProtection(config)
    zero.rules[0].thresholds = [{ metric: 'cache_rate', operator: 'lt', value: 0 }]
    zero.rules[0].recovery!.recover_rate = 0
    expect(validTestProtection(zero, types, groups)).toBe(true)
    for (const patch of [
      { test_definition_id: 2 }, { thresholds: [{ metric: 'success_rate', operator: 'lt', value: 80 }] },
      { thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80 }, { metric: 'cache_rate', operator: 'gt', value: 90 }] },
      { on_fail: { scheduling: 'keep', group_mode: 'keep' } }, { on_pass: { scheduling: 'keep', group_mode: 'keep' } },
      { vote: { enabled: true, reject_above: 0, pass_at_least: 1 } },
    ]) {
      const next = copyTestProtection(config)
      Object.assign(next.rules[0], patch)
      expect(validTestProtection(next, types, groups)).toBe(false)
    }
    expect(valid({ enabled: false, cooldown_seconds: 0, recover_rate: 0 })).toBe(true)
  })

})


describe('strategy condition priority and required pass', () => {
  it('edits priorities and gates independently, and prevents a review from being required', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-protection-enabled]').setValue(true)
    const numeric = wrapper.get('[data-protection-type="3"]')
    await numeric.get('[data-rule-priority]').setValue(700)
    await numeric.get('[data-rule-required-pass]').setValue(true)
    expect(wrapper.props('modelValue').rules.find(rule => rule.test_definition_id === 3)).toMatchObject({ priority: 700, required_pass: true })
    expect(wrapper.props('modelValue').rules.find(rule => rule.test_definition_id === 1)).toMatchObject({ priority: 0, required_pass: false })
    await numeric.get('[data-rule-vote]').setValue(true)
    expect(numeric.get('[data-rule-required-pass]').attributes('disabled')).toBeDefined()
    expect(wrapper.props('modelValue').rules.find(rule => rule.test_definition_id === 3)?.required_pass).toBe(false)
    await numeric.get('[data-rule-vote]').setValue(false)
    expect(numeric.get('[data-rule-required-pass]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('validates priority boundaries and rejects a required review condition', () => {
    const rule = { test_definition_id: 3, priority: 0, required_pass: true, expected_answer: '7', answer_match: 'numeric' as const }
    for (const priority of [0, 1000]) expect(validTestProtection({ enabled: true, rules: [{ ...rule, priority }] }, types, groups)).toBe(true)
    for (const priority of [-1, 1001, 0.5, NaN]) expect(validTestProtection({ enabled: true, rules: [{ ...rule, priority }] }, types, groups)).toBe(false)
    expect(validTestProtection({ enabled: true, rules: [{ ...rule, vote: { enabled: true, pass_at_least: 1, reject_above: 0 } }] }, types, groups)).toBe(false)
  })
})
