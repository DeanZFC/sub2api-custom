<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('tests.title') }}</h2>
        <button class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
      </header>

      <p v-if="auth.isAdmin && reviewError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ reviewError }}</p>

      <div v-if="displayResults.length" class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 dark:border-dark-700">
        <nav class="flex min-w-0 flex-1 gap-1 overflow-x-auto" role="tablist" :aria-label="t('tests.groupFilter')">
          <button v-for="group in availableGroups" :id="tabId(group.key)" :key="group.key" type="button" role="tab" :aria-selected="activeGroup === group.key" aria-controls="quality-results" :tabindex="activeGroup === group.key ? 0 : -1" class="shrink-0 border-b-2 px-4 py-3 text-base font-semibold transition-colors" :class="activeGroup === group.key ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'" @click="activeGroup = group.key" @keydown="onGroupKeydown($event, group.key)">{{ group.name }}</button>
        </nav>
        <label class="mb-2 flex w-full items-center gap-2 text-sm text-gray-500 dark:text-gray-400 sm:w-auto">
          <span class="shrink-0">{{ t('tests.modelFilter') }}</span>
          <select v-model="modelFilter" class="input min-w-0 flex-1 text-sm sm:w-48"><option value="">{{ t('tests.allModels') }}</option><option v-for="model in availableModels" :key="model" :value="model">{{ model }}</option></select>
        </label>
      </div>

      <p v-if="!displayResults.length" class="py-16 text-center text-sm text-gray-500">{{ loading ? t('common.loading') : t('tests.empty') }}</p>
      <section v-else id="quality-results" ref="resultsPanel" class="min-w-0 space-y-5" role="tabpanel" :aria-labelledby="tabId(activeGroup)" tabindex="0">
        <p v-if="!accountResults.length" class="py-16 text-center text-sm text-gray-500">{{ t('tests.noMatches') }}</p>
        <article v-for="account in accountResults" :key="account.key" class="grid overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900 lg:grid-cols-[12rem_minmax(0,1fr)]" data-account-result>
          <header class="flex min-w-0 flex-col justify-center border-b border-gray-200 p-4 dark:border-dark-700 sm:p-5 lg:border-b-0 lg:border-r" data-account-info>
            <div class="min-w-0 break-words"><h3 class="text-xl font-semibold text-gray-900 dark:text-white">{{ account.name }}</h3><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ account.groupName }}</p></div>
          </header>
          <div class="min-w-0 divide-y divide-gray-200 dark:divide-dark-700" data-account-tests>
            <section
              v-for="test in account.tests"
              :key="test.key"
              class="min-w-0 p-4 sm:p-5"
              data-test-section
              :data-numeric-test="test.latest.output_kind === 'number' ? '' : undefined"
              :data-statistics-test="test.latest.output_kind === 'statistics' ? '' : undefined"
              :data-content-test="!['number', 'statistics'].includes(test.latest.output_kind) ? '' : undefined"
            >
              <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
                <div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><h4 class="text-base font-semibold text-gray-900 dark:text-white">{{ test.name }}</h4><span :class="statusClass(test.latest)">{{ statusLabel(test.latest) }}</span></div><p class="mt-1 break-words text-xs text-gray-500 dark:text-gray-400">{{ modelLabel(test.latest) }}<span v-if="test.latest.output_kind !== 'statistics' && test.latest.latency_ms != null"> · {{ test.latest.latency_ms }}ms</span></p></div>
                <button v-if="historyAnchor(test)" type="button" class="shrink-0 text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="openHistory(test)">{{ t('tests.viewHistory') }}</button>
              </div>
              <template v-if="test.latest.output_kind === 'statistics'">
                <TestResultOutput :result="test.latest" />
                <AdminTestDecision v-if="reviewFor(test.latest)" :review="reviewFor(test.latest)!" @decided="loadReviews" />
              </template>
              <div v-else-if="test.latest.output_kind === 'number'" class="grid grid-cols-3 gap-3" data-numeric-gallery>
                <figure v-for="(result, index) in test.recent" :key="result.id" class="min-w-0">
                  <strong class="block break-all text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ result.output_numeric ?? '-' }}</strong>
                  <figcaption class="mt-1 text-xs text-gray-500 dark:text-gray-400"><span class="block">{{ index === 0 ? t('tests.latestResult') : t('tests.previousResult') }}</span><span class="mt-1 block">{{ formatDate(resultTime(result)) }}</span><span v-if="result.latency_ms != null" class="mt-1 block">{{ result.latency_ms }}ms</span></figcaption>
                  <AdminTestDecision v-if="reviewFor(result)" :review="reviewFor(result)!" @decided="loadReviews" />
                </figure>
              </div>
              <div v-else class="grid items-start gap-4 lg:grid-cols-3" data-result-gallery>
                <figure v-for="(result, index) in test.recent" :key="result.id" class="min-w-0">
                  <TestResultOutput :result="result" compact />
                  <figcaption class="mt-2 flex flex-wrap items-center gap-x-1 text-xs text-gray-500 dark:text-gray-400"><span :class="index === 0 ? 'font-medium text-gray-700 dark:text-gray-200' : ''">{{ index === 0 ? t('tests.latestResult') : t('tests.previousResult') }}</span><span>· {{ formatDate(resultTime(result)) }}</span></figcaption>
                  <AdminTestDecision v-if="reviewFor(result)" :review="reviewFor(result)!" @decided="loadReviews" />
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
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { testResultsAPI } from '@/api/testResults'
import { listReviews, listResults as listAdminResults, type TestAdminReview } from '@/api/admin/tests'
import type { TestResult } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import AdminTestDecision from '@/components/tests/AdminTestDecision.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'

interface TestSeries {
  key: string
  name: string
  order: number
  latest: TestResult
  results: TestResult[]
  recent: TestResult[]
}

const { t } = useI18n()
const app = useAppStore()
const auth = useAuthStore()
const loading = ref(false)
const allResults = ref<TestResult[]>([])
const adminReviews = ref<TestAdminReview[]>([])
const reviewError = ref('')
let reviewRequest = 0
const reviewIndex = computed(() => new Map(auth.isAdmin ? adminReviews.value.map(review => [review.result.id, review]) : []))
const reviewFor = (result: TestResult) => reviewIndex.value.get(result.id)
const displayResults = computed(() => {
  const records = new Map(allResults.value.map(result => [result.id, result]))
  if (auth.isAdmin) for (const review of adminReviews.value) records.set(review.result.id, review.result)
  return [...records.values()]
})
const activeGroup = ref('')
const modelFilter = ref('')
const historyTarget = ref<TestSeries | null>(null)
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
    const order = orderValue(result.plan_order)
    const previous = groups.get(key)
    if (!previous || order < previous.order) groups.set(key, { key, name: groupName(result), order })
  }
  return [...groups.values()].sort((a, b) => a.order - b.order || a.name.localeCompare(b.name))
})
watch(availableGroups, groups => {
  if (!groups.some(group => group.key === activeGroup.value)) activeGroup.value = groups[0]?.key || ''
}, { immediate: true })
const availableModels = computed(() => [...new Set(displayResults.value.map(result => result.model_id).filter((model): model is string => Boolean(model)))].sort())
const filteredResults = computed(() => sortResults(displayResults.value.filter(result => groupKey(result) === activeGroup.value && (!modelFilter.value || result.model_id === modelFilter.value))))
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
      series = { key: seriesKey, name: testName(result), order: orderValue(result.test_order ?? result.test_definition?.sort_order), latest: result, results: [], recent: [] }
      account.tests.set(seriesKey, series)
    }
    series.results.push(result)
  }
  return [...accounts.values()].sort((a, b) => a.order - b.order).map(account => {
    const tests = [...account.tests.values()].sort((a, b) => a.order - b.order || a.name.localeCompare(b.name) || a.key.localeCompare(b.key))
    for (const test of tests) test.recent = test.results.slice(0, 3)
    return { ...account, tests }
  })
})

const isSuccessful = (result: TestResult) => ['success', 'passed'].includes(result.status)
const historyAnchor = (series: TestSeries) => series.results.find(isSuccessful)
const closeHistory = () => {
  historyRequest++
  historyTarget.value = null
  historyLoading.value = false
}
const loadHistory = async () => {
  const anchor = historyTarget.value && historyAnchor(historyTarget.value)
  if (!anchor || historyLoading.value) return
  const request = ++historyRequest
  historyLoading.value = true
  historyError.value = ''
  try {
    // Paused or moved accounts remain reviewable through administrator access.
    const planID = anchor.plan_id
    const page = auth.isAdmin && planID != null ? await (async () => {
      const rows = await listAdminResults(planID, adminHistoryLimit + 1)
      const items = sortResults(rows.filter(result => isSuccessful(result) && groupKey(result) === groupKey(anchor)
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
    if (request === historyRequest) historyError.value = extractApiErrorMessage(error, t('tests.loadFailed'))
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
const statusClass = (result: TestResult) => result.status === 'running' || result.status === 'pending' ? 'badge badge-warning' : 'badge badge-success'
const statusLabel = (result: TestResult) => result.status === 'running' || result.status === 'pending' ? t('tests.running') : t('tests.completed')
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
const loadReviews = async () => {
  const request = ++reviewRequest
  if (!auth.isAdmin) {
    adminReviews.value = []
    return
  }
  try {
    const reviews = await listReviews()
    if (request !== reviewRequest) return
    adminReviews.value = reviews
    reviewError.value = ''
  } catch (error) {
    if (request !== reviewRequest) return
    adminReviews.value = []
    reviewError.value = extractApiErrorMessage(error, t('tests.adminReview.loadFailed'))
  }
}
const load = async () => {
  if (loading.value) return
  loading.value = true
  const reviews = loadReviews()
  try {
    const results = await testResultsAPI.list(3)
    // Failed upstream responses must stay private, including against an older server.
    allResults.value = sortResults(results.filter(result => ['success', 'passed', 'pending', 'running'].includes(result.status)))
  } catch (error) {
    app.showError(extractApiErrorMessage(error, t('tests.loadFailed')))
  } finally {
    await reviews
    loading.value = false
  }
}
let resultTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  resultTimer = setInterval(() => { if (!document.hidden) void load() }, 5000)
})
onUnmounted(() => {
  reviewRequest++
  clearInterval(resultTimer)
  historyRequest++
})
</script>
