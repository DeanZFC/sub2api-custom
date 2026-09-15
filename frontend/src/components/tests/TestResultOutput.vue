<template>
  <div>
    <template v-if="result.output_kind === 'html' && result.output_html">
      <iframe
        class="h-[28rem] w-full rounded-lg border border-gray-200 bg-white dark:border-dark-700"
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
        :srcdoc="safeHTML"
        :title="t('tests.htmlResult')"
      />
    </template>
    <div v-else-if="result.output_kind === 'number' && result.output_numeric != null" class="rounded-lg bg-gray-50 p-5 text-center text-3xl font-semibold text-gray-900 dark:bg-dark-800 dark:text-white">
      {{ result.output_numeric }}
    </div>
    <pre v-else-if="result.response_text" class="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-800">{{ result.response_text }}</pre>
    <p v-else-if="result.status === 'running' || result.status === 'pending'" class="flex items-center gap-2 rounded-lg bg-blue-50 px-4 py-3 text-sm text-blue-700 dark:bg-blue-950/30 dark:text-blue-300">
      <span class="h-2 w-2 animate-pulse rounded-full bg-blue-500" />
      {{ t('tests.running') }}
    </p>
    <p v-else class="text-sm text-gray-500">{{ t('tests.noOutput') }}</p>
    <details v-if="result.response_text && ['html', 'number'].includes(result.output_kind)" class="mt-3 text-sm text-gray-500">
      <summary class="cursor-pointer">{{ t('tests.rawOutput') }}</summary>
      <pre class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words">{{ result.response_text }}</pre>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestResult } from '@/types'
import { buildTestPreviewHTML } from '@/utils/testPreview'

const props = defineProps<{ result: TestResult }>()
const { t } = useI18n()
// Render HTML results as soon as they arrive. The helper strips unsafe markup
// while keeping the test animation in an isolated sandboxed iframe.
const safeHTML = computed(() => buildTestPreviewHTML(props.result.output_html || ''))
</script>
