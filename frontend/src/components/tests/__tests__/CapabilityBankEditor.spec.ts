import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import CapabilityBankEditor from '../CapabilityBankEditor.vue'
import CapabilityResultOutput from '../CapabilityResultOutput.vue'
import { defaultCapabilityConfig, parseCapabilityConfig, validCapabilityConfig } from '@/utils/qualityCapability'
const global = { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] }
describe('capability bank', () => {
  it('edits a structured default and rejects impossible pass counts', async () => {
    const w = mount(CapabilityBankEditor, { props: { modelValue: '' }, global }); await flushPromises()
    const initial = parseCapabilityConfig(w.emitted('update:modelValue')![0][0] as string)
    expect(validCapabilityConfig(initial)).toBe(true)
    await w.findAll('input[type="number"]')[1].setValue(4)
    expect(w.find('[role="alert"]').exists()).toBe(true); w.unmount()
  })
  it('rejects malformed, duplicate and out-of-range reference questions', () => {
    expect(parseCapabilityConfig('{"version":1,"families":[],"questions":[null]}')).toBeNull()
    const c = defaultCapabilityConfig(); c.families = []; c.samples = 1; c.required_passes = 1
    c.questions = [{ key: 'one', prompt: 'question', expected: '9223372036854775808', match: 'number' }]
    expect(validCapabilityConfig(c)).toBe(false); c.questions[0].expected = '+007'; expect(validCapabilityConfig(c)).toBe(true)
    c.questions.push({ ...c.questions[0] }); expect(validCapabilityConfig(c)).toBe(false)
  })
  it('renders a wrong answer as text with its reference for review', () => {
    const w = mount(CapabilityResultOutput, { global, props: { result: { version: 1, seed: '123', samples: 3, required_passes: 2, passed: 0, verdict: 'fail', answers: [{ key: 'one', prompt: 'Count', expected: '7', match: 'number', answer: '<script>bad</script>', verdict: 'fail' }] } } })
    expect(w.text()).toContain('<script>bad</script>'); expect(w.find('script').exists()).toBe(false); expect(w.text()).toContain('7'); w.unmount()
  })
})
