<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('tests.title') }}</h2>
        <button class="btn btn-secondary" :disabled="loading" @click="load(true)"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
      </header>

      <p v-if="auth.isAdmin && reviewError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ reviewError }}</p>
      <div v-if="auth.isAdmin && hasAutomaticReviews" class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-700" data-review-filter>
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.tests.autoReview.filterHint') }}</p>
        <label class="flex items-center gap-2 text-sm"><input v-model="showAllReviewResults" type="checkbox" data-show-all-reviews />{{ t('admin.tests.autoReview.showAllResults') }}</label>
      </div>

      <div v-if="displayResults.length" class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 dark:border-dark-700">
        <nav class="flex min-w-0 flex-1 gap-1 overflow-x-auto" role="tablist" :aria-label="t('tests.groupFilter')">
          <button v-for="group in availableGroups" :id="tabId(group.key)" :key="group.key" type="button" role="tab" :aria-selected="activeGroup === group.key" aria-controls="quality-results" :tabindex="activeGroup === group.key ? 0 : -1" class="shrink-0 border-b-2 px-4 py-3 text-base font-semibold transition-colors" :class="activeGroup === group.key ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'" @click="activeGroup = group.key" @keydown="onGroupKeydown($event, group.key)">{{ group.name }}</button>
        </nav>
        <label class="mb-2 flex w-full items-center gap-2 text-sm text-gray-500 dark:text-gray-400 sm:w-auto">
          <span class="shrink-0">{{ t('tests.modelFilter') }}</span>
          <select v-model="modelFilter" class="input min-w-0 flex-1 text-sm sm:w-48" data-result-model-filter><option value="">{{ t('tests.allModels') }}</option><option v-for="model in availableModels" :key="model" :value="model">{{ model }}</option></select>
        </label>
      </div>

      <div v-if="votesLoadError" class="rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/20 dark:text-amber-200" role="alert" data-votes-error>{{ votesLoadError }}<button type="button" class="ml-3 underline" :disabled="loading" @click="load(true)">{{ t('common.retry') }}</button></div>
      <p v-if="!displayResults.length" class="py-16 text-center text-sm text-gray-500">{{ loading ? t('common.loading') : t('tests.empty') }}</p>
      <section v-else id="quality-results" ref="resultsPanel" class="min-w-0 space-y-5" role="tabpanel" :aria-labelledby="tabId(activeGroup)" tabindex="0">
        <section v-if="filteredVotes.length" class="space-y-4" data-voting-section>
          <div><h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('tests.voting.title') }}</h3><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('tests.voting.description') }}</p></div>
          <TestVoteCard v-for="item in filteredVotes" :key="item.result.id" :item="item" :busy="votingResultIds.has(item.result.id)" :error="voteErrors[item.result.id]" @vote="castVote(item.result.id, $event)" />
        </section>
        <p v-if="!accountResults.length && !filteredVotes.length" class="py-16 text-center text-sm text-gray-500">{{ t('tests.noMatches') }}</p>
        <article v-for="account in accountResults" :key="account.key" class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900" data-account-result>
          <header class="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-gray-200 bg-gray-50/70 px-4 py-2.5 dark:border-dark-700 dark:bg-dark-800/50" data-account-info>
            <div class="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 break-words"><h3 class="min-w-0 text-base font-semibold text-gray-900 dark:text-white">{{ account.name }}</h3><p class="text-xs text-gray-500 dark:text-gray-400">{{ account.groupName }}</p></div>
            <div class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 text-xs">
              <div v-if="account.schedulable != null" class="flex items-center gap-2" data-scheduling-status>
                <span class="text-gray-500 dark:text-gray-400">{{ t('tests.currentScheduling') }}</span>
                <span class="inline-flex items-center gap-1.5 font-medium" :class="account.schedulable ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'"><span class="h-1.5 w-1.5 rounded-full bg-current" />{{ t(account.schedulable ? 'tests.schedulingActive' : 'tests.schedulingPaused') }}</span>
              </div>
              <div v-if="modelFilter && account.selectedModelSchedulable != null" class="flex items-center gap-2" data-selected-model-status>
                <span class="text-gray-500 dark:text-gray-400">{{ t('tests.currentModelScheduling') }}</span>
                <span class="font-medium" :class="account.selectedModelSchedulable ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'">{{ t(account.selectedModelSchedulable ? 'tests.modelSchedulingActive' : 'tests.modelSchedulingPaused') }}</span>
              </div>
            </div>
          </header>
          <div class="grid min-w-0 grid-cols-[repeat(auto-fit,minmax(min(100%,14rem),1fr))] gap-3 p-3" data-account-tests>
            <section
              v-for="test in account.tests"
              :key="test.key"
              class="min-w-0 rounded-md border border-gray-100 p-3 dark:border-dark-800"
              data-test-section
              :data-numeric-test="test.latest.output_kind === 'number' ? '' : undefined"
              :data-statistics-test="test.latest.output_kind === 'statistics' ? '' : undefined"
              :data-content-test="!['number', 'statistics'].includes(test.latest.output_kind) ? '' : undefined"
            >
              <div class="mb-3">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <h4 class="min-w-0 break-words text-sm font-semibold text-gray-900 dark:text-white">{{ test.name }}</h4>
                  <button v-if="historyAnchor(test)" type="button" class="shrink-0 text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="openHistory(test)">{{ t('tests.viewHistory') }}</button>
                </div>
                <div class="mt-1.5 flex flex-wrap items-center gap-1.5"><span :class="statusClass(test.latest)">{{ statusLabel(test.latest) }}</span><span class="min-w-0 break-all text-xs text-gray-500 dark:text-gray-400">{{ modelLabel(test.latest) }}<span v-if="test.latest.output_kind !== 'statistics' && test.latest.latency_ms != null"> · {{ test.latest.latency_ms }}ms</span></span></div>
                <p v-if="test.latest.model_schedulable != null" class="mt-1 text-xs font-medium" :class="test.latest.model_schedulable ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'" data-model-scheduling-status>{{ t(test.latest.model_schedulable ? 'tests.modelSchedulingActive' : 'tests.modelSchedulingPaused') }}</p>
              </div>
              <template v-if="test.latest.output_kind === 'statistics'">
                <TestResultOutput :result="test.latest" compact />
                <AdminTestDecision v-if="reviewFor(test.latest)" :review="reviewFor(test.latest)!" compact @decided="refreshAfterDecision" />
              </template>
              <div v-else-if="test.latest.output_kind === 'number'" data-numeric-gallery>
                <figure :key="test.latest.id" class="min-w-0">
                  <strong class="block break-all text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ test.latest.output_numeric ?? '-' }}</strong>
                  <figcaption class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ formatDate(resultTime(test.latest)) }}</figcaption>
                  <AdminTestDecision v-if="reviewFor(test.latest)" :review="reviewFor(test.latest)!" compact @decided="refreshAfterDecision" />
                </figure>
              </div>
              <div v-else data-result-gallery>
                <figure :key="test.latest.id" class="min-w-0">
                  <TestResultOutput :result="test.latest" compact :max-preview-height="180" />
                  <figcaption class="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"><span>{{ formatDate(resultTime(test.latest)) }}</span><button v-if="test.latest.output_kind === 'html' && test.latest.output_html" type="button" class="font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400" data-expand-preview @click="previewTarget = test.latest">{{ t('tests.expandPreview') }}</button></figcaption>
                  <AdminTestDecision v-if="reviewFor(test.latest)" :review="reviewFor(test.latest)!" compact @decided="refreshAfterDecision" />
                </figure>
              </div>
            </section>
          </div>
        </article>
      </section>
    </div>

    <BaseDialog :show="!!historyTarget" :title="historyTarget ? `${targetName(historyTarget.latest)} · ${historyTarget.name}` : ''" width="extra-wide" @close="closeHistory">
      <div v-if="historyTarget" class="space-y-4">
        <div class="divide-y divide-gray-200 dark:divide-dark-700"><article v-for="result in historyResults" :key="result.id" class="py-5 first:pt-0"><div class="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"><span>{{ modelLabel(result) }}<span v-if="result.output_kind !== 'statistics' && result.latency_ms != null"> · {{ result.latency_ms }}ms</span> · {{ formatDate(resultTime(result)) }}</span><span :class="statusClass(result)">{{ statusLabel(result) }}</span></div><TestResultOutput :result="result" /></article></div>
        <p v-if="historyLoading" class="py-4 text-center text-sm text-gray-500" role="status">{{ t('common.loading') }}</p>
        <div v-else-if="historyError" class="flex flex-wrap items-center justify-center gap-3 py-4 text-sm"><p class="text-red-600 dark:text-red-400" role="alert">{{ historyError }}</p><button type="button" class="btn btn-secondary" @click="loadHistory"><Icon name="refresh" size="sm" />{{ t('common.retry') }}</button></div>
        <p v-else-if="!historyResults.length" class="py-4 text-center text-sm text-gray-500">{{ t('tests.empty') }}</p>
        <div v-else-if="historyBeforeId" class="flex justify-center"><button type="button" class="btn btn-secondary" @click="loadHistory">{{ t('tests.loadMore') }}</button></div>
      </div>
    </BaseDialog>
    <BaseDialog :show="!!previewTarget" :title="previewTarget ? `${targetName(previewTarget)} · ${testName(previewTarget)}` : ''" width="extra-wide" @close="previewTarget = null">
      <template v-if="previewTarget">
        <TestResultOutput :result="previewTarget" />
        <AdminTestDecision v-if="reviewFor(previewTarget)" :review="reviewFor(previewTarget)!" @decided="refreshAfterDecision" />
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { testResultsAPI } from '@/api/testResults'
import { listReviews, listResults as listAdminResults, type TestAdminReview } from '@/api/admin/tests'
import type { TestResult, TestVote, TestVoteResult } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import AdminTestDecision from '@/components/tests/AdminTestDecision.vue'
import TestVoteCard from '@/components/tests/TestVoteCard.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'

interface TestSeries {
  key: string
  name: string
  order: number
  latest: TestResult
  results: TestResult[]
}

const { t } = useI18n()
const app = useAppStore()
const auth = useAuthStore()
const loading = ref(false)
const allResults = ref<TestResult[]>([])
const voteResults = ref<TestVoteResult[]>([])
const votesLoadError = ref('')
const votingResultIds = ref(new Set<number>())
const voteErrors = ref<Record<number, string>>({})
let dataRevision = 0
let unmounted = false
let loadPromise: Promise<void> | undefined
const adminReviews = ref<TestAdminReview[]>([])
const reviewError = ref('')
const reviewIndex = computed(() => new Map(auth.isAdmin ? adminReviews.value.map(review => [review.result.id, review]) : []))
const reviewFor = (result: TestResult) => reviewIndex.value.get(result.id)
const showAllReviewResults = ref(false)
const hasAutomaticReviews = computed(() => auth.isAdmin && regularResults.value.some(result => !!result.execution_info?.auto_review))
const reviewNeedsAttention = (result: TestResult) => {
  if (!auth.isAdmin || showAllReviewResults.value) return true
  const review = reviewFor(result)
  const automatic = result.execution_info?.auto_review
  return !automatic || (automatic.verdict === 'pending' && !review?.admin_verdict)
}
// Reviews and votes provide a fallback when a result has no live scheduling
// projection. A paused account remains visible in the regular result list.
const pausedAccountIds = computed(() => {
  const ids = new Set<number>()
  for (const review of adminReviews.value) {
    if (review.account_paused && review.result.account_id != null) ids.add(review.result.account_id)
  }
  for (const item of voteResults.value) {
    if (item.voting.account_paused && item.result.account_id != null) ids.add(item.result.account_id)
  }
  return ids
})
const isHiddenPausedAccount = (result: TestResult) => result.account_id != null && pausedAccountIds.value.has(result.account_id)
const visibleVoteResults = computed(() => voteResults.value.filter(item => !isHiddenPausedAccount(item.result)))
const regularResults = computed(() => {
  const records = new Map(allResults.value.map(result => [result.id, result]))
  // The normal result endpoint contains the freshest group projection. Active
  // review records are merged only when that result is unavailable; an older
  // review payload must not move a freshly re-routed account back to its
  // source group until the next poll.
  if (auth.isAdmin) for (const review of adminReviews.value) {
    const current = records.get(review.result.id)
    records.set(review.result.id, current ? { ...review.result, ...current, account_name: review.result.account_name, ...(review.result.execution_info ? { execution_info: { ...current.execution_info, ...review.result.execution_info } } : {}) } : review.result)
  }
  return [...records.values()]
})
const displayResults = computed(() => {
  const records = new Map(regularResults.value.map(result => [result.id, result]))
  for (const item of visibleVoteResults.value) if (!records.has(item.result.id)) records.set(item.result.id, item.result)
  return [...records.values()]
})
const activeGroup = ref('')
const modelFilter = ref('')
const historyTarget = ref<TestSeries | null>(null)
const previewTarget = ref<TestResult | null>(null)
watch(displayResults, results => {
  if (previewTarget.value) previewTarget.value = results.find(result => result.id === previewTarget.value?.id) ?? null
})
const historyResults = ref<TestResult[]>([])
const historyLoading = ref(false)
const historyError = ref('')
const historyBeforeId = ref<number>()
let adminHistoryLimit = 20
let historyRequest = 0
const resultsPanel = ref<HTMLElement | null>(null)
watch([activeGroup, modelFilter], () => {
  if (resultsPanel.value) resultsPanel.value.scrollTop = 0
}, { flush: 'post' })

const resultTime = (result: TestResult) => result.started_at || result.finished_at || result.created_at
const resultTimestamp = (result: TestResult) => new Date(resultTime(result) || 0).getTime()
const sortResults = (items: TestResult[]) => [...items].sort((a, b) => resultTimestamp(b) - resultTimestamp(a) || b.id - a.id)
const testName = (result: TestResult) => result.test_name || result.plan_name || result.test_definition?.name || result.plan?.name || t('tests.unknownType')
const orderValue = (value?: number) => typeof value === 'number' && Number.isFinite(value) ? value : Number.MAX_SAFE_INTEGER
const exposesAccount = (result: TestResult) => result.target_mode !== 'group' && result.account_id != null
const targetKey = (result: TestResult) => exposesAccount(result) ? `account:${result.account_id}` : 'group'
const groupKey = (result: TestResult) => result.group_id != null ? `group:${result.group_id}` : result.group_name ? `name:${result.group_name}` : 'ungrouped'
const groupName = (result: TestResult) => result.group_name || (result.group_id != null ? `${t('tests.group')} #${result.group_id}` : t('tests.ungrouped'))
const targetName = (result: TestResult) => exposesAccount(result) ? `${t('tests.account')} #${result.account_id}${auth.isAdmin && result.account_name ? ` · ${result.account_name}` : ''}` : t('tests.groupCheck')
const testKey = (result: TestResult) => `${result.test_definition_id ?? result.test_definition?.id ?? testName(result)}:${result.model_id || ''}:${result.reasoning_effort || ''}`
const tabId = (key: string) => `quality-group-${encodeURIComponent(key)}`

const availableGroups = computed(() => {
  const groups = new Map<string, { key: string; name: string; order: number }>()
  for (const result of displayResults.value) {
    const key = groupKey(result)
    const order = orderValue(result.group_order ?? result.plan_order)
    const previous = groups.get(key)
    if (!previous || order < previous.order) groups.set(key, { key, name: groupName(result), order })
  }
  return [...groups.values()].sort((a, b) => a.order - b.order || a.name.localeCompare(b.name))
})
watch(availableGroups, groups => {
  if (!groups.some(group => group.key === activeGroup.value)) activeGroup.value = groups[0]?.key || ''
}, { immediate: true })
const availableModels = computed(() => [...new Set(displayResults.value.map(result => result.model_id).filter((model): model is string => Boolean(model)))].sort())
const projectedVotes = computed(() => {
  const records = new Map(regularResults.value.map(result => [result.id, result]))
  return visibleVoteResults.value.map(item => {
    const current = records.get(item.result.id)
    return current ? { ...item, result: { ...item.result, group_id: current.group_id, group_name: current.group_name, group_order: current.group_order, plan_order: current.plan_order,
      account_schedulable: current.account_schedulable ?? item.result.account_schedulable,
      model_schedulable: current.model_schedulable ?? item.result.model_schedulable,
    } } : item
  })
})
const filteredVotes = computed(() => projectedVotes.value.filter(item => reviewNeedsAttention(reviewFor(item.result)?.result || item.result) && groupKey(item.result) === activeGroup.value && (!modelFilter.value || item.result.model_id === modelFilter.value)))
const filteredResults = computed(() => sortResults(regularResults.value.filter(result => groupKey(result) === activeGroup.value && (!modelFilter.value || result.model_id === modelFilter.value))))
const schedulingState = (result: TestResult) => result.account_schedulable
  ?? (isHiddenPausedAccount(result) ? false : undefined)
const isSkippedResult = (result: TestResult) => result.execution_info?.failure_kind === 'quality_deferred' || (result.status === 'failed' && (result.error_message || '').startsWith('检测跳过：'))
const resultRank = (result: TestResult) => {
  if (result.model_schedulable === false) return 3
  if (isSkippedResult(result)) return 1
  if (['failed', 'error'].includes(result.status) || result.output_model_check?.verdict === 'fail' || result.execution_info?.capability?.verdict === 'fail' || result.execution_info?.auto_review?.verdict === 'fail') return 2
  if (!isSuccessful(result) || result.execution_info?.auto_review?.verdict === 'pending' || (result.output_kind === 'model_check' && result.output_model_check?.verdict !== 'pass') || (result.output_kind === 'capability' && result.execution_info?.capability?.verdict !== 'pass')) return 1
  return 0
}
const accountResults = computed(() => {
  const accounts = new Map<string, { key: string; name: string; groupName: string; order: number; tests: Map<string, TestSeries> }>()
  for (const result of filteredResults.value) {
    const key = targetKey(result)
    let account = accounts.get(key)
    if (!account) {
      account = { key, name: targetName(result), groupName: groupName(result), order: exposesAccount(result) ? result.account_id! : -1, tests: new Map() }
      accounts.set(key, account)
    }
    const seriesKey = testKey(result)
    let series = account.tests.get(seriesKey)
    if (!series) {
      series = { key: seriesKey, name: testName(result), order: orderValue(result.test_order ?? result.test_definition?.sort_order), latest: result, results: [] }
      account.tests.set(seriesKey, series)
    }
    series.results.push(result)
  }
  return [...accounts.values()].map(account => {
    const tests = [...account.tests.values()].filter(test => reviewNeedsAttention(test.latest)).sort((a, b) => a.order - b.order || a.name.localeCompare(b.name) || a.key.localeCompare(b.key))
    const states = tests.map(test => schedulingState(test.latest))
    const schedulable = states.includes(false) ? false : states.includes(true) ? true : undefined
    const modelStates = modelFilter.value ? tests.map(test => test.latest.model_schedulable) : []
    const selectedModelSchedulable = modelStates.includes(false) ? false : modelStates.includes(true) ? true : undefined
    const rank = schedulable === false ? 3 : Math.max(...tests.map(test => resultRank(test.latest)))
    return { ...account, tests, schedulable, selectedModelSchedulable, rank }
  }).filter(account => account.tests.length > 0).sort((a, b) => a.rank - b.rank || a.order - b.order)
})

const isSuccessful = (result: TestResult) => ['success', 'passed'].includes(result.status)
const historyAnchor = (series: TestSeries) => series.results.find(result => ['success', 'passed', 'failed', 'error'].includes(result.status))
const closeHistory = () => {
  historyRequest++
  historyTarget.value = null
  historyResults.value = []
  historyBeforeId.value = undefined
  historyError.value = ''
  historyLoading.value = false
}
const loadHistory = async () => {
  const anchor = historyTarget.value && historyAnchor(historyTarget.value)
  if (!anchor || historyLoading.value) return
  const request = ++historyRequest
  historyLoading.value = true
  historyError.value = ''
  try {
    // Administrator history can include results from before a group move.
    const planID = anchor.plan_id
    const page = auth.isAdmin && planID != null ? await (async () => {
      const rows = await listAdminResults(planID, adminHistoryLimit + 1)
      const items = sortResults(rows.filter(result => isSuccessful(result) && (exposesAccount(anchor) || groupKey(result) === groupKey(anchor))
        && targetKey(result) === targetKey(anchor) && testKey(result) === testKey(anchor)))
      return { items: items.slice(0, adminHistoryLimit), next_before_id: items.length > adminHistoryLimit ? items[adminHistoryLimit - 1].id : undefined }
    })() : await testResultsAPI.history(anchor.id, historyBeforeId.value)
    if (request !== historyRequest) return
    const records = new Map(historyResults.value.map(result => [result.id, result]))
    for (const result of page.items.filter(isSuccessful)) records.set(result.id, result)
    historyResults.value = sortResults([...records.values()])
    historyBeforeId.value = page.next_before_id
    if (auth.isAdmin) adminHistoryLimit += 20
  } catch (error) {
    if (request !== historyRequest) return
    const failure = error as { status?: number; response?: { status?: number } }
    const status = failure?.status ?? failure?.response?.status
    if (status === 403 || status === 404) {
      closeHistory()
      void load(true)
    } else historyError.value = extractApiErrorMessage(error, t('tests.loadFailed'))
  } finally {
    if (request === historyRequest) historyLoading.value = false
  }
}
const openHistory = (series: TestSeries) => {
  if (!historyAnchor(series)) return
  historyRequest++
  historyTarget.value = series
  historyResults.value = []
  historyBeforeId.value = undefined
  adminHistoryLimit = 20
  historyError.value = ''
  historyLoading.value = false
  void loadHistory()
}
const statusClass = (result: TestResult) => result.status === 'running' || result.status === 'pending' || isSkippedResult(result) ? 'badge badge-warning'
  : !isSuccessful(result) ? 'badge badge-danger'
  : result.execution_info?.auto_review ? result.execution_info.auto_review.verdict === 'pass' ? 'badge badge-success' : result.execution_info.auto_review.verdict === 'fail' ? 'badge badge-danger' : 'badge badge-warning'
  : result.output_kind === 'capability' ? result.execution_info?.capability?.verdict === 'pass' ? 'badge badge-success' : result.execution_info?.capability?.verdict === 'fail' ? 'badge badge-danger' : 'badge badge-warning'
  : result.output_kind === 'model_check' ? result.output_model_check?.verdict === 'pass' ? 'badge badge-success' : result.output_model_check?.verdict === 'fail' ? 'badge badge-danger' : 'badge badge-warning' : 'badge badge-success'
const statusLabel = (result: TestResult) => result.status === 'running' || result.status === 'pending' ? t('tests.running')
  : isSkippedResult(result) ? t('tests.skipped')
  : !isSuccessful(result) ? t('tests.failed')
  : result.execution_info?.auto_review ? t(`admin.tests.autoReview.${result.execution_info.auto_review.verdict}`)
  : result.output_kind === 'capability' ? t(`admin.tests.capability.verdicts.${result.execution_info?.capability?.verdict || 'pending'}`)
  : result.output_kind === 'model_check' ? t(`tests.modelCheck.${result.output_model_check?.verdict || 'unknown'}`) : t('tests.completed')
const modelLabel = (result: TestResult) => `${result.model_id || '-'}${result.reasoning_effort ? ` · ${t('tests.reasoningEffort')}: ${result.reasoning_effort}` : ''}`
const formatDate = (value?: string) => value ? new Date(value).toLocaleString() : '-'
const onGroupKeydown = async (event: KeyboardEvent, key: string) => {
  const groups = availableGroups.value
  const index = groups.findIndex(group => group.key === key)
  let next = index
  if (event.key === 'ArrowRight') next = (index + 1) % groups.length
  else if (event.key === 'ArrowLeft') next = (index - 1 + groups.length) % groups.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = groups.length - 1
  else return
  event.preventDefault()
  activeGroup.value = groups[next].key
  await nextTick()
  document.getElementById(tabId(activeGroup.value))?.focus()
}
const refreshAfterDecision = () => {
  closeHistory()
  return load(true)
}
const reconcileOpenHistory = () => {
  const anchor = historyTarget.value && historyAnchor(historyTarget.value)
  if (!anchor) return
  // A group move or revoked result access invalidates every cached page and
  // any in-flight history response, including when someone else moved it.
  const visible = regularResults.value.some(result => result.plan_id === anchor.plan_id && targetKey(result) === targetKey(anchor)
    && testKey(result) === testKey(anchor) && groupKey(result) === groupKey(anchor))
  if (!visible) closeHistory()
}
const visibleVote = (item: TestVoteResult) => item.voting.enabled && isSuccessful(item.result)
  && item.result.account_id != null && item.result.target_mode !== 'group' && item.result.output_kind !== 'statistics'
const castVote = async (id: number, vote: TestVote) => {
  if (votingResultIds.value.has(id)) return
  const current = voteResults.value.find(item => item.result.id === id)
  if (!current?.voting.open) return
  dataRevision++
  votingResultIds.value.add(id)
  delete voteErrors.value[id]
  try {
    await testResultsAPI.vote(id, vote)
  } catch (error) {
    if (!unmounted) {
      voteErrors.value[id] = extractApiErrorMessage(error, t('tests.voting.voteFailed'))
      // A closed or revoked result may disappear during the refresh below.
      app.showError(voteErrors.value[id])
    }
  } finally {
    dataRevision++
    if (!unmounted) {
      // Voting can move an account to a group the user cannot access. Never
      // retain an old result, an open history, or the POST response as proof
      // of continued access; reload both public projections after each vote.
      allResults.value = []
      voteResults.value = []
      closeHistory()
      previewTarget.value = null
      await load(true)
    }
    votingResultIds.value.delete(id)
  }
}
const load = (force = false): Promise<void> => {
  if (unmounted) return Promise.resolve()
  if (loadPromise && !force) return loadPromise
  loading.value = true
  const revision = ++dataRevision
  const administrator = auth.isAdmin
  const request = Promise.allSettled([
    testResultsAPI.list(3),
    testResultsAPI.votes(),
    administrator ? listReviews() : Promise.resolve<TestAdminReview[]>([]),
  ]).then(([results, votes, reviews]) => {
    if (unmounted || revision !== dataRevision) return
    // Publish one complete refresh together. An old voting or review response
    // must never keep the previous group's tab alongside freshly moved results.
    allResults.value = results.status === 'fulfilled'
      ? sortResults(results.value.filter(result => ['success', 'passed', 'failed', 'error', 'pending', 'running'].includes(result.status))) : []
    voteResults.value = votes.status === 'fulfilled' ? votes.value.filter(visibleVote) : []
    adminReviews.value = administrator && auth.isAdmin && reviews.status === 'fulfilled' ? reviews.value : []
    votesLoadError.value = votes.status === 'rejected' ? extractApiErrorMessage(votes.reason, t('tests.voting.loadFailed')) : ''
    reviewError.value = administrator && reviews.status === 'rejected' ? extractApiErrorMessage(reviews.reason, t('tests.adminReview.loadFailed')) : ''
    if (results.status === 'rejected') app.showError(extractApiErrorMessage(results.reason, t('tests.loadFailed')))
    reconcileOpenHistory()
  }).finally(() => {
    if (loadPromise === request) {
      loading.value = false
      loadPromise = undefined
    }
  })
  loadPromise = request
  return request
}
const refreshVisiblePage = () => {
  if (!document.hidden) void load(true)
}
let resultTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  resultTimer = setInterval(() => { if (!document.hidden) void load() }, 5000)
  document.addEventListener('visibilitychange', refreshVisiblePage)
  window.addEventListener('focus', refreshVisiblePage)
})
onUnmounted(() => {
  unmounted = true
  dataRevision++
  clearInterval(resultTimer)
  document.removeEventListener('visibilitychange', refreshVisiblePage)
  window.removeEventListener('focus', refreshVisiblePage)
  historyRequest++
})
</script>
