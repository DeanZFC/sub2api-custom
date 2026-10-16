<template>
  <div v-if="info" class="text-xs text-gray-500 dark:text-gray-400" data-test-usage>
    <p v-if="info.next_check_at" class="mb-1 text-amber-700 dark:text-amber-300" data-next-check>
      {{ t('admin.tests.nextCheck', { time: new Date(info.next_check_at).toLocaleString(), count: info.consecutive_failures || 0 }) }}
    </p>
    <details>
      <summary class="cursor-pointer">
        {{ t('admin.tests.testUsage') }} · {{ t('admin.tests.usageRequests', { count: info.attempts?.length || 0 }) }} · {{ t('admin.tests.usageInput') }} {{ tokens(info.input_tokens) }} / {{ t('admin.tests.usageOutput') }} {{ tokens(info.output_tokens) }}
      </summary>
      <p class="mt-1">{{ t('admin.tests.usageReasoning') }}: {{ tokens(info.reasoning_tokens) }} · {{ t('admin.tests.usageCacheRead') }}: {{ tokens(info.cache_read_tokens) }} · {{ t('admin.tests.usageCacheWrite') }}: {{ tokens(info.cache_creation_tokens) }}</p>
      <p class="mt-1">{{ t('admin.tests.usageNote') }}</p>
      <ol class="mt-2 space-y-1">
        <li v-for="(attempt, index) in info.attempts" :key="index" data-usage-attempt>
          {{ t('admin.tests.usageAttempt', { index: index + 1 }) }} · {{ new Date(attempt.started_at).toLocaleString() }}<template v-if="attempt.http_status"> · HTTP {{ attempt.http_status }}</template><template v-if="attempt.failure_kind"> · {{ attempt.failure_kind }}</template>
          · {{ t('admin.tests.usageInput') }} {{ tokens(attempt.input_tokens) }} / {{ t('admin.tests.usageOutput') }} {{ tokens(attempt.output_tokens) }} · {{ t('admin.tests.usageReasoning') }} {{ tokens(attempt.reasoning_tokens) }}
        </li>
      </ol>
    </details>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TestExecutionInfo } from '@/types'
defineProps<{ info?: TestExecutionInfo | null }>()
const { t } = useI18n()
const tokens = (value?: number | null) => value == null ? t('admin.tests.usageUnknown') : value.toLocaleString()
</script>
