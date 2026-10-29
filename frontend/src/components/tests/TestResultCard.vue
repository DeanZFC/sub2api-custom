<template>
  <section
    class="flex min-w-0 flex-col overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700"
    data-test-section
    :data-numeric-test="test.latest.output_kind === 'number' ? '' : undefined"
    :data-statistics-test="test.latest.output_kind === 'statistics' ? '' : undefined"
    :data-content-test="!['number', 'statistics'].includes(test.latest.output_kind) ? '' : undefined"
  >
    <div class="space-y-2 px-4 pt-4">
      <div class="flex flex-wrap items-start justify-between gap-2">
        <h4 class="min-w-0 break-words text-sm font-semibold text-gray-900 dark:text-white">{{ test.name }}</h4>
        <span :class="statusClass(test.latest)" data-current-verdict>{{ statusLabel(test.latest) }}</span>
      </div>
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span class="min-w-0 break-all text-xs text-gray-600 dark:text-gray-300">{{ test.latest.model_id || '-' }}</span>
        <span v-if="test.latest.reasoning_effort" class="text-xs text-gray-500 dark:text-gray-400">{{ t('tests.reasoningEffort') }}: {{ test.latest.reasoning_effort }}</span>
        <span v-if="test.latest.output_kind !== 'statistics' && test.latest.latency_ms != null" class="text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ test.latest.latency_ms }}ms</span>
      </div>
      <p v-if="test.latest.model_schedulable != null" class="mt-1 text-xs font-medium" :class="test.latest.model_schedulable ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'" data-model-scheduling-status>{{ t(test.latest.model_schedulable ? 'tests.modelSchedulingActive' : 'tests.modelSchedulingPaused') }}</p>
    </div>
    <div class="min-w-0 p-4">
      <TestResultOutput v-if="test.latest.output_kind === 'statistics'" :result="test.latest" compact />
      <figure v-else-if="test.latest.output_kind === 'number'" :key="test.latest.id" class="min-w-0" data-numeric-gallery>
        <figcaption class="text-xs text-gray-500 dark:text-gray-400">{{ t('tests.currentResult') }}</figcaption>
        <strong class="mt-1 block break-all text-3xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ test.latest.output_numeric ?? '-' }}</strong>
      </figure>
      <figure v-else :key="test.latest.id" class="min-w-0" data-result-gallery>
        <div class="grid min-w-0 gap-3" :class="previousPreviews.length ? 'grid-cols-1 sm:grid-cols-[minmax(0,1fr)_6rem] xl:grid-cols-[minmax(0,1fr)_8rem]' : ''">
          <div class="min-w-0" data-latest-preview>
            <TestResultOutput :result="test.latest" compact :max-preview-height="320" />
          </div>
          <div v-if="previousPreviews.length" class="grid min-w-0 grid-cols-2 content-start gap-2 sm:grid-cols-1" data-recent-previews>
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
      <TestRecentPassRate v-if="test.latest.output_kind !== 'statistics'" :evidence="test.latest.pass_rate" />
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
const previousPreviews = computed(() => test.value.latest.output_kind === 'html'
  ? test.value.results.filter(result => result.id !== test.value.latest.id && ['success', 'passed'].includes(result.status)
    && result.output_kind === 'html' && result.output_html?.trim()).slice(0, 2)
  : [])
const historyAnchor = computed(() => test.value.results.find(result => ['success', 'passed', 'failed', 'error'].includes(result.status)))
const statusClass = (result: TestResult) => props.statusClass(result)
const statusLabel = (result: TestResult) => props.statusLabel(result)
const resultTime = (result: TestResult) => props.resultTime(result)
const formatDate = (value?: string) => props.formatDate(value)
</script>
