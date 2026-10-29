<template>
  <section class="space-y-5" data-group-quality-targets>
    <header class="rounded-xl border border-primary-200 bg-primary-50/60 p-5 dark:border-primary-900 dark:bg-primary-950/20">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="max-w-3xl"><h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ tr('title') }}</h3><p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">{{ tr('activeIntro') }}</p></div>
        <button type="button" class="btn btn-primary" data-target-create :disabled="loading || busy" @click="edit()">{{ tr('create') }}</button>
      </div>
      <button type="button" class="mt-4 flex w-full flex-wrap items-center justify-between gap-2 rounded-lg border border-primary-100 bg-white p-4 text-left dark:border-dark-600 dark:bg-dark-900" data-target-capability @click="emit('navigate', 'plans')"><strong>{{ tr('capability') }} →</strong><span class="text-sm text-gray-500">{{ tr('capabilityHint') }}</span></button>
    </header>
    <p v-if="loadError" role="alert" class="text-sm text-red-600">{{ loadError }} <button class="underline" @click="refresh">{{ t('common.retry') }}</button></p>
    <p v-if="summaryError" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ tr('summaryFailed') }} <button class="underline" @click="refresh">{{ t('common.retry') }}</button></p>
    <p v-if="!loading && !loadError && !targets.length" class="rounded-xl border border-dashed border-gray-300 p-8 text-center text-sm text-gray-500 dark:border-dark-600">{{ tr('empty') }}</p>
    <article v-for="policy in targets" :key="policy.id" :data-group-target="policy.id" class="space-y-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap items-center gap-2"><h4 class="text-lg font-semibold">{{ groupName(policy.group_ids[0]) }}</h4><span class="badge" :class="statusClass(policy)">{{ tr(targetStatus(policy)) }}</span><span v-if="policy.mode === 'observe'" class="badge badge-gray">{{ t('admin.tests.passive.observe') }}</span><span v-if="policy.model" class="badge badge-gray">{{ policy.model }}</span><span v-if="policy.enabled && policy.mode === 'enforce'" class="text-xs text-gray-500">{{ tr('activeRecovery') }}</span></div>
        <div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" :data-target-toggle="policy.id" :disabled="busy || loading" @click="toggle(policy)">{{ tr(policy.enabled ? 'disable' : 'enable') }}</button><button type="button" class="btn btn-secondary btn-sm" :data-target-edit="policy.id" :disabled="busy" @click="edit(policy)">{{ tr('edit') }}</button><button type="button" class="btn btn-secondary btn-sm" @click="emit('navigate', 'records', policy.id)">{{ tr('records') }}</button><button type="button" class="btn btn-danger btn-sm" :data-target-delete="policy.id" :disabled="busy || loading" @click="deleting = policy">{{ tr('delete') }}</button></div>
      </div>
      <p class="text-xs leading-5 text-gray-500" data-target-full-window>{{ tr('activeSampling', { min: policy.min_samples || 20, confirmations: policy.confirmations || 2, fresh: policy.new_samples || 5 }) }}</p>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div v-for="row in metricRows(policy)" :key="row.key" :data-target-metric="row.key" class="rounded-lg bg-gray-50 p-4 dark:bg-dark-800">
          <p class="text-xs text-gray-500">{{ tr(row.key) }} · {{ tr('target') }} {{ row.target }}</p>
          <p class="mt-2 text-2xl font-semibold tabular-nums" :class="row.known && row.breach ? 'text-amber-700 dark:text-amber-300' : 'text-gray-900 dark:text-white'">{{ row.value }}</p>
          <p class="mt-2 text-xs text-gray-500">{{ tr('samples', { count: row.samples }) }}<span v-if="row.samples < minimum(policy)"> · {{ tr('remaining', { count: minimum(policy) - row.samples }) }}</span></p>
          <p v-if="row.unknown > 0" class="mt-1 text-xs text-amber-700 dark:text-amber-300">{{ tr(row.key === 'cache' ? 'unknownSamples' : 'unknownFailures', { count: row.unknown }) }}</p>
        </div>
      </div>
      <p class="text-xs text-gray-500">{{ tr('window', { minutes: (summaryFor(policy)?.window_seconds || 3600) / 60 }) }}<span v-if="summaryFor(policy)"> · {{ tr('updated', { date: date(summaryFor(policy)?.generated_at) }) }}</span></p>
      <p v-if="summaryFor(policy)?.snapshot_ready === false || summaryFor(policy)?.stale" class="text-xs text-amber-700" data-target-snapshot>{{ tr(summaryFor(policy)?.snapshot_ready === false ? 'snapshotPending' : 'snapshotStale') }}</p>
      <details v-if="summaryFor(policy)?.contributors?.length" class="rounded-lg border p-3 dark:border-dark-700" data-target-contributors>
        <summary class="cursor-pointer text-sm font-medium">{{ tr('contributors') }}</summary><p class="my-2 text-xs text-gray-500">{{ tr('contributorsHint') }} · {{ date(summaryFor(policy)?.window_started_at) }} — {{ date(summaryFor(policy)?.generated_at) }}</p>
        <div class="overflow-x-auto"><table class="w-full text-left text-xs"><thead><tr><th class="p-2">{{ tr('accountModel') }}</th><th class="p-2">{{ tr('trafficKind') }}</th><th class="p-2">{{ tr('uncachedTokens') }}</th><th class="p-2">{{ tr('failures') }}</th><th class="p-2">{{ tr('slowRequests') }}</th><th class="p-2">{{ tr('ttft') }}</th><th class="p-2">{{ tr('tps') }}</th></tr></thead><tbody><tr v-for="c in summaryFor(policy)?.contributors" :key="`${c.account_id}-${c.model}-${c.traffic_kind}`" class="border-t dark:border-dark-700"><td class="p-2">{{ c.account_name || `#${c.account_id}` }} · {{ c.model || t('admin.tests.passive.allModels') }}</td><td class="p-2">{{ tr('trafficKinds.' + c.traffic_kind) }}</td><td class="p-2">{{ tokenCount(c.uncached_tokens) }}<span v-if="c.cache_unknown_samples" class="block text-amber-700">{{ tr('unknownSamples', { count: c.cache_unknown_samples }) }}</span></td><td class="p-2">{{ c.failure_samples }} / {{ c.failure_samples + c.success_samples }}</td><td class="p-2">{{ c.slow_requests }} / {{ c.ttft_samples }}</td><td class="p-2">{{ seconds(c.ttft_p90_ms) }}</td><td class="p-2">{{ formatMonitorOutputTps(c.output_tps) }} ({{ c.tps_samples ?? 0 }})</td></tr></tbody></table></div>
        <p v-if="summaryFor(policy)?.contributors_truncated" class="mt-2 text-xs text-amber-700">{{ tr('contributorsTruncated', { shown: summaryFor(policy)?.contributors?.length || 0, total: summaryFor(policy)?.contributor_total || 0 }) }}</p>
      </details>
      <p v-if="summaryFor(policy) && !summaryFor(policy)?.telemetry_complete" class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.tests.passive.telemetryIncomplete') }}</p>
      <div class="flex flex-wrap gap-2 text-xs"><span class="badge badge-gray">{{ tr('scopeCount', { count: policyCounts(policy)?.total || 0 }) }}</span><span v-if="countPhase(policy, ['paused', 'probing'])" class="badge badge-warning">{{ tr('pausedCount', { count: countPhase(policy, ['paused', 'probing']) }) }}</span><span v-if="countPhase(policy, ['insufficient', 'suspect', 'observed'])" class="badge badge-gray">{{ tr('pendingCount', { count: countPhase(policy, ['insufficient', 'suspect', 'observed']) }) }}</span><span v-if="!policyCounts(policy)?.total" class="text-gray-500">{{ tr('noStates') }}</span></div>
      <div v-if="policy.enabled && policy.mode === 'enforce'" class="space-y-2">
        <p class="text-sm font-medium" data-target-verified>{{ tr('verifiedCount', { count: verifiedCount(policy) }) }}</p>
        <p v-if="verifiedCount(policy) === 0" role="status" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm font-medium leading-6 text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200" data-target-no-verified>{{ tr('noVerified') }}</p>
        <p class="text-xs text-gray-500">{{ tr('verifiedCaveat') }}</p>
      </div>
      <details v-if="policyStates(policy).length" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
        <summary class="cursor-pointer text-sm font-medium">{{ tr('latestEvidence') }}</summary><p class="my-3 text-xs text-gray-500">{{ tr('frozenEvidence') }}</p>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700"><th class="p-2">{{ tr('accountModel') }}</th><th class="p-2">{{ tr('phase') }}</th><th class="p-2">{{ tr('latestEvidence') }}</th><th class="p-2">{{ tr('reason') }}</th></tr></thead><tbody>
          <tr v-for="state in policyStates(policy)" :key="`${state.account_id}-${state.model}`" class="border-b border-gray-100 align-top last:border-0 dark:border-dark-800" :data-target-state="`${state.account_id}-${state.model}`">
            <td class="p-2"><strong>{{ state.account_name || `#${state.account_id}` }}</strong><p class="mt-1 text-xs text-gray-500">#{{ state.account_id }} · {{ state.model || t('admin.tests.passive.allModels') }}</p></td>
            <td class="p-2 text-xs"><span class="badge" :class="['paused', 'probing'].includes(state.phase) ? 'badge-warning' : 'badge-gray'">{{ phaseLabel(state) }}</span><p class="mt-2">{{ confirmationProgress(policy, state) }}</p><p v-if="state.phase === 'suspect' && state.suspicion_since && !state.suspicion_since.startsWith('0001')" class="mt-1" data-target-suspicion-since>{{ tr('suspicionSince', { date: date(state.suspicion_since) }) }}</p><p v-if="state.phase === 'suspect' && state.recovery_since && !state.recovery_since.startsWith('0001')" class="mt-1" data-target-recovery-since>{{ tr('recoverySince', { date: date(state.recovery_since) }) }}</p><p v-if="state.phase === 'paused'" class="mt-2">{{ tr('nextActiveRecovery', { date: date(state.next_at) }) }}</p></td>
            <td class="space-y-1 p-2 text-xs"><p v-if="policy.cache_rate.enabled">{{ tr('cache') }}: {{ percent(state.evidence.cache_rate) }} · {{ tr('samples', { count: state.evidence.cache_samples }) }}</p><p v-if="policy.error_rate.enabled">{{ tr('availability') }}: {{ percent(state.evidence.error_rate == null ? null : 100 - state.evidence.error_rate) }} · {{ tr('samples', { count: state.evidence.samples }) }}</p><p v-if="policy.ttft.enabled">{{ tr('ttft') }}: {{ seconds(state.evidence.ttft) }} · {{ tr('samples', { count: state.evidence.ttft_samples }) }}</p><p v-if="stateSampleCount(policy, state) < minimum(policy)" class="text-amber-700 dark:text-amber-300">{{ tr('remaining', { count: minimum(policy) - stateSampleCount(policy, state) }) }}</p><p v-if="policy.tps?.enabled" data-target-state-tps>{{ tr('tps') }}: {{ formatMonitorOutputTps(state.evidence.output_tps) }} · {{ tr('samples', { count: state.evidence.tps_samples ?? 0 }) }}</p><PassiveFreshEvidence :evidence="state.evidence" /><ActiveRecoveryEvidence v-if="state.active_recovery" :result="state.active_recovery" :recovered-at="state.recovered_at" /><p class="text-gray-500">{{ date(state.updated_at) }}</p></td>
            <td class="max-w-80 break-words p-2 text-xs"><p>{{ reason(state.reason) || '—' }}</p><p v-if="!state.evidence.complete" class="mt-2 text-amber-700 dark:text-amber-300">{{ t('admin.tests.passive.telemetryIncomplete') }}</p><p v-if="state.other_hold" class="mt-2 text-amber-700 dark:text-amber-300">{{ tr('otherHold') }}</p></td>
          </tr>
        </tbody></table></div>
        <p v-if="statePaginated[policy.id]" class="mt-2 text-xs text-amber-700">{{ t('admin.tests.experience.refreshPaused') }} <button class="underline" @click="loadPolicyStates(policy.id)">{{ t('common.refresh') }}</button></p><div class="mt-3 flex items-center gap-3"><span class="text-xs text-gray-500">{{ t('admin.tests.experience.loadedStates', { loaded: policyStates(policy).length, total: policyCounts(policy)?.total || 0 }) }}</span><button v-if="stateCursors[policy.id]" class="btn btn-secondary btn-sm" :disabled="stateLoading[policy.id]" :data-target-more-states="policy.id" @click="loadPolicyStates(policy.id, true)">{{ t('admin.tests.experience.moreStates') }}</button></div>
      </details>
    </article>
    <aside class="space-y-2 rounded-lg bg-gray-50 p-4 text-xs leading-5 text-gray-600 dark:bg-dark-800 dark:text-gray-300"><p>{{ tr('evidenceScope') }}</p><p>{{ tr('limitations') }}</p><p>{{ tr('sharedScope') }}</p></aside>
    <BaseDialog :show="!!editing" :title="tr('editorTitle')" width="wide" @close="editing = null">
      <form v-if="editing" class="space-y-5" @submit.prevent="save">
        <label class="input-label block">{{ tr('group') }}<select :value="editing.group_ids[0] || 0" class="input mt-2 w-full" data-target-group :disabled="busy" @change="selectGroup(Number(($event.target as HTMLSelectElement).value))"><option :value="0">{{ tr('chooseGroup') }}</option><option v-for="group in eligibleGroups" :key="group.id" :value="group.id">{{ group.name }}</option></select></label>
        <p v-if="editing.id" class="text-xs text-gray-500">{{ tr('existing') }}</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="input-label block"><span class="flex items-center gap-2"><input v-model="editing.cache_rate.enabled" type="checkbox" data-target-cache-enabled />{{ tr('cacheTarget') }}</span><input :value="editing.cache_rate.pause" type="number" min="0" max="99.9" step="0.1" class="input mt-2 w-full" data-target-cache :disabled="!editing.cache_rate.enabled" @input="setThreshold('cache_rate', $event)" /></label>
          <label class="input-label block"><span class="flex items-center gap-2"><input v-model="editing.error_rate.enabled" type="checkbox" data-target-availability-enabled />{{ tr('availabilityTarget') }}</span><input :value="rounded(100 - editing.error_rate.pause)" type="number" min="0" max="99.9" step="0.1" class="input mt-2 w-full" data-target-availability :disabled="!editing.error_rate.enabled" @input="setThreshold('error_rate', $event)" /></label>
          <label class="input-label block"><span class="flex items-center gap-2"><input v-model="editing.ttft.enabled" type="checkbox" data-target-ttft-enabled />{{ tr('ttftTarget') }}</span><input :value="editing.ttft.pause / 1000" type="number" min="0.001" max="600" step="0.001" class="input mt-2 w-full" data-target-ttft :disabled="!editing.ttft.enabled" @input="setThreshold('ttft', $event)" /></label>
          <label class="input-label block"><span class="flex items-center gap-2"><input v-model="editing.tps.enabled" type="checkbox" data-target-tps-enabled />{{ tr('tpsTarget') }}</span><input :value="editing.tps.pause" type="number" min="0.1" max="1000000" step="0.1" class="input mt-2 w-full" data-target-tps :disabled="!editing.tps.enabled" @input="setThreshold('tps', $event)" /><span class="mt-2 block text-xs font-normal text-gray-500">{{ t('channelMonitorV2.metrics.outputTpsDetail') }}</span></label>
        </div>
        <label class="flex items-center gap-2 text-sm font-medium"><input v-model="editing.enabled" type="checkbox" data-target-enabled />{{ editing.mode === 'observe' ? tr('enableObservation') : tr('enabled') }}</label>

        <div v-if="editing.mode === 'observe'" class="rounded-lg bg-amber-50 p-3 text-sm dark:bg-amber-950/20"><p>{{ tr('observationRetained') }}</p><button type="button" class="btn btn-secondary btn-sm mt-2" data-target-enforce @click="editing.mode = 'enforce'">{{ tr('switchToEnforce') }}</button></div>
        <p v-if="editing.model" class="text-xs text-gray-500">{{ tr('modelRetained', { model: editing.model }) }}</p>
        <div class="rounded-lg bg-primary-50 p-4 dark:bg-primary-950/20"><strong class="text-sm">{{ tr('recovery') }}</strong><p class="mt-2 text-xs leading-5 text-gray-600 dark:text-gray-300">{{ tr('activeRecoveryHint') }}</p><p class="mt-2 text-xs leading-5 text-gray-600 dark:text-gray-300">{{ tr('activeBudgetHint') }}</p><p class="mt-2 text-xs">{{ tr('recoveryTargets') }}: {{ recoveryLabels(editing).join(' · ') }}</p></div>
        <details class="rounded-lg border border-gray-200 p-4 text-xs leading-5 dark:border-dark-700" data-active-recovery-settings>
          <summary class="cursor-pointer text-sm font-medium">{{ tr('details') }}</summary>
          <p class="mt-3">{{ tr('activeSampling', { min: editing.min_samples || 20, confirmations: editing.confirmations || 2, fresh: editing.new_samples || 5 }) }}</p>
          <p class="mt-2">{{ tr('activeStability', { availability: editing.stability.availability_pause_seconds, performance: editing.stability.performance_pause_seconds, recovery: Math.max(120, editing.stability.recovery_seconds) }) }}</p>
          <p class="mt-2">{{ tr('activeCacheHint') }}</p>
          <p class="mt-2">{{ tr('activeStrictHint') }}</p>
          <p class="mt-2">{{ tr('sharedScope') }}</p>
          <p v-if="editing.excluded_account_ids.length" class="mt-2">{{ tr('exclusionsRetained', { count: editing.excluded_account_ids.length }) }}</p>
        </details>

        <p class="text-xs leading-5 text-gray-500">{{ tr('limitations') }}</p><p v-if="editing.id" class="text-xs text-amber-700 dark:text-amber-300">{{ tr('resetHint') }}</p><p v-if="validationError" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ validationError }}</p>
      </form>
      <template #footer><button type="button" class="btn btn-secondary" :disabled="busy" @click="editing = null">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" data-target-save :disabled="busy || !!validationError" @click="save">{{ t('common.save') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="!!deleting" :title="tr('deleteTitle')" width="narrow" @close="cancelDelete">
      <p v-if="deleting" class="text-sm leading-6 text-gray-600 dark:text-gray-300" data-target-delete-dialog>{{ tr('deleteConfirm', { group: groupName(deleting.group_ids[0]) }) }}</p>
      <template #footer><button type="button" class="btn btn-secondary" data-target-delete-cancel :disabled="busy" @click="cancelDelete">{{ t('common.cancel') }}</button><button type="button" class="btn btn-danger" data-target-delete-confirm :disabled="busy" @click="removeTarget">{{ tr('delete') }}</button></template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatMonitorOutputTps } from '@/features/channel-monitor-v2/monitorFormat'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PassiveFreshEvidence from './PassiveFreshEvidence.vue'
import ActiveRecoveryEvidence from './ActiveRecoveryEvidence.vue'
import { passiveQualityAPI as api, groupQualityTargetDraft, isGroupQualityTarget, emptyPassiveSummary, type GroupQualityTargetDraft, type GroupQualityTargetSummary, type PassivePolicy, type PassiveState } from '@/api/admin/passiveQuality'
import { useQualityLabels } from '@/composables/useQualityLabels'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'

const props = defineProps<{ groups: AdminGroup[]; visible: boolean; policyId?: number }>()
const emit = defineEmits<{ navigate: [tab: string, policy?: number, account?: number] }>()
const { t, locale } = useI18n(), app = useAppStore(), { reason, date } = useQualityLabels()
const tr = (key: string, values: Record<string, unknown> = {}) => t('admin.tests.groupTargets.' + key, values)
const policies = ref<PassivePolicy[]>([]), states = ref<PassiveState[]>([]), summaries = ref<GroupQualityTargetSummary[]>([])
const stateSummary = ref(emptyPassiveSummary()), stateCursors = ref<Record<number, string>>({}), stateLoading = ref<Record<number, boolean>>({})
const targets = computed(() => policies.value.filter(isGroupQualityTarget))
const eligibleGroups = computed(() => props.groups.filter(g => g.status === 'active' && g.platform !== 'composite'))
const loading = ref(false), busy = ref(false), loadError = ref(''), summaryError = ref(false)
const editing = ref<GroupQualityTargetDraft | null>(null), baseline = ref(''), deleting = ref<PassivePolicy | null>(null)
const rounded = (value: number) => Math.round(value * 1000) / 1000
const percent = (value: number | null | undefined) => value == null || !Number.isFinite(value) ? tr('unknown') : `${rounded(value)}%`
const seconds = (value: number | null | undefined) => value == null || !Number.isFinite(value) ? tr('unknown') : `${rounded(value / 1000)} ${t('admin.tests.experience.seconds')}`
const tokenCount = (value: number | undefined) => value == null || !Number.isFinite(value) ? tr('unknown') : new Intl.NumberFormat(locale.value, { maximumFractionDigits: 0 }).format(value)
const groupName = (id?: number) => props.groups.find(g => g.id === id)?.name || `#${id}`
const summaryFor = (p: PassivePolicy) => summaries.value.find(s => s.policy_id === p.id && s.group_id === p.group_ids[0])
const policyStates = (p: PassivePolicy) => states.value.filter(s => s.policy_id === p.id)
const policyCounts = (p: PassivePolicy) => stateSummary.value.by_policy.find(s => s.policy_id === p.id)
const countPhase = (p: PassivePolicy, phases: string[]) => phases.reduce((sum, phase) => sum + (policyCounts(p)?.phases[phase] || 0), 0)
const minimum = (p: PassivePolicy) => p.min_samples || 20
function verifiedCount(p: PassivePolicy) { return policyCounts(p)?.verified || 0 }
function stateSampleCount(p: PassivePolicy, s: PassiveState) {
  const counts = [p.cache_rate.enabled ? s.evidence.cache_samples : undefined, p.error_rate.enabled ? s.evidence.samples : undefined, p.ttft.enabled ? s.evidence.ttft_samples : undefined, p.tps?.enabled ? s.evidence.tps_samples ?? 0 : undefined].filter((v): v is number => v !== undefined)
  return counts.length ? Math.min(...counts) : s.evidence.samples
}
function confirmationProgress(p: PassivePolicy, s: PassiveState) {
  if (['paused', 'probing'].includes(s.phase)) return tr('activeRecovery')
  const recovering = s.phase === 'suspect' && s.recovery_since && !s.recovery_since.startsWith('0001')
  return tr(recovering ? 'recoveryConfirmations' : 'confirmations', { count: recovering ? s.recovery_hits || 0 : s.hits, required: p.confirmations || 2 })
}
function phaseLabel(s: PassiveState) { if (s.phase === 'probing') return tr('activeProbing'); if (s.phase === 'healthy' && !s.evidence.complete) return tr('insufficient'); return t('admin.tests.passive.phases.' + s.phase) }
function metricRows(p: PassivePolicy) {
  const s = summaryFor(p)
  return [
    { key: 'cache', enabled: p.cache_rate.enabled, known: s?.actual_cache_rate != null, value: percent(s?.actual_cache_rate), samples: s?.cache_samples || 0, unknown: s?.cache_unknown_samples || 0, breach: s?.actual_cache_rate != null && s.actual_cache_rate < p.cache_rate.pause, target: `≥ ${percent(p.cache_rate.pause)}` },
    { key: 'availability', enabled: p.error_rate.enabled, known: s?.actual_availability != null, value: percent(s?.actual_availability), samples: (s?.success_samples || 0) + (s?.failure_samples || 0), unknown: s?.failure_unknown_samples || 0, breach: s?.actual_availability != null && s.actual_availability < 100 - p.error_rate.pause, target: `≥ ${percent(100 - p.error_rate.pause)}` },
    { key: 'tps', enabled: p.tps?.enabled, known: s?.output_tps != null, value: formatMonitorOutputTps(s?.output_tps), samples: s?.tps_samples || 0, unknown: 0, breach: s?.output_tps != null && s.output_tps < p.tps.pause, target: `≥ ${formatMonitorOutputTps(p.tps?.pause)}` },
    { key: 'ttft', enabled: p.ttft.enabled, known: s?.ttft_p90_ms != null, value: seconds(s?.ttft_p90_ms), samples: s?.ttft_samples || 0, unknown: 0, breach: s?.ttft_p90_ms != null && s.ttft_p90_ms > p.ttft.pause, target: `≤ ${seconds(p.ttft.pause)}` },
  ].filter(row => row.enabled)
}
function targetStatus(p: PassivePolicy) {
  if (!p.enabled) return 'disabled'
  if (summaryFor(p)?.telemetry_complete !== true || summaryFor(p)?.stale || summaryFor(p)?.snapshot_ready === false) return 'insufficient'
  const rows = metricRows(p)
  if (rows.some(row => row.known && row.samples >= minimum(p) && row.breach)) return 'belowTarget'
  if (!rows.length || rows.some(row => !row.known || row.samples < minimum(p) || row.unknown > 0)) return 'insufficient'
  if (countPhase(p, ['paused', 'probing']) > 0) return 'protecting'
  return 'atTarget'
}
function statusClass(p: PassivePolicy) { const status = targetStatus(p); return status === 'atTarget' ? 'badge-success' : ['belowTarget', 'protecting'].includes(status) ? 'badge-warning' : 'badge-gray' }
const validationError = computed(() => {
  const p = editing.value
  if (!p) return ''
  if (p.group_ids.length !== 1 || !eligibleGroups.value.some(g => g.id === p.group_ids[0]) || ![p.cache_rate, p.error_rate, p.ttft, p.tps].some(m => m.enabled)) return tr('invalid')
  if (p.cache_rate.enabled && (!Number.isFinite(p.cache_rate.pause) || p.cache_rate.pause < 0 || p.cache_rate.pause > 99.9)) return tr('invalid')
  if (p.error_rate.enabled && (!Number.isFinite(p.error_rate.pause) || p.error_rate.pause < .1 - 1e-9 || p.error_rate.pause > 100)) return tr('invalid')
  if (p.ttft.enabled && (!Number.isFinite(p.ttft.pause) || p.ttft.pause < 1 || p.ttft.pause > 600000)) return tr('invalid')
  if (p.tps.enabled && (!Number.isFinite(p.tps.pause) || p.tps.pause <= 0 || p.tps.pause >= 1000000 || !Number.isFinite(p.tps.recover) || p.tps.recover <= p.tps.pause || p.tps.recover > 1000000)) return tr('invalid')
  if (![p.stability.availability_pause_seconds, p.stability.performance_pause_seconds, p.stability.recovery_seconds].every(v => Number.isInteger(v) && v >= 0 && v <= 3600)) return tr('invalidStability')
  return ''
})
function edit(p?: PassivePolicy) { editing.value = groupQualityTargetDraft(p); baseline.value = JSON.stringify(editing.value) }
function selectGroup(id: number) {
  const existing = targets.value.find(p => p.group_ids.includes(id))
  if (existing) { edit(existing); return }
  editing.value = groupQualityTargetDraft(undefined, groupName(id))
  editing.value.group_ids = id ? [id] : []
  baseline.value = ''
}
function setThreshold(metric: 'cache_rate' | 'error_rate' | 'ttft' | 'tps', event: Event) {
  if (!editing.value) return
  const value = (event.target as HTMLInputElement).valueAsNumber
  const m = editing.value[metric]
  m.pause = metric === 'error_rate' ? rounded(100 - value) : metric === 'ttft' ? Math.round(value * 1000) : value
  m.recover = metric === 'tps' ? Math.min(1000000, Math.max(value + 5, value * 1.2)) : metric === 'cache_rate' ? Math.min(100, value + 5) : metric === 'error_rate' ? Math.max(0, rounded(m.pause - 5)) : m.pause * .8
}
function recoveryLabels(p: GroupQualityTargetDraft) {
  return [p.cache_rate.enabled ? `${tr('cache')}: ${percent(p.cache_rate.recover)}` : '', p.error_rate.enabled ? `${tr('availability')}: ${percent(100 - p.error_rate.recover)}` : '', p.ttft.enabled ? `${tr('ttft')}: ${seconds(p.ttft.recover)}` : '', p.tps.enabled ? `${tr('tps')}: ${formatMonitorOutputTps(p.tps.recover)}` : ''].filter(Boolean)
}
let request = 0
const stateRequests = new Map<number, number>()
const statePaginated = ref<Record<number, boolean>>({})
async function loadPolicyStates(id: number, append = false) {
  if (append && stateLoading.value[id]) return
  append = append && !!stateCursors.value[id]
  const current = (stateRequests.get(id) || 0) + 1
  stateRequests.set(id, current); stateLoading.value[id] = true
  try {
    const page = await api.statePage({ policy_id: id, cursor: append ? stateCursors.value[id] : undefined, limit: 20 })
    if (stateRequests.get(id) !== current) return
    states.value = append ? [...states.value, ...page.items] : [...states.value.filter(s => s.policy_id !== id), ...page.items]
    stateCursors.value[id] = page.has_more ? page.next_cursor || '' : ''; statePaginated.value[id] = append
  } catch (e) { if (stateRequests.get(id) === current) loadError.value = extractApiErrorMessage(e, tr('loadFailed')) }
  finally { if (stateRequests.get(id) === current) stateLoading.value[id] = false }
}
async function refresh() {
  const current = ++request; loading.value = true
  const [policyResult, stateResult, summaryResult] = await Promise.allSettled([api.policies(), api.stateSummary(), api.groupTargets()])
  if (current !== request) return
  if (policyResult.status === 'fulfilled' && stateResult.status === 'fulfilled') {
    const previous = new Map(policies.value.map(policy => [policy.id, policy.version]))
    for (const policy of policyResult.value) if (previous.has(policy.id) && previous.get(policy.id) !== policy.version) {
      stateRequests.set(policy.id, (stateRequests.get(policy.id) || 0) + 1)
      states.value = states.value.filter(state => state.policy_id !== policy.id)
      delete statePaginated.value[policy.id]; delete stateCursors.value[policy.id]; delete stateLoading.value[policy.id]
    }
    policies.value = policyResult.value; stateSummary.value = stateResult.value; loadError.value = ''
    const ids = new Set(targets.value.map(p => p.id))
    states.value = states.value.filter(s => ids.has(s.policy_id))
    for (const [id, token] of stateRequests) if (!ids.has(id)) { stateRequests.set(id, token + 1); delete statePaginated.value[id]; delete stateCursors.value[id]; delete stateLoading.value[id] }
    await Promise.all(targets.value.filter(p => !statePaginated.value[p.id] && !stateLoading.value[p.id]).map(p => loadPolicyStates(p.id)))
  } else { loadError.value = extractApiErrorMessage(policyResult.status === 'rejected' ? policyResult.reason : stateResult.status === 'rejected' ? stateResult.reason : undefined, tr('loadFailed')) }
  if (current !== request) return
  summaryError.value = summaryResult.status === 'rejected'
  summaries.value = summaryResult.status === 'fulfilled' ? summaryResult.value : []
  loading.value = false
}
async function save() {
  const p = editing.value
  if (!p || validationError.value || busy.value) return
  if (p.id && JSON.stringify(p) === baseline.value) { editing.value = null; return }
  // Keep the version the administrator reviewed. A refresh cannot authorize
  // overwriting a newer policy, or turn a concurrent create into an update.
  const payload = { ...p, name: p.name || tr('policyName', { group: groupName(p.group_ids[0]) }) }
  busy.value = true
  try { await api.saveGroupQualityTarget(payload); editing.value = null; await refresh(); app.showSuccess(tr('saved')) }
  catch (e) { app.showError(extractApiErrorMessage(e, tr('loadFailed'))) }
  finally { busy.value = false }
}
async function toggle(p: PassivePolicy) {
  busy.value = true
  try { await api.saveGroupQualityTarget({ ...groupQualityTargetDraft(p), enabled: !p.enabled }); await refresh() }
  catch (e) { app.showError(extractApiErrorMessage(e, tr('loadFailed'))) }
  finally { busy.value = false }
}
function cancelDelete() { if (!busy.value) deleting.value = null }
async function removeTarget() {
  const p = deleting.value
  if (!p || busy.value) return
  busy.value = true
  try {
    await api.remove(p.id)
    // Invalidate older list and evidence requests before removing local state.
    request++
    stateRequests.set(p.id, (stateRequests.get(p.id) || 0) + 1)
    policies.value = policies.value.filter(policy => policy.id !== p.id)
    states.value = states.value.filter(state => state.policy_id !== p.id)
    summaries.value = summaries.value.filter(summary => summary.policy_id !== p.id)
    stateSummary.value.by_policy = stateSummary.value.by_policy.filter(summary => summary.policy_id !== p.id)
    delete stateCursors.value[p.id]; delete stateLoading.value[p.id]; delete statePaginated.value[p.id]
    if (editing.value?.id === p.id) { editing.value = null; baseline.value = '' }
    deleting.value = null
    await refresh()
    app.showSuccess(tr('deleted'))
  } catch (e) { app.showError(extractApiErrorMessage(e, tr('deleteFailed'))) }
  finally { busy.value = false }
}
let timer: ReturnType<typeof setInterval> | undefined
let openedPolicy = 0
watch(() => [props.policyId, targets.value] as const, ([id]) => {
  if (!id || id === openedPolicy) return
  const policy = targets.value.find(p => p.id === id)
  if (policy) { openedPolicy = id; edit(policy) }
})
onMounted(() => { if (props.visible) void refresh(); timer = setInterval(() => { if (props.visible && !document.hidden && !loading.value && !busy.value) void refresh() }, 15000) })
watch(() => props.visible, visible => { if (visible) void refresh(); else { editing.value = null; deleting.value = null } })
onUnmounted(() => { request++; for (const [id, token] of stateRequests) stateRequests.set(id, token + 1); clearInterval(timer) })
defineExpose({ refresh })
</script>
