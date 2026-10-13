import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestProtectionEditor from '../TestProtectionEditor.vue'
import type { AdminGroup, TestProtectionConfig, TestType } from '@/types'
import { copyTestProtection, defaultProtectionRule, defaultTestProtectionRecovery, validTestProtection } from '@/utils/testProtection'

const types: TestType[] = [
  { id: 1, name: 'Model', key: 'model_check', output_kind: 'model_check', prompt: '', enabled: true },
  { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: '', enabled: true },
  { id: 3, name: 'Statistics', key: 'hourly_stats', output_kind: 'statistics', prompt: '', enabled: true },
  { id: 4, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: '', enabled: true },
]
const groups = [{ id: 8, name: 'Default' }, { id: 10, name: 'Passed' }] as AdminGroup[]
const mountEditor = (config: TestProtectionConfig, selectedTypes = types) => {
  const wrapper = mount(TestProtectionEditor, {
    props: { modelValue: config, types: selectedTypes, groups, 'onUpdate:modelValue': (value: TestProtectionConfig) => wrapper.setProps({ modelValue: value }) },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] },
  })
  return wrapper
}

describe('all automatic checks pass', () => {
  it('preserves thresholds, answers, and recovery while replacing conflicting advanced actions', async () => {
    const original: TestProtectionConfig = { enabled: true, mode: 'per_test', rules: types.map(defaultProtectionRule) }
    original.rules[0].priority = 999
    original.rules[1].expected_answer = '21'
    original.rules[1].on_pass = { scheduling: 'pause', group_mode: 'assign', group_ids: [8] }
    original.rules[2].thresholds = [{ metric: 'cache_rate', operator: 'lt', value: 80 }]
    original.rules[2].recovery = { ...defaultTestProtectionRecovery(original.rules[2]), enabled: true }
    original.rules[3].vote = { enabled: true, public_enabled: true, pass_at_least: 3, reject_above: 0 }
    const wrapper = mountEditor(original)
    await wrapper.get('[data-protection-mode]').setValue('all_pass')
    expect(wrapper.find('[data-all-pass-policy]').exists()).toBe(true)
    for (const selector of ['[data-rule-priority]', '[data-rule-required-pass]', '[data-action-scheduling]', '[data-rule-public-vote]', '[data-combination-rules]']) expect(wrapper.find(selector).exists()).toBe(false)
    const value = wrapper.props('modelValue')
    expect(value.rules[0].priority).toBe(0)
    expect(value.rules[1].expected_answer).toBe('21')
    expect(value.rules[1].on_pass).toEqual({ scheduling: 'resume', group_mode: 'keep' })
    expect(value.rules[2].thresholds).toEqual(original.rules[2].thresholds)
    expect(value.rules[2].recovery).toEqual(original.rules[2].recovery)
    expect(value.rules[3].vote?.public_enabled).toBe(false)
    expect(validTestProtection(value, types, groups)).toBe(true)
    expect(original.rules[1].on_pass?.scheduling).toBe('pause')
    await wrapper.get('[data-action-group-mode]').setValue('assign')
    expect(validTestProtection(wrapper.props('modelValue'), types, groups)).toBe(false)
    await wrapper.get('[data-action-group="10"]').setValue(true)
    const saved = copyTestProtection(wrapper.props('modelValue'))
    expect(saved.pass_group_ids).toEqual([10])
    expect(validTestProtection(saved, types, groups)).toBe(true)
    saved.pass_group_ids?.push(8)
    expect(wrapper.props('modelValue').pass_group_ids).toEqual([10])
    await wrapper.get('[data-protection-mode]').setValue('combined')
    expect(wrapper.props('modelValue').pass_group_ids).toBeUndefined()
    expect(wrapper.findAll('[data-combination-rule]')).toHaveLength(2)
    wrapper.unmount()
  })

  it('lets administrators choose roles without special defaults for built-in check names', async () => {
    const wrapper = mountEditor({ enabled: false, mode: 'all_pass', rules: [] })
    await wrapper.get('[data-protection-enabled]').setValue(true)
    expect(wrapper.props('modelValue').rules.filter(rule => !rule.vote?.enabled)).toHaveLength(4)
    expect(wrapper.find('[data-rule-vote]').exists()).toBe(false)
    for (const id of [1, 3]) {
      expect(wrapper.get(`[data-protection-type="${id}"] [data-rule-role]`).find('option[value="review"]').exists()).toBe(false)
    }
    await wrapper.get('[data-protection-type="4"] [data-rule-role]').setValue('review')
    const value = wrapper.props('modelValue')
    expect(value.rules.filter(rule => !rule.vote?.enabled)).toHaveLength(3)
    expect(value.rules[3].vote).toMatchObject({ enabled: true, public_enabled: false })
    expect(validTestProtection(value, types, groups)).toBe(true)
    expect(validTestProtection({ ...value, rules: [value.rules[3]] }, types, groups)).toBe(false)
    expect(validTestProtection({ ...value, pass_group_ids: [999] }, types, groups)).toBe(false)
    wrapper.unmount()
  })

  it('adds a custom check with an explicit role and retains criteria while switching roles', async () => {
    const custom: TestType = { id: 107, name: 'Custom score', key: 'custom_score', output_kind: 'number', prompt: 'Score', enabled: true }
    const initial: TestProtectionConfig = { enabled: true, mode: 'all_pass', rules: types.map(defaultProtectionRule) }
    const wrapper = mountEditor(initial)
    await wrapper.setProps({ types: [...types, custom] })
    const section = wrapper.get('[data-protection-type="107"]')
    const role = section.get('[data-rule-role]')
    expect((role.element as HTMLSelectElement).value).toBe('observe')
    expect(section.find('[data-rule-answer]').exists()).toBe(false)
    await role.setValue('automatic')
    expect(wrapper.props('modelValue').rules).toHaveLength(5)
    await section.get('[data-rule-answer]').setValue('42')
    await section.get('[data-add-threshold]').trigger('click')
    await section.get('[data-threshold] input').setValue(40)
    const automaticRule = wrapper.props('modelValue').rules.find(rule => rule.test_definition_id === custom.id)!
    await role.setValue('review')
    expect(section.find('[data-threshold]').exists()).toBe(false)
    expect(wrapper.props('modelValue').rules.find(rule => rule.test_definition_id === custom.id)?.vote).toMatchObject({ enabled: true, public_enabled: false })
    await role.setValue('observe')
    expect(wrapper.props('modelValue').rules.some(rule => rule.test_definition_id === custom.id)).toBe(false)
    await role.setValue('automatic')
    const saved = copyTestProtection(wrapper.props('modelValue'))
    const restored = saved.rules.find(rule => rule.test_definition_id === custom.id)!
    expect(restored.expected_answer).toBe('42')
    expect(restored.thresholds).toEqual(automaticRule.thresholds)
    expect(restored.vote?.enabled).toBe(false)
    expect(validTestProtection(saved, [...types, custom], groups)).toBe(true)
    expect(initial.rules).toHaveLength(4)
    wrapper.unmount()

    const reopened = mountEditor(saved, [...types, custom])
    expect((reopened.get('[data-protection-type="107"] [data-rule-role]').element as HTMLSelectElement).value).toBe('automatic')
    expect((reopened.get('[data-protection-type="107"] [data-rule-answer]').element as HTMLTextAreaElement).value).toBe('42')
    reopened.unmount()
  })

  it('allows removing a disabled check from protection without enabling it again', async () => {
    const disabled = { ...types[3], enabled: false }
    const selectedTypes = [types[0], disabled]
    const wrapper = mountEditor({ enabled: true, mode: 'all_pass', rules: selectedTypes.map(defaultProtectionRule) }, selectedTypes)
    expect(validTestProtection(wrapper.props('modelValue'), selectedTypes, groups)).toBe(false)
    const role = wrapper.get('[data-protection-type="4"] [data-rule-role]')
    expect(role.get('option[value="automatic"]').attributes('disabled')).toBeDefined()
    expect(role.get('option[value="review"]').attributes('disabled')).toBeDefined()
    await role.setValue('observe')
    expect(role.attributes('disabled')).toBeDefined()
    expect(validTestProtection(wrapper.props('modelValue'), selectedTypes, groups)).toBe(true)
    wrapper.unmount()
  })
})
