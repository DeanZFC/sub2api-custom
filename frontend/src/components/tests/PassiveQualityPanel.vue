<template>
  <div class="space-y-4" data-passive-quality>
    <div class="grid grid-cols-2 gap-3 lg:grid-cols-5">
      <button v-for="card in summaryCards" :key="card.key" type="button" class="rounded-lg border border-gray-200 bg-white p-3 text-left dark:border-dark-700 dark:bg-dark-900" @click="selectSummary(card.key)">
        <span class="text-xs text-gray-500">{{ tr(card.key) }}</span><strong class="mt-1 block text-xl text-gray-900 dark:text-white">{{ card.count }}</strong>
      </button>
    </div>
    <p v-if="loadError" role="alert" class="text-sm text-red-600">{{ loadError }} <button class="underline" @click="refresh()">{{ t('common.retry') }}</button></p>
    <section v-if="tab === 'passive'" class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3"><p class="max-w-3xl text-sm text-gray-500">{{ tr('intro') }}</p><button class="btn btn-primary" data-passive-create @click="edit()">{{ q('createTraffic') }}</button></div>
      <div v-if="!integrated && policies.some(isGroupQualityTarget)" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-primary-200 p-4 text-sm dark:border-primary-900" data-target-managed><p>{{ targetText('managedSeparately') }}</p><button type="button" class="btn btn-secondary btn-sm" @click="emit('tab', 'group-targets')">{{ targetText('openTargets') }}</button></div>
      <div v-if="!genericPolicies.length && !loading" class="rounded-lg border border-dashed p-8 text-center text-gray-500">{{ tr('empty') }}</div>
      <article v-for="policy in genericPolicies" :key="policy.id" class="space-y-3 rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900">
        <div class="flex flex-wrap items-center justify-between gap-3"><div class="flex items-center gap-2"><strong>{{ policy.name }}</strong><span class="badge badge-gray">{{ tr(policy.mode) }}</span><span class="badge badge-gray">{{ tr(policy.schema_version === 2 ? 'v2.badge' : 'v2.legacy') }}</span><span v-if="policy.mode === 'enforce' && policy.schema_version !== 2" class="badge badge-gray">{{ tr(policy.action === 'limit' ? 'actionLimit' : 'actionPause') }}</span><span class="badge" :class="policy.enabled ? 'badge-success' : 'badge-gray'">{{ t(policy.enabled ? 'common.enabled' : 'common.disabled') }}</span></div><div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="busy" @click="toggle(policy)">{{ tr(policy.enabled ? 'disablePolicy' : 'enablePolicy') }}</button><button class="btn btn-secondary btn-sm" @click="edit(policy)">{{ t('common.edit') }}</button><button class="btn btn-secondary btn-sm" @click="showPolicy(policy)">{{ tr('records') }}</button><button class="btn btn-secondary btn-sm text-red-600" :disabled="busy" @click="remove(policy)">{{ t('common.delete') }}</button></div></div>
        <p v-if="policy.consolidated_group_ids?.length" class="text-xs text-gray-500">{{ q('consolidated', { groups: policy.consolidated_group_ids.map(id => groups.find(g => g.id === id)?.name || `#${id}`).join(' / ') }) }}</p>
        <p v-if="policy.schema_version !== 2" class="text-xs text-amber-600">{{ tr('v2.legacyHint') }}</p><p class="text-sm text-gray-600 dark:text-gray-300">{{ describe(policy) }}</p><p class="text-xs text-gray-500">{{ policy.group_ids.map(id => groups.find(g => g.id === id)?.name || `#${id}`).join(' / ') }} · {{ policyModelLabel(policy) }} · {{ tr(policy.scope === 'account' ? 'accountScope' : 'modelScope') }}</p>
      </article>
    </section>
    <section v-if="tab === 'records'" class="space-y-4">
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
          <td class="space-y-1 p-2 text-xs"><div>{{ tr('samples') }}: {{ state.evidence.samples }} · {{ tr('failures') }}: {{ state.evidence.failures }}</div><div>{{ targetState(state) ? targetText('availability') : tr('errorRate') }}: {{ metric(targetState(state) && state.evidence.error_rate != null ? 100 - state.evidence.error_rate : state.evidence.error_rate, '%') }}</div><div>{{ tr('ttft') }}: {{ metric(state.evidence.ttft, ' ms') }} ({{ state.evidence.ttft_samples }})</div><div data-passive-tps>{{ tr('tps') }}: {{ metric(state.evidence.output_tps, ' token/s') }} ({{ state.evidence.tps_samples ?? 0 }})</div><div>{{ targetState(state) ? targetText('actualCache') : tr('cacheRate') }}: {{ metric(state.evidence.cache_rate, '%') }} ({{ state.evidence.cache_samples }})</div><div v-if="state.latency && state.phase !== 'exempt'" class="rounded border p-2"><strong>{{ tr('v2.latencyState') }}</strong>: {{ tr('v2.phases.' + state.latency.phase) }} · {{ tr('v2.confirmations', { hits: state.latency.hits }) }} · {{ tr('v2.recoveryConfirmations', { hits: state.latency.recovery_hits ?? 0 }) }}<p>{{ reasonText(state.latency.reason) }}</p></div><div v-if="state.speed && state.phase !== 'exempt'" class="rounded border p-2" data-speed-state><strong>{{ tr('v2.speedState') }}</strong>: {{ tr('v2.phases.' + state.speed.phase) }} · {{ tr('v2.confirmations', { hits: state.speed.hits }) }} · {{ tr('v2.recoveryConfirmations', { hits: state.speed.recovery_hits ?? 0 }) }}<p>{{ reasonText(state.speed.reason) }}</p></div><PassiveFreshEvidence :evidence="state.evidence" /><PassiveCacheEvidence v-if="state.cache && state.phase !== 'exempt'" :cache="state.cache" :config="policies.find(p => p.id === state.policy_id)?.cache_protection" /><div v-if="!state.evidence.complete" class="text-amber-600">{{ tr('telemetryIncomplete') }}</div></td>
          <td class="p-2 text-xs"><template v-if="state.phase === 'trial'"><div>{{ tr('trialUsage', { used: state.used, inflight: state.inflight }) }}</div><div>{{ tr('deadline') }}: {{ date(state.trial_ends_at) }}</div></template><div v-else-if="['limited', 'cache_limited'].includes(statePhase(state))">{{ tr('limitedStateHint') }}</div><div v-else-if="['paused', 'probing', 'exempt'].includes(state.phase)">{{ tr('nextAt') }}: {{ date(state.next_at) }}</div><div v-if="!state.model && state.probe_model && ['paused', 'probing', 'trial'].includes(state.phase)" class="mt-1">{{ tr('probeModel') }}: {{ state.probe_model }}</div><div class="mt-1 text-gray-500">{{ date(state.updated_at) }}</div></td>
          <td class="p-2"><div class="flex flex-wrap gap-2"><button v-if="state.phase === 'paused'" class="btn btn-secondary btn-sm" :disabled="busy" @click="act(state, 'verify')">{{ targetState(state) ? targetText('startTrial') : tr('verify') }}</button><button v-if="state.phase !== 'exempt'" class="btn btn-secondary btn-sm" :disabled="busy" @click="exemptState = state">{{ tr('exempt') }}</button><button v-else class="btn btn-secondary btn-sm" :disabled="busy" @click="act(state, 'cancel_exemption')">{{ tr('cancelExemption') }}</button></div></td>
        </tr><tr v-if="!filteredStates.length"><td colspan="5" class="p-8 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
      </tbody></table></div>
      <p v-if="statesPaginated" class="text-xs text-amber-700">{{ q('refreshPaused') }}</p><div class="flex items-center gap-3"><span class="text-xs text-gray-500">{{ q('loadedStates', { loaded: states.length, total: stateTotal }) }}</span><button v-if="stateCursor" class="btn btn-secondary btn-sm" :disabled="loadingStates" data-more-states @click="loadStates(true)">{{ q('moreStates') }}</button></div><div class="flex items-center justify-between"><h3 class="font-semibold">{{ tr('history') }}</h3><button class="btn btn-secondary btn-sm" :disabled="loadingEvents" @click="loadEvents(false)">{{ t('common.refresh') }}</button></div>
      <div class="flex flex-wrap gap-3 text-sm"><label>{{ q('eventKind') }}<input v-model.trim="kindFilter" data-event-kind class="input ml-2" maxlength="100" /></label><label>{{ q('since') }}<input v-model="sinceFilter" data-event-since type="datetime-local" class="input ml-2" /></label><label>{{ q('until') }}<input v-model="untilFilter" data-event-until type="datetime-local" class="input ml-2" /></label></div><ol class="space-y-2"><li v-for="event in filteredEvents" :key="event.id" class="rounded border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-xs text-gray-500">{{ date(event.created_at) }} · #{{ event.account_id }} · {{ event.policy_name }} · {{ event.detail.model || tr('allModels') }}</span><div>{{ targetState(event.detail) ? targetText('targetState') + ' · ' : '' }}{{ eventText(event.kind) }} · {{ reasonText(event.detail.reason) }}</div><p class="text-xs text-gray-500">{{ tr('samples') }} {{ event.detail.evidence.samples }} · {{ targetState(event.detail) ? targetText('availability') : tr('errorRate') }} {{ metric(targetState(event.detail) && event.detail.evidence.error_rate != null ? 100 - event.detail.evidence.error_rate : event.detail.evidence.error_rate, '%') }} · {{ tr('ttft') }} {{ metric(event.detail.evidence.ttft, ' ms') }} · {{ tr('tps') }} {{ metric(event.detail.evidence.output_tps, ' token/s') }} · {{ targetState(event.detail) ? targetText('actualCache') : tr('cacheRate') }} {{ metric(event.detail.evidence.cache_rate, '%') }}</p><PassiveCacheEvidence v-if="event.detail.cache" :cache="event.detail.cache" /></li></ol>
      <button v-if="hasMore" data-more-events class="btn btn-secondary" :disabled="loadingEvents" @click="loadEvents(true)">{{ tr('more') }}</button>
    </section>

    <BaseDialog :show="!!editing" :title="tr('editorTitle')" width="wide" @close="editing = null">
      <form v-if="editing" class="space-y-6" @submit.prevent="save">
        <div v-if="editing.schema_version !== 2" class="space-y-2 rounded-lg bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-200" data-legacy-policy>
          <strong>{{ tr('v2.legacy') }}</strong><p>{{ tr('v2.legacyHint') }}</p><p>{{ tr('v2.upgradeHint') }}</p><button type="button" class="btn btn-secondary btn-sm" data-upgrade-policy @click="editing = upgradePassivePolicy(editing)">{{ tr('v2.upgrade') }}</button>
        </div>
        <div class="flex flex-wrap items-end justify-between gap-3">
          <label class="input-label min-w-0 flex-1">{{ t('admin.tests.name') }}<input v-model.trim="editing.name" class="input mt-1 w-full" maxlength="160" required /></label>
          <label class="flex items-center gap-2 py-2 text-sm"><input v-model="editing.enabled" type="checkbox" />{{ t('common.enabled') }}</label>
        </div>
        <fieldset><legend class="mb-2 font-semibold">{{ q('scopeStep') }}</legend><div class="flex max-h-40 flex-wrap gap-3 overflow-auto"><label v-for="group in groups.filter(g => g.status === 'active' && g.platform !== 'composite' && !managedGroupIDs.has(g.id))" :key="group.id" class="flex items-center gap-2 text-sm"><input v-model="editing.group_ids" type="checkbox" :value="group.id" :disabled="!editing.group_ids.includes(group.id) && !groupCompatible(group)" />{{ group.name }}</label></div>
          <p class="mt-3 text-xs text-gray-500">{{ q('scopeEmpty') }}</p>
          <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ q('scopeSummary', { groups: editing.group_ids.length, accounts: coveredAccounts }) }}</p>
          <details :open="editing.schema_version === 2 || !!editing.model || !!editing.excluded_account_ids.length" class="mt-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700"><summary class="cursor-pointer text-sm font-medium">{{ q('scopeAdvanced') }}</summary>
          <div class="mt-3 grid gap-3 sm:grid-cols-2"><label class="input-label">{{ tr('modelFilter') }}<input v-model.trim="editing.model" list="passive-models" class="input mt-1 w-full" :placeholder="policyModelLabel({ ...editing, model: '' })" maxlength="200" /><datalist id="passive-models"><option v-for="model in modelChoices" :key="model" :value="model" /></datalist></label><label class="input-label">{{ tr('scope') }}<select v-model="editing.scope" :disabled="editing.schema_version !== 2 && !editing.model" class="input mt-1 w-full"><option value="model" :disabled="editing.schema_version !== 2 && !editing.model">{{ tr(editing.schema_version === 2 ? 'v2.modelScope' : 'modelScope') }}</option><option value="account">{{ tr('accountScope') }}</option></select></label></div>
          <p class="mt-2 text-xs text-gray-500">{{ tr(editing.schema_version === 2 ? 'v2.modelFilterHint' : 'modelFilterHint') }}</p>
          <details class="mt-3 text-sm"><summary class="cursor-pointer">{{ t('admin.tests.excludedAccounts') }} ({{ editing.excluded_account_ids.length }})</summary><input v-model="accountSearch" type="search" class="input my-2 w-full" :placeholder="t('admin.tests.searchExcludedAccounts')" /><div class="max-h-40 space-y-2 overflow-auto"><label v-for="account in exclusionChoices" :key="account.id" class="flex items-center gap-2"><input v-model="editing.excluded_account_ids" type="checkbox" :value="account.id" />{{ account.name }} (#{{ account.id }})</label></div></details>
          </details>
        </fieldset>
        <section class="space-y-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
          <div class="flex flex-wrap justify-between gap-2"><h3 class="font-semibold">{{ q('sensitivity') }}</h3><span class="text-xs text-gray-500" data-current-profile>{{ q('profileCurrent', { profile: q('profiles.' + currentProfile) }) }}</span></div>
          <div class="grid gap-2 sm:grid-cols-3" role="group" :aria-label="q('sensitivity')">
            <button v-for="profile in qualityProfileKeys" :key="profile" type="button" :data-quality-profile="profile" :aria-pressed="currentProfile === profile" class="rounded-lg border p-3 text-left" :class="currentProfile === profile ? 'border-primary-500 bg-primary-50 dark:bg-primary-950/30' : 'border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-900'" @click="applyProfile(profile)"><strong class="text-sm">{{ q('profiles.' + profile) }}</strong><span class="mt-1 block text-xs leading-5 text-gray-500">{{ q('profileDescriptions.' + profile) }}</span></button>
          </div>
          <p class="text-xs text-gray-500">{{ editing.schema_version === 2 ? tr('v2.profileHint') : q('profileHint') }}</p>
        </section>
        <fieldset><legend class="mb-3 font-semibold">{{ q('signalsStep') }}</legend><div class="space-y-3"><div v-for="field in metricFields" :key="field.key" :data-passive-metric="field.key" class="grid items-end gap-3 sm:grid-cols-3"><label class="flex items-center gap-2 text-sm"><input v-model="editing[field.key].enabled" type="checkbox" />{{ q(field.label) }}</label><label v-if="editing[field.key].enabled" class="input-label">{{ tr(['cache_rate', 'tps'].includes(field.key) ? 'pauseBelow' : 'pauseAbove') }}<input :value="editing[field.key].pause / field.divisor" @input="setMetric(field.key, 'pause', $event, field.divisor)" :disabled="!editing[field.key].enabled" type="number" min="0" :max="field.max / field.divisor" :step="field.divisor === 1000 ? 0.001 : 0.1" class="input mt-1 w-full" /></label><label v-if="editing[field.key].enabled" class="input-label">{{ tr(['cache_rate', 'tps'].includes(field.key) ? 'recoverAbove' : 'recoverBelow') }}<input :value="editing[field.key].recover / field.divisor" @input="setMetric(field.key, 'recover', $event, field.divisor)" :disabled="!editing[field.key].enabled" type="number" min="0" :max="field.max / field.divisor" :step="field.divisor === 1000 ? 0.001 : 0.1" class="input mt-1 w-full" /></label></div></div><p class="mt-3 text-xs text-gray-500">{{ editing.schema_version === 2 ? tr('v2.metricHint') : q('metricHelp') }}</p><p v-if="editing.tps.enabled" class="mt-2 text-xs text-gray-500">{{ t('channelMonitorV2.metrics.outputTpsDetail') }}</p><details class="mt-2 text-xs text-gray-500"><summary class="cursor-pointer">{{ tr('evidence') }}</summary><p class="mt-2">{{ tr(editing.schema_version === 2 ? 'v2.metricHint' : 'metricHint') }}</p></details><p v-if="!editing.model && (editing.ttft.enabled || editing.tps.enabled || editing.cache_rate.enabled)" class="mt-2 text-xs text-amber-600">{{ tr(editing.schema_version === 2 ? editing.scope === 'model' ? 'v2.modelIsolationHint' : 'v2.mixedModelHint' : 'mixedModelHint') }}</p></fieldset>
        <PassiveQualityV2Settings v-if="editing.schema_version === 2" v-model="editing" />
        <fieldset v-else class="space-y-3"><legend class="mb-3 font-semibold">{{ q('responseStep') }}</legend>
          <div class="grid gap-2 sm:grid-cols-3" role="group" :aria-label="q('responseStep')"><button v-for="choice in responseChoices" :key="choice" type="button" :data-quality-response="choice" :aria-pressed="response === choice" class="rounded-lg border p-3 text-left" :class="response === choice ? 'border-primary-500 bg-primary-50 dark:bg-primary-950/30' : 'border-gray-200 dark:border-dark-700'" @click="chooseResponse(choice)"><strong class="text-sm">{{ q('responses.' + choice) }}</strong><span class="mt-1 block text-xs leading-5 text-gray-500">{{ q('responseHints.' + choice) }}</span></button></div>
        </fieldset>
        <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" :open="advancedInvalid" data-quality-advanced>
          <summary class="cursor-pointer text-sm font-semibold">{{ q('advanced') }} · {{ q('profiles.' + currentProfile) }}</summary>
          <p class="my-3 text-xs text-gray-500">{{ q('advancedHint') }}</p>
          <div class="space-y-5">
        <fieldset><legend class="mb-3 font-semibold">{{ tr(editing.schema_version === 2 ? 'v2.windowTitle' : 'window') }}</legend><div class="grid gap-3 sm:grid-cols-3"><label v-for="field in windowFields" :key="field.key" class="input-label">{{ tr(field.key) }}<input v-model.number="editing[field.key]" type="number" :min="field.min" :max="field.max" step="1" class="input mt-1 w-full" /></label></div><p class="mt-2 text-xs text-gray-500">{{ tr('windowHint') }}</p></fieldset>
<label class="input-label block">{{ tr('consecutive_failures') }}<input v-model.number="editing.consecutive_failures" type="number" min="0" :max="editing.max_samples" class="input ml-3 w-24" /></label>
        <fieldset><legend class="mb-3 font-semibold">{{ tr(editing.schema_version === 2 ? 'v2.recoveryTitle' : 'recovery') }}</legend><div class="grid gap-3 sm:grid-cols-3"><label v-for="field in recoveryFields.filter(f => editing?.action !== 'limit' || f.key === 'recovery_samples')" :key="field.key" class="input-label">{{ tr(field.key) }}<input v-model.number="editing[field.key]" type="number" :min="field.min" :max="field.max" step="1" class="input mt-1 w-full" /></label></div><p class="mt-3 text-xs text-gray-500">{{ tr(editing.schema_version === 2 ? 'v2.availabilityRecoveryHint' : editing.action === 'limit' ? 'limitedRecovery' : 'recoveryHint') }}</p></fieldset>
          <div v-if="editing.action === 'limit'" class="grid gap-3 sm:grid-cols-2"><label class="input-label">{{ tr('concurrencyLimit') }}<input v-model.number="editing.concurrency_limit" type="number" min="1" max="1000" class="input mt-1 w-full" /></label><label class="input-label">{{ tr('priorityPenalty') }}<input v-model.number="editing.priority_penalty" type="number" min="0" max="1000" class="input mt-1 w-full" /></label></div>
          </div>
        </details>
        <QualityPolicySummary :policy="editing" :groups="groups" :accounts="accounts" :policies="policies" />
        <p v-if="validationError" role="alert" class="text-sm text-amber-600">{{ validationError }}</p>
        <p v-if="editing.id" class="text-xs text-amber-700" data-policy-change-hint>{{ q('policyChangeHint') }}</p><QualityReplayPanel :policy="editing" :accounts="accounts" :disabled="!!validationError" />
      </form>
      <template #footer><button class="btn btn-secondary" @click="editing = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !!validationError" data-passive-save @click="save">{{ t('common.save') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="!!exemptState" :title="tr('exempt')" @close="exemptState = null"><p class="mb-4 text-sm">{{ exemptState?.scope === 'model' && exemptState.model ? tr('v2.modelExemptHint', { model: exemptState.model }) : tr('exemptHint') }}</p><label class="input-label">{{ tr('exemptMinutes') }}<input v-model.number="exemptMinutes" type="number" min="1" max="1440" class="input ml-2 w-28" /></label><template #footer><button class="btn btn-secondary" @click="exemptState = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !Number.isInteger(exemptMinutes) || exemptMinutes < 1 || exemptMinutes > 1440" @click="confirmExemption">{{ t('common.confirm') }}</button></template></BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { useQualityLabels } from '@/composables/useQualityLabels'
import BaseDialog from '@/components/common/BaseDialog.vue'
import QualityPolicySummary from './QualityPolicySummary.vue'
import PassiveQualityV2Settings from './PassiveQualityV2Settings.vue'
import PassiveCacheEvidence from './PassiveCacheEvidence.vue'
import PassiveFreshEvidence from './PassiveFreshEvidence.vue'
import { applyQualityProfile, matchingQualityProfile, policyResponse, setPolicyResponse, qualityProfileKeys, type QualityProfile, type QualityResponse } from './qualityPolicyProfiles'
import QualityReplayPanel from '@/components/tests/QualityReplayPanel.vue'
import { passiveQualityAPI as api, passivePolicyDraft, upgradePassivePolicy, passiveCacheValidation, isGroupQualityTarget, emptyPassiveSummary, type PassivePolicy, type PassiveState, type PassiveEvent } from '@/api/admin/passiveQuality'
import { adminAPI } from '@/api/admin'
import type { AccountListItem, AdminGroup } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ tab: string; groups: AdminGroup[]; accounts: AccountListItem[]; integrated?: boolean }>()
const emit = defineEmits<{ tab: [value: string, policy?: number, account?: number, phase?: string, advanced?: boolean] }>()
const { t, te } = useI18n()
const route = useRoute()
const app = useAppStore()
const tr = (key: string, args: Record<string, unknown> = {}) => t(`admin.tests.passive.${key}`, args)
const q = (key: string, args: Record<string, unknown> = {}) => t('admin.tests.experience.' + key, args)
const targetText = (key: string, args: Record<string, unknown> = {}) => t('admin.tests.groupTargets.' + key, args)
const policies = ref<PassivePolicy[]>([]), states = ref<PassiveState[]>([]), events = ref<PassiveEvent[]>([])
const summary = ref(emptyPassiveSummary()), stateCursor = ref(''), eventCursor = ref(''), stateTotal = ref(0), loadingStates = ref(false), modelFilter = ref(''), kindFilter = ref(''), sinceFilter = ref(''), untilFilter = ref('')
const managedGroupIDs = computed(() => new Set(policies.value.filter(isGroupQualityTarget).flatMap(p => p.group_ids)))
const genericPolicies = computed(() => policies.value.filter(p => !isGroupQualityTarget(p)))
const loading = ref(false), loadingEvents = ref(false), busy = ref(false), loadError = ref(''), hasMore = ref(false)
const editing = ref<PassivePolicy | null>(null), exemptState = ref<PassiveState | null>(null), exemptMinutes = ref(30)
const phases = ['cache_limited', 'limited', 'healthy', 'insufficient', 'suspect', 'observed', 'paused', 'probing', 'trial', 'exempt']
const recordPhase = (value: unknown) => typeof value === 'string' && phases.includes(value) ? value : ''
const phaseFilter = ref(recordPhase(route?.query.phase)), policyFilter = ref(Number(route?.query.policy_id) || 0), accountFilter = ref(Number(route?.query.account_id) || 0), accountSearch = ref('')
const modelChoices = ref<string[]>([])
const editingBaseline = ref('')
const summaryCards = computed(() => [
  { key: 'activePolicies', count: policies.value.filter(p => p.enabled).length },
  { key: 'pausedAccounts', count: summary.value.paused_accounts },
  { key: 'limitedAccounts', count: summary.value.limited_accounts },
  { key: 'trialAccounts', count: summary.value.trial_accounts },
  { key: 'observedAccounts', count: summary.value.observed_accounts },
])
const filteredStates = computed(() => states.value)
const filteredEvents = computed(() => events.value)
const windowFields = [{ key: 'window_seconds', min: 60, max: 86400 }, { key: 'max_samples', min: 5, max: 2000 }, { key: 'min_samples', min: 5, max: 2000 }, { key: 'confirmations', min: 1, max: 10 }, { key: 'new_samples', min: 1, max: 2000 }] as const
const recoveryFields = [{ key: 'cooldown_seconds', min: 30, max: 86400 }, { key: 'max_cooldown_seconds', min: 30, max: 86400 }, { key: 'trial_seconds', min: 60, max: 3600 }, { key: 'trial_requests', min: 5, max: 2000 }, { key: 'trial_concurrency', min: 1, max: 20 }, { key: 'recovery_samples', min: 5, max: 2000 }] as const
const metricFields = [{ key: 'error_rate', label: 'errorSignal', max: 100, divisor: 1 }, { key: 'ttft', label: 'ttftSeconds', max: 600000, divisor: 1000 }, { key: 'tps', label: 'tpsSignal', max: 1000000, divisor: 1 }, { key: 'cache_rate', label: 'cacheSignal', max: 100, divisor: 1 }] as const
const currentProfile = computed(() => editing.value ? matchingQualityProfile(editing.value) : 'balanced')
const response = computed(() => editing.value ? policyResponse(editing.value) : 'observe')
const responseChoices: QualityResponse[] = ['observe', 'limit', 'pause']
const coveredAccounts = computed(() => props.accounts.filter(a => !editing.value?.excluded_account_ids.includes(a.id) && a.group_ids?.some(id => editing.value?.group_ids.includes(id))).length)
function applyProfile(profile: QualityProfile) { if (editing.value) editing.value = applyQualityProfile(editing.value, profile) }
function chooseResponse(choice: QualityResponse) { if (editing.value) editing.value = setPolicyResponse(editing.value, choice) }
function setMetric(key: 'error_rate' | 'ttft' | 'tps' | 'cache_rate', threshold: 'pause' | 'recover', event: Event, divisor: number) {
  if (!editing.value) return
  const value = (event.target as HTMLInputElement).valueAsNumber
  editing.value[key][threshold] = divisor === 1000 ? Math.round(value * divisor) : value
}
const validationError = computed(() => {
  const p = editing.value
  if (!p) return ''
  const cacheError = passiveCacheValidation(p)
  if (cacheError) return tr('v2.' + cacheError)
  if (p.action === 'limit' && (!Number.isInteger(p.concurrency_limit) || p.concurrency_limit < 1 || p.concurrency_limit > 1000 || !Number.isInteger(p.priority_penalty) || p.priority_penalty < 0 || p.priority_penalty > 1000)) return tr('invalidLimits')
  if (!p.name || !p.group_ids.length || p.group_ids.length > 100) return tr('required')
  const groups = props.groups.filter(g => p.group_ids.includes(g.id))
  if (groups.length !== p.group_ids.length || groups.some(g => g.status !== 'active' || g.platform === 'composite' || g.platform !== groups[0].platform)) return t('admin.tests.invalidStrategyGroups')
  if ([...windowFields, ...recoveryFields.filter(f => p.action !== 'limit' || f.key === 'recovery_samples')].some(f => !Number.isInteger(p[f.key]) || p[f.key] < f.min || p[f.key] > f.max) || !Number.isInteger(p.consecutive_failures) || p.consecutive_failures < 0 || p.consecutive_failures > p.max_samples) return tr('invalidLimits')
  if (p.min_samples > p.max_samples) return q('minTooLarge')
  if (p.new_samples > p.max_samples) return q('freshTooLarge')
  if (p.recovery_samples > p.max_samples) return q('recoveryTooLarge')
  if (p.action !== 'limit' && (p.recovery_samples > p.trial_requests || p.trial_concurrency > p.trial_requests)) return q('trialTooSmall')
  if (p.action !== 'limit' && p.max_cooldown_seconds < p.cooldown_seconds) return q('cooldownTooSmall')
  if (!metricFields.some(f => p[f.key].enabled) && !p.consecutive_failures) return tr('selectMetric')
  for (const f of metricFields) { const m = p[f.key]; if (m.enabled && (!Number.isFinite(m.pause) || !Number.isFinite(m.recover) || Math.min(m.pause, m.recover) < 0 || Math.max(m.pause, m.recover) > f.max || (['cache_rate', 'tps'].includes(f.key) ? m.recover <= m.pause : m.recover >= m.pause))) return q('thresholdInvalid', { metric: q(f.label) }) }
  return ''
})
const advancedInvalid = computed(() => [tr('invalidLimits'), ...['invalidMetricActions', 'invalidCacheWindow', 'invalidCacheConfig', 'invalidCacheOrder'].map(key => tr('v2.' + key)), ...['minTooLarge', 'freshTooLarge', 'recoveryTooLarge', 'trialTooSmall', 'cooldownTooSmall'].map(key => q(key))].includes(validationError.value))
const exclusionChoices = computed(() => {
  const p = editing.value
  if (!p) return []
  const choices = new Map(props.accounts.filter(a => a.group_ids?.some(id => p.group_ids.includes(id)) || p.excluded_account_ids.includes(a.id)).map(a => [a.id, { id: a.id, name: a.name }]))
  for (const id of p.excluded_account_ids) if (!choices.has(id)) choices.set(id, { id, name: t('admin.tests.unavailableExcludedAccount') })
  return [...choices.values()].filter(a => `${a.name} ${a.id}`.toLowerCase().includes(accountSearch.value.toLowerCase()))
})
function groupCompatible(group: AdminGroup) { const first = props.groups.find(g => editing.value?.group_ids.includes(g.id)); return !first || first.platform === group.platform }
function statePhase(s: PassiveState) { if (targetState(s) && s.phase === 'healthy' && !s.evidence.complete) return 'insufficient'; if (['paused', 'probing', 'trial', 'exempt'].includes(s.phase)) return s.phase; if (s.phase === 'limited' || s.latency?.phase === 'limited' || s.speed?.phase === 'limited') return 'limited'; if (Object.values(s.cache || {}).some(c => ['reduced', 'recovering'].includes(c.phase))) return 'cache_limited'; if (s.latency?.phase === 'watching' || s.speed?.phase === 'watching' || Object.values(s.cache || {}).some(c => c.phase === 'watching')) return 'observed'; if (s.phase === 'healthy' && Object.values(s.cache || {}).some(c => c.phase === 'insufficient')) return 'insufficient'; return s.phase }
function metric(value: number | null | undefined, unit: string) { return value == null ? '—' : `${Math.round(value * 100) / 100}${unit}` }
function date(value: string) { return !value || value.startsWith('0001') ? '—' : new Date(value).toLocaleString() }
const { reason: reasonText } = useQualityLabels()
function eventText(value: string) { if (te('admin.tests.passive.v2.events.' + value)) return tr('v2.events.' + value); return te(`admin.tests.passive.phases.${value}`) ? tr(`phases.${value}`) : te(`admin.tests.passive.events.${value}`) ? tr(`events.${value}`) : value }
function targetState(state: PassiveState) { return isGroupQualityTarget(state) || policies.value.some(p => p.id === state.policy_id && isGroupQualityTarget(p)) }
function phaseLabel(state: PassiveState) { if (targetState(state) && state.phase === 'healthy' && !state.evidence.complete) return targetText('insufficient'); return targetState(state) && state.phase === 'probing' ? targetText('trialPreparing') : tr('phases.' + statePhase(state)) }
function policyModelLabel(p: Pick<PassivePolicy, 'model' | 'schema_version' | 'scope'>) { return p.model || tr(p.schema_version === 2 ? p.scope === 'model' ? 'v2.allModelsIndependent' : 'v2.allModelsAggregated' : 'allModels') }
function describe(p: PassivePolicy) { if (p.schema_version === 2) return tr('v2.preview'); if (p.mode === 'observe') return q('windowPreview', { minutes: Number((p.window_seconds / 60).toFixed(2)), max: p.max_samples, min: p.min_samples, confirmations: p.confirmations, fresh: p.new_samples }) + ' ' + q('observeFlow'); return tr(p.action === 'limit' ? 'summaryLimit' : 'summary', { cap: p.concurrency_limit, penalty: p.priority_penalty, seconds: p.window_seconds, max: p.max_samples, min: p.min_samples, confirmations: p.confirmations, fresh: p.new_samples, mode: tr(p.mode), cooldown: p.cooldown_seconds, requests: p.trial_requests, concurrency: p.trial_concurrency }) }
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
  if (request === refreshRequest && props.tab === 'records' && (policyChanged || (recordsRequest === statesRequest && refreshRecords))) await loadStates()
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
    stateCursor.value = page.has_more ? page.next_cursor || '' : ''; stateTotal.value = page.total || 0; statesPaginated.value = append; loadError.value = ''
  } catch (e) { if (request === statesRequest) loadError.value = extractApiErrorMessage(e, tr('loadFailed')) } finally { if (request === statesRequest) loadingStates.value = false }
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
  if (key === 'activePolicies') emit('tab', 'passive', undefined, undefined, undefined, true)
  else emit('tab', 'records', undefined, undefined, phaseFilter.value || undefined)
}
function showPolicy(p: PassivePolicy) { policyFilter.value = p.id; phaseFilter.value = ''; accountFilter.value = 0; modelFilter.value = ''; emit('tab', 'records', p.id) }
function edit(p?: PassivePolicy) { if (p && isGroupQualityTarget(p)) { emit('tab', 'group-targets'); return } editing.value = passivePolicyDraft(p); editingBaseline.value = JSON.stringify(editing.value); accountSearch.value = '' }
async function save() {
  if (!editing.value || validationError.value || busy.value) return
  // Avoid an unnecessary write for an unchanged draft.
  if (editing.value.id && JSON.stringify(editing.value) === editingBaseline.value) {
    editing.value = null
    return
  }
  busy.value = true
  try {
    await api.save(editing.value)
    editing.value = null
    await refresh()
    app.showSuccess(t('common.saved'))
  } catch (e) { report(e) } finally { busy.value = false }
}
async function toggle(p: PassivePolicy) { busy.value = true; try { await api.save({ ...p, enabled: !p.enabled }); await refresh() } catch (e) { report(e) } finally { busy.value = false } }
async function remove(p: PassivePolicy) { if (!window.confirm(tr('deleteConfirm', { name: p.name }))) return; busy.value = true; try { await api.remove(p.id); await refresh() } catch (e) { report(e) } finally { busy.value = false } }
async function act(s: PassiveState, action: string, seconds = 0) { busy.value = true; try { await api.act(s, action, seconds); exemptState.value = null; await refresh(); await loadEvents() } catch (e) { report(e) } finally { busy.value = false } }
async function confirmExemption() { if (exemptState.value) await act(exemptState.value, 'exempt', exemptMinutes.value * 60) }
watch(() => editing.value?.model, model => { if (editing.value && editing.value.schema_version !== 2 && !model) editing.value.scope = 'account' }, { flush: 'sync' })
let modelsRequest = 0
watch(() => editing.value?.group_ids.join(','), async () => {
  const request = ++modelsRequest
  modelChoices.value = []
  const groups = props.groups.filter(g => editing.value?.group_ids.includes(g.id))
  if (!groups.length) return
  try { const lists = await Promise.all(groups.map(g => adminAPI.groups.getModelAllowlistCandidates(g.id, g.platform))); if (request === modelsRequest) modelChoices.value = lists.slice(1).reduce((a, b) => a.filter(x => b.includes(x)), lists[0]) } catch (e) { if (request === modelsRequest) report(e) }
})
watch(() => props.tab, tab => { if (tab !== 'passive') editing.value = null; if (tab !== 'records') exemptState.value = null })
watch(() => [props.tab, accountFilter.value, policyFilter.value, modelFilter.value], () => { if (props.tab === 'records') { void loadEvents(); void loadStates() } }, { immediate: true })
watch(phaseFilter, () => { if (props.tab === 'records') void loadStates() })
watch([kindFilter, sinceFilter, untilFilter], () => { if (props.tab === 'records') void loadEvents() })
watch(() => route?.query.account_id, value => { accountFilter.value = Number(value) || 0 })
watch(() => route?.query.policy_id, value => { policyFilter.value = Number(value) || 0 })
watch(() => route?.query.phase, value => { phaseFilter.value = recordPhase(value) })
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { void refresh(false); timer = setInterval(() => { if (!document.hidden && ['passive', 'records'].includes(props.tab)) void refresh(!statesPaginated.value && !loadingStates.value) }, 15000) })
onUnmounted(() => { clearInterval(timer); modelsRequest++; eventsRequest++; statesRequest++; refreshRequest++ })
defineExpose({ refresh })
</script>
