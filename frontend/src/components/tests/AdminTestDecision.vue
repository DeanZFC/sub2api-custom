<template>
  <div class="mt-3 space-y-2" data-admin-decision>
    <div class="flex flex-wrap items-center gap-2">
      <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('tests.adminReview.title') }}</span>
      <strong v-if="review.admin_verdict" class="inline-flex items-center gap-1 text-xs font-semibold" :class="review.admin_verdict === 'pass' ? 'text-emerald-700 dark:text-emerald-400' : 'text-red-700 dark:text-red-400'" data-admin-verdict><Icon :name="review.admin_verdict === 'pass' ? 'check' : 'x'" size="sm" />{{ t(`tests.adminReview.${review.admin_verdict}`) }}</strong>
      <span v-if="review.account_paused" class="text-xs text-red-600 dark:text-red-400">{{ t('tests.voting.paused') }}</span>
    </div>
    <TestPassRateEvidence v-if="showPassRate && review.result.pass_rate" :evidence="review.result.pass_rate" />
    <component :is="compact && review.admin_verdict ? 'details' : 'div'" class="text-xs" :data-admin-edit-decision="compact && review.admin_verdict ? '' : undefined">
      <summary v-if="compact && review.admin_verdict" class="w-fit cursor-pointer text-gray-500 dark:text-gray-400">{{ t('tests.adminReview.editDecision') }}</summary>
      <div class="flex flex-wrap gap-2" :class="compact && review.admin_verdict ? 'mt-2' : ''">
        <button type="button" class="inline-flex min-w-0 max-w-full items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-emerald-50 hover:text-emerald-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500 disabled:cursor-default disabled:opacity-50 dark:text-gray-400 dark:hover:bg-emerald-900/20 dark:hover:text-emerald-400" :disabled="busy || review.admin_verdict === 'pass'" :aria-pressed="review.admin_verdict === 'pass'" data-admin-pass @click="decide('pass')"><Icon name="check" size="sm" /><span class="min-w-0 break-words">{{ t('tests.adminReview.markPass') }}</span></button>
        <button type="button" class="inline-flex min-w-0 max-w-full items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-red-50 hover:text-red-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-red-500 disabled:cursor-default disabled:opacity-50 dark:text-gray-400 dark:hover:bg-red-900/20 dark:hover:text-red-400" :disabled="busy || review.admin_verdict === 'fail'" :aria-pressed="review.admin_verdict === 'fail'" data-admin-fail @click="decide('fail')"><Icon name="x" size="sm" /><span class="min-w-0 break-words">{{ t('tests.adminReview.markFail') }}</span></button>
      </div>
      <p v-if="compact && review.admin_verdict" class="mt-2 text-gray-500 dark:text-gray-400" data-review-round-hint>{{ t('tests.adminReview.roundOverrideHint') }}</p>
    </component>
    <details v-if="compact && !review.admin_verdict" class="text-xs text-gray-500 dark:text-gray-400">
      <summary class="w-fit cursor-pointer">{{ t('tests.adminReview.details') }}</summary>
      <p class="mt-1" data-review-round-hint>{{ t('tests.adminReview.roundOverrideHint') }}</p>
    </details>
    <p v-if="!compact" class="text-xs text-gray-500 dark:text-gray-400" data-review-round-hint>{{ t('tests.adminReview.roundOverrideHint') }}</p>
    <p v-if="review.admin_verdict === 'pass' && review.verdict !== 'pass'" class="text-xs text-amber-700 dark:text-amber-400">{{ t('tests.adminReview.automaticHold') }}</p>
    <p v-if="error" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { decideResult, type TestAdminReview } from '@/api/admin/tests'
import Icon from '@/components/icons/Icon.vue'
import TestPassRateEvidence from './TestPassRateEvidence.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = withDefaults(defineProps<{ review: TestAdminReview; compact?: boolean; showPassRate?: boolean }>(), { compact: false, showPassRate: true })
const emit = defineEmits<{ decided: [] }>()
const { t } = useI18n()
const busy = ref(false)
const error = ref('')
async function decide(verdict: 'pass' | 'fail') {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await decideResult(props.review.result.id, props.review.generation, verdict)
    emit('decided')
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('tests.adminReview.saveFailed'))
    emit('decided')
  } finally {
    busy.value = false
  }
}
</script>
