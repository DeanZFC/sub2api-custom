<template>
  <section class="space-y-5" data-quality-overview>
    <div class="rounded-xl border border-primary-100 bg-primary-50/60 p-5 dark:border-primary-900 dark:bg-primary-950/20">
      <h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.experience.overviewTitle') }}</h3>
      <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.tests.experience.overviewHint') }}</p>
      <div class="mt-3 grid gap-3 md:grid-cols-2">
        <button v-for="goal in goals" :key="goal.tab" :data-overview-goal="goal.tab" type="button" class="rounded-lg border border-primary-100 bg-white p-4 text-left transition hover:border-primary-400 dark:border-dark-600 dark:bg-dark-900" @click="$emit('navigate', goal.tab)"><strong class="text-sm text-gray-900 dark:text-white">{{ t('admin.tests.experience.' + goal.title) }} →</strong><span class="mt-2 block text-xs leading-5 text-gray-500">{{ t('admin.tests.experience.' + goal.hint) }}</span></button>
      </div>
    </div>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="load(false)">
      <label class="input-label">{{ tr('search') }}<input v-model.trim="search" class="input mt-1" maxlength="200" /></label>
      <label class="input-label">{{ t('admin.tests.target') }}<select v-model.number="group" class="input mt-1"><option :value="0">{{ t('admin.tests.passive.all') }}</option><option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }}</option></select></label>
      <label class="input-label">{{ t('admin.tests.model') }}<input v-model.trim="model" class="input mt-1" maxlength="200" /></label>
      <label class="flex items-center gap-2 py-2 text-sm"><input v-model="problemsOnly" type="checkbox" />{{ tr('problemsOnly') }}</label>
      <button class="btn btn-secondary" :disabled="loading">{{ t('common.refresh') }}</button>
    </form>
    <p class="text-xs text-gray-500">{{ tr('coverageHint') }} · {{ date(updatedAt) }}</p>
    <p v-if="paginated" class="flex flex-wrap items-center gap-2 text-xs text-amber-700" data-overview-refresh-paused>{{ t('admin.tests.experience.refreshPaused') }}<button type="button" class="underline" :disabled="loading" @click="refreshLoaded">{{ t('admin.tests.experience.refreshLoaded') }}</button></p><p v-if="error" role="alert" class="text-sm text-red-600">{{ error }} <button class="underline" @click="load(false)">{{ t('common.retry') }}</button></p>
    <div class="space-y-3">
      <article v-for="account in items" :key="account.id" class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900" :data-quality-account="account.id">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-800">
          <div><strong>{{ account.name }}</strong><span class="ml-2 text-xs text-gray-500">#{{ account.id }} · {{ account.platform }}</span><p class="mt-1 text-xs text-gray-500">{{ tr('coverage', { active: account.active_plans, passive: account.passive_policies }) }}</p></div>
          <div class="flex flex-wrap items-center gap-3">
            <span class="badge" :class="account.controls.some(c => c.blocking) ? 'badge-warning' : 'badge-gray'">{{ tr(account.controls.some(c => c.blocking) ? 'restricted' : account.controls.some(c => c.phase === 'limited') ? 'limited' : account.controls.some(c => c.phase === 'cache_limited') ? 'cacheLimited' : 'noHold') }}</span>
          </div>
        </div>
        <p v-if="!account.controls.length" class="px-5 py-4 text-sm text-gray-500">{{ tr('noEvidence') }}</p>
        <div v-for="(control, index) in account.controls" :key="`${control.source}-${control.id}-${control.metric || ''}-${control.model}-${index}`" class="grid gap-3 border-b border-gray-100 px-5 py-4 text-sm last:border-0 dark:border-dark-800 lg:grid-cols-[1fr_2fr_1fr_auto]">
          <div><span class="text-xs text-gray-500">{{ tr(`sources.${control.source}`) }}</span><p v-if="control.metric" class="mt-1 text-xs text-gray-500">{{ tr('metrics.' + control.metric) }}</p><p class="mt-1 font-medium">{{ control.name && control.source !== 'account' ? control.name : reason(control.reason) }}</p><p class="mt-1 text-xs text-gray-500">{{ control.model || tr('allModels') }} · {{ tr(control.scope === 'account' ? 'accountScope' : 'modelScope') }}</p></div>
          <div><span class="badge" :class="control.blocking ? 'badge-warning' : 'badge-gray'">{{ t(`admin.tests.passive.phases.${controlPhase(control)}`) }}</span><p class="mt-2 break-words text-gray-600 dark:text-gray-300">{{ reason(control.reason) || '—' }}</p><p v-if="control.evidence" class="mt-2 text-xs text-gray-500">{{ control.metric === 'group_quality_target' ? t('admin.tests.groupTargets.evidenceSummary', { samples: control.evidence.samples, availability: metric(control.evidence.error_rate == null ? null : 100 - control.evidence.error_rate, '%'), ttft: metric(control.evidence.ttft == null ? null : control.evidence.ttft / 1000, ' s'), cache: metric(control.evidence.cache_rate, '%'), tps: metric(control.evidence.output_tps, ' token/s') }) : tr('evidence', { samples: control.evidence.samples, error: metric(control.evidence.error_rate, '%'), ttft: metric(control.evidence.ttft, ' ms'), cache: metric(control.evidence.cache_rate, '%'), tps: metric(control.evidence.output_tps, ' token/s') }) }}</p><div v-if="control.cache" class="mt-2 space-y-1 text-xs" data-overview-cache><strong>{{ t('admin.tests.passive.v2.allocation', { value: control.allocation_percent }) }}</strong><p>{{ t('admin.tests.passive.v2.actualRate') }}: {{ metric(control.cache.action.cache_rate, '%') }} · {{ t('admin.tests.passive.v2.protectionRate') }}: {{ metric(control.cache.action.protection_rate, '%') }}</p><p>{{ t('admin.tests.passive.v2.sampleEvidence', { total: control.cache.action.total_samples, reusable: control.cache.action.reusable_samples, cold: control.cache.action.cold_samples, unknown: control.cache.action.unknown_samples, missing: control.cache.action.missing_telemetry_samples }) }}</p></div><TestPassRateEvidence v-if="control.pass_rate" :evidence="control.pass_rate" class="mt-3" /></div>
          <div class="text-xs text-gray-500"><p>{{ tr(`recovery.${control.recovery}`) }}</p><p class="mt-2">{{ date(control.next_at) }}</p><p class="mt-2">{{ tr('updated') }} {{ date(control.updated_at) }}</p></div>
          <button v-if="control.source !== 'account'" class="btn btn-secondary btn-sm self-start" @click="$emit('navigate', control.source === 'active' ? 'plans' : 'records', control.id, account.id)">{{ tr('inspect') }}</button>
        </div>
      </article>
      <p v-if="!loading && !items.length && !error" class="rounded-xl border border-dashed p-10 text-center text-gray-500">{{ t('common.noData') }}</p>
    </div>
    <button v-if="nextAfter" class="btn btn-secondary" :disabled="loading" @click="load(true)">{{ t('admin.tests.passive.more') }}</button>
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { channelQualityAPI, type QualityAccount, type QualityControl } from '@/api/admin/channelQuality'
import { useQualityLabels } from '@/composables/useQualityLabels'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'
import TestPassRateEvidence from './TestPassRateEvidence.vue'
const props = defineProps<{ groups: AdminGroup[]; accountId?: number; visible: boolean }>()
defineEmits<{ navigate: [tab: string, policy?: number, account?: number] }>()
const { t } = useI18n()
const goals = [{ tab: 'passive', title: 'trafficGoal', hint: 'trafficGoalHint' }, { tab: 'plans', title: 'scheduledGoal', hint: 'scheduledGoalHint' }]
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.quality.${key}`, values)
const { reason, date } = useQualityLabels()
const search = ref(''), model = ref(''), group = ref(0), problemsOnly = ref(false)
const items = ref<QualityAccount[]>([]), nextAfter = ref(0), loading = ref(false), error = ref(''), updatedAt = ref('')
let request = 0
const paginated = ref(false)
let filterKey = ''
async function load(append = false) {
  const key = JSON.stringify([search.value, model.value, group.value, props.accountId, problemsOnly.value])
  append = append && key === filterKey && nextAfter.value > 0
  const token = ++request
  loading.value = true
  try {
    const page = await channelQualityAPI.overview({ search: search.value, model: model.value, group_id: group.value, account_id: props.accountId, problems_only: problemsOnly.value, after: append ? nextAfter.value : undefined })
    if (token !== request) return
    items.value = append ? [...items.value, ...page.items] : page.items
    paginated.value = append
    filterKey = key
    nextAfter.value = page.next_after || 0
    updatedAt.value = page.generated_at
    error.value = ''
  } catch (e) { if (token === request) error.value = extractApiErrorMessage(e, tr('loadFailed')) }
  finally { if (token === request) loading.value = false }
}
async function refreshLoaded() {
  const key = JSON.stringify([search.value, model.value, group.value, props.accountId, problemsOnly.value])
  if (key !== filterKey) { await load(); return }
  const token = ++request, count = items.value.length
  loading.value = true
  try {
    const refreshed: QualityAccount[] = []; let after = 0; let generated = ''
    do {
      const page = await channelQualityAPI.overview({ search: search.value, model: model.value, group_id: group.value, account_id: props.accountId, problems_only: problemsOnly.value, after: after || undefined })
      if (token !== request) return
      refreshed.push(...page.items); generated = page.generated_at
      const next = page.next_after || 0
      if (!next || next <= after) { after = 0; break }
      after = next
    } while (refreshed.length < count)
    items.value = refreshed; nextAfter.value = after; updatedAt.value = generated; error.value = ''
  } catch (e) { if (token === request) error.value = extractApiErrorMessage(e, tr('loadFailed')) } finally { if (token === request) loading.value = false }
}
function controlPhase(control: QualityControl) { return control.metric === 'group_quality_target' && control.phase === 'healthy' && !control.evidence?.complete ? 'insufficient' : control.phase }
const metric = (value: number | null | undefined, suffix: string) => value == null ? '—' : `${Math.round(value * 100) / 100}${suffix}`
watch(() => [props.visible, props.accountId], () => { if (props.visible) void load() })
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { if (props.visible) void load(); timer = setInterval(() => { if (props.visible && !document.hidden && !loading.value && !paginated.value) void load() }, 15000) })
onUnmounted(() => { request++; clearInterval(timer) })
defineExpose({ refresh: load })
</script>
