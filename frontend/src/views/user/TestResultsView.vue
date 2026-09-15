<template>
  <AppLayout>
    <div class="flex flex-col gap-4">
      <header class="flex shrink-0 flex-wrap items-end justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
        <div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('tests.title') }}</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('tests.description') }}</p></div>
        <button class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="sm" /> {{ t('common.refresh') }}</button>
      </header>

      <section v-if="allResults.length" class="card shrink-0 p-4">
        <div class="flex gap-2 overflow-x-auto border-b border-gray-200 dark:border-dark-700">
          <button v-for="tab in testTabs" :key="tab.key" class="shrink-0 border-b-2 px-3 py-2 text-sm font-medium transition-colors" :class="activeType === tab.key ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'" @click="activeType = tab.key">{{ tab.name }}</button>
        </div>
      </section>

      <p v-if="!allResults.length" class="card overflow-auto p-10 text-center text-sm text-gray-500">{{ loading ? t('common.loading') : t('tests.empty') }}</p>
      <div v-else class="grid gap-4 lg:grid-cols-[13rem,minmax(0,1fr)]">
        <aside class="card flex self-start flex-col p-2">
          <h3 class="shrink-0 px-3 pb-2 pt-1 text-base font-semibold text-gray-700 dark:text-gray-200">{{ t('tests.groupFilter') }}</h3>
          <nav class="space-y-1" :aria-label="t('tests.groupFilter')">
            <button v-for="group in availableGroups" :key="group.key" type="button" class="flex w-full items-center rounded-lg px-3 py-2 text-left text-base transition-colors" :class="activeGroup === group.key ? 'bg-primary-50 font-semibold text-primary-700 dark:bg-primary-950/40 dark:text-primary-300' : 'text-gray-600 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-dark-800'" @click="activeGroup = group.key">
              <span class="min-w-0 truncate">{{ group.name }}</span>
            </button>
          </nav>
          <label class="input-label mt-4 block shrink-0 border-t border-gray-100 px-3 pt-3 text-sm dark:border-dark-700">{{ t('tests.modelFilter') }}<select v-model="modelFilter" class="input mt-1 w-full text-sm"><option value="">{{ t('tests.allModels') }}</option><option v-for="model in availableModels" :key="model" :value="model">{{ model }}</option></select></label>
        </aside>
        <section ref="resultsPanel" class="min-w-0 space-y-4 pb-2 pr-1" tabindex="0" :aria-label="t('tests.title')">
          <p v-if="!groupedLatest.length" class="card p-10 text-center text-sm text-gray-500">{{ t('tests.noMatches') }}</p>
          <div v-for="group in groupedLatest" :key="group.key" class="space-y-2">
            <h3 class="flex items-center gap-2 border-b border-gray-200 pb-2 text-base font-semibold text-gray-900 dark:border-dark-700 dark:text-white"><span class="h-2 w-2 rounded-full bg-primary-500" />{{ group.name }}</h3>
            <article v-for="result in group.results" :key="result.id" class="card cursor-pointer p-3 transition-shadow hover:shadow-md sm:p-4" role="button" tabindex="0" @click="openHistory(result)" @keydown.enter="openHistory(result)">
              <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><h4 class="text-lg font-semibold text-gray-900 dark:text-white">{{ targetName(result) }}</h4><span :class="statusClass(result)">{{ result.status }}</span></div><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ result.model_id || '-' }}<template v-if="result.reasoning_effort"> · {{ t('tests.reasoningEffort') }}: {{ result.reasoning_effort }}</template> · {{ result.latency_ms ?? '-' }}ms · {{ formatDate(result.started_at || result.finished_at || result.created_at) }}</p></div><span class="shrink-0 text-xs text-primary-600 dark:text-primary-400">{{ t('tests.viewHistory') }} ({{ historyFor(result).length }})</span></div>
              <div class="result-output mt-2 max-w-5xl mx-auto" @click.stop>
                <TestResultOutput :result="result" />
              </div>
            </article>
          </div>
        </section>
      </div>
    </div>

    <BaseDialog :show="!!historyTarget" :title="historyTarget ? `${targetName(historyTarget)} · ${testName(historyTarget)}` : ''" width="extra-wide" @close="historyTarget = null">
      <div v-if="historyTarget" class="space-y-4"><p class="text-xs text-gray-500">{{ t('tests.historyHint') }}</p><article v-for="result in historyFor(historyTarget)" :key="result.id" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><div class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"><span>{{ result.model_id || '-' }}<template v-if="result.reasoning_effort"> · {{ t('tests.reasoningEffort') }}: {{ result.reasoning_effort }}</template> · {{ result.latency_ms ?? '-' }}ms · {{ formatDate(result.started_at || result.finished_at || result.created_at) }}</span><span :class="statusClass(result)">{{ result.status }}</span></div><TestResultOutput class="mt-3" :result="result" /></article></div>
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
const activeGroup = ref('')
const modelFilter = ref('')
const historyTarget = ref<TestResult | null>(null)
const resultsPanel = ref<HTMLElement | null>(null)
// Only an explicit filter change resets the results panel. Polling updates
// keep the reader's scroll position while navigation stays outside the panel.
watch([activeType, activeGroup, modelFilter], () => {
  if (resultsPanel.value) resultsPanel.value.scrollTop = 0
}, { flush: 'post' })
// Reset before availableGroups selects the new type's first group.
watch(activeType, () => {
  activeGroup.value = ''
  modelFilter.value = ''
}, { flush: 'sync' })

const sortResults = (items: TestResult[]) => [...items].sort((a, b) => {
  const ad = new Date(a.started_at || a.finished_at || a.created_at || 0).getTime()
  const bd = new Date(b.started_at || b.finished_at || b.created_at || 0).getTime()
  return bd - ad || b.id - a.id
})
const testName = (result: TestResult) => result.test_name || result.plan_name || result.test_definition?.name || result.plan?.name || t('tests.unknownType')
const testOrder = (result: TestResult) => {
  const value = result.test_order ?? result.test_definition?.sort_order
  return typeof value === 'number' && Number.isFinite(value) ? value : Number.MAX_SAFE_INTEGER
}
// Group plans produce one result per tested account. Keep the latest result for
// each account inside its group, while still collapsing group-only results.
const exposesAccount = (result: TestResult) => result.target_mode !== 'group' && result.account_id != null
const targetKey = (result: TestResult) => exposesAccount(result) ? `account:${result.account_id}:group:${result.group_id ?? ''}` : result.group_id != null ? `group:${result.group_id}` : `plan:${result.plan_id ?? result.id}`
const groupKey = (result: TestResult) => result.group_id != null ? `group:${result.group_id}` : result.group_name ? `name:${result.group_name}` : result.account_id != null ? `account:${result.account_id}` : 'ungrouped'
const groupName = (result: TestResult) => result.group_name || (result.group_id != null ? `${t('tests.group')} #${result.group_id}` : t('tests.ungrouped'))
const targetName = (result: TestResult) => exposesAccount(result) ? `${t('tests.account')} #${result.account_id}` : result.group_id != null ? groupName(result) : `#${result.id}`

// The administrator controls the order through each test rule/plan's
// `sort_order` (returned on results as `plan_order`).
// A group can have several rules, so derive its position from the first group
// encountered after sorting all rules by that configured order. This keeps the
// user-facing result page independent from the global groups.sort_order field.
const groupDisplayOrder = computed(() => {
  const order = new Map<string, number>()
  const ordered = [...allResults.value].sort((a, b) => {
    const aPlanOrder = typeof a.plan_order === 'number' && Number.isFinite(a.plan_order) ? a.plan_order : Number.MAX_SAFE_INTEGER
    const bPlanOrder = typeof b.plan_order === 'number' && Number.isFinite(b.plan_order) ? b.plan_order : Number.MAX_SAFE_INTEGER
    const byPlanOrder = aPlanOrder - bPlanOrder
    if (byPlanOrder !== 0) return byPlanOrder
    const byName = testName(a).localeCompare(testName(b))
    if (byName !== 0) return byName
    const byGroupName = groupName(a).localeCompare(groupName(b))
    if (byGroupName !== 0) return byGroupName
    const ad = new Date(a.started_at || a.finished_at || a.created_at || 0).getTime()
    const bd = new Date(b.started_at || b.finished_at || b.created_at || 0).getTime()
    return bd - ad || b.id - a.id
  })
  for (const result of ordered) {
    const key = groupKey(result)
    if (!order.has(key)) order.set(key, order.size)
  }
  return order
})

const testTabs = computed(() => {
  const names = new Map<string, { name: string; order: number }>()
  for (const result of allResults.value) {
    const name = testName(result)
    if (!names.has(name)) names.set(name, { name, order: testOrder(result) })
  }
  return Array.from(names, ([key, value]) => ({ key, name: value.name, order: value.order }))
    .sort((a, b) => a.order - b.order || a.name.localeCompare(b.name))
})
const typeResults = computed(() => activeType.value ? allResults.value.filter(result => testName(result) === activeType.value) : allResults.value)
watch(testTabs, tabs => {
  if (!tabs.some(tab => tab.key === activeType.value)) activeType.value = tabs[0]?.key || ''
}, { immediate: true })
const availableGroups = computed(() => {
  const seen = new Map<string, { name: string; order: number }>()
  for (const result of typeResults.value) {
    const key = groupKey(result)
    if (!seen.has(key)) {
      const order = groupDisplayOrder.value.get(key) ?? Number.MAX_SAFE_INTEGER
      seen.set(key, { name: groupName(result), order })
    }
  }
  return Array.from(seen, ([key, value]) => ({ key, name: value.name, order: value.order }))
    .sort((a, b) => a.order - b.order || a.name.localeCompare(b.name))
})
watch(availableGroups, groups => {
  if (!groups.some(group => group.key === activeGroup.value)) activeGroup.value = groups[0]?.key || ''
}, { immediate: true })
const availableModels = computed(() => Array.from(new Set(typeResults.value.map(result => result.model_id).filter((model): model is string => Boolean(model)))).sort())
const filteredResults = computed(() => sortResults(typeResults.value.filter(result => (!activeGroup.value || groupKey(result) === activeGroup.value) && (!modelFilter.value || result.model_id === modelFilter.value))))
const latestResults = computed(() => {
  const latest = new Map<string, TestResult>()
  for (const result of filteredResults.value) {
    const key = targetKey(result)
    if (!latest.has(key)) latest.set(key, result)
  }
  return Array.from(latest.values())
})
const groupedLatest = computed(() => {
  const groups = new Map<string, { key: string; name: string; order: number; results: TestResult[] }>()
  for (const result of latestResults.value) {
    const key = groupKey(result)
    const group = groups.get(key)
    if (group) group.results.push(result)
    else {
      const order = groupDisplayOrder.value.get(key) ?? Number.MAX_SAFE_INTEGER
      groups.set(key, { key, name: groupName(result), order, results: [result] })
    }
  }
  return Array.from(groups.values()).sort((a, b) => a.order - b.order || a.name.localeCompare(b.name))
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
  try {
    // Keep failures out of both current results and history, including when
    // this client temporarily talks to a server that has not been upgraded.
    const results = await testResultsAPI.list(200)
    allResults.value = sortResults(results.filter(result => ['success', 'passed', 'pending', 'running'].includes(result.status)))
  }
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
