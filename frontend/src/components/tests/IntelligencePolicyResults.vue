<template>
  <section class="space-y-5">
    <p v-if="error" role="alert" class="text-sm text-red-600">
      {{ error }} <button class="underline" type="button" @click="load()">{{ t('common.retry') }}</button>
    </p>
    <p v-if="retryNotice" role="status" class="text-sm text-gray-600 dark:text-gray-300">{{ retryNotice }}</p>
    <p v-if="retryError" role="alert" class="text-sm text-red-600">{{ retryError }}</p>
    <p v-if="loading && !data" role="status" class="py-8 text-center text-sm">{{ t('common.loading') }}</p>
    <template v-if="data">
      <div class="flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg bg-gray-50 px-4 py-3 text-sm dark:bg-dark-800">
        <span class="flex items-center gap-2"><i class="h-2 w-2 rounded-full bg-rose-500" />{{ tr('isolated') }} <b class="tabular-nums">{{ blocked.length }}</b></span>
        <span class="flex items-center gap-2"><i class="h-2 w-2 rounded-full bg-amber-500" />{{ tr('suspects') }} <b class="tabular-nums">{{ suspects.length }}</b></span>
        <span v-if="plan.enabled && plan.next_run_at" class="text-xs text-gray-500 dark:text-gray-400 sm:ml-auto">{{ tr('nextRun') }} · {{ time(plan.next_run_at) }}</span>
      </div>
      <ul v-if="data.states.length" class="max-h-72 divide-y divide-gray-100 overflow-y-auto rounded-lg border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
        <li v-for="s in data.states" :key="s.account_id + '-' + s.model" class="result-grid px-4 py-3">
          <div>
            <p class="identity">{{ s.account_name }} <span class="metadata">#{{ s.account_id }}</span></p>
            <p class="model">{{ s.model }}</p>
          </div>
          <div class="space-y-1.5">
            <span :class="['badge', s.blocked ? 'badge-danger' : 'badge-warning']">{{ tr(s.blocked ? 'isolated' : 'states.suspect') }}</span>
            <template v-if="s.blocked">
              <p class="metadata">{{ tr('holdEvidence') }}<span v-if="s.hold_round"> · {{ time(s.hold_round.finished_at) }}</span></p>
              <IntelligenceCheckDetails :checks="s.hold_round?.checks" :artwork-veto="s.hold_round?.artwork_veto" :review-reason="s.hold_round?.review_reason" />
            </template>
          </div>
          <div class="space-y-1 text-xs">
            <p v-if="s.blocked">{{ tr('recovering', { pass: s.passes, total: plan.protection?.intelligence?.recovery_passes }) }}</p>
            <p v-if="s.next_check" class="metadata">{{ tr('nextCheck') }} · {{ time(s.next_check) }}</p>
            <button v-if="s.blocked" type="button" class="result-action" @click="openRelease(s)">{{ tr('releaseHold') }}</button>
          </div>
        </li>
      </ul>

      <section>
        <header class="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h4 class="text-sm font-semibold">{{ tr('recentResults') }}</h4>
          <label v-if="data.results.length" class="flex cursor-pointer items-center gap-2 text-xs">
            <input v-model="issuesOnly" type="checkbox" class="rounded border-gray-300 text-primary-600" />{{ tr('needsAttention') }}
          </label>
        </header>
        <p v-if="!filteredResults.length" class="rounded-lg bg-gray-50 py-6 text-center text-sm text-gray-500 dark:bg-dark-800 dark:text-gray-400">{{ tr(data.results.length ? 'noIssues' : 'emptyResults') }}</p>
        <div v-else class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700">
          <div aria-hidden="true" class="result-grid result-head bg-gray-50 px-4 py-2 text-xs font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400 md:grid">
            <span>{{ tr('account') }} / {{ tr('modelScope') }}</span><span>{{ tr('verdict') }}</span><span>{{ tr('artwork') }}</span>
          </div>
          <ol class="divide-y divide-gray-100 dark:divide-dark-700">
            <li v-for="r in visibleResults" :key="r.result_id" :data-result-id="r.result_id" class="result-grid border-gray-100 px-4 py-3.5 dark:border-dark-700">
              <div>
                <p class="identity">{{ r.account_name }} <span class="metadata">#{{ r.account_id }}</span></p>
                <p class="model">{{ r.model }}</p>
                <p class="metadata mt-1">{{ time(r.finished_at) }}</p>
              </div>
              <div class="space-y-1.5">
                <span :class="['badge', verdictClass[r.verdict]]">{{ verdict(r.verdict) }}</span>
                <IntelligenceCheckDetails
                  :checks="r.evidence?.checks"
                  :http-status="r.evidence?.candy_http_status"
                  :artwork-veto="r.reviewed === 'fail'"
                  :artwork-missing="r.artwork === 'missing'"
                  :review-reason="r.review_reason"
                />
                <p v-if="r.error_message" class="break-words text-xs">{{ r.error_message }}</p>
                <p v-if="r.reviewed && r.evidence" class="metadata">{{ tr('autoVerdict') }} · {{ verdict(r.evidence.auto_verdict) }}</p>
              </div>
              <div class="space-y-1 text-xs">
                <p class="metadata"><span class="md:hidden">{{ tr('artwork') }} · </span>{{ artStatus(r) }}</p>
                <p v-if="r.review_reason && r.reviewed === 'pass'" class="whitespace-pre-wrap break-words">{{ tr('reviewNote') }} · {{ r.review_reason }}</p>
                <button v-if="r.artwork === 'default'" type="button" class="result-action" @click="open(r)">{{ tr('review') }}</button>
                <button v-else-if="plan.enabled && r.artwork === 'missing' && r.retryable_artwork !== false" type="button" class="result-action" :disabled="!!retryId" @click="retry(r.result_id)">{{ tr('retryArtwork') }}</button>
              </div>
            </li>
          </ol>
        </div>
        <nav v-if="pageCount > 1" class="mt-3 flex items-center justify-end gap-3 text-xs">
          <span class="metadata">{{ tr('resultRange', { from: offset + 1, to: offset + visibleResults.length, total: filteredResults.length }) }}</span>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="page === 1" @click="page--">{{ tr('prev') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="page === pageCount" @click="page++">{{ tr('next') }}</button>
        </nav>
      </section>
    </template>
    <BaseDialog
      :z-index="60"
      :show="!!releaseTarget"
      :title="tr('releaseHold')"
      width="narrow"
      :close-on-escape="!releasing"
      :show-close-button="!releasing"
      @close="releaseTarget = undefined"
    >
      <p v-if="releaseTarget" class="text-sm">
        {{
          tr('releaseConfirm', {
            account: [releaseTarget.account_name, '#' + releaseTarget.account_id].filter(Boolean).join(' '),
            model: releaseTarget.model,
          })
        }}
      </p>
      <p v-if="releaseError" role="alert" class="mt-2 text-sm text-red-600">{{ releaseError }}</p>
      <template #footer>
        <button class="btn btn-secondary" type="button" :disabled="releasing" @click="releaseTarget = undefined">
          {{ t('common.cancel') }}
        </button>
        <button class="btn btn-primary" type="button" :disabled="releasing" @click="release">{{ tr('releaseHold') }}</button>
      </template>
    </BaseDialog>
    <BaseDialog
      :z-index="60"
      :show="!!selected"
      :title="tr('review')"
      width="wide"
      :close-on-escape="!saving"
      :show-close-button="!saving"
      @close="close"
    >
      <div v-if="selected" class="space-y-3 text-sm">
        <strong>{{ selected.account_name }} #{{ selected.account_id }} · {{ selected.model }}</strong>
        <p>{{ time(selected.finished_at) }} · {{ artStatus(selected) }}</p>
        <p class="text-xs text-gray-500">{{ tr('reviewRule') }}</p>
        <p v-if="selected.review_reason" class="whitespace-pre-wrap break-words text-xs">
          {{ tr('reviewNote') }} · {{ selected.review_reason }}
        </p>
        <p v-if="artLoading" role="status">{{ t('common.loading') }}</p>
        <TestResultOutput v-else-if="preview" :result="preview" :max-preview-height="460" />
        <p v-if="artError" role="alert" class="text-red-600">
          {{ artError }} <button type="button" class="underline" @click="artRefresh++">{{ t('common.retry') }}</button>
        </p>
        <label class="input-label"
          >{{ tr('reason') }}<textarea v-model="reason" class="input block w-full" rows="2" maxlength="500" :disabled="saving" />
        </label>
        <p v-if="reviewError" role="alert" class="text-red-600">{{ reviewError }}</p>
      </div>
      <template #footer>
        <button class="btn btn-secondary" type="button" :disabled="!preview || saving" @click="submit('fail')">
          {{ tr('reject') }}
        </button>
        <button class="btn btn-primary" type="button" :disabled="!preview || saving" @click="submit('pass')">
          {{ tr('approve') }}
        </button>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import IntelligenceCheckDetails from './IntelligenceCheckDetails.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import {
  releaseIntelligenceHold,
  type IntelligenceAccountState,
  policyArtwork,
  policyOverview,
  reviewPolicyArtwork,
  type IntelligenceObservation,
  type IntelligenceOverview,
} from '@/api/admin/intelligence'
import { adminAPI } from '@/api/admin'
import type { TestPlan, TestResult } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ plan: TestPlan; refreshKey: number }>()
const { t, locale } = useI18n()
const tr = (s: string, params: Record<string, unknown> = {}) => t('channelMonitorV2.intelligence.' + s, params)
const time = (stamp: string) => new Date(stamp).toLocaleString(locale.value)
const data = ref<IntelligenceOverview>()
const error = ref(''),
  loading = ref(false)
const blocked = computed(() => data.value?.states.filter((s) => s.blocked) || [])
const suspects = computed(() => data.value?.states.filter((s) => !s.blocked && s.verdict === 'suspect') || [])
const issuesOnly = ref(false)
const page = ref(1)
const pageSize = 10
const filteredResults = computed(() => {
  const rows = data.value?.results || []
  if (!issuesOnly.value) return rows
  return rows.filter(r => r.verdict !== 'pass' || r.error_message || r.evidence?.checks?.some(c => c.status !== 'pass'))
})
const pageCount = computed(() => Math.ceil(filteredResults.value.length / pageSize))
const offset = computed(() => (page.value - 1) * pageSize)
const visibleResults = computed(() => filteredResults.value.slice(offset.value, offset.value + pageSize))
watch(issuesOnly, () => { page.value = 1 })
watch(pageCount, count => { page.value = Math.min(page.value, Math.max(count, 1)) })
const verdictClass = { pass: 'badge-success', confirmed: 'badge-danger', suspect: 'badge-warning', unknown: 'badge-gray' }
const verdict = (v: IntelligenceObservation['verdict']) => tr('states.' + (v === 'confirmed' ? 'degraded' : v))
const artStatus = (r: IntelligenceObservation) =>
  tr('art.' + (r.reviewed === 'pass' ? 'approved' : r.reviewed === 'fail' ? 'veto' : r.artwork))
const releaseTarget = ref<IntelligenceAccountState>(),
  releaseError = ref(''),
  releasing = ref(false)
function openRelease(state: IntelligenceAccountState) {
  releaseTarget.value = state
  releaseError.value = ''
}
async function release() {
  const state = releaseTarget.value
  if (!state || releasing.value) return
  releasing.value = true
  try {
    await releaseIntelligenceHold(props.plan.id, state.account_id, state.model)
    if (mounted) {
      releaseTarget.value = undefined
      await load()
    }
  } catch (e) {
    if (mounted) releaseError.value = extractApiErrorMessage(e, tr('saveError'))
  } finally {
    releasing.value = false
  }
}
const retryId = ref<number>(),
  retryError = ref(''),
  retryNotice = ref('')
let mounted = true
async function retry(id: number) {
  if (retryId.value) return
  retryId.value = id
  retryError.value = ''
  retryNotice.value = ''
  try {
    await adminAPI.tests.retryResult(id)
    if (mounted) {
      retryNotice.value = tr('retrySubmitted')
      await load()
    }
  } catch (e) {
    if (mounted) retryError.value = extractApiErrorMessage(e, tr('saveError'))
  } finally {
    retryId.value = undefined
  }
}
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
async function load() {
  clearTimeout(refreshTimer)
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = ''
  try {
    const result = await policyOverview(props.plan.id, request.signal)
    if (!request.signal.aborted) data.value = result
  } catch (e) {
    if (!request.signal.aborted) error.value = extractApiErrorMessage(e, tr('loadError'))
  } finally {
    if (!request.signal.aborted) {
      loading.value = false
      if (!document.hidden) refreshTimer = setTimeout(load, 15000)
    }
  }
}
function onVisibility() {
  if (document.hidden) clearTimeout(refreshTimer)
  else void load()
}
document.addEventListener('visibilitychange', onVisibility)
const selectedId = ref<number>(),
  selected = computed(() => data.value?.results.find((r) => r.result_id === selectedId.value))
const reason = ref(''),
  html = ref(''),
  saving = ref(false),
  artLoading = ref(false),
  artError = ref(''),
  reviewError = ref(''),
  artRefresh = ref(0)
const preview = computed<TestResult | undefined>(() =>
  selected.value && html.value
    ? { id: selected.value.result_id, status: 'success', output_kind: 'html', output_html: html.value }
    : undefined,
)
function open(r: IntelligenceObservation) {
  selectedId.value = r.result_id
  reason.value = ''
  reviewError.value = ''
}
function close() {
  if (!saving.value) selectedId.value = undefined
}
watch([selectedId, artRefresh], async ([id], _, cleanup) => {
  const request = new AbortController()
  cleanup(() => request.abort())
  html.value = ''
  artError.value = ''
  artLoading.value = !!id
  if (!id) return
  try {
    const content = await policyArtwork(id, request.signal)
    if (!request.signal.aborted) {
      html.value = content
      if (!content) artError.value = tr('artError')
    }
  } catch (e) {
    if (!request.signal.aborted) artError.value = extractApiErrorMessage(e, tr('artError'))
  } finally {
    if (!request.signal.aborted) artLoading.value = false
  }
})
async function submit(choice: 'pass' | 'fail') {
  const id = selectedId.value
  if (!id || !preview.value || saving.value) return
  saving.value = true
  reviewError.value = ''
  try {
    await reviewPolicyArtwork(id, choice, reason.value.trim())
    if (selectedId.value === id) {
      selectedId.value = undefined
      await load()
    }
  } catch (e) {
    if (selectedId.value === id) reviewError.value = extractApiErrorMessage(e, tr('saveError'))
  } finally {
    saving.value = false
  }
}
watch(
  [() => props.plan.id, () => props.refreshKey],
  ([id], old) => {
    if (!old || id !== old[0]) {
      data.value = undefined
      selectedId.value = undefined
    }
    void load()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  mounted = false
  selectedId.value = undefined
  controller?.abort()
  clearTimeout(refreshTimer)
  document.removeEventListener('visibilitychange', onVisibility)
})
</script>

<style scoped>
.result-grid {
  @apply grid grid-cols-1 items-start gap-x-5 gap-y-2 md:grid-cols-[minmax(0,1fr)_minmax(0,1.25fr)_minmax(0,.8fr)];
}
.result-head {
  @apply hidden md:grid;
}
.identity {
  @apply break-words text-sm font-semibold text-gray-900 dark:text-gray-100;
}
.model {
  @apply mt-0.5 break-all font-mono text-xs text-gray-700 dark:text-gray-300;
}
.metadata {
  @apply text-xs font-normal text-gray-500 dark:text-gray-400;
}
.result-action {
  @apply rounded text-left text-xs font-medium text-primary-700 hover:underline disabled:opacity-50 dark:text-primary-300;
}
</style>
