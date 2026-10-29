<template>
  <section class="space-y-3" data-capability-result>
    <div class="rounded-lg bg-gray-50 p-4 dark:bg-dark-800"><strong>{{ t(`admin.tests.capability.verdicts.${result.verdict}`) }}</strong><p class="mt-1 text-sm text-gray-500">{{ t('admin.tests.capability.result', { passed: result.passed, tested: result.answers.length, required: result.required_passes, total: result.samples }) }}</p></div>
    <details v-for="(answer, index) in result.answers" :key="index" class="rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700"><summary class="cursor-pointer font-medium">{{ index + 1 }}. {{ t(`admin.tests.capability.verdicts.${answer.verdict}`) }} · {{ answer.key }}</summary><p class="mt-3 whitespace-pre-wrap">{{ answer.prompt }}</p><p v-if="answer.match === 'not_date'" class="mt-3 text-gray-500" data-not-date-rule>{{ t('admin.tests.capability.notDateRule') }}</p><p v-else class="mt-3 text-gray-500">{{ t('admin.tests.capability.expected') }}: {{ answer.expected }}</p><pre class="mt-2 whitespace-pre-wrap break-words rounded bg-gray-50 p-3 dark:bg-dark-800">{{ answer.answer }}</pre></details>
  </section>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CapabilityResult } from '@/utils/qualityCapability'
defineProps<{ result: CapabilityResult }>()
const { t } = useI18n()
</script>
