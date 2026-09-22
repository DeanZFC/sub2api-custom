import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestProtectionEditor from '../TestProtectionEditor.vue'
import type { AdminGroup, TestCombinationCondition, TestProtectionConfig, TestType } from '@/types'
import { copyTestProtection, defaultProtectionRule, validTestProtection } from '@/utils/testProtection'

const types: TestType[] = [
  { id: 1, name: 'Model consistency', key: 'model', output_kind: 'model_check', prompt: '', enabled: true },
  { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true },
  { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
  { id: 4, name: 'Statistics', key: 'stats', output_kind: 'statistics', prompt: '', enabled: true },
]
const groups = [{ id: 8, name: 'A' }, { id: 9, name: 'B' }] as AdminGroup[]
const leaf = (id = 1): TestCombinationCondition => ({ operator: 'test', test_definition_id: id, verdict: 'pass' })
const combined = (): TestProtectionConfig => ({
  enabled: true, mode: 'combined', rules: types.map(defaultProtectionRule),
  combinations: [{ id: 'promote', name: 'All required checks pass', priority: 1,
    condition: { operator: 'all', conditions: [leaf(1), { operator: 'any', conditions: [leaf(2), leaf(3)] }] },
    action: { scheduling: 'keep', group_mode: 'assign', group_ids: [8] },
  }],
})
const mountEditor = (config: TestProtectionConfig) => {
  const wrapper = mount(TestProtectionEditor, {
    props: { modelValue: config, types, groups, 'onUpdate:modelValue': (value: TestProtectionConfig) => wrapper.setProps({ modelValue: value }) },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] },
  })
  return wrapper
}

describe('combined quality rules', () => {
  it('switches modes explicitly and preserves both legacy settings and combined drafts', async () => {
    const original: TestProtectionConfig = { enabled: true, rules: types.map(defaultProtectionRule) }
    original.rules[0].priority = 7
    original.rules[0].required_pass = true
    original.rules[0].on_pass = { scheduling: 'resume', group_mode: 'assign', group_ids: [8] }
    const wrapper = mountEditor(original)
    expect(wrapper.find('[data-combination-rules]').exists()).toBe(false)
    await wrapper.get('[data-protection-mode]').setValue('combined')
    expect(wrapper.findAll('[data-combination-rule]')).toHaveLength(2)
    expect(wrapper.find('[data-rule-priority]').exists()).toBe(false)
    expect(wrapper.find('[data-rule-required-pass]').exists()).toBe(false)
    expect(wrapper.get('[data-protection-type="1"]').find('[data-outcome]').exists()).toBe(false)
    expect(wrapper.find('[data-rule-model-match]').exists()).toBe(true)
    expect(wrapper.find('[data-rule-answer]').exists()).toBe(true)
    expect(wrapper.props('modelValue').combinations?.every(rule => rule.action.scheduling === 'keep' && rule.action.group_mode === 'keep')).toBe(true)
    expect(wrapper.props('modelValue').rules).toEqual(original.rules)
    expect(original.mode).toBeUndefined()
    await wrapper.findAll('[data-combination-name]')[0].setValue('My promotion')
    const draft = copyTestProtection(wrapper.props('modelValue')).combinations
    await wrapper.get('[data-protection-mode]').setValue('per_test')
    expect(wrapper.props('modelValue').combinations).toBeUndefined()
    expect(wrapper.get('[data-protection-type="1"] [data-rule-priority]').element).toHaveProperty('value', '7')
    await wrapper.get('[data-protection-mode]').setValue('combined')
    expect(wrapper.props('modelValue').combinations).toEqual(draft)
    wrapper.unmount()
  })

  it('edits nested AND/OR conditions and combined scheduling plus group actions', async () => {
    const original = combined()
    const wrapper = mountEditor(original)
    const rule = wrapper.get('[data-combination-rule="promote"]')
    await rule.get('[data-combination-name]').setValue('Two checks pass')
    await rule.get('[data-combination-priority]').setValue(9)
    await rule.findAll('[data-condition-operator]')[1].setValue('all')
    await rule.findAll('[data-condition-test]')[2].setValue('1')
    await rule.findAll('[data-condition-verdict]')[2].setValue('fail')
    await rule.get('[data-action-scheduling]').setValue('pause')
    await rule.get('[data-action-group="9"]').setValue(true)
    expect(wrapper.props('modelValue').combinations?.[0]).toMatchObject({ name: 'Two checks pass', priority: 9, action: { scheduling: 'pause', group_mode: 'assign', group_ids: [8, 9] }, condition: { conditions: [{}, { operator: 'all', conditions: [{}, { test_definition_id: 1, verdict: 'fail' }] }] } })
    expect(original.combinations?.[0].condition.conditions?.[1].operator).toBe('any')
    expect(original.combinations?.[0].action.group_ids).toEqual([8])
    const root = rule.get('[data-condition-depth="1"]')
    await root.findAll('[data-add-condition-group]').at(-1)!.trigger('click')
    expect(wrapper.props('modelValue').combinations?.[0].condition.conditions).toHaveLength(3)
    const newGroup = rule.findAll('[data-condition-depth="2"]').at(-1)!
    await newGroup.get('[data-condition-operator]').setValue('any')
    await newGroup.get('[data-add-condition]').trigger('click')
    expect(wrapper.props('modelValue').combinations?.[0].condition.conditions?.[2]).toMatchObject({ operator: 'any', conditions: [leaf(), leaf()] })
    await newGroup.get('[data-remove-condition]').trigger('click')
    expect(wrapper.props('modelValue').combinations?.[0].condition.conditions).toHaveLength(2)
    await wrapper.get('[data-add-combination]').trigger('click')
    expect(wrapper.findAll('[data-combination-rule]')).toHaveLength(2)
    await wrapper.findAll('[data-remove-combination]')[1].trigger('click')
    expect(wrapper.findAll('[data-combination-rule]')).toHaveLength(1)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    wrapper.unmount()
  })

  it('retains dangling references and blocks saving until the removed check is restored or repaired', async () => {
    const wrapper = mountEditor(combined())
    await wrapper.get('[data-protection-type="2"] [data-rule-enabled]').setValue(false)
    expect(wrapper.find('[data-missing-condition-test]').exists()).toBe(true)
    expect(wrapper.props('modelValue').combinations?.[0].condition.conditions?.[1].conditions?.[0].test_definition_id).toBe(2)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(false)
    await wrapper.get('[data-protection-type="2"] [data-rule-enabled]').setValue(true)
    expect(wrapper.find('[data-missing-condition-test]').exists()).toBe(false)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    wrapper.unmount()
  })

  it('keeps cache recovery settings and independent pause/resume explicit in combined mode', async () => {
    const config = combined()
    const statistics = config.rules[3]
    statistics.thresholds = [{ metric: 'cache_rate', operator: 'lt', value: 80 }]
    statistics.on_pass = { scheduling: 'resume', group_mode: 'assign', group_ids: [8] }
    statistics.on_fail = { scheduling: 'pause', group_mode: 'assign', group_ids: [9] }
    statistics.recovery = { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 80 }
    const wrapper = mountEditor(config)
    expect(wrapper.find('[data-independent-recovery]').exists()).toBe(true)
    expect(wrapper.find('[data-recovery-cooldown]').exists()).toBe(true)
    await wrapper.get('[data-recovery-cooldown]').setValue(600)
    expect(wrapper.props('modelValue').rules[3].on_pass).toEqual(statistics.on_pass)
    expect(wrapper.props('modelValue').rules[3].on_fail).toEqual(statistics.on_fail)
    expect(statistics.recovery.cooldown_seconds).toBe(300)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    wrapper.unmount()
  })

  it('rejects a combined pause that would prevent its referenced cache recovery trial', async () => {
    const config = combined()
    config.rules[3].thresholds = [{ metric: 'cache_rate', operator: 'lt', value: 80 }]
    config.rules[3].recovery = { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 80 }
    config.combinations![0].condition.conditions![1].conditions!.push(leaf(4))
    config.combinations![0].action.scheduling = 'pause'
    expect(validTestProtection(config, types, groups)).toBe(false)
    const wrapper = mountEditor(config)
    expect(wrapper.find('[data-combination-recovery-conflict]').exists()).toBe(true)
    await wrapper.get('[data-action-scheduling]').setValue('keep')
    expect(wrapper.find('[data-combination-recovery-conflict]').exists()).toBe(false)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    await wrapper.get('[data-action-scheduling]').setValue('resume')
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    await wrapper.get('[data-action-scheduling]').setValue('pause')
    await wrapper.get('[data-recovery-enabled]').setValue(false)
    expect(wrapper.find('[data-combination-recovery-conflict]').exists()).toBe(false)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    wrapper.unmount()
  })

  it('rejects a candy-only pause when a separate resume rule waits for cache recovery', async () => {
    const config = combined()
    config.rules[3].thresholds = [{ metric: 'cache_rate', operator: 'lt', value: 80 }]
    config.rules[3].recovery = { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 80 }
    config.combinations = [
      { id: 'pause-candy', name: 'Pause when candy fails', priority: 1,
        condition: { operator: 'test', test_definition_id: 2, verdict: 'fail' },
        action: { scheduling: 'pause', group_mode: 'keep' } },
      { id: 'resume-healthy', name: 'Resume after candy and cache pass', priority: 1,
        condition: { operator: 'all', conditions: [leaf(2), { operator: 'any', conditions: [leaf(4)] }] },
        action: { scheduling: 'resume', group_mode: 'keep' } },
    ]
    expect(validTestProtection(config, types, groups)).toBe(false)
    const wrapper = mountEditor(config)
    expect(wrapper.get('[data-combination-rule="pause-candy"]').find('[data-combination-recovery-conflict]').exists()).toBe(false)
    expect(wrapper.get('[data-combination-rule="resume-healthy"]').find('[data-combination-recovery-conflict]').exists()).toBe(true)
    await wrapper.get('[data-combination-rule="pause-candy"] [data-action-scheduling]').setValue('keep')
    await wrapper.get('[data-combination-rule="pause-candy"] [data-action-group-mode]').setValue('assign')
    await wrapper.get('[data-combination-rule="pause-candy"] [data-action-group="9"]').setValue(true)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    await wrapper.get('[data-combination-rule="pause-candy"] [data-action-scheduling]').setValue('pause')
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(false)
    const resume = wrapper.get('[data-combination-rule="resume-healthy"]')
    await resume.findAll('[data-condition-test]')[1].setValue('1')
    expect(wrapper.find('[data-combination-recovery-conflict]').exists()).toBe(false)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    await resume.findAll('[data-condition-test]')[1].setValue('4')
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(false)
    await wrapper.get('[data-recovery-enabled]').setValue(false)
    expect(wrapper.find('[data-combination-recovery-conflict]').exists()).toBe(false)
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(true)
    wrapper.unmount()
  })

  it('deep copies nested conditions and actions when editing or copying a strategy', () => {
    const original = combined()
    const copy = copyTestProtection(original)
    copy.combinations![0].condition.conditions![1].conditions![0].verdict = 'fail'
    copy.combinations![0].action.group_ids!.push(9)
    copy.combinations![0].name = 'Different'
    expect(original.combinations![0].condition.conditions![1].conditions![0].verdict).toBe('pass')
    expect(original.combinations![0].action.group_ids).toEqual([8])
    expect(original.combinations![0].name).toBe('All required checks pass')
  })

  it('validates rule references, groups, IDs, priorities, condition shapes and complexity bounds', () => {
    expect(validTestProtection(combined(), types, groups)).toBe(true)
    const invalid: ((config: TestProtectionConfig) => void)[] = [
      config => { config.combinations = [] },
      config => { config.combinations = Array.from({ length: 33 }, (_, i) => ({ ...config.combinations![0], id: String(i) })) },
      config => { config.combinations!.push({ ...config.combinations![0] }) },
      config => { config.combinations![0].id = ' ' },
      config => { config.combinations![0].id = 'a'.repeat(65) },
      config => { config.combinations![0].name = '' },
      config => { config.combinations![0].name = 'a'.repeat(101) },
      config => { config.combinations![0].priority = -1 },
      config => { config.combinations![0].priority = 1001 },
      config => { config.combinations![0].priority = 1.5 },
      config => { config.combinations![0].condition = { operator: 'all', conditions: [] } },
      config => { config.combinations![0].condition = leaf(99) },
      config => { config.combinations![0].condition = { ...leaf(), conditions: [leaf()] } },
      config => { config.combinations![0].condition = { operator: 'all', test_definition_id: 1, conditions: [leaf()] } },
      config => { config.combinations![0].action.group_ids = [99] },
      config => { config.combinations![0].action.group_ids = [] },
      config => { config.combinations![0].condition = { operator: 'all', conditions: Array.from({ length: 128 }, () => leaf()) } },
      config => { config.mode = 'per_test' },
    ]
    for (const mutate of invalid) {
      const config = combined(); mutate(config)
      expect(validTestProtection(config, types, groups)).toBe(false)
    }
    const config = combined()
    config.combinations![0].condition = { operator: 'all', conditions: Array.from({ length: 127 }, () => leaf()) }
    expect(validTestProtection(config, types, groups)).toBe(true)
    let condition = leaf()
    for (let i = 0; i < 5; i++) condition = { operator: 'any', conditions: [condition] }
    config.combinations![0].condition = condition
    expect(validTestProtection(config, types, groups)).toBe(true)
    config.combinations![0].condition = { operator: 'all', conditions: [condition] }
    expect(validTestProtection(config, types, groups)).toBe(false)
  })

  it('ignores dormant per-test routing fields while still validating enabled cache protection', () => {
    const config = combined()
    config.rules[0].priority = -1
    config.rules[1].required_pass = true
    config.rules[1].vote = { enabled: true, reject_above: 3, pass_at_least: 3 }
    config.rules[0].on_pass = { scheduling: 'keep', group_mode: 'assign', group_ids: [99] }
    expect(validTestProtection(config, types, groups)).toBe(true)
    config.rules[3].recovery = { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 80 }
    expect(validTestProtection(config, types, groups)).toBe(false)
  })
})
