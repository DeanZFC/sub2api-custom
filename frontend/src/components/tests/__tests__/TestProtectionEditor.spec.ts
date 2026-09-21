import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestProtectionEditor from '../TestProtectionEditor.vue'
import type { TestProtectionConfig, TestType } from '@/types'
import { validTestProtection } from '@/utils/testProtection'

const types: TestType[] = [
  { id: 1, name: 'Statistics', key: 'stats', output_kind: 'statistics', prompt: '', enabled: true },
  { id: 2, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
  { id: 3, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true },
]
const mountEditor = (config: TestProtectionConfig = { enabled: false, rules: [] }, targetMode = 'account') => {
  const wrapper = mount(TestProtectionEditor, {
    props: { modelValue: config, types, targetMode, 'onUpdate:modelValue': (value: TestProtectionConfig) => wrapper.setProps({ modelValue: value }) },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] },
  })
  return wrapper
}

describe('quality protection settings', () => {
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
    expect(validTestProtection(wrapper.props('modelValue'), 'account', types)).toBe(false)
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

})
