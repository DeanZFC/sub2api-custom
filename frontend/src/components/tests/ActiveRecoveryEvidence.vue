<template>
  <section class="mt-2 space-y-1 rounded border border-primary-200 p-2 text-xs dark:border-primary-900" data-active-recovery-evidence>
    <strong>{{ tr('activeEvidence') }} · {{ tr('activeVerdicts.' + result.verdict) }}</strong>
    <p>{{ tr('activeEvidenceCounts', { requests: result.requests, warmup: result.warmup_requests, batches: result.batches }) }}</p>
    <p>{{ tr('availability') }}: {{ metric(evidence.error_rate == null ? null : 100 - evidence.error_rate, '%') }} · {{ tr('cache') }}: {{ metric(evidence.cache_rate, '%') }}</p>
    <p>{{ tr('ttft') }}: {{ metric(evidence.ttft == null ? null : evidence.ttft / 1000, ' s') }} · {{ tr('tps') }}: {{ metric(evidence.output_tps, ' token/s') }}</p>
    <p v-if="result.reason">{{ reason(result.reason) }}</p>
    <p>{{ date(result.started_at) }} — {{ date(result.finished_at) }}</p>
    <p v-if="result.deferred_until">{{ tr('nextActiveRecovery', { date: date(result.deferred_until) }) }}</p>
    <p v-if="recoveredAt && !recoveredAt.startsWith('0001')">{{ tr('activeRecoveredAt', { date: date(recoveredAt) }) }}</p>
    <p class="text-gray-500">{{ tr('activeEvidenceHint') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PassiveActiveRecoveryResult } from '@/api/admin/passiveQuality'
import { useQualityLabels } from '@/composables/useQualityLabels'

const props = defineProps<{ result: PassiveActiveRecoveryResult; recoveredAt?: string }>()
const { t } = useI18n()
const { reason, date } = useQualityLabels()
const tr = (key: string, args: Record<string, unknown> = {}) => t('admin.tests.groupTargets.' + key, args)
const evidence = computed(() => props.result.evidence)
const metric = (value: number | null | undefined, unit: string) => value == null || !Number.isFinite(value) ? tr('unknown') : String(Math.round(value * 1000) / 1000) + unit
</script>
