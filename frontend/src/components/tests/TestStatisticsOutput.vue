<template>
  <div class="min-w-0" data-statistics-output>
    <dl class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <div class="min-w-0" :title="t('tests.statisticsSourceHint')"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('tests.statisticsSuccessRate') }}</dt><dd class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white" data-success-rate>{{ successRate }}</dd><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('tests.statisticsRequestSamples', { success: count(statistics.success_requests), total: count(statistics.total_requests) }) }}</p></div>
      <div class="min-w-0" :title="t('tests.statisticsSourceHint')"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('tests.statisticsCacheRate') }}</dt><dd class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white" data-cache-rate>{{ cacheRate }}</dd><p class="mt-1 break-words text-xs text-gray-500 dark:text-gray-400">{{ t('tests.statisticsTokenSamples', { cached: count(statistics.cache_read_tokens), input: count(statistics.cache_input_tokens) }) }}</p></div>
      <div class="min-w-0" :title="t('tests.statisticsSourceHint')"><dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('tests.statisticsFirstToken') }}</dt><dd class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white" data-first-token>{{ firstToken }}</dd><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('tests.statisticsFirstTokenSamples', { count: count(statistics.first_token_samples) }) }}</p></div>
      <div class="min-w-0" data-statistics-recent-requests>
        <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('tests.statisticsRecentRequests') }}</dt>
        <dd v-if="recentRequests.length" class="mt-1 w-fit min-w-[6rem]">
          <time :datetime="recentRequests[0].created_at" class="block text-xs font-medium tabular-nums text-gray-500 dark:text-gray-400">{{ formatRecentTime(recentRequests[0].created_at) }}</time>
          <ol class="mt-1 flex h-6 items-center justify-end gap-1" :aria-label="t('tests.statisticsRecentRequestsHint')">
            <li
              v-for="(request, index) in timeline"
              :key="index"
              class="block h-6 w-1.5 shrink-0 rounded-full shadow-sm"
              :class="request.success ? 'bg-emerald-500 shadow-emerald-200 dark:bg-emerald-400 dark:shadow-none' : 'bg-red-500 shadow-red-200 dark:bg-red-400 dark:shadow-none'"
              :aria-label="t(request.success ? 'tests.statisticsRequestSuccess' : 'tests.statisticsRequestFailure')"
              data-request-outcome
            />
          </ol>
        </dd>
        <dd v-else class="mt-1 text-2xl font-semibold text-gray-400">—</dd>
      </div>
    </dl>
    <div class="mt-4 flex flex-wrap gap-x-4 gap-y-1 border-t border-gray-100 pt-2 text-xs text-gray-500 dark:border-dark-800 dark:text-gray-400">
      <span>{{ t('tests.statisticsWindow') }}: {{ formatTime(statistics.window_start) }} – {{ formatTime(statistics.window_end) }}</span>
      <span>{{ t('tests.statisticsUpdated') }}: {{ formatTime(updatedAt || statistics.window_end) }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestOutputStatistics } from '@/types'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{ statistics: TestOutputStatistics; updatedAt?: string }>()
const { t } = useI18n()
const validCount = (value: number) => Number.isFinite(value) && value >= 0
const count = (value: number) => validCount(value) ? Math.floor(value).toLocaleString() : '—'
const ratio = (value: number | null, samples: number) => validCount(samples) && samples > 0 && value != null && Number.isFinite(value) && value >= 0 && value <= 1 ? `${(value * 100).toFixed(1)}%` : '—'
const successRate = computed(() => ratio(props.statistics.success_rate, props.statistics.total_requests))
const cacheRate = computed(() => ratio(props.statistics.cache_rate, props.statistics.cache_input_tokens))
const firstToken = computed(() => {
  const value = props.statistics.avg_first_token_ms
  return props.statistics.first_token_samples > 0 && Number.isFinite(props.statistics.first_token_samples) && value != null && Number.isFinite(value) && value >= 0 ? `${Math.round(value).toLocaleString()}ms` : '—'
})
const recentRequests = computed(() => (props.statistics.recent_requests ?? []).slice(0, 10))
// Match the account list: oldest on the left, newest on the right.
const timeline = computed(() => [...recentRequests.value].reverse())
const formatRecentTime = (value: string) => formatDateTime(value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '—'
}
</script>
