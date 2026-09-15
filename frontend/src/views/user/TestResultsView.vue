<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 pb-5 dark:border-dark-700">
        <div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('tests.title') }}</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('tests.description') }}</p></div>
        <button class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="sm" /> {{ t('common.refresh') }}</button>
      </header>

      <section v-if="allResults.length" class="card space-y-4 p-4">
        <div class="flex gap-2 overflow-x-auto border-b border-gray-200 dark:border-dark-700">
          <button v-for="tab in testTabs" :key="tab.key" class="shrink-0 border-b-2 px-3 py-2 text-sm font-medium transition-colors" :class="activeType === tab.key ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'" @click="activeType = tab.key">{{ tab.name }}</button>
        </div>
        <div class="grid gap-3 md:grid-cols-2">
          <label class="input-label">{{ t('tests.groupFilter') }}<select v-model="groupFilter" class="input mt-1 w-full"><option value="">{{ t('tests.allGroups') }}</option><option v-for="group in availableGroups" :key="group.key" :value="group.key">{{ group.name }}</option></select></label>
          <label class="input-label">{{ t('tests.modelFilter') }}<select v-model="modelFilter" class="input mt-1 w-full"><option value="">{{ t('tests.allModels') }}</option><option v-for="model in availableModels" :key="model" :value="model">{{ model }}</option></select></label>
        </div>
      </section>

      <p v-if="!loading && !allResults.length" class="card p-10 text-center text-sm text-gray-500">{{ t('tests.empty') }}</p>
      <p v-else-if="!groupedLatest.length" class="card p-10 text-center text-sm text-gray-500">{{ t('tests.noMatches') }}</p>
      <div v-else class="space-y-6">
        <section v-for="group in groupedLatest" :key="group.key" class="space-y-2">
          <h3 class="flex items-center gap-2 border-b border-gray-200 pb-2 text-base font-semibold text-gray-900 dark:border-dark-700 dark:text-white"><span class="h-2 w-2 rounded-full bg-primary-500" />{{ group.name }}<span class="text-xs font-normal text-gray-500">{{ group.results.length }}</span></h3>
          <article v-for="result in group.results" :key="result.id" class="card cursor-pointer p-4 transition-shadow hover:shadow-md" role="button" tabindex="0" @click="openHistory(result)" @keydown.enter="openHistory(result)">
            <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><h4 class="font-medium text-gray-900 dark:text-white">{{ targetName(result) }}</h4><span :class="statusClass(result)">{{ result.status }}</span></div><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ result.model_id || '-' }}<template v-if="result.reasoning_effort"> · {{ t('tests.reasoningEffort') }}: {{ result.reasoning_effort }}</template> · {{ result.latency_ms ?? '-' }}ms · {{ formatDate(result.created_at || result.finished_at) }}</p></div><span class="shrink-0 text-xs text-primary-600 dark:text-primary-400">{{ t('tests.viewHistory') }} ({{ historyFor(result).length }})</span></div>
            <div v-if="result.error_message" class="mt-3 rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300">{{ result.error_message }}</div>
            <div class="mt-3" @click.stop>
              <TestResultOutput :result="result" />
            </div>
          </article>
        </section>
      </div>
    </div>

    <BaseDialog :show="!!historyTarget" :title="historyTarget ? `${targetName(historyTarget)} · ${testName(historyTarget)}` : ''" width="extra-wide" @close="historyTarget = null">
      <div v-if="historyTarget" class="space-y-4"><p class="text-xs text-gray-500">{{ t('tests.historyHint') }}</p><article v-for="result in historyFor(historyTarget)" :key="result.id" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><div class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"><span>{{ result.model_id || '-' }}<template v-if="result.reasoning_effort"> · {{ t('tests.reasoningEffort') }}: {{ result.reasoning_effort }}</template> · {{ result.latency_ms ?? '-' }}ms · {{ formatDate(result.created_at || result.finished_at) }}</span><span :class="statusClass(result)">{{ result.status }}</span></div><div v-if="result.error_message" class="mt-3 rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300">{{ result.error_message }}</div><TestResultOutput class="mt-3" :result="result" /></article></div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { testResultsAPI } from '@/api/testResults'
import type { TestResult } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const app = useAppStore()
const loading = ref(false)
const allResults = ref<TestResult[]>([])
const activeType = ref('')
const groupFilter = ref('')
const modelFilter = ref('')
const historyTarget = ref<TestResult | null>(null)
watch(activeType, () => {
  groupFilter.value = ''
  modelFilter.value = ''
})

const sortResults = (items: TestResult[]) => [...items].sort((a, b) => {
  const ad = new Date(a.created_at || a.finished_at || a.started_at || 0).getTime()
  const bd = new Date(b.created_at || b.finished_at || b.started_at || 0).getTime()
  return bd - ad || b.id - a.id
})
const testName = (result: TestResult) => result.test_name || result.plan_name || result.test_definition?.name || result.plan?.name || t('tests.unknownType')
// Group plans produce one result per tested account. Keep the latest result for
// each account inside its group, while still collapsing group-only results.
const targetKey = (result: TestResult) => result.account_id != null ? `account:${result.account_id}:group:${result.group_id ?? ''}` : result.group_id != null ? `group:${result.group_id}` : `plan:${result.plan_id ?? result.id}`
const groupKey = (result: TestResult) => result.group_id != null ? `group:${result.group_id}` : result.group_name ? `name:${result.group_name}` : result.account_id != null ? `account:${result.account_id}` : 'ungrouped'
const groupName = (result: TestResult) => result.group_name || (result.group_id != null ? `${t('tests.group')} #${result.group_id}` : t('tests.ungrouped'))
const targetName = (result: TestResult) => result.account_id != null ? `${t('tests.account')} #${result.account_id}` : result.group_id != null ? groupName(result) : `#${result.id}`

const testTabs = computed(() => {
  const names = new Map<string, string>()
  for (const result of allResults.value) {
    const name = testName(result)
    if (!names.has(name)) names.set(name, name)
  }
  return Array.from(names, ([key, name]) => ({ key, name }))
})
const typeResults = computed(() => activeType.value ? allResults.value.filter(result => testName(result) === activeType.value) : allResults.value)
watch(testTabs, tabs => {
  if (!tabs.some(tab => tab.key === activeType.value)) activeType.value = tabs[0]?.key || ''
}, { immediate: true })
const availableGroups = computed(() => {
  const seen = new Map<string, string>()
  for (const result of typeResults.value) seen.set(groupKey(result), groupName(result))
  return Array.from(seen, ([key, name]) => ({ key, name })).sort((a, b) => a.name.localeCompare(b.name))
})
const availableModels = computed(() => Array.from(new Set(typeResults.value.map(result => result.model_id).filter((model): model is string => Boolean(model)))).sort())
const filteredResults = computed(() => sortResults(typeResults.value.filter(result => (!groupFilter.value || groupKey(result) === groupFilter.value) && (!modelFilter.value || result.model_id === modelFilter.value))))
const latestResults = computed(() => {
  const latest = new Map<string, TestResult>()
  for (const result of filteredResults.value) {
    const key = targetKey(result)
    if (!latest.has(key)) latest.set(key, result)
  }
  return Array.from(latest.values())
})
const groupedLatest = computed(() => {
  const groups = new Map<string, { key: string; name: string; results: TestResult[] }>()
  for (const result of latestResults.value) {
    const key = groupKey(result)
    const group = groups.get(key)
    if (group) group.results.push(result)
    else groups.set(key, { key, name: groupName(result), results: [result] })
  }
  return Array.from(groups.values()).sort((a, b) => a.name.localeCompare(b.name))
})
const historyFor = (result: TestResult) => filteredResults.value.filter(item => targetKey(item) === targetKey(result) && testName(item) === testName(result))
const openHistory = (result: TestResult) => { historyTarget.value = result }
const statusClass = (result: TestResult) => {
  if (result.status === 'running' || result.status === 'pending') return 'badge badge-warning'
  return result.status === 'success' || result.status === 'passed' ? 'badge badge-success' : 'badge badge-danger'
}
const formatDate = (value?: string) => value ? new Date(value).toLocaleString() : '-'
const load = async () => {
  if (loading.value) return
  loading.value = true
  try { allResults.value = sortResults(await testResultsAPI.list(200)) }
  catch (error) { app.showError(extractApiErrorMessage(error, t('tests.loadFailed'))) }
  finally { loading.value = false }
}
let resultTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  resultTimer = setInterval(() => { if (!document.hidden) void load() }, 5000)
})
onUnmounted(() => clearInterval(resultTimer))
</script>
