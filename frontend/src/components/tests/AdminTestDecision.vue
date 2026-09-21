<template>
  <div class="mt-3 space-y-2" data-admin-decision>
    <div class="flex flex-wrap items-center gap-2">
      <span class="text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('tests.adminReview.title') }}</span>
      <span v-if="review.admin_verdict" class="text-xs" :class="review.admin_verdict === 'pass' ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'">{{ t(`tests.adminReview.${review.admin_verdict}`) }}</span>
      <span v-if="review.account_paused" class="badge badge-warning">{{ t('tests.voting.paused') }}</span>
    </div>
    <div class="flex flex-wrap gap-2">
      <button type="button" class="btn btn-secondary btn-sm min-w-0 max-w-full whitespace-normal" :disabled="busy || review.admin_verdict === 'pass'" :aria-pressed="review.admin_verdict === 'pass'" data-admin-pass @click="decide('pass')"><Icon name="check" size="sm" /><span class="min-w-0 break-words">{{ t('tests.adminReview.markPass') }}</span></button>
      <button type="button" class="btn btn-secondary btn-sm min-w-0 max-w-full whitespace-normal text-red-600 dark:text-red-400" :disabled="busy || review.admin_verdict === 'fail'" :aria-pressed="review.admin_verdict === 'fail'" data-admin-fail @click="decide('fail')"><Icon name="x" size="sm" /><span class="min-w-0 break-words">{{ t('tests.adminReview.markFail') }}</span></button>
    </div>
    <p v-if="review.admin_verdict === 'pass' && review.verdict !== 'pass'" class="text-xs text-amber-700 dark:text-amber-400">{{ t('tests.adminReview.automaticHold') }}</p>
    <p v-if="error" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { decideResult, type TestAdminReview } from '@/api/admin/tests'
import Icon from '@/components/icons/Icon.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ review: TestAdminReview }>()
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
