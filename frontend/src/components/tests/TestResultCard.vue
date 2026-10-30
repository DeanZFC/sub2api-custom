<template>
  <section
    class="flex min-w-0 flex-col overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700"
    data-test-section
    :data-numeric-test="test.latest.output_kind === 'number' ? '' : undefined"
    :data-content-test="test.latest.output_kind !== 'number' ? '' : undefined"
  >
    <div class="space-y-2 px-4 pt-4">
      <div class="flex flex-wrap items-start justify-between gap-2">
        <h4 class="min-w-0 break-words text-sm font-semibold text-gray-900 dark:text-white">{{ test.name }}</h4>
        <span :class="statusClass(test.latest)" data-current-verdict>{{ statusLabel(test.latest) }}</span>
      </div>
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span class="min-w-0 break-all text-xs text-gray-600 dark:text-gray-300">{{ test.latest.model_id || '-' }}</span>
        <span v-if="test.latest.reasoning_effort" class="text-xs text-gray-500 dark:text-gray-400">{{ t('tests.reasoningEffort') }}: {{ test.latest.reasoning_effort }}</span>
        <span v-if="test.latest.latency_ms != null" class="text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ test.latest.latency_ms }}ms</span>
      </div>
      <p v-if="test.latest.model_schedulable != null" class="mt-1 text-xs font-medium" :class="test.latest.model_schedulable ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'" data-model-scheduling-status>{{ t(test.latest.model_schedulable ? 'tests.modelSchedulingActive' : 'tests.modelSchedulingPaused') }}</p>
    </div>
    <div class="min-w-0 p-4">
      <figure v-if="test.latest.output_kind === 'number'" :key="test.latest.id" class="min-w-0 [container-type:inline-size]" data-numeric-gallery>
        <div class="grid gap-2" :class="previousPreviews.length ? 'numeric-result-grid grid-cols-2' : 'grid-cols-1'" :style="{ '--numeric-result-count': previousPreviews.length + 1 }">
          <div class="flex min-w-0 flex-col items-center rounded-xl border border-primary-200 bg-primary-50/60 px-2 py-3 text-center dark:border-primary-800 dark:bg-primary-950/20" data-latest-preview>
            <p class="text-[11px] font-medium leading-4 text-primary-700 dark:text-primary-300">{{ t('tests.currentResult') }}</p>
            <strong class="my-2 block break-all text-3xl font-semibold leading-9 tabular-nums text-gray-900 dark:text-white">{{ test.latest.output_numeric ?? '-' }}</strong>
            <time :datetime="resultTime(test.latest)" :title="formatDate(resultTime(test.latest))" class="mt-auto block text-[10px] leading-4 tabular-nums text-primary-700/80 dark:text-primary-300/80"><span class="block">{{ numericDate(test.latest) }}</span><span class="block">{{ numericTime(test.latest) }}</span></time>
          </div>
          <button
            v-for="result in previousPreviews"
            :key="result.id"
            type="button"
            class="flex min-w-0 flex-col items-center rounded-xl border border-gray-200 bg-gray-50/70 px-2 py-3 text-center transition-colors hover:border-primary-300 hover:bg-primary-50/40 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:border-dark-700 dark:bg-dark-800/50 dark:hover:border-primary-700 dark:hover:bg-primary-950/20"
            :aria-label="`${result.output_numeric} · ${t('tests.previewResultAt', { time: formatDate(resultTime(result)) })}`"
            :title="t('tests.previewResultAt', { time: formatDate(resultTime(result)) })"
            :data-previous-preview="result.id"
            @click="$emit('preview', result)"
          >
            <span class="text-[11px] leading-4 text-gray-500 dark:text-gray-400">{{ t('tests.previousResult') }}</span>
            <span class="my-2 block break-all text-2xl font-semibold leading-9 tabular-nums text-gray-600 dark:text-gray-300" data-previous-numeric-result>{{ result.output_numeric }}</span>
            <time :datetime="resultTime(result)" class="mt-auto block text-[10px] leading-4 tabular-nums text-gray-500 dark:text-gray-400"><span class="block">{{ numericDate(result) }}</span><span class="block">{{ numericTime(result) }}</span></time>
          </button>
        </div>
      </figure>
      <figure
        v-else
        :key="test.latest.id"
        class="min-w-0"
        data-result-gallery
      >
        <div class="grid min-w-0 gap-3" :class="previousPreviews.length ? 'grid-cols-1 sm:grid-cols-[minmax(0,1fr)_6rem] xl:grid-cols-[minmax(0,1fr)_8rem]' : ''">
          <div class="min-w-0" data-latest-preview>
            <TestResultOutput :result="test.latest" compact :max-preview-height="320" />
          </div>
          <div v-if="previousPreviews.length" class="grid min-w-0 grid-cols-3 content-start gap-2 sm:grid-cols-1" data-recent-previews>
            <button
              v-for="result in previousPreviews"
              :key="result.id"
              type="button"
              class="min-w-0 overflow-hidden rounded-lg border border-gray-200 bg-gray-50 p-1 text-left transition-colors hover:border-primary-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:border-dark-700 dark:bg-dark-800"
              :aria-label="t('tests.previewResultAt', { time: formatDate(resultTime(result)) })"
              :title="t('tests.previewResultAt', { time: formatDate(resultTime(result)) })"
              :data-previous-preview="result.id"
              @click="$emit('preview', result)"
            >
              <div class="pointer-events-none min-w-0" aria-hidden="true" inert>
                <TestResultOutput :result="result" compact thumbnail :max-preview-height="104" />
              </div>
              <time :datetime="resultTime(result)" class="mt-1 block truncate px-0.5 text-[10px] tabular-nums text-gray-500 dark:text-gray-400">{{ formatDate(resultTime(result)) }}</time>
            </button>
          </div>
        </div>
      </figure>
      <AdminTestDecision v-if="review" :review="review" compact :show-pass-rate="false" @decided="$emit('decided')" />
    </div>
    <footer class="mt-auto space-y-3 border-t border-gray-100 p-3 dark:border-dark-800" data-test-footer>
      <TestRecentPassRate :evidence="test.latest.pass_rate" :all-pass-progress="test.latest.all_pass_progress" />
      <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 text-xs text-gray-500 dark:text-gray-400">
        <time :datetime="resultTime(test.latest)">{{ formatDate(resultTime(test.latest)) }}</time>
        <div class="flex flex-wrap items-center gap-3" data-test-actions>
          <button v-if="historyAnchor" type="button" class="font-medium underline-offset-4 hover:text-gray-900 hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 dark:hover:text-gray-100" data-view-history @click="$emit('view-history', test)">{{ t('tests.viewHistory') }}</button>
          <button v-if="test.latest.output_kind === 'html' && test.latest.output_html" type="button" class="font-medium underline-offset-4 hover:text-gray-900 hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 dark:hover:text-gray-100" data-expand-preview @click="$emit('preview', test.latest)">{{ t('tests.expandPreview') }}</button>
        </div>
      </div>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestResult } from '@/types'
import type { TestAdminReview } from '@/api/admin/tests'
import TestResultOutput from './TestResultOutput.vue'
import AdminTestDecision from './AdminTestDecision.vue'
import TestRecentPassRate from './TestRecentPassRate.vue'

export interface TestCardSeries {
  key: string
  name: string
  order: number
  latest: TestResult
  results: TestResult[]
}

const props = defineProps<{
  test: TestCardSeries
  review?: TestAdminReview
  statusClass: (result: TestResult) => string
  statusLabel: (result: TestResult) => string
  resultTime: (result: TestResult) => string | undefined
  formatDate: (value?: string) => string
}>()

defineEmits<{
  'view-history': [test: TestCardSeries]
  preview: [result: TestResult]
  decided: []
}>()

const { t } = useI18n()
const test = computed(() => props.test)
const previousPreviews = computed(() => test.value.results.filter(result => result.id !== test.value.latest.id
  && ['success', 'passed'].includes(result.status) && result.output_kind === test.value.latest.output_kind
  && (result.output_kind === 'html' ? result.output_html?.trim() : result.output_kind === 'number' && result.output_numeric != null))
  .slice(0, 3))
const historyAnchor = computed(() => test.value.results.find(result => ['success', 'passed', 'failed', 'error'].includes(result.status)))
const statusClass = (result: TestResult) => props.statusClass(result)
const statusLabel = (result: TestResult) => props.statusLabel(result)
const resultTime = (result: TestResult) => props.resultTime(result)
const formatDate = (value?: string) => props.formatDate(value)
const numericDate = (result: TestResult) => {
  const value = resultTime(result)
  return value ? new Date(value).toLocaleDateString() : '-'
}
const numericTime = (result: TestResult) => {
  const value = resultTime(result)
  return value ? new Date(value).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }) : '-'
}
</script>

<style scoped>
@container (min-width: 20rem) {
  .numeric-result-grid {
    grid-template-columns: repeat(var(--numeric-result-count), minmax(0, 1fr));
  }
}
</style>
