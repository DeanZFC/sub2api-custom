<template>
  <div class="min-w-0" :aria-busy="loading" data-admin-test-history>
    <p v-if="!results.length" class="py-8 text-center text-sm text-gray-500" role="status">{{ loading ? t('common.loading') : t('common.noData') }}</p>
    <div v-else class="grid min-w-0 gap-5 md:grid-cols-[13rem_minmax(0,1fr)]">
      <aside class="min-w-0 md:border-r md:border-gray-200 md:pr-4 md:dark:border-dark-700">
        <label class="relative block">
          <span class="sr-only">{{ t('admin.tests.searchResultsAccount') }}</span>
          <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-3 text-gray-400" />
          <input v-model="accountSearch" type="search" class="input w-full pl-9 text-sm" :placeholder="t('admin.tests.searchResultsAccount')" data-account-search />
        </label>
        <label class="mt-3 block md:hidden">
          <span class="sr-only">{{ t('admin.tests.account') }}</span>
          <select v-model="selectedKey" class="input w-full text-sm" data-account-select>
            <option v-for="account in visibleAccounts" :key="account.key" :value="account.key">{{ account.label }} · {{ t('admin.tests.resultCount', { count: account.results.length }) }}</option>
          </select>
        </label>
        <nav class="mt-3 hidden divide-y divide-gray-100 md:block dark:divide-dark-800" :aria-label="t('admin.tests.account')">
          <button v-for="account in visibleAccounts" :key="account.key" type="button" class="block w-full px-3 py-3 text-left transition-colors" :class="selectedKey === account.key ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-dark-800'" :aria-current="selectedKey === account.key ? 'true' : undefined" :data-account-key="account.key" @click="selectedKey = account.key">
            <span class="block break-words text-sm font-semibold">{{ account.name }}</span>
            <span v-if="account.id != null" class="mt-1 block text-xs tabular-nums text-gray-500 dark:text-gray-400">#{{ account.id }}</span>
            <span class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs"><span class="text-gray-500 dark:text-gray-400">{{ t('admin.tests.resultCount', { count: account.results.length }) }}</span><span v-if="account.failed" class="text-red-600 dark:text-red-400">{{ t('admin.tests.failedCount', { count: account.failed }) }}</span><span v-if="account.running" class="text-blue-600 dark:text-blue-400">{{ t('admin.tests.runningCount', { count: account.running }) }}</span></span>
          </button>
        </nav>
        <p v-if="!visibleAccounts.length" class="py-5 text-sm text-gray-500">{{ t('admin.tests.noMatchingResults') }}</p>
      </aside>

      <section v-if="selectedAccount" class="min-w-0" data-selected-account>
        <header class="mb-4 flex flex-wrap items-start justify-between gap-2">
          <div class="min-w-0"><h3 class="break-words text-base font-semibold text-gray-900 dark:text-white">{{ selectedAccount.label }}</h3><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(history ? 'admin.tests.showHistory' : 'admin.tests.latestResults') }} · {{ t('admin.tests.resultCount', { count: selectedAccount.results.length }) }}</p></div>
          <span v-if="loading" class="text-xs text-gray-500" role="status">{{ t('common.loading') }}</span>
        </header>
        <div class="mb-2 grid gap-3 sm:grid-cols-2">
          <label class="min-w-0 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.type') }}<select v-model="typeFilter" class="input mt-1 w-full text-sm" data-type-filter><option value="">{{ t('admin.tests.allResultTypes') }}</option><option v-for="type in availableTypes" :key="type.key" :value="type.key">{{ type.name }}</option></select></label>
          <label class="min-w-0 text-xs text-gray-500 dark:text-gray-400">{{ t('common.status') }}<select v-model="statusFilter" class="input mt-1 w-full text-sm" data-status-filter><option value="">{{ t('admin.tests.allResultStatuses') }}</option><option value="success">{{ t('admin.scheduledTests.success') }}</option><option value="failed">{{ t('admin.scheduledTests.failed') }}</option><option value="running">{{ t('admin.scheduledTests.running') }}</option></select></label>
        </div>
        <p v-if="!filteredResults.length" class="py-8 text-center text-sm text-gray-500">{{ t('admin.tests.noMatchingResults') }}</p>
        <div v-else class="divide-y divide-gray-200 dark:divide-dark-700">
          <article v-for="result in pageResults" :key="result.id" class="min-w-0 py-4" :data-result-id="result.id">
            <div class="flex items-start gap-2">
              <button type="button" class="flex min-w-0 flex-1 items-start gap-2 text-left" :aria-expanded="expanded.has(result.id)" :aria-controls="`admin-test-output-${result.id}`" :aria-label="t(expanded.has(result.id) ? 'admin.tests.collapseResult' : 'admin.tests.expandResult')" data-expand-result @click="toggleResult(result.id)">
                <Icon :name="expanded.has(result.id) ? 'chevronDown' : 'chevronRight'" size="sm" class="mt-0.5 shrink-0 text-gray-400" />
                <span class="min-w-0 flex-1">
                  <span class="flex flex-wrap items-center gap-2"><strong class="break-words text-sm font-semibold text-gray-900 dark:text-white">{{ resultTypeName(result) }}</strong><span :class="statusClass(result)">{{ statusLabel(result) }}</span></span>
                  <span class="mt-1 block break-words text-xs text-gray-500 dark:text-gray-400">{{ result.model_id || '-' }}<template v-if="result.reasoning_effort"> · {{ t('admin.tests.reasoningEffort') }}: {{ result.reasoning_effort }}</template></span>
                  <span class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-gray-400"><time :datetime="resultTime(result)">{{ formatDate(resultTime(result)) }}</time><span v-if="result.output_kind !== 'statistics' || isRunning(result)" class="tabular-nums">{{ resultDuration(result) }}</span><span v-if="result.id > 0" class="tabular-nums">#{{ result.id }}</span></span>
                </span>
              </button>
              <div class="flex shrink-0 gap-1">
                <button v-if="result.id > 0 && result.status === 'failed' && result.account_id" type="button" class="btn btn-secondary h-8 w-8 p-0" :title="t('admin.tests.retry')" :aria-label="t('admin.tests.retry')" :disabled="retryingResultId === result.id || deletingResultId === result.id" data-retry-result @click="emit('retry', result)"><Icon name="refresh" size="sm" :class="{ 'animate-spin': retryingResultId === result.id }" /></button>
                <button v-if="result.id > 0" type="button" class="btn btn-secondary h-8 w-8 p-0 text-red-600 dark:text-red-400" :title="t('common.delete')" :aria-label="t('common.delete')" :disabled="deletingResultId === result.id || retryingResultId === result.id" data-delete-result @click="emit('delete', result)"><Icon name="trash" size="sm" /></button>
              </div>
            </div>
            <p v-if="result.target_mode === 'group' && result.account_id" class="ml-6 mt-2 break-words text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.executedByAccount') }}: {{ accountLabel(result) }}</p>
            <p v-if="!expanded.has(result.id)" class="ml-6 mt-2 break-words text-sm" :class="result.status === 'failed' ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-gray-300'" data-result-summary>{{ resultSummary(result) }}</p>
            <div v-if="expanded.has(result.id)" :id="`admin-test-output-${result.id}`" class="mt-3 min-w-0 sm:ml-6" data-result-detail>
              <p v-if="result.error_message" class="mb-3 whitespace-pre-wrap break-words text-sm text-red-600 dark:text-red-400" role="alert">{{ result.error_message }}</p>
              <TestResultOutput :result="result" />
            </div>
          </article>
        </div>
        <Pagination v-if="filteredResults.length > pageSize" :total="filteredResults.length" :page="page" :page-size="pageSize" :show-page-size-selector="false" @update:page="page = $event" />
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountListItem, TestResult, TestType } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import Pagination from '@/components/common/Pagination.vue'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'

const props = defineProps<{
  results: TestResult[]
  accounts: AccountListItem[]
  types: TestType[]
  now: number
  history: boolean
  loading: boolean
  retryingResultId: number | null
  deletingResultId: number | null
}>()
const emit = defineEmits<{ retry: [result: TestResult]; delete: [result: TestResult] }>()
const { t } = useI18n()
const accountSearch = ref('')
const selectedKey = ref('')
const typeFilter = ref('')
const statusFilter = ref('')
const page = ref(1)
const pageSize = 15
const expanded = ref(new Set<number>())
const isRunning = (result: TestResult) => result.status === 'running' || result.status === 'pending'
const resultTime = (result: TestResult) => result.started_at || result.created_at || result.finished_at
const timestamp = (result: TestResult) => {
  const value = Date.parse(resultTime(result) || '')
  return Number.isFinite(value) ? value : 0
}
const accountNames = computed(() => new Map(props.accounts.map(account => [account.id, account.name])))
const accountName = (result: TestResult) => result.account_name || accountNames.value.get(result.account_id!) || t('admin.tests.account')
const accountLabel = (result: TestResult) => `${accountName(result)} #${result.account_id}`
const resultTypeKey = (result: TestResult) => String(result.test_definition_id ?? result.test_definition?.id ?? result.test_name ?? 'unknown')
const resultTypeName = (result: TestResult) => result.test_name || props.types.find(type => type.id === result.test_definition_id)?.name || result.test_definition?.name || t('admin.tests.uncategorized')

const accountGroups = computed(() => {
  const groups = new Map<string, { key: string; id: number | null; name: string; label: string; results: TestResult[]; failed: number; running: number }>()
  const results = [...props.results].sort((a, b) => timestamp(b) - timestamp(a) || b.id - a.id)
  for (const result of results) {
    const groupTarget = result.target_mode === 'group'
    const id = groupTarget ? null : result.account_id ?? null
    const key = groupTarget ? 'group' : id != null ? `account:${id}` : 'unassigned'
    let group = groups.get(key)
    if (!group) {
      const name = groupTarget ? t('admin.tests.groupResultTarget') : id != null ? accountName(result) : t('admin.tests.unassignedResultTarget')
      group = { key, id, name, label: id != null ? `${name} #${id}` : name, results: [], failed: 0, running: 0 }
      groups.set(key, group)
    }
    group.results.push(result)
    if (result.status === 'failed') group.failed++
    if (isRunning(result)) group.running++
  }
  return [...groups.values()].sort((a, b) => (a.key === 'group' ? -2 : a.id ?? -1) - (b.key === 'group' ? -2 : b.id ?? -1))
})
const visibleAccounts = computed(() => {
  const query = accountSearch.value.trim().toLocaleLowerCase()
  return accountGroups.value.filter(account => account.label.toLocaleLowerCase().includes(query))
})
watch(visibleAccounts, accounts => {
  if (!accounts.some(account => account.key === selectedKey.value)) selectedKey.value = accounts[0]?.key || ''
}, { immediate: true })
const selectedAccount = computed(() => visibleAccounts.value.find(account => account.key === selectedKey.value))
const availableTypes = computed(() => {
  const types = new Map<string, string>()
  for (const result of selectedAccount.value?.results || []) types.set(resultTypeKey(result), resultTypeName(result))
  return [...types].map(([key, name]) => ({ key, name }))
})
const filteredResults = computed(() => (selectedAccount.value?.results || []).filter(result => {
  if (typeFilter.value && resultTypeKey(result) !== typeFilter.value) return false
  if (statusFilter.value === 'success') return result.status === 'success' || result.status === 'passed'
  if (statusFilter.value === 'running') return isRunning(result)
  return !statusFilter.value || result.status === statusFilter.value
}))
const pageResults = computed(() => filteredResults.value.slice((page.value - 1) * pageSize, page.value * pageSize))
watch(selectedKey, () => { typeFilter.value = ''; statusFilter.value = ''; page.value = 1 })
watch([typeFilter, statusFilter], () => { page.value = 1 })
watch(() => filteredResults.value.length, count => { page.value = Math.min(page.value, Math.max(1, Math.ceil(count / pageSize))) })
watch([selectedKey, typeFilter, statusFilter, page], () => {
  expanded.value = new Set(pageResults.value.length ? [pageResults.value[0].id] : [])
}, { immediate: true })
watch(pageResults, results => {
  if (!expanded.value.size) return
  const retained = results.filter(result => expanded.value.has(result.id)).map(result => result.id)
  if (!retained.length && results.length) retained.push(results[0].id)
  expanded.value = new Set(retained)
})
const toggleResult = (id: number) => {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}
const formatDate = (value?: string) => value ? new Date(value).toLocaleString() : '-'
const resultDuration = (result: TestResult) => {
  if (!isRunning(result)) return result.latency_ms == null ? '-' : `${result.latency_ms}ms`
  const started = timestamp(result)
  if (!started) return '-'
  const seconds = Math.max(0, Math.floor((props.now - started) / 1000))
  const secondPart = String(seconds % 60).padStart(2, '0')
  const minutePart = String(Math.floor(seconds / 60) % 60).padStart(2, '0')
  const hours = Math.floor(seconds / 3600)
  const duration = hours > 0 ? `${hours}:${minutePart}:${secondPart}` : `${minutePart}:${secondPart}`
  return t('admin.tests.elapsed', { duration })
}
const statusClass = (result: TestResult) => result.status === 'failed' ? 'badge badge-danger' : isRunning(result) ? 'badge badge-warning' : ['success', 'passed'].includes(result.status) ? 'badge badge-success' : 'badge badge-gray'
const statusLabel = (result: TestResult) => isRunning(result) ? t('admin.scheduledTests.running') : result.status === 'failed' ? t('admin.scheduledTests.failed') : ['success', 'passed'].includes(result.status) ? t('admin.scheduledTests.success') : result.status
const resultSummary = (result: TestResult) => {
  if (result.status === 'failed') return (result.error_message || t('admin.scheduledTests.failed')).slice(0, 180)
  if (isRunning(result)) return t('tests.running')
  if (result.output_kind === 'statistics') return result.output_statistics ? t('admin.tests.statistics') : t('tests.noOutput')
  if (result.output_kind === 'number' && result.output_numeric != null) return String(result.output_numeric)
  if (result.output_kind === 'html' && result.output_html) return 'HTML / SVG'
  const text = (result.response_text || '').replace(/\s+/g, ' ').trim()
  return text ? text.length > 180 ? `${text.slice(0, 180)}...` : text : t('tests.noOutput')
}
</script>
