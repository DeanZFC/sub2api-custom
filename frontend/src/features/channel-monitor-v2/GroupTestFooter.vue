<template>
  <div class="mt-3 flex h-20 items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-dark-700" data-testid="group-test-footer">
    <div class="min-w-0 text-xs" :title="candyDetail">
      <p class="truncate font-medium text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.tests.candy') }}</p>
      <p class="mt-1 truncate font-semibold tabular-nums" :class="summary.total && !summary.unknown && summary.passed === summary.total ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-700 dark:text-gray-200'" data-testid="candy-summary">
        <template v-if="state === 'ready' && summary.total">{{ summary.passed }}/{{ summary.total }} <span class="ml-1">{{ summary.percent == null ? t('channelMonitorV2.tests.unrated') : `${summary.percent}%` }}</span></template>
        <template v-else>{{ placeholder }}</template>
      </p>
    </div>
    <button
      type="button"
      class="min-w-0 shrink-0 rounded-lg text-right text-xs focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 disabled:cursor-default"
      :disabled="state !== 'ready' || !results.pelican.length"
      :aria-label="t('channelMonitorV2.tests.viewAll', { group: groupName })"
      :title="t('channelMonitorV2.tests.viewAll', { group: groupName })"
      data-testid="pelican-gallery-open"
      @click="$emit('open')"
    >
      <span class="mb-1 block text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.tests.pelican') }}<span v-if="state === 'ready' && results.pelican.length"> · {{ accountCount ? t('channelMonitorV2.tests.accounts', { count: accountCount }) : t('channelMonitorV2.tests.groupResult') }} ›</span></span>
      <span v-if="state === 'ready' && previews.length" class="flex justify-end gap-1" aria-hidden="true" inert>
        <span v-for="result in previews" :key="result.id" class="pointer-events-none block h-8 w-10 overflow-hidden rounded border border-gray-200 bg-white dark:border-dark-700" data-testid="pelican-thumbnail">
          <TestResultOutput :result="result" compact :max-preview-height="30" />
        </span>
      </span>
      <span v-else class="block text-gray-400">{{ state === 'ready' && results.pelican.length ? t('channelMonitorV2.tests.viewResults') : placeholder }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import { candySummary, pelicanAccountCount, type GroupTestResults, type TestResultsState } from './monitorTestResults'

const props = defineProps<{ results: GroupTestResults; state: TestResultsState; groupName: string }>()
defineEmits<{ open: [] }>()
const { t } = useI18n()
const summary = computed(() => candySummary(props.results.candy))
const accountCount = computed(() => pelicanAccountCount(props.results.pelican))
const previews = computed(() => props.results.pelican.filter(result => result.output_html?.trim()).slice(0, 3))
const placeholder = computed(() => t(`channelMonitorV2.tests.${props.state === 'ready' ? 'empty' : props.state}`))
const candyDetail = computed(() => props.state === 'ready' && summary.value.total
  ? t('channelMonitorV2.tests.candyDetail', summary.value) : placeholder.value)
</script>
