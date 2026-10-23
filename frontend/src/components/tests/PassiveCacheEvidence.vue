<template>
  <div class="space-y-3" data-cache-evidence>
    <article v-for="[model, state] in Object.entries(cache || {})" :key="model" class="rounded-lg border border-gray-200 p-3 text-xs dark:border-dark-700">
      <div class="flex flex-wrap items-center gap-2"><strong>{{ model }}</strong><span class="badge badge-gray">{{ phase(state.phase) }}</span><strong>{{ v('allocation', { value: state.allocation_percent }) }}</strong></div>
      <p class="mt-1 text-gray-500">{{ reason(state.reason) }}</p>
      <p class="mt-1">{{ v('progress', { quick: state.quick_hits, quickTarget: config?.quick.confirmations ?? '—', action: state.hits, actionTarget: config?.action_window.confirmations ?? '—', recovery: state.recovery_hits, recoveryTarget: config?.recovery_confirmations ?? '—' }) }}</p>
      <p v-if="state.hold_until && !state.hold_until.startsWith('0001')" class="mt-1">{{ v('holdUntil') }}: {{ date(state.hold_until) }}</p>
      <div v-for="kind in windowKinds" :key="kind" class="mt-3 space-y-1" :data-evidence-window="kind">
        <strong>{{ v(kind === 'quick' ? 'quick' : 'action_window') }}</strong>
        <template v-if="state.evidence?.[kind]">
          <p>{{ v('windowEvidence', { seconds: state.evidence[kind].window_seconds, max: state.evidence[kind].max_samples, fresh: state.evidence[kind].fresh }) }}</p>
          <p>{{ v('actualRate') }}: {{ metric(state.evidence[kind].cache_rate) }} · {{ v('protectionRate') }}: {{ metric(state.evidence[kind].protection_rate) }}</p>
          <p>{{ v('sampleEvidence', { total: state.evidence[kind].total_samples, reusable: state.evidence[kind].reusable_samples, cold: state.evidence[kind].cold_samples, unknown: state.evidence[kind].unknown_samples, missing: state.evidence[kind].missing_telemetry_samples }) }}</p>
          <p>{{ v('tokenEvidence', { read: state.evidence[kind].eligible_read_tokens, input: state.evidence[kind].eligible_input_tokens, actualRead: state.evidence[kind].read_tokens, actualInput: state.evidence[kind].input_tokens }) }}</p>
          <p>{{ v('sessionEvidence', { sessions: state.evidence[kind].sessions, concentration: state.evidence[kind].max_session_share_percent, breached: state.evidence[kind].breached_sessions ?? '—', healthy: state.evidence[kind].healthy_sessions ?? '—' }) }}</p>
          <div class="rounded border border-gray-100 p-2 dark:border-dark-800" data-cache-impact>
            <p>{{ v('impactEvidence', { switches: state.evidence[kind].account_switches ?? '—', ttft: duration(state.evidence[kind].ttft_p90), ttftSamples: state.evidence[kind].ttft_samples ?? '—', cost: cost(state.evidence[kind].input_cost), costSamples: state.evidence[kind].input_cost_samples ?? '—', total: state.evidence[kind].total_samples }) }}</p>
            <p class="mt-1 text-gray-500">{{ v('impactScope') }}</p>
          </div>
          <p v-for="[key, count] in Object.entries(state.evidence[kind].reasons || {})" :key="key" class="text-gray-500">{{ reason(key) }}: {{ count }}</p>
          <p v-if="!state.evidence[kind].complete" class="text-amber-600">{{ t('admin.tests.passive.telemetryIncomplete') }}</p>
        </template>
      </div>
    </article>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PassiveCacheProtection, PassiveCacheState } from '@/api/admin/passiveQuality'
import { useQualityLabels } from '@/composables/useQualityLabels'
defineProps<{ cache?: Record<string, PassiveCacheState>; config?: PassiveCacheProtection }>()
const { t, te } = useI18n(), { reason, date } = useQualityLabels()
const v = (key: string, args: Record<string, unknown> = {}) => t('admin.tests.passive.v2.' + key, args)
const phase = (key: string) => te('admin.tests.passive.v2.phases.' + key) ? v('phases.' + key) : key
const metric = (value?: number | null) => value == null ? '—' : `${Number(value.toFixed(2))}%`
const duration = (value?: number | null) => value == null ? '—' : Number(value.toFixed(2)) + ' ms'
const cost = (value?: number | null) => value == null ? '—' : '$' + value.toFixed(6)
const windowKinds = ['quick', 'action'] as const
</script>
