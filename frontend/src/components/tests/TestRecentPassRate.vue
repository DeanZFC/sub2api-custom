<template>
  <div class="rounded-md bg-gray-50 p-2.5 text-xs dark:bg-dark-800/60" data-recent-pass-rate>
    <div v-if="allPassProgress && allPassProgress.required > 1" class="mb-2 flex flex-wrap items-center gap-2" data-all-pass-progress>
      <p class="font-medium tabular-nums text-gray-700 dark:text-gray-200">{{ t('admin.tests.protection.allPass.consecutiveProgress', { current: allPassProgress.consecutive_passes, required: allPassProgress.required }) }}</p>
      <span class="rounded px-1.5 py-0.5 font-medium" :class="allPassProgress.round_verdict === 'fail' ? 'bg-red-100 text-red-800 dark:bg-red-950/50 dark:text-red-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">{{ t(`admin.tests.protection.allPass.progressStates.${roundProgressState}`) }}</span>
    </div>
    <template v-if="evidence">
      <div class="flex flex-wrap items-center gap-2">
        <p class="font-medium tabular-nums text-gray-700 dark:text-gray-200" data-recent-pass-rate-counts>{{ tr('recentCounts', { count: evidence.window_size, window, passed: evidence.passed, rate }) }}</p>
        <span class="rounded px-1.5 py-0.5 font-medium" :class="stateClass" data-recent-pass-rate-state>{{ stateLabel }}</span>
        <span v-if="state !== 'collecting' && collecting" class="rounded bg-amber-100 px-1.5 py-0.5 font-medium text-amber-800 dark:bg-amber-950/50 dark:text-amber-300" data-recent-pass-rate-progress>{{ progress }}</span>
      </div>
      <details class="mt-1 text-gray-500 dark:text-gray-400">
        <summary class="w-fit cursor-pointer">{{ tr('evidenceDetails') }}</summary>
        <div class="mt-2 space-y-1" data-recent-pass-rate-details>
          <p>{{ tr('recentPauseRule', { threshold: evidence.pause_below }) }}</p>
          <p v-if="evidence.stale" data-pass-rate-stale>{{ tr('staleHint') }}</p>
          <p v-if="evidence.blocked" data-pass-rate-recovery-requirement>{{ tr('recoveryRequirement', { count: evidence.window_size, rate: evidence.recover_at_least, consecutive: evidence.recover_consecutive, current: evidence.consecutive_passes }) }}</p>
          <p v-if="evidence.reason" class="break-words" data-pass-rate-reason>{{ reason }}</p>
          <p>{{ tr('latestVerdict') }}: {{ verdict }} · {{ latestAt }}</p>
          <p class="break-all">{{ tr('modelAndVersion', { model: evidence.model || '—', version: evidence.rule_version || '—' }) }}</p>
          <p>{{ tr('currentWindowHint') }}</p>
        </div>
      </details>
    </template>
    <p v-else-if="!allPassProgress || allPassProgress.required <= 1" class="text-gray-500 dark:text-gray-400" data-recent-pass-rate-unavailable>{{ tr('recentUnavailable') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestAllPassProgress, TestPassRateSummary } from '@/types'

const props = defineProps<{ evidence?: TestPassRateSummary | null; allPassProgress?: TestAllPassProgress | null }>()
const { t } = useI18n()
const roundProgressState = computed(() => props.allPassProgress?.round_verdict === 'fail' ? 'failed'
  : props.allPassProgress?.round_verdict !== 'pass' ? 'pending'
    : props.allPassProgress.consecutive_passes >= props.allPassProgress.required ? 'ready' : 'collecting')
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.protection.passRate.${key}`, values)
const rate = computed(() => {
  const value = props.evidence?.pass_rate
  return value == null || !Number.isFinite(value) ? '—' : `${Math.round(value * 10) / 10}%`
})
const window = computed(() => {
  const seconds = props.evidence?.window_seconds || 0
  for (const [unit, duration] of [['Days', 86400], ['Hours', 3600], ['Minutes', 60], ['Seconds', 1]] as const) {
    if (seconds >= duration && seconds % duration === 0) return tr(`recentWindow${unit}`, { count: seconds / duration })
  }
  return '—'
})
const collecting = computed(() => !!props.evidence && (!props.evidence.ready || props.evidence.samples < props.evidence.window_size))
const progress = computed(() => tr('recentProgress', { samples: props.evidence?.samples ?? 0, count: props.evidence?.window_size ?? 0 }))
const state = computed(() => props.evidence?.blocked ? 'blocked' : props.evidence?.stale ? 'stale' : collecting.value ? 'collecting' : rate.value === '—' ? 'unknown' : 'ready')
const stateLabel = computed(() => state.value === 'collecting' ? progress.value : tr(`states.${state.value}`))
const stateClass = computed(() => state.value === 'blocked'
  ? 'bg-red-100 text-red-800 dark:bg-red-950/50 dark:text-red-300'
  : ['stale', 'collecting'].includes(state.value)
    ? 'bg-amber-100 text-amber-800 dark:bg-amber-950/50 dark:text-amber-300'
    : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300')
const verdict = computed(() => tr(`verdicts.${['pass', 'fail'].includes(props.evidence?.latest_verdict || '') ? props.evidence!.latest_verdict : 'pending'}`))
const latestAt = computed(() => {
  const value = props.evidence?.latest_at
  return value && !value.startsWith('0001') && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : '—'
})
const knownReasons = new Set(['pass_rate_below_threshold', 'single_failure', 'insufficient_samples', 'stale_evidence', 'recovery_pending', 'recovered', 'healthy', 'pending_verdict'])
const reason = computed(() => knownReasons.has(props.evidence?.reason || '') ? tr(`reasons.${props.evidence!.reason}`) : props.evidence?.reason || '')
</script>
