<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('tests.title') }}</h2>
        <button class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
      </header>

      <div v-if="allResults.length" class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 dark:border-dark-700">
        <nav class="flex min-w-0 flex-1 gap-1 overflow-x-auto" role="tablist" :aria-label="t('tests.groupFilter')">
          <button v-for="group in availableGroups" :id="tabId(group.key)" :key="group.key" type="button" role="tab" :aria-selected="activeGroup === group.key" aria-controls="quality-results" :tabindex="activeGroup === group.key ? 0 : -1" class="shrink-0 border-b-2 px-4 py-3 text-base font-semibold transition-colors" :class="activeGroup === group.key ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'" @click="activeGroup = group.key" @keydown="onGroupKeydown($event, group.key)">{{ group.name }}</button>
        </nav>
        <label class="mb-2 flex w-full items-center gap-2 text-sm text-gray-500 dark:text-gray-400 sm:w-auto">
          <span class="shrink-0">{{ t('tests.modelFilter') }}</span>
          <select v-model="modelFilter" class="input min-w-0 flex-1 text-sm sm:w-48"><option value="">{{ t('tests.allModels') }}</option><option v-for="model in availableModels" :key="model" :value="model">{{ model }}</option></select>
        </label>
      </div>

      <p v-if="!allResults.length" class="py-16 text-center text-sm text-gray-500">{{ loading ? t('common.loading') : t('tests.empty') }}</p>
      <section v-else id="quality-results" ref="resultsPanel" class="min-w-0 space-y-5" role="tabpanel" :aria-labelledby="tabId(activeGroup)" tabindex="0">
        <p v-if="!accountResults.length" class="py-16 text-center text-sm text-gray-500">{{ t('tests.noMatches') }}</p>
        <article v-for="account in accountResults" :key="account.key" class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900" data-account-result>
          <div class="flex flex-col gap-5 p-4 sm:p-5 lg:flex-row lg:gap-8">
            <div class="min-w-0 shrink-0 lg:w-40"><h3 class="text-xl font-semibold text-gray-900 dark:text-white">{{ account.name }}</h3><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ account.groupName }}</p></div>
            <div v-if="account.numericTests.length" class="grid min-w-0 flex-1 gap-x-8 gap-y-5 sm:grid-cols-2 xl:grid-cols-3">
              <section v-for="test in account.numericTests" :key="test.key" data-numeric-test>
                <div class="flex flex-wrap items-center gap-2"><h4 class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ test.name }}</h4><span :class="statusClass(test.latest)">{{ statusLabel(test.latest) }}</span></div>
                <div class="mt-2 flex items-baseline gap-3"><strong class="text-3xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ test.latest.output_numeric ?? '-' }}</strong><button type="button" class="text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="openHistory(test)">{{ t('tests.viewHistory') }}</button></div>
                <p class="mt-2 break-words text-xs text-gray-500 dark:text-gray-400">{{ modelLabel(test.latest) }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ formatDate(resultTime(test.latest)) }}<span v-if="test.latest.latency_ms != null"> · {{ test.latest.latency_ms }}ms</span></p>
              </section>
            </div>
          </div>

          <section v-for="test in account.contentTests" :key="test.key" class="border-t border-gray-200 p-4 dark:border-dark-700 sm:p-5" data-content-test>
            <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><h4 class="text-base font-semibold text-gray-900 dark:text-white">{{ test.name }}</h4><span :class="statusClass(test.latest)">{{ statusLabel(test.latest) }}</span></div><p class="mt-1 break-words text-xs text-gray-500 dark:text-gray-400">{{ modelLabel(test.latest) }}<span v-if="test.latest.latency_ms != null"> · {{ test.latest.latency_ms }}ms</span></p></div>
              <button type="button" class="shrink-0 text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="openHistory(test)">{{ t('tests.viewHistory') }}</button>
            </div>
            <div v-if="test.latest.output_kind === 'html'" class="grid items-end gap-4 sm:grid-cols-2 xl:grid-cols-[1.5fr_1fr_1fr_1fr]" data-result-gallery>
              <figure v-for="(result, index) in test.recent" :key="result.id" class="min-w-0">
                <TestResultOutput :result="result" compact />
                <figcaption class="mt-2 flex flex-wrap items-center gap-x-1 text-xs text-gray-500 dark:text-gray-400"><span :class="index === 0 ? 'font-medium text-gray-700 dark:text-gray-200' : ''">{{ index === 0 ? t('tests.latestResult') : t('tests.previousResult') }}</span><span>· {{ formatDate(resultTime(result)) }}</span></figcaption>
              </figure>
            </div>
            <template v-else><TestResultOutput :result="test.latest" /><p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ formatDate(resultTime(test.latest)) }}</p></template>
          </section>
        </article>
      </section>
    </div>

    <BaseDialog :show="!!historyTarget" :title="historyTarget ? `${targetName(historyTarget.latest)} · ${historyTarget.name}` : ''" width="extra-wide" @close="historyTarget = null">
      <div v-if="historyTarget" class="divide-y divide-gray-200 dark:divide-dark-700"><article v-for="result in historyTarget.results" :key="result.id" class="py-5 first:pt-0"><div class="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"><span>{{ modelLabel(result) }}<span v-if="result.latency_ms != null"> · {{ result.latency_ms }}ms</span> · {{ formatDate(resultTime(result)) }}</span><span :class="statusClass(result)">{{ statusLabel(result) }}</span></div><TestResultOutput :result="result" /></article></div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { testResultsAPI } from '@/api/testResults'
import type { TestResult } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import { useAppStore } from '@/stores/app'
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
const loading = ref(false)
const allResults = ref<TestResult[]>([])
const activeGroup = ref('')
const modelFilter = ref('')
const historyTarget = ref<TestSeries | null>(null)
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
const targetName = (result: TestResult) => exposesAccount(result) ? `${t('tests.account')} #${result.account_id}` : t('tests.groupCheck')
const testKey = (result: TestResult) => `${result.test_definition_id ?? result.test_definition?.id ?? testName(result)}:${result.model_id || ''}:${result.reasoning_effort || ''}`
const tabId = (key: string) => `quality-group-${encodeURIComponent(key)}`

const availableGroups = computed(() => {
  const groups = new Map<string, { key: string; name: string; order: number }>()
  for (const result of allResults.value) {
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
const availableModels = computed(() => [...new Set(allResults.value.map(result => result.model_id).filter((model): model is string => Boolean(model)))].sort())
const filteredResults = computed(() => sortResults(allResults.value.filter(result => groupKey(result) === activeGroup.value && (!modelFilter.value || result.model_id === modelFilter.value))))
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
    for (const test of tests) test.recent = test.results.slice(0, 4)
    return { ...account, numericTests: tests.filter(test => test.latest.output_kind === 'number'), contentTests: tests.filter(test => test.latest.output_kind !== 'number') }
  })
})

const openHistory = (series: TestSeries) => { historyTarget.value = series }
const statusClass = (result: TestResult) => result.status === 'running' || result.status === 'pending' || result.output_kind === 'html' ? 'badge badge-warning' : 'badge badge-success'
const statusLabel = (result: TestResult) => result.status === 'running' || result.status === 'pending' ? t('tests.running') : result.output_kind === 'html' ? t('tests.awaitingReview') : t('tests.completed')
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
const load = async () => {
  if (loading.value) return
  loading.value = true
  try {
    const results = await testResultsAPI.list(4)
    // Failed upstream responses must stay private, including against an older server.
    allResults.value = sortResults(results.filter(result => ['success', 'passed', 'pending', 'running'].includes(result.status)))
  } catch (error) {
    app.showError(extractApiErrorMessage(error, t('tests.loadFailed')))
  } finally { loading.value = false }
}
let resultTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  resultTimer = setInterval(() => { if (!document.hidden) void load() }, 5000)
})
onUnmounted(() => clearInterval(resultTimer))
</script>
