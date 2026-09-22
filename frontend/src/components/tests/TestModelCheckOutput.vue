<template>
  <div class="min-w-0 space-y-3" data-model-check-output>
    <div class="flex flex-wrap items-center gap-2">
      <span :class="verdictClass" data-model-check-verdict>{{ t(`tests.modelCheck.${result.verdict}`) }}</span>
      <span class="text-xs text-gray-500 dark:text-gray-400">{{ t(`tests.modelCheck.${result.match_mode}`) }}</span>
    </div>
    <dl class="space-y-2 text-xs">
      <div class="min-w-0"><dt class="text-gray-500 dark:text-gray-400">{{ t('tests.modelCheck.requestedModel') }}</dt><dd class="mt-0.5 break-all font-mono text-gray-900 dark:text-gray-100" data-model-check-requested>{{ result.requested_model || '-' }}</dd></div>
      <div class="min-w-0"><dt class="text-gray-500 dark:text-gray-400">{{ t('tests.modelCheck.upstreamModel') }}</dt><dd class="mt-0.5 break-all font-mono text-gray-900 dark:text-gray-100" data-model-check-upstream>{{ result.upstream_model || '-' }}</dd></div>
      <div class="min-w-0"><dt class="text-gray-500 dark:text-gray-400">{{ t('tests.modelCheck.returnedModels') }}</dt><dd class="mt-0.5 break-all font-mono text-gray-900 dark:text-gray-100" data-model-check-returned><span v-for="(model, index) in result.returned_models" :key="index" class="block">{{ model }}</span><span v-if="!result.returned_models.length">-</span></dd></div>
    </dl>
    <p v-if="result.reason !== 'match'" class="text-xs text-gray-500 dark:text-gray-400" data-model-check-reason>{{ t(`tests.modelCheck.reasons.${result.reason}`) }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestOutputModelCheck } from '@/types'

const props = defineProps<{ result: TestOutputModelCheck }>()
const { t } = useI18n()
const verdictClass = computed(() => props.result.verdict === 'pass' ? 'badge badge-success' : props.result.verdict === 'fail' ? 'badge badge-danger' : 'badge badge-warning')
</script>
