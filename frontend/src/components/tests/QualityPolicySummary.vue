<template>
  <section class="space-y-3 rounded-xl border border-primary-200 bg-primary-50/60 p-4 dark:border-primary-900 dark:bg-primary-950/20" aria-live="polite" data-policy-preview>
    <h3 class="font-semibold text-gray-900 dark:text-white">{{ q('previewTitle') }}</h3>
    <p class="text-sm">{{ groupNames }} · {{ policy.model ? q('modelSummary', { model: policy.model, scope: t('admin.tests.passive.' + (policy.scope === 'model' ? 'modelScope' : 'accountScope')) }) : q('allModelsSummary') }}</p>
    <p class="text-sm text-gray-600 dark:text-gray-300">{{ q('windowPreview', { minutes: display(policy.window_seconds / 60), max: policy.max_samples, min: policy.min_samples, confirmations: policy.confirmations, fresh: policy.new_samples }) }}</p>
    <ul class="space-y-1 text-sm">
      <li v-for="metric in metrics.filter(m => policy[m.key].enabled)" :key="metric.key">{{ q('thresholdPreview', { metric: q(metric.label), pauseSign: metric.key === 'cache_rate' ? '≤' : '≥', recoverSign: metric.key === 'cache_rate' ? '≥' : '≤', pause: display(policy[metric.key].pause / metric.divisor), recover: display(policy[metric.key].recover / metric.divisor) }) }}</li>
      <li v-if="policy.consecutive_failures">{{ t('admin.tests.passive.consecutiveReason', { count: policy.consecutive_failures }) }}</li>
    </ul>
    <p v-if="!policy.enabled" class="text-sm font-medium text-amber-700 dark:text-amber-300">{{ q('inactivePreview') }}</p>
    <template v-else>
      <p class="text-sm font-medium">{{ q(response + 'Flow') }}</p>
      <p v-if="response === 'pause'" class="text-xs text-gray-600 dark:text-gray-300">{{ q('recoveryPreview', { seconds: policy.cooldown_seconds, requests: policy.trial_requests, concurrency: policy.trial_concurrency, duration: policy.trial_seconds, samples: policy.recovery_samples }) }}</p>
      <p v-if="response === 'pause'" class="text-xs text-amber-700 dark:text-amber-300">{{ q('probeCost') }}</p>
      <p v-if="response === 'limit'" class="text-xs text-gray-600 dark:text-gray-300">{{ q('limitPreview', { concurrency: policy.concurrency_limit, penalty: policy.priority_penalty, samples: policy.recovery_samples, confirmations: policy.confirmations }) }}</p>
    </template>
    <p v-if="overlapping.length" class="rounded-lg bg-amber-50 p-3 text-xs text-amber-800 dark:bg-amber-950/30 dark:text-amber-200" data-policy-overlap>{{ q('overlap', { names: overlapping.map(p => p.name).join(' / ') }) }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PassivePolicy } from '@/api/admin/passiveQuality'
import type { AccountListItem, AdminGroup } from '@/types'
import { policyResponse } from './qualityPolicyProfiles'
const props = defineProps<{ policy: PassivePolicy; groups: AdminGroup[]; accounts: AccountListItem[]; policies: PassivePolicy[] }>()
const { t } = useI18n()
const q = (key: string, args: Record<string, unknown> = {}) => t('admin.tests.experience.' + key, args)
const response = computed(() => policyResponse(props.policy))
const groupNames = computed(() => props.policy.group_ids.map(id => props.groups.find(g => g.id === id)?.name || '#' + id).join(' / ') || '—')
const metrics = [{ key: 'error_rate', label: 'errorSignal', divisor: 1 }, { key: 'ttft', label: 'ttftSeconds', divisor: 1000 }, { key: 'cache_rate', label: 'cacheSignal', divisor: 1 }] as const
const display = (n: number) => Number.isFinite(n) ? Number(n.toFixed(3)) : '—'
const overlapping = computed(() => props.policies.filter(other => {
  const p = props.policy
  if (other.id === p.id || !other.enabled) return false
  if (p.model && other.model && p.model !== other.model && p.scope === 'model' && other.scope === 'model') return false
  if (p.group_ids.some(id => other.group_ids.includes(id))) return true
  return props.accounts.some(a => !p.excluded_account_ids.includes(a.id) && !other.excluded_account_ids.includes(a.id)
    && a.group_ids?.some(id => p.group_ids.includes(id)) && a.group_ids?.some(id => other.group_ids.includes(id)))
}))
</script>
