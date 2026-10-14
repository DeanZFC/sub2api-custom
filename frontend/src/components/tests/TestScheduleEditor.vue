<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700" data-test-schedule>
    <label class="input-label block">{{ t('admin.tests.timing.title') }}
      <select :value="schedule ? 'periods' : 'cron'" class="input mt-1 w-full" data-schedule-mode @change="setMode(($event.target as HTMLSelectElement).value)">
        <option value="periods">{{ t('admin.tests.timing.periodMode') }}</option>
        <option value="cron">{{ t('admin.tests.timing.cronMode') }}</option>
      </select>
    </label>
    <template v-if="schedule">
      <label class="input-label block">{{ t('admin.tests.timing.timezone') }}
        <input :value="schedule.timezone" type="text" class="input mt-1 w-full" placeholder="Asia/Shanghai" data-schedule-timezone @input="patch({ timezone: ($event.target as HTMLInputElement).value.trim() })" />
        <span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.timing.timezoneHint') }}</span>
      </label>
      <div v-for="(period, index) in schedule.periods" :key="index" class="space-y-2 rounded-md bg-gray-50 p-3 dark:bg-dark-800" data-schedule-period>
        <div class="grid items-end gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.2fr)_auto]">
          <label class="input-label block">{{ t('admin.tests.timing.start') }}<input :value="period.start" type="time" step="60" class="input mt-1 w-full" data-period-start @input="patchPeriod(index, { start: ($event.target as HTMLInputElement).value })" /></label>
          <label class="input-label block">{{ t('admin.tests.timing.end') }}<input :value="period.end" type="text" placeholder="20:00" class="input mt-1 w-full" data-period-end @input="patchPeriod(index, { end: ($event.target as HTMLInputElement).value.trim() })" /></label>
          <label class="input-label block">{{ t('admin.tests.timing.interval') }}
            <div class="mt-1 flex gap-2"><input :value="period.interval_minutes / unitFor(index)" type="number" min="1" :max="unitFor(index) === 60 ? 24 : 1440" step="1" class="input min-w-0 w-full" data-period-interval @input="patchPeriod(index, { interval_minutes: ($event.target as HTMLInputElement).valueAsNumber * unitFor(index) })" />
              <select :value="unitFor(index)" class="input w-20 shrink-0" data-period-unit :aria-label="t('admin.tests.timing.intervalUnit')" @change="setUnit(index, Number(($event.target as HTMLSelectElement).value))"><option :value="60">{{ t('admin.tests.timing.hours') }}</option><option :value="1">{{ t('admin.tests.timing.minutes') }}</option></select>
            </div>
          </label>
          <button type="button" class="btn btn-secondary" :disabled="schedule.periods.length === 1" :aria-label="t('admin.tests.timing.removePeriod', { number: index + 1 })" data-remove-period @click="removePeriod(index)">{{ t('common.remove') }}</button>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400" data-period-preview>{{ t('admin.tests.timing.times') }} {{ preview(period) }}<span v-if="period.end !== '24:00' && period.end < period.start" class="ml-2">{{ t('admin.tests.timing.overnight') }}</span></p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="schedule.periods.length >= 24" data-add-period @click="addPeriod">{{ t('admin.tests.timing.addPeriod') }}</button>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.timing.periodHint') }}</p>
      <p v-if="error" role="alert" class="text-xs text-red-600 dark:text-red-400" data-schedule-error>{{ t(`admin.tests.timing.${error}`) }}</p>
    </template>
    <label v-else class="input-label block">{{ t('admin.tests.cron') }}<input :value="cronExpression" class="input mt-1 w-full" placeholder="0 * * * *" data-schedule-cron @input="emit('update:cronExpression', ($event.target as HTMLInputElement).value.trim())" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.cronHint') }}</span></label>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestSchedule, TestSchedulePeriod } from '@/types'
import { copyTestSchedule, defaultTestSchedule, testScheduleError, testScheduleTimes } from '@/utils/testSchedule'

const props = defineProps<{ schedule?: TestSchedule | null; cronExpression?: string }>()
const emit = defineEmits<{ 'update:schedule': [value: TestSchedule | null]; 'update:cronExpression': [value: string] }>()
const { t } = useI18n()
const units = ref<Record<number, number>>({})
const savedPeriods = ref<TestSchedule | null>(null)
const error = computed(() => props.schedule ? testScheduleError(props.schedule) : null)
const unitFor = (index: number) => units.value[index] ?? ((props.schedule?.periods[index]?.interval_minutes ?? 60) % 60 === 0 ? 60 : 1)
const patch = (value: Partial<TestSchedule>) => emit('update:schedule', { ...copyTestSchedule(props.schedule)!, ...value })
const patchPeriod = (index: number, value: Partial<TestSchedulePeriod>) => {
  const schedule = copyTestSchedule(props.schedule)!
  schedule.periods[index] = { ...schedule.periods[index], ...value }
  emit('update:schedule', schedule)
}
const setMode = (mode: string) => {
  if (mode === 'cron') {
    savedPeriods.value = copyTestSchedule(props.schedule)
    emit('update:schedule', null)
    if (!props.cronExpression?.trim()) emit('update:cronExpression', '0 * * * *')
  } else emit('update:schedule', copyTestSchedule(savedPeriods.value) || defaultTestSchedule())
}
const setUnit = (index: number, unit: number) => {
  const value = props.schedule!.periods[index].interval_minutes / unitFor(index)
  units.value[index] = unit
  patchPeriod(index, { interval_minutes: value * unit })
}
const addPeriod = () => {
  const periods = props.schedule!.periods
  patch({ periods: [...periods, { start: periods[periods.length - 1]?.end === '24:00' ? '00:00' : periods[periods.length - 1]?.end || '08:00', end: '20:00', interval_minutes: 60 }] })
}
const removePeriod = (index: number) => {
  units.value = {}
  patch({ periods: props.schedule!.periods.filter((_, i) => i !== index) })
}
const preview = (period: TestSchedulePeriod) => {
  const times = testScheduleTimes(period)
  return times.length > 6 ? [...times.slice(0, 5), '…', times[times.length - 1]].join(' · ') : times.join(' · ') || '—'
}
</script>
