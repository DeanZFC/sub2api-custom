import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestGroupWorkflowEditor from '../TestGroupWorkflowEditor.vue'
import type { AdminGroup, TestGroupWorkflow, TestType } from '@/types'
import { copyTestProtection, groupWorkflowProtection, validTestGroupWorkflow } from '@/utils/testProtection'

const types: TestType[] = [
  { id: 1, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true },
  { id: 2, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
]
const groups = [{ id: 8, name: 'Premium', platform: 'openai' }, { id: 9, name: 'Pro', platform: 'openai' }] as AdminGroup[]
const workflow: TestGroupWorkflow = { automatic_test_id: 1, review_test_id: 2, pass_group_id: 8, fail_group_id: 9 }
const mountEditor = (value: TestGroupWorkflow = { ...workflow }) => {
  const wrapper = mount(TestGroupWorkflowEditor, {
    props: { modelValue: value, types, groups, 'onUpdate:modelValue': (next: TestGroupWorkflow) => wrapper.setProps({ modelValue: next }) },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] },
  })
  return wrapper
}

describe('group workflow user voting', () => {
  it('defaults private and keeps administrator review enabled when user voting is closed', async () => {
    const wrapper = mountEditor()
    expect((wrapper.get('[data-workflow-public-vote]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.find('[data-workflow-vote-pass]').exists()).toBe(false)
    await wrapper.get('[data-workflow-public-vote]').setValue(true)
    expect(wrapper.props('modelValue').review_vote).toEqual({ enabled: true, public_enabled: true, reject_above: 0, pass_at_least: 1 })
    await wrapper.get('[data-workflow-vote-reject]').setValue(5)
    await wrapper.get('[data-workflow-vote-pass]').setValue(3)
    await wrapper.get('[data-workflow-public-vote]').setValue(false)
    expect(wrapper.props('modelValue').review_vote).toEqual({ enabled: true, public_enabled: false, reject_above: 5, pass_at_least: 3 })
    expect(wrapper.find('[data-workflow-vote-pass]').exists()).toBe(false)
    expect(validTestGroupWorkflow(wrapper.props('modelValue'), types, groups)).toBe(true)
    expect(workflow.review_vote).toBeUndefined()
    wrapper.unmount()
  })

  it('validates public vote thresholds and copies them separately from canonical rules', async () => {
    const value = { ...workflow, review_vote: { enabled: true, public_enabled: true, reject_above: 5, pass_at_least: 3 } }
    const wrapper = mountEditor(value)
    await wrapper.get('[data-workflow-vote-pass]').setValue(0)
    expect(validTestGroupWorkflow(wrapper.props('modelValue'), types, groups)).toBe(false)
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    await wrapper.get('[data-workflow-vote-pass]').setValue(3)
    const config = groupWorkflowProtection(wrapper.props('modelValue'))
    expect(config.rules[0].vote).toBeUndefined()
    expect(config.rules[0]).toMatchObject({ answer_match: 'numeric', expected_answer: '21' })
    expect(config.rules[1].vote).toEqual(value.review_vote)
    config.rules[1].vote!.pass_at_least = 10
    expect(config.group_workflow!.review_vote!.pass_at_least).toBe(3)
    const copy = copyTestProtection(config)
    copy.group_workflow!.review_vote!.public_enabled = false
    expect(config.group_workflow!.review_vote!.public_enabled).toBe(true)
    expect(value.review_vote.public_enabled).toBe(true)
    wrapper.unmount()
  })
})
