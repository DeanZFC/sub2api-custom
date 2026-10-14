import { mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import type { TestSchedule } from '@/types'
import TestScheduleEditor from '../TestScheduleEditor.vue'
import { defaultTestSchedule, testScheduleError, testScheduleTimes } from '@/utils/testSchedule'

const makeWrapper = (initial: TestSchedule | null = defaultTestSchedule()) => mount(defineComponent({
  components: { TestScheduleEditor },
  setup() { return { schedule: ref(initial), cron: ref('0 * * * *') } },
  template: '<TestScheduleEditor v-model:schedule="schedule" v-model:cron-expression="cron" />',
}), { global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] } })

describe('test time periods', () => {
  it('previews anchored daytime and overnight executions', () => {
    const schedule = defaultTestSchedule()
    expect(testScheduleError(schedule)).toBeNull()
    expect(testScheduleTimes(schedule.periods[0])).toEqual(Array.from({ length: 12 }, (_, i) => `${String(i + 8).padStart(2, '0')}:00`))
    expect(testScheduleTimes(schedule.periods[1])).toEqual(['20:00', '23:00', '02:00', '05:00'])
    expect(testScheduleTimes({ start: '23:15', end: '06:00', interval_minutes: 90 })).toEqual(['23:15', '00:45', '02:15', '03:45', '05:15'])
    const wrapper = makeWrapper()
    expect(wrapper.findAll('[data-schedule-period]')).toHaveLength(2)
    expect(wrapper.findAll('[data-period-preview]')[1].text()).toContain('20:00 · 23:00 · 02:00 · 05:00')
    expect(wrapper.find('[data-schedule-error]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('rejects overlaps across midnight, empty ranges and invalid intervals', () => {
    const schedule = defaultTestSchedule()
    schedule.periods[1].end = '08:01'
    expect(testScheduleError(schedule)).toBe('overlapError')
    schedule.periods = [{ start: '00:00', end: '24:00', interval_minutes: 180 }]
    expect(testScheduleError(schedule)).toBeNull()
    schedule.periods[0].end = '00:00'
    expect(testScheduleError(schedule)).toBe('timeError')
    schedule.periods[0].end = '01:00'
    expect(testScheduleError(schedule)).toBe('intervalError')
    schedule.periods[0].interval_minutes = 0
    expect(testScheduleError(schedule)).toBe('intervalError')
    schedule.periods[0].interval_minutes = 1.5
    expect(testScheduleError(schedule)).toBe('intervalError')
    schedule.timezone = 'Mars/Test'
    expect(testScheduleError(schedule)).toBe('timezoneError')
  })

  it('edits units and preserves both modes while switching', async () => {
    const wrapper = makeWrapper(null)
    expect(wrapper.get('[data-schedule-cron]').element).toHaveProperty('value', '0 * * * *')
    await wrapper.get('[data-schedule-mode]').setValue('periods')
    await wrapper.findAll('[data-period-unit]')[0].setValue('1')
    await wrapper.findAll('[data-period-interval]')[0].setValue(30)
    expect(wrapper.vm.schedule!.periods[0].interval_minutes).toBe(30)
    await wrapper.get('[data-schedule-mode]').setValue('cron')
    expect(wrapper.vm.schedule).toBeNull()
    await wrapper.get('[data-schedule-cron]').setValue('15 * * * *')
    await wrapper.get('[data-schedule-mode]').setValue('periods')
    expect(wrapper.vm.schedule!.periods[0].interval_minutes).toBe(30)
    expect(wrapper.vm.cron).toBe('15 * * * *')
    wrapper.unmount()
  })

  it('validates changes immediately and supports adding and removing ranges', async () => {
    const wrapper = makeWrapper()
    await wrapper.findAll('[data-period-end]')[1].setValue('09:00')
    expect(wrapper.get('[data-schedule-error]').text()).toContain('overlapError')
    await wrapper.findAll('[data-remove-period]')[1].trigger('click')
    expect(wrapper.find('[data-schedule-error]').exists()).toBe(false)
    expect(wrapper.findAll('[data-remove-period]')[0].attributes('disabled')).toBeDefined()
    await wrapper.get('[data-add-period]').trigger('click')
    await wrapper.findAll('[data-period-end]')[1].setValue('08:00')
    await wrapper.findAll('[data-period-interval]')[1].setValue(3)
    expect(wrapper.vm.schedule).toEqual(defaultTestSchedule())
    wrapper.unmount()
  })
})
