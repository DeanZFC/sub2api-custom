<template>
  <div class="space-y-2 rounded-lg border border-gray-200 p-3 text-xs dark:border-dark-700" data-pass-rate-evidence>
    <div class="flex flex-wrap items-center gap-2"><strong>{{ tr('currentWindow') }}</strong><span class="badge" :class="evidence.blocked ? 'badge-danger' : evidence.stale || !evidence.ready ? 'badge-warning' : 'badge-success'" data-pass-rate-state>{{ tr(`states.${state}`) }}</span></div>
    <p class="tabular-nums" data-pass-rate-counts>{{ tr('counts', { passed: evidence.passed, samples: evidence.samples, rate: rate }) }}</p>
    <p v-if="!evidence.ready" data-pass-rate-collecting>{{ tr('collectingHint', { remaining: Math.max(0, evidence.window_size - evidence.samples), required: evidence.window_size }) }}</p>
    <p v-if="evidence.stale" class="text-amber-700 dark:text-amber-300" data-pass-rate-stale>{{ tr('staleHint') }}</p>
    <p v-if="evidence.blocked" data-pass-rate-recovery-requirement>{{ tr('recoveryRequirement', { count: evidence.window_size, rate: evidence.recover_at_least, consecutive: evidence.recover_consecutive, current: evidence.consecutive_passes }) }}</p>
    <p v-if="evidence.reason" class="break-words text-gray-600 dark:text-gray-300" data-pass-rate-reason>{{ reason }}</p>
    <details class="text-gray-500 dark:text-gray-400">
      <summary class="cursor-pointer">{{ tr('evidenceDetails') }}</summary>
      <p class="mt-2">{{ tr('samplingHint', { count: evidence.window_size, days: evidence.window_seconds / 86400, threshold: evidence.pause_below }) }}</p>
      <p class="mt-1">{{ tr('latestVerdict') }}: {{ verdict }} · {{ latestAt }}</p>
      <p class="mt-1 break-all">{{ tr('modelAndVersion', { model: evidence.model || '—', version: evidence.rule_version || '—' }) }}</p>
      <p class="mt-1">{{ tr('currentWindowHint') }}</p>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestPassRateSummary } from '@/types'

const props = defineProps<{ evidence: TestPassRateSummary }>()
const { t } = useI18n()
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.protection.passRate.${key}`, values)
const state = computed(() => props.evidence.blocked ? 'blocked' : props.evidence.stale ? 'stale' : props.evidence.ready ? 'ready' : 'collecting')
const rate = computed(() => props.evidence.pass_rate == null || !Number.isFinite(props.evidence.pass_rate) ? '—' : `${Math.round(props.evidence.pass_rate * 10) / 10}%`)
const verdict = computed(() => tr(`verdicts.${['pass', 'fail'].includes(props.evidence.latest_verdict) ? props.evidence.latest_verdict : 'pending'}`))
const latestAt = computed(() => {
  const value = props.evidence.latest_at
  return value && !value.startsWith('0001') && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : '—'
})
const knownReasons = new Set(['pass_rate_below_threshold', 'single_failure', 'insufficient_samples', 'stale_evidence', 'recovery_pending', 'recovered', 'healthy', 'pending_verdict'])
const reason = computed(() => knownReasons.has(props.evidence.reason) ? tr(`reasons.${props.evidence.reason}`) : props.evidence.reason)
</script>
