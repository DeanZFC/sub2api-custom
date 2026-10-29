<template>
  <div class="space-y-4" data-passive-quality>
    <div class="grid grid-cols-2 gap-3 lg:grid-cols-5">
      <button v-for="card in summaryCards" :key="card.key" type="button" class="rounded-lg border border-gray-200 bg-white p-3 text-left dark:border-dark-700 dark:bg-dark-900" @click="selectSummary(card.key)">
        <span class="text-xs text-gray-500">{{ tr(card.key) }}</span><strong class="mt-1 block text-xl text-gray-900 dark:text-white">{{ card.count }}</strong>
      </button>
    </div>
    <p v-if="loadError || stateError" role="alert" class="text-sm text-red-600">{{ loadError || stateError }} <button class="underline" @click="refresh()">{{ t('common.retry') }}</button></p>
    <section class="space-y-4">
      <div class="flex flex-wrap items-center gap-3">
        <label class="text-sm">{{ tr('phase') }}<select v-model="phaseFilter" data-record-phase class="input ml-2"><option value="">{{ tr('all') }}</option><option v-for="phase in phases" :key="phase" :value="phase">{{ tr(`phases.${phase}`) }}</option></select></label>
        <label class="text-sm">{{ tr('policy') }}<select v-model.number="policyFilter" data-record-policy class="input ml-2"><option :value="0">{{ tr('all') }}</option><option v-for="p in policies" :key="p.id" :value="p.id">{{ p.name }}</option></select></label>
        <label class="text-sm">{{ tr('accountID') }}<input v-model.number="accountFilter" data-record-account type="number" min="0" class="input ml-2 w-28" /></label><button class="btn btn-secondary btn-sm" :disabled="loading" @click="refresh()">{{ t('common.refresh') }}</button>
      </div>
      <label class="text-sm">{{ tr('modelFilter') }}<input v-model.trim="modelFilter" class="input ml-2" maxlength="200" data-record-model /></label><p class="text-xs text-gray-500">{{ tr('recordHint') }}</p>
      <div class="overflow-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b text-gray-500"><th class="p-2">{{ tr('accountPolicy') }}</th><th class="p-2">{{ tr('phase') }}</th><th class="p-2">{{ tr('evidence') }}</th><th class="p-2">{{ tr('recovery') }}</th><th class="p-2">{{ t('common.actions') }}</th></tr></thead><tbody>
        <tr v-for="state in filteredStates" :key="`${state.policy_id}-${state.account_id}-${state.model}`" class="border-b border-gray-100 align-top dark:border-dark-800">
          <td class="p-2"><strong>{{ state.account_name || `#${state.account_id}` }}</strong><div v-if="targetState(state)" class="mt-1 text-xs text-primary-600">{{ targetText('targetState') }}</div><div class="text-xs text-gray-500">#{{ state.account_id }} · {{ state.policy_name }}</div><div class="mt-1 text-xs">{{ state.model || tr('allModels') }} · {{ tr(state.scope === 'account' ? 'accountScope' : 'modelScope') }}</div></td>
          <td class="p-2"><span class="badge" :class="['paused', 'probing'].includes(state.phase) ? 'badge-warning' : statePhase(state) === 'healthy' ? 'badge-success' : 'badge-gray'">{{ phaseLabel(state) }}</span><p v-if="state.latency || state.cache" class="mt-1 text-xs">{{ targetState(state) ? targetText('targetState') : tr('v2.availabilityState') }}: {{ tr('phases.' + state.phase) }} · {{ tr('v2.confirmations', { hits: state.hits }) }}</p><p v-if="state.mode === 'observe'" class="mt-1 text-xs text-gray-500">{{ tr('observe') }}</p><p v-if="state.other_hold" class="mt-1 text-xs text-amber-600">{{ tr('otherHold') }}</p><p class="mt-1 max-w-64 break-words text-xs text-gray-500">{{ reasonText(state.reason) }}</p></td>
          <td class="space-y-1 p-2 text-xs"><div>{{ tr('samples') }}: {{ state.evidence.samples }} · {{ tr('failures') }}: {{ state.evidence.failures }}</div><div>{{ targetState(state) ? targetText('availability') : tr('errorRate') }}: {{ metric(targetState(state) && state.evidence.error_rate != null ? 100 - state.evidence.error_rate : state.evidence.error_rate, '%') }}</div><div>{{ tr('ttft') }}: {{ metric(state.evidence.ttft, ' ms') }} ({{ state.evidence.ttft_samples }})</div><div data-passive-tps>{{ tr('tps') }}: {{ metric(state.evidence.output_tps, ' token/s') }} ({{ state.evidence.tps_samples ?? 0 }})</div><div>{{ targetState(state) ? targetText('actualCache') : tr('cacheRate') }}: {{ metric(state.evidence.cache_rate, '%') }} ({{ state.evidence.cache_samples }})</div><div v-if="state.latency && state.phase !== 'exempt'" class="rounded border p-2"><strong>{{ tr('v2.latencyState') }}</strong>: {{ tr('v2.phases.' + state.latency.phase) }} · {{ tr('v2.confirmations', { hits: state.latency.hits }) }} · {{ tr('v2.recoveryConfirmations', { hits: state.latency.recovery_hits ?? 0 }) }}<p>{{ reasonText(state.latency.reason) }}</p></div><div v-if="state.speed && state.phase !== 'exempt'" class="rounded border p-2" data-speed-state><strong>{{ tr('v2.speedState') }}</strong>: {{ tr('v2.phases.' + state.speed.phase) }} · {{ tr('v2.confirmations', { hits: state.speed.hits }) }} · {{ tr('v2.recoveryConfirmations', { hits: state.speed.recovery_hits ?? 0 }) }}<p>{{ reasonText(state.speed.reason) }}</p></div><PassiveFreshEvidence :evidence="state.evidence" /><ActiveRecoveryEvidence v-if="state.active_recovery" :result="state.active_recovery" :recovered-at="state.recovered_at" /><PassiveCacheEvidence v-if="state.cache && state.phase !== 'exempt'" :cache="state.cache" :config="policies.find(p => p.id === state.policy_id)?.cache_protection" /><div v-if="!state.evidence.complete" class="text-amber-600">{{ tr('telemetryIncomplete') }}</div></td>
          <td class="p-2 text-xs"><template v-if="state.phase === 'trial' && !activeState(state)"><div>{{ tr('trialUsage', { used: state.used, inflight: state.inflight }) }}</div><div>{{ tr('deadline') }}: {{ date(state.trial_ends_at) }}</div></template><div v-else-if="['limited', 'cache_limited'].includes(statePhase(state))">{{ tr('limitedStateHint') }}</div><div v-else-if="['paused', 'probing', 'exempt'].includes(state.phase)">{{ tr('nextAt') }}: {{ date(state.next_at) }}</div><div v-if="!state.model && state.probe_model && ['paused', 'probing', 'trial'].includes(state.phase)" class="mt-1">{{ tr('probeModel') }}: {{ state.probe_model }}</div><div class="mt-1 text-gray-500">{{ date(state.updated_at) }}</div></td>
          <td class="p-2"><div class="flex flex-wrap gap-2"><button v-if="state.phase === 'paused'" class="btn btn-secondary btn-sm" :disabled="busy" @click="act(state, 'verify')">{{ activeState(state) ? targetText('startActiveRecovery') : targetState(state) ? targetText('startTrial') : tr('verify') }}</button><button v-if="state.phase !== 'exempt'" class="btn btn-secondary btn-sm" :disabled="busy" @click="exemptState = state">{{ tr('exempt') }}</button><button v-else class="btn btn-secondary btn-sm" :disabled="busy" @click="act(state, 'cancel_exemption')">{{ tr('cancelExemption') }}</button></div></td>
        </tr><tr v-if="!filteredStates.length"><td colspan="5" class="p-8 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
      </tbody></table></div>
      <p v-if="statesPaginated" class="text-xs text-amber-700">{{ q('refreshPaused') }}</p><div class="flex items-center gap-3"><span class="text-xs text-gray-500">{{ q('loadedStates', { loaded: states.length, total: stateTotal }) }}</span><button v-if="stateCursor" class="btn btn-secondary btn-sm" :disabled="loadingStates" data-more-states @click="loadStates(true)">{{ q('moreStates') }}</button></div><div class="flex items-center justify-between"><h3 class="font-semibold">{{ tr('history') }}</h3><button class="btn btn-secondary btn-sm" :disabled="loadingEvents" @click="loadEvents(false)">{{ t('common.refresh') }}</button></div>
      <div class="flex flex-wrap gap-3 text-sm"><label>{{ q('eventKind') }}<input v-model.trim="kindFilter" data-event-kind class="input ml-2" maxlength="100" /></label><label>{{ q('since') }}<input v-model="sinceFilter" data-event-since type="datetime-local" class="input ml-2" /></label><label>{{ q('until') }}<input v-model="untilFilter" data-event-until type="datetime-local" class="input ml-2" /></label></div><ol class="space-y-2"><li v-for="event in filteredEvents" :key="event.id" class="rounded border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-xs text-gray-500">{{ date(event.created_at) }} · #{{ event.account_id }} · {{ event.policy_name }} · {{ event.detail.model || tr('allModels') }}</span><div>{{ targetState(event.detail) ? targetText('targetState') + ' · ' : '' }}{{ eventText(event.kind) }} · {{ reasonText(event.detail.reason) }}</div><p class="text-xs text-gray-500">{{ tr('samples') }} {{ event.detail.evidence.samples }} · {{ targetState(event.detail) ? targetText('availability') : tr('errorRate') }} {{ metric(targetState(event.detail) && event.detail.evidence.error_rate != null ? 100 - event.detail.evidence.error_rate : event.detail.evidence.error_rate, '%') }} · {{ tr('ttft') }} {{ metric(event.detail.evidence.ttft, ' ms') }} · {{ tr('tps') }} {{ metric(event.detail.evidence.output_tps, ' token/s') }} · {{ targetState(event.detail) ? targetText('actualCache') : tr('cacheRate') }} {{ metric(event.detail.evidence.cache_rate, '%') }}</p><ActiveRecoveryEvidence v-if="event.detail.active_recovery" :result="event.detail.active_recovery" :recovered-at="event.detail.recovered_at" /><PassiveCacheEvidence v-if="event.detail.cache" :cache="event.detail.cache" /></li></ol>
      <button v-if="hasMore" data-more-events class="btn btn-secondary" :disabled="loadingEvents" @click="loadEvents(true)">{{ tr('more') }}</button>
    </section>

    <BaseDialog :show="!!exemptState" :title="tr('exempt')" @close="exemptState = null"><p class="mb-4 text-sm">{{ exemptState?.scope === 'model' && exemptState.model ? tr('v2.modelExemptHint', { model: exemptState.model }) : tr('exemptHint') }}</p><label class="input-label">{{ tr('exemptMinutes') }}<input v-model.number="exemptMinutes" type="number" min="1" max="1440" class="input ml-2 w-28" /></label><template #footer><button class="btn btn-secondary" @click="exemptState = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !Number.isInteger(exemptMinutes) || exemptMinutes < 1 || exemptMinutes > 1440" @click="confirmExemption">{{ t('common.confirm') }}</button></template></BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { useQualityLabels } from '@/composables/useQualityLabels'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PassiveCacheEvidence from './PassiveCacheEvidence.vue'
import PassiveFreshEvidence from './PassiveFreshEvidence.vue'
import ActiveRecoveryEvidence from './ActiveRecoveryEvidence.vue'
import { passiveQualityAPI as api, isGroupQualityTarget, isActiveQualityRecovery, emptyPassiveSummary, type PassivePolicy, type PassiveState, type PassiveEvent } from '@/api/admin/passiveQuality'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
const emit = defineEmits<{ tab: [value: string, policy?: number, account?: number, phase?: string] }>()
const { t, te } = useI18n()
const route = useRoute()
const app = useAppStore()
const tr = (key: string, args: Record<string, unknown> = {}) => t(`admin.tests.passive.${key}`, args)
const q = (key: string, args: Record<string, unknown> = {}) => t('admin.tests.experience.' + key, args)
const targetText = (key: string, args: Record<string, unknown> = {}) => t('admin.tests.groupTargets.' + key, args)
const activeState = (s: PassiveState) => isActiveQualityRecovery(policies.value.find(p => p.id === s.policy_id))
const policies = ref<PassivePolicy[]>([]), states = ref<PassiveState[]>([]), events = ref<PassiveEvent[]>([])
const summary = ref(emptyPassiveSummary()), stateCursor = ref(''), eventCursor = ref(''), stateTotal = ref(0), loadingStates = ref(false), modelFilter = ref(''), kindFilter = ref(''), sinceFilter = ref(''), untilFilter = ref('')
const loading = ref(false), loadingEvents = ref(false), busy = ref(false), loadError = ref(''), stateError = ref(''), hasMore = ref(false)
const exemptState = ref<PassiveState | null>(null), exemptMinutes = ref(30)
const phases = ['cache_limited', 'limited', 'healthy', 'insufficient', 'suspect', 'observed', 'paused', 'probing', 'trial', 'exempt']
const recordPhase = (value: unknown) => typeof value === 'string' && phases.includes(value) ? value : ''
const phaseFilter = ref(recordPhase(route?.query.phase)), policyFilter = ref(Number(route?.query.policy_id) || 0), accountFilter = ref(Number(route?.query.account_id) || 0)
const summaryCards = computed(() => [
  { key: 'activePolicies', count: policies.value.filter(p => p.enabled).length },
  { key: 'pausedAccounts', count: summary.value.paused_accounts },
  { key: 'limitedAccounts', count: summary.value.limited_accounts },
  { key: 'trialAccounts', count: summary.value.trial_accounts },
  { key: 'observedAccounts', count: summary.value.observed_accounts },
])
const filteredStates = computed(() => states.value)
const filteredEvents = computed(() => events.value)
function statePhase(s: PassiveState) { if (targetState(s) && s.phase === 'healthy' && !s.evidence.complete) return 'insufficient'; if (['paused', 'probing', 'trial', 'exempt'].includes(s.phase)) return s.phase; if (s.phase === 'limited' || s.latency?.phase === 'limited' || s.speed?.phase === 'limited') return 'limited'; if (Object.values(s.cache || {}).some(c => ['reduced', 'recovering'].includes(c.phase))) return 'cache_limited'; if (s.latency?.phase === 'watching' || s.speed?.phase === 'watching' || Object.values(s.cache || {}).some(c => c.phase === 'watching')) return 'observed'; if (s.phase === 'healthy' && Object.values(s.cache || {}).some(c => c.phase === 'insufficient')) return 'insufficient'; return s.phase }
function metric(value: number | null | undefined, unit: string) { return value == null ? '—' : `${Math.round(value * 100) / 100}${unit}` }
function date(value: string) { return !value || value.startsWith('0001') ? '—' : new Date(value).toLocaleString() }
const { reason: reasonText } = useQualityLabels()
function eventText(value: string) { if (te('admin.tests.groupTargets.activeEvents.' + value)) return targetText('activeEvents.' + value); if (te('admin.tests.passive.v2.events.' + value)) return tr('v2.events.' + value); return te(`admin.tests.passive.phases.${value}`) ? tr(`phases.${value}`) : te(`admin.tests.passive.events.${value}`) ? tr(`events.${value}`) : value }
function targetState(state: PassiveState) { return isGroupQualityTarget(state) || policies.value.some(p => p.id === state.policy_id && isGroupQualityTarget(p)) }
function phaseLabel(state: PassiveState) { if (activeState(state) && ['probing', 'trial'].includes(state.phase)) return targetText('activeProbing'); if (targetState(state) && state.phase === 'healthy' && !state.evidence.complete) return targetText('insufficient'); return targetState(state) && state.phase === 'probing' ? targetText('trialPreparing') : tr('phases.' + statePhase(state)) }
function report(error: unknown) { app.showError(extractApiErrorMessage(error, tr('loadFailed'))) }
const statesPaginated = ref(false)
let refreshRequest = 0, statesRequest = 0, eventsRequest = 0
let stateFilterKey = '', eventFilterKey = ''
function recordFilters() { return { policy_id: policyFilter.value || undefined, account_id: accountFilter.value || undefined, model: modelFilter.value || undefined } }
async function refresh(refreshRecords = true) {
  const request = ++refreshRequest, recordsRequest = statesRequest; let policyChanged = false; loading.value = true
  try {
    const [p, s] = await Promise.all([api.policies(), api.stateSummary()])
    if (request === refreshRequest) {
      const versions = new Map(p.map(policy => [policy.id, policy.version]))
      policyChanged = policies.value.some(policy => versions.get(policy.id) !== policy.version)
      policies.value = p; summary.value = s; loadError.value = ''
      if (policyChanged) { statesRequest++; states.value = []; stateCursor.value = ''; stateTotal.value = 0; statesPaginated.value = false; loadingStates.value = false }
    }
  } catch (e) { if (request === refreshRequest) loadError.value = extractApiErrorMessage(e, tr('loadFailed')) } finally { if (request === refreshRequest) loading.value = false }
  if (request === refreshRequest && (policyChanged || (recordsRequest === statesRequest && refreshRecords))) await loadStates()
}
async function loadStates(append = false) {
  if (append && loadingStates.value) return
  const filters = { ...recordFilters(), phase: phaseFilter.value || undefined }, key = JSON.stringify(filters)
  append = append && key === stateFilterKey && !!stateCursor.value
  if (key !== stateFilterKey) { states.value = []; stateCursor.value = ''; stateTotal.value = 0; stateFilterKey = key; statesPaginated.value = false }
  const request = ++statesRequest; loadingStates.value = true
  try {
    const page = await api.statePage({ ...filters, cursor: append ? stateCursor.value : undefined })
    if (request !== statesRequest) return
    states.value = append ? [...states.value, ...page.items] : page.items
    stateCursor.value = page.has_more ? page.next_cursor || '' : ''; stateTotal.value = page.total || 0; statesPaginated.value = append; stateError.value = ''
  } catch (e) { if (request === statesRequest) stateError.value = extractApiErrorMessage(e, tr('loadFailed')) } finally { if (request === statesRequest) loadingStates.value = false }
}
async function loadEvents(append = false) {
  if (append && loadingEvents.value) return
  const filters = { ...recordFilters(), kind: kindFilter.value || undefined, since: sinceFilter.value ? new Date(sinceFilter.value).toISOString() : undefined, until: untilFilter.value ? new Date(untilFilter.value).toISOString() : undefined }, key = JSON.stringify(filters)
  append = append && key === eventFilterKey && !!eventCursor.value
  if (key !== eventFilterKey) { events.value = []; eventCursor.value = ''; hasMore.value = false; eventFilterKey = key }
  const request = ++eventsRequest; loadingEvents.value = true
  try { const page = await api.eventPage({ ...filters, cursor: append ? eventCursor.value : undefined }); if (request === eventsRequest) { events.value = append ? [...events.value, ...page.items] : page.items; eventCursor.value = page.next_cursor || ''; hasMore.value = page.has_more } } catch (e) { if (request === eventsRequest) report(e) } finally { if (request === eventsRequest) loadingEvents.value = false }
}
function selectSummary(key: string) {
  policyFilter.value = 0; accountFilter.value = 0; modelFilter.value = ''
  phaseFilter.value = key === 'limitedAccounts' ? 'limited' : key === 'trialAccounts' ? 'trial' : key === 'observedAccounts' ? 'observed' : key === 'pausedAccounts' ? 'paused' : ''
  if (key === 'activePolicies') emit('tab', 'group-targets')
  else emit('tab', 'records', undefined, undefined, phaseFilter.value || undefined)
}
async function act(s: PassiveState, action: string, seconds = 0) { busy.value = true; try { await api.act(s, action, seconds); if (action === 'verify' && activeState(s)) app.showSuccess(targetText('activeRecoveryRequested')); exemptState.value = null; await refresh(); await loadEvents() } catch (e) { report(e) } finally { busy.value = false } }
async function confirmExemption() { if (exemptState.value) await act(exemptState.value, 'exempt', exemptMinutes.value * 60) }
watch([accountFilter, policyFilter, modelFilter], () => { void loadEvents(); void loadStates() }, { immediate: true })
watch(phaseFilter, () => { void loadStates() })
watch([kindFilter, sinceFilter, untilFilter], () => { void loadEvents() })
watch(() => route?.query.account_id, value => { accountFilter.value = Number(value) || 0 })
watch(() => route?.query.policy_id, value => { policyFilter.value = Number(value) || 0 })
watch(() => route?.query.phase, value => { phaseFilter.value = recordPhase(value) })
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { void refresh(false); timer = setInterval(() => { if (!document.hidden) void refresh(!statesPaginated.value && !loadingStates.value) }, 15000) })
onUnmounted(() => { clearInterval(timer); eventsRequest++; statesRequest++; refreshRequest++ })
defineExpose({ refresh })
</script>
