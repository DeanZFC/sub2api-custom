import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestProtectionEditor from '../TestProtectionEditor.vue'
import type { AdminGroup, TestOutcomeAction, TestProtectionConfig, TestType } from '@/types'
import { copyTestProtection, validTestProtection } from '@/utils/testProtection'

const types: TestType[] = [
  { id: 1, name: 'Statistics', key: 'stats', output_kind: 'statistics', prompt: '', enabled: true },
  { id: 2, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
  { id: 3, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true },
]
const groups = [{ id: 8, name: 'Basic', platform: 'openai' }, { id: 9, name: 'Premium', platform: 'openai' }, { id: 10, name: 'Priority', platform: 'openai' }] as AdminGroup[]
const mountEditor = (config: TestProtectionConfig = { enabled: false, rules: [] }, targetMode = 'account') => {
  const wrapper = mount(TestProtectionEditor, {
    props: { modelValue: config, types, targetMode, groups, 'onUpdate:modelValue': (value: TestProtectionConfig) => wrapper.setProps({ modelValue: value }) },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] },
  })
  return wrapper
}

describe('quality protection settings', () => {
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
    await section.get('[data-rule-failure]').setValue(false)
    expect(validTestProtection(wrapper.props('modelValue'), 'account', [modelType], groups)).toBe(true)
    await section.get('[data-rule-model-match]').setValue('snapshot')
    expect(wrapper.props('modelValue').rules[0].model_match).toBe('snapshot')
    await section.get('[data-add-threshold]').trigger('click')
    expect(section.findAll('[data-threshold] select')[0].findAll('option').map(option => option.attributes('value'))).toEqual(['latency_ms'])
    for (const patch of [{ model_match: 'fuzzy' }, { expected_answer: 'OK' }, { vote: { enabled: true, reject_above: 0, pass_at_least: 1 } }, { thresholds: [{ metric: 'output_numeric', operator: 'gt', value: 1 }] }]) {
      const config = copyTestProtection(wrapper.props('modelValue'))
      Object.assign(config.rules[0], patch)
      expect(validTestProtection(config, 'account', [modelType], groups)).toBe(false)
    }
    expect(validTestProtection({ enabled: true, rules: [{ test_definition_id: 3, pause_on_failure: true, model_match: 'snapshot' }] }, 'account', types)).toBe(false)
    wrapper.unmount()
  })

  it('defaults to disabled, blocks group mode and initializes independent type rules', async () => {
    const wrapper = mountEditor()
    expect(wrapper.find('[data-protection-type]').exists()).toBe(false)
    await wrapper.get('[data-protection-enabled]').setValue(true)
    expect(wrapper.findAll('[data-protection-type]')).toHaveLength(3)
    expect(wrapper.props('modelValue').rules.map(rule => rule.test_definition_id)).toEqual([1, 2, 3])
    expect(wrapper.props('modelValue').rules.every(rule => rule.pause_on_failure)).toBe(true)
    await wrapper.setProps({ targetMode: 'group' })
    expect(wrapper.get('[data-protection-enabled]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-protection-type]').exists()).toBe(false)
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
    await html.get('[data-rule-answer]').setValue('A pelican on a bicycle')
    await html.get('[data-vote-reject]').setValue(0)
    expect(html.find('[data-rule-answer-match]').exists()).toBe(false)
    expect(wrapper.props('modelValue').rules[1]).toMatchObject({ expected_answer: 'A pelican on a bicycle', vote: { enabled: true, reject_above: 0, pass_at_least: 3 } })
    expect(wrapper.props('modelValue').rules[0].min_samples).toBe(25)
    expect(validTestProtection(wrapper.props('modelValue'), 'account', types)).toBe(true)
  })

  it('requires at least one judgement rule and validates percentage and voting limits', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-protection-enabled]').setValue(true)
    for (const section of wrapper.findAll('[data-protection-type]')) await section.get('[data-rule-enabled]').setValue(false)
    expect(validTestProtection(wrapper.props('modelValue'), 'account', types)).toBe(false)
    await wrapper.get('[data-protection-type="1"] [data-rule-enabled]').setValue(true)
    const stats = wrapper.get('[data-protection-type="1"]')
    await stats.get('[data-rule-failure]').setValue(false)
    expect(validTestProtection(wrapper.props('modelValue'), 'account', types)).toBe(true)
    const legacy = copyTestProtection(wrapper.props('modelValue'))
    delete legacy.rules[0].on_fail
    expect(validTestProtection(legacy, 'account', types)).toBe(false)
    await stats.get('[data-add-threshold]').trigger('click')
    await stats.get('[data-threshold] input').setValue(101)
    expect(validTestProtection(wrapper.props('modelValue'), 'account', types)).toBe(false)
    await stats.get('[data-threshold] input').setValue(99)
    expect(validTestProtection(wrapper.props('modelValue'), 'all_accounts', types)).toBe(true)
    expect(validTestProtection(wrapper.props('modelValue'), 'group', types)).toBe(false)
    await wrapper.get('[data-protection-type="2"] [data-rule-enabled]').setValue(true)
    await wrapper.get('[data-protection-type="2"] [data-rule-vote]').setValue(true)
    await wrapper.get('[data-vote-pass]').setValue(0)
    expect(validTestProtection(wrapper.props('modelValue'), 'account', types)).toBe(false)
  })
  it('keeps disabled type settings readable but prevents protection activation and saving', async () => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 3, pause_on_failure: true, expected_answer: '29', answer_match: 'numeric' }] }
    const wrapper = mountEditor(config)
    const disabledTypes = types.map(type => type.id === 3 ? { ...type, enabled: false } : type)
    await wrapper.setProps({ types: disabledTypes })
    const section = wrapper.get('[data-protection-type="3"]')
    expect(section.find('[data-disabled-type-hint]').exists()).toBe(true)
    expect((section.get('[data-rule-answer]').element as HTMLTextAreaElement).value).toBe('29')
    expect(section.get('fieldset').attributes('disabled')).toBeDefined()
    expect(validTestProtection(wrapper.props('modelValue'), 'account', disabledTypes)).toBe(false)
    await section.get('[data-rule-enabled]').setValue(false)
    expect(section.get('[data-rule-enabled]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-protection-enabled]').setValue(false)
    await wrapper.get('[data-protection-enabled]').setValue(true)
    expect(wrapper.props('modelValue').rules.map(rule => rule.test_definition_id)).toEqual([1, 2])
    expect(validTestProtection(wrapper.props('modelValue'), 'account', disabledTypes)).toBe(true)
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
    expect(validTestProtection(wrapper.props('modelValue'), 'account', types, groups)).toBe(true)
    await pass.get('[data-group-search]').setValue('premium')
    expect(pass.find('[data-action-group="9"]').exists()).toBe(true)
    expect(pass.find('[data-action-group="10"]').exists()).toBe(false)
    expect(pass.get('[data-selected-groups]').text()).toContain('Priority')
    await pass.get('[data-group-search]').setValue('10')
    expect(pass.find('[data-action-group="10"]').exists()).toBe(true)
    await pass.get('[data-action-group-mode]').setValue('keep')
    expect(wrapper.props('modelValue').rules[0].on_pass?.group_ids).toBeUndefined()
  })

  it('preserves legacy defaults without rewriting actions until they are edited', async () => {
    const legacy: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 2, pause_on_failure: true }] }
    const wrapper = mountEditor(legacy)
    const html = wrapper.get('[data-protection-type="2"]')
    expect((html.get('[data-outcome="pass"] [data-action-scheduling]').element as HTMLSelectElement).value).toBe('resume')
    expect((html.get('[data-outcome="fail"] [data-action-scheduling]').element as HTMLSelectElement).value).toBe('pause')
    await html.get('[data-rule-vote]').setValue(true)
    expect(wrapper.props('modelValue').rules[0].on_pass).toBeUndefined()
    await html.get('[data-outcome="fail"] [data-action-scheduling]').setValue('keep')
    expect(wrapper.props('modelValue').rules[0].on_fail?.scheduling).toBe('keep')
    expect(legacy.rules[0].on_fail).toBeUndefined()
  })

  it('allows an empty outcome to remove managed groups but rejects empty or invalid targets', () => {
    const config: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 3, expected_answer: '29', answer_match: 'numeric', on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9] }, on_fail: { scheduling: 'keep', group_mode: 'assign', group_ids: [] } }] }
    expect(validTestProtection(config, 'account', types, groups)).toBe(true)
    for (const ids of [[], [9, 9], [0], [-1], [1.5], [99], Array.from({ length: 101 }, (_, index) => index + 1)]) {
      const next = copyTestProtection(config)
      next.rules[0].on_pass!.group_ids = ids
      expect(validTestProtection(next, 'account', types, groups)).toBe(false)
    }
    for (const patch of [{ group_mode: 'keep' }, { scheduling: 'invalid' }, { group_mode: 'invalid' }]) {
      const next = copyTestProtection(config)
      Object.assign(next.rules[0].on_pass!, patch as Partial<TestOutcomeAction>)
      expect(validTestProtection(next, 'account', types, groups)).toBe(false)
    }
  })

  it('deep copies actions and visibly rejects removed or incompatible groups without losing IDs', async () => {
    const original: TestProtectionConfig = { enabled: true, rules: [{ test_definition_id: 1, pause_on_failure: true, on_pass: { scheduling: 'resume', group_mode: 'assign', group_ids: [9] }, on_fail: { scheduling: 'pause', group_mode: 'assign', group_ids: [8] } }] }
    const copied = copyTestProtection(original)
    copied.rules[0].on_pass!.group_ids!.push(10)
    copied.rules[0].on_fail!.group_ids!.splice(0, 1)
    expect(original.rules[0].on_pass!.group_ids).toEqual([9])
    expect(original.rules[0].on_fail!.group_ids).toEqual([8])
    const wrapper = mountEditor(original)
    await wrapper.setProps({ groups: groups.filter(group => group.id !== 9) })
    expect(wrapper.find('[data-unavailable-groups]').exists()).toBe(true)
    expect(wrapper.props('modelValue').rules[0].on_pass!.group_ids).toEqual([9])
    expect(validTestProtection(original, 'account', types, groups.filter(group => group.id !== 9))).toBe(false)
  })

})
