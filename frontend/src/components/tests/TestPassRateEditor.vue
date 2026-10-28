<template>
  <section class="space-y-3 rounded-lg border border-blue-200 bg-blue-50/40 p-3 dark:border-blue-900 dark:bg-blue-950/10" data-pass-rate-editor>
    <label class="flex items-center gap-2 text-sm font-semibold">
      <input :checked="modelValue?.enabled === true" type="checkbox" data-pass-rate-enabled @change="patch({ enabled: ($event.target as HTMLInputElement).checked })" />
      {{ tr('title') }}
    </label>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ tr('description') }}</p>
    <template v-if="modelValue?.enabled">
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="input-label">{{ tr('windowSize') }}<input :value="config.window_size" type="number" min="1" max="1000" step="1" class="input mt-1 w-full" data-pass-rate-window @input="patch({ window_size: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
        <label class="input-label">{{ tr('pauseBelow') }}<input :value="config.pause_below" type="number" min="0" max="100" step="any" class="input mt-1 w-full" data-pass-rate-threshold @input="patch({ pause_below: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
      </div>
      <p class="text-xs text-gray-600 dark:text-gray-300" data-pass-rate-summary>{{ tr('samplingHint', { count: config.window_size, days: config.window_seconds / 86400, threshold: config.pause_below }) }}</p>
      <p v-if="insufficientSchedule" class="text-xs text-amber-700 dark:text-amber-300" role="status" data-pass-rate-schedule-warning>{{ tr('scheduleWarning', { count: capacity, required: config.window_size }) }}</p>
      <details class="rounded-md border border-blue-100 p-3 dark:border-blue-900" data-pass-rate-advanced>
        <summary class="cursor-pointer text-sm font-medium">{{ tr('advanced') }}</summary>
        <div class="mt-3 grid gap-3 sm:grid-cols-3">
          <label class="input-label">{{ tr('lookbackDays') }}<input :value="config.window_seconds / 86400" type="number" :min="1 / 24" max="30" step="any" class="input mt-1 w-full" data-pass-rate-lookback @input="setLookback(($event.target as HTMLInputElement).valueAsNumber)" /></label>
          <label class="input-label">{{ tr('recoverAtLeast') }}<input :value="config.recover_at_least" type="number" :min="config.pause_below" max="100" step="any" class="input mt-1 w-full" data-pass-rate-recovery @input="patch({ recover_at_least: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
          <label class="input-label">{{ tr('recoverConsecutive') }}<input :value="config.recover_consecutive" type="number" min="1" :max="config.window_size" step="1" class="input mt-1 w-full" data-pass-rate-consecutive @input="patch({ recover_consecutive: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
        </div>
        <label class="mt-3 flex items-center gap-2 text-sm"><input :checked="config.pause_on_single_failure" type="checkbox" data-pass-rate-single-failure @change="patch({ pause_on_single_failure: ($event.target as HTMLInputElement).checked })" />{{ tr('singleFailure') }}</label>
        <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ tr('existingActionsHint') }}</p>
      </details>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ tr('recoveryHint', { count: config.window_size, rate: config.recover_at_least, consecutive: config.recover_consecutive }) }}</p>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ tr('validSamplesHint') }}</p>
      <p v-if="!validTestPassRate(config, outputKind)" class="text-xs text-red-600 dark:text-red-400" role="alert" data-pass-rate-invalid>{{ tr('invalid') }}</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestPassRateConfig, TestSchedule } from '@/types'
import { defaultTestPassRate, passRateScheduleCapacity, validTestPassRate } from '@/utils/testPassRate'

const props = defineProps<{ modelValue?: TestPassRateConfig; outputKind: string; schedule?: TestSchedule | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: TestPassRateConfig] }>()
const { t } = useI18n()
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.protection.passRate.${key}`, values)
const config = computed(() => props.modelValue || defaultTestPassRate())
const capacity = computed(() => passRateScheduleCapacity(config.value, props.schedule))
const insufficientSchedule = computed(() => capacity.value !== null && capacity.value < config.value.window_size)
const patch = (value: Partial<TestPassRateConfig>) => emit('update:modelValue', { ...config.value, ...value })
const setLookback = (days: number) => patch({ window_seconds: Number.isFinite(days) ? Math.round(days * 86400) : days })
</script>
