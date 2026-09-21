<template>
  <article class="min-w-0 rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900 sm:p-5" :data-vote-result="item.result.id">
    <header class="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h4 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('tests.account') }} #{{ item.result.account_id }} · {{ item.result.test_name || t('tests.unknownType') }}</h4>
        <p class="mt-1 break-words text-xs text-gray-500 dark:text-gray-400">{{ item.result.model_id || '-' }}<span v-if="item.result.reasoning_effort"> · {{ t('tests.reasoningEffort') }}: {{ item.result.reasoning_effort }}</span> · {{ formatDate(item.result.started_at || item.result.created_at) }}</p>
      </div>
      <span v-if="item.voting.account_paused" class="badge badge-warning">{{ t('tests.voting.paused') }}</span>
    </header>
    <TestResultOutput :result="item.result" compact />
    <div v-if="item.voting.reference_answer" class="mt-4 rounded-md bg-gray-50 p-3 dark:bg-dark-800">
      <p class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{ t('tests.voting.referenceAnswer') }}</p>
      <p class="mt-1 whitespace-pre-wrap break-words text-sm text-gray-600 dark:text-gray-400" data-reference-answer>{{ item.voting.reference_answer }}</p>
    </div>
    <footer class="mt-4 border-t border-gray-100 pt-4 dark:border-dark-700">
      <div class="flex flex-wrap items-center gap-3">
        <button type="button" class="btn btn-secondary" :class="item.voting.my_vote === 'pass' ? 'border-green-500 bg-green-50 text-green-700 dark:bg-green-950/30 dark:text-green-300' : ''" :aria-pressed="item.voting.my_vote === 'pass'" :disabled="busy || !item.voting.open" data-vote-pass @click="$emit('vote', 'pass')">{{ t('tests.voting.pass') }} <span class="tabular-nums">{{ item.voting.pass_count }}</span></button>
        <button type="button" class="btn btn-secondary" :class="item.voting.my_vote === 'fail' ? 'border-red-500 bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300' : ''" :aria-pressed="item.voting.my_vote === 'fail'" :disabled="busy || !item.voting.open" data-vote-fail @click="$emit('vote', 'fail')">{{ t('tests.voting.fail') }} <span class="tabular-nums">{{ item.voting.fail_count }}</span></button>
        <span v-if="busy" class="text-xs text-gray-500" role="status">{{ t('common.loading') }}</span>
        <span v-else-if="!item.voting.open" class="text-xs text-gray-500">{{ t('tests.voting.closed') }}</span>
        <span v-else class="text-xs text-gray-500 dark:text-gray-400">{{ t(item.voting.my_vote ? 'tests.voting.canChange' : 'tests.voting.oneVote') }}</span>
      </div>
      <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('tests.voting.thresholdHint', { reject: item.voting.reject_above, pass: item.voting.pass_at_least }) }}</p>
      <p v-if="error" class="mt-2 text-sm text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
    </footer>
  </article>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TestVote, TestVoteResult } from '@/types'
import TestResultOutput from './TestResultOutput.vue'
defineProps<{ item: TestVoteResult; busy: boolean; error?: string }>()
defineEmits<{ vote: [vote: TestVote] }>()
const { t } = useI18n()
const formatDate = (value?: string) => value ? new Date(value).toLocaleString() : '-'
</script>
