import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import type { TestPassRateConfig } from '@/types'
import resources from '@/i18n/locales/en/admin/resources'
import { defaultTestPassRate } from '@/utils/testPassRate'
import TestPassRateEditor from '../TestPassRateEditor.vue'

const mountEditor = (config?: TestPassRateConfig) => {
  const wrapper = mount(TestPassRateEditor, {
    props: { outputKind: 'number', modelValue: config, 'onUpdate:modelValue': (value: TestPassRateConfig) => wrapper.setProps({ modelValue: value }) },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: resources } } })] },
  })
  return wrapper
}

describe('recent capability pass-rate editor', () => {
  it('opts in explicitly and keeps edited settings when disabled and re-enabled', async () => {
    const wrapper = mountEditor()
    expect(wrapper.get<HTMLInputElement>('[data-pass-rate-enabled]').element.checked).toBe(false)
    expect(wrapper.find('[data-pass-rate-window]').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.get('[data-pass-rate-enabled]').setValue(true)
    expect(wrapper.props('modelValue')).toEqual(defaultTestPassRate())
    await wrapper.get('[data-pass-rate-window]').setValue(20)
    await wrapper.get('[data-pass-rate-threshold]').setValue(75)
    await wrapper.get('[data-pass-rate-lookback]').setValue(3)
    await wrapper.get('[data-pass-rate-single-failure]').setValue(true)
    expect(wrapper.props('modelValue')).toMatchObject({ window_size: 20, pause_below: 75, window_seconds: 259200, pause_on_single_failure: true })
    await wrapper.get('[data-pass-rate-enabled]').setValue(false)
    expect(wrapper.props('modelValue')?.window_size).toBe(20)
    await wrapper.get('[data-pass-rate-enabled]').setValue(true)
    expect(wrapper.props('modelValue')?.window_size).toBe(20)
    wrapper.unmount()
  })

  it('explains invalid recovery settings and warns when a schedule cannot fill the window', async () => {
    const original = defaultTestPassRate()
    const wrapper = mountEditor(original)
    await wrapper.setProps({ schedule: { timezone: 'Asia/Shanghai', periods: [{ start: '08:00', end: '09:00', interval_minutes: 60 }] } })
    expect(wrapper.get('[data-pass-rate-schedule-warning]').text()).toContain('at most 8 checks')
    await wrapper.get('[data-pass-rate-recovery]').setValue(70)
    expect(wrapper.find('[data-pass-rate-invalid]').exists()).toBe(true)
    await wrapper.get('[data-pass-rate-recovery]').setValue(90)
    expect(wrapper.find('[data-pass-rate-invalid]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Network or service execution errors are excluded')
    expect(wrapper.text()).toContain('an invalid answer format counts as a failure')
    expect(wrapper.text()).toContain('Turning this option off does not disable those actions')
    expect(original).toEqual(defaultTestPassRate())
    wrapper.unmount()
  })

  it.each([
    ['window', 'window_size'], ['threshold', 'pause_below'], ['lookback', 'window_seconds'],
    ['recovery', 'recover_at_least'], ['consecutive', 'recover_consecutive'],
  ] as const)('keeps an empty %s field invalid instead of silently applying a default', async (selector, key) => {
    const wrapper = mountEditor(defaultTestPassRate())
    await wrapper.get(`[data-pass-rate-${selector}]`).setValue('')
    expect(Number.isNaN(wrapper.props('modelValue')?.[key])).toBe(true)
    expect(wrapper.find('[data-pass-rate-invalid]').exists()).toBe(true)
    wrapper.unmount()
  })
})
