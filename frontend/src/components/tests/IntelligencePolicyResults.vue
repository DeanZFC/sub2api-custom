<template>
  <section class="card space-y-4 p-4">
    <header class="flex flex-wrap items-baseline justify-between gap-2">
      <h3 class="font-semibold">{{ tr('resultsTitle') }}</h3>
      <span v-if="plan.enabled && plan.next_run_at" class="text-xs text-gray-500"
        >{{ tr('nextRun') }} · {{ time(plan.next_run_at) }}</span
      >
    </header>
    <p v-if="error" role="alert" class="text-sm text-red-600">
      {{ error }} <button class="underline" type="button" @click="load()">{{ t('common.retry') }}</button>
    </p>
    <p v-if="retryNotice" role="status" class="text-xs text-gray-500">{{ retryNotice }}</p>
    <p v-if="retryError" role="alert" class="text-sm text-red-600">{{ retryError }}</p>
    <p v-if="loading && !data" role="status">{{ t('common.loading') }}</p>
    <template v-if="data">
      <div class="flex flex-wrap gap-x-4 gap-y-1 text-sm">
        <span>{{ tr('isolated') }} {{ blocked.length }}</span
        ><span>{{ tr('suspects') }} {{ suspects.length }}</span>
      </div>
      <ul v-if="data.states.length" class="max-h-60 divide-y overflow-y-auto text-sm dark:divide-dark-700">
        <li
          v-for="s in data.states"
          :key="s.account_id + '-' + s.model"
          class="flex flex-wrap items-baseline gap-x-3 gap-y-1 py-2"
        >
          <strong>{{ s.account_name || '#' + s.account_id }}</strong
          ><span>{{ s.model }}</span>
          <span>{{ s.blocked ? tr('isolated') : tr('states.suspect') }}</span>
          <span v-if="s.blocked">{{
            tr('recovering', { pass: s.passes, total: plan.protection?.intelligence?.recovery_passes })
          }}</span>
          <button
            v-if="s.blocked"
            type="button"
            class="text-xs text-primary-600 underline dark:text-primary-300"
            @click="openRelease(s)"
          >
            {{ tr('releaseHold') }}
          </button>
          <span v-if="s.next_check" class="text-xs text-gray-500">{{ tr('nextCheck') }} · {{ time(s.next_check) }}</span>
        </li>
      </ul>
      <h4 class="text-sm font-medium">{{ tr('recentResults') }}</h4>
      <p v-if="!data.results.length" class="text-sm text-gray-500">{{ tr('emptyResults') }}</p>
      <div v-else class="max-h-96 overflow-auto">
        <table class="w-full text-left text-xs">
          <thead>
            <tr class="text-gray-500">
              <th>{{ tr('account') }}</th>
              <th>{{ tr('modelScope') }}</th>
              <th>{{ tr('completed') }}</th>
              <th>{{ tr('verdict') }}</th>
              <th>{{ tr('artwork') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in data.results" :key="r.result_id" class="border-t dark:border-dark-700">
              <td>{{ r.account_name || '#' + r.account_id }}</td>
              <td>{{ r.model }}</td>
              <td>{{ time(r.finished_at) }}</td>
              <td>
                <details v-if="r.evidence || r.review_reason">
                  <summary class="cursor-pointer">{{ verdict(r.verdict) }}</summary>
                  <div class="mt-2 min-w-40 space-y-1">
                    <p v-if="r.evidence">
                      <strong>{{ tr('autoVerdict') }}</strong> · {{ verdict(r.evidence.auto_verdict) }}
                    </p>
                    <dl v-if="r.evidence?.checks?.length">
                      <div v-for="c in r.evidence.checks" :key="c.key" class="py-0.5">
                        <dt class="font-medium">
                          {{ tr('checks.' + (c.key === 'http_429' ? 'rate_limit' : c.key)) }} ·
                          {{ tr('checkStates.' + c.status) }}
                        </dt>
                        <dd v-if="c.detail" class="break-words text-gray-500">{{ c.detail }}</dd>
                      </div>
                    </dl>
                    <p v-if="r.reviewed">{{ tr('review') }} · {{ artStatus(r) }}</p>
                    <p v-if="r.review_reason" class="whitespace-pre-wrap break-words">
                      {{ tr('reviewNote') }} · {{ r.review_reason }}
                    </p>
                  </div>
                </details>
                <span v-else>{{ verdict(r.verdict) }}</span>
              </td>
              <td>
                <button
                  v-if="r.artwork === 'default'"
                  type="button"
                  class="text-left text-primary-600 underline dark:text-primary-300"
                  @click="open(r)"
                >
                  {{ artStatus(r) }} · {{ tr('review') }}
                </button>
                <button
                  v-else-if="plan.enabled && r.artwork === 'missing' && r.retryable_artwork !== false"
                  type="button"
                  class="text-primary-600 underline dark:text-primary-300"
                  :disabled="!!retryId"
                  @click="retry(r.result_id)"
                >
                  {{ tr('retryArtwork') }}
                </button>
                <span v-else>{{ artStatus(r) }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
    <BaseDialog
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
            account: releaseTarget.account_name || '#' + releaseTarget.account_id,
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
      :show="!!selected"
      :title="tr('review')"
      width="wide"
      :close-on-escape="!saving"
      :show-close-button="!saving"
      @close="close"
    >
      <div v-if="selected" class="space-y-3 text-sm">
        <strong>{{ selected.account_name }} · {{ selected.model }}</strong>
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
th,
td {
  @apply px-2 py-2 align-top;
}
</style>
