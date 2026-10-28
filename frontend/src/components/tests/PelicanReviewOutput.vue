<template>
  <section class="mb-3 space-y-2 rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700" data-auto-review-output>
    <div class="flex flex-wrap items-center gap-2">
      <strong>{{ tr('title') }}</strong>
      <span class="badge" :class="review.verdict === 'pass' ? 'badge-success' : review.verdict === 'fail' ? 'badge-danger' : 'badge-warning'" data-auto-review-verdict>{{ tr(review.verdict) }}</span>
    </div>
    <p v-if="review.reason" class="break-words text-gray-600 dark:text-gray-300" data-auto-review-reason>{{ review.reason }}</p>
    <details v-if="!compact && review.criteria?.length" data-auto-review-criteria>
      <summary class="cursor-pointer text-gray-500">{{ tr('criteriaTitle') }}</summary>
      <ul class="mt-2 space-y-1">
        <li v-for="criterion in review.criteria" :key="criterion.key" class="flex gap-2"><span :class="criterion.passed ? 'text-green-600' : 'text-red-600'">{{ criterion.passed ? '✓' : '✗' }}</span><span>{{ criterionName(criterion.key) }}<span v-if="criterion.reason">：{{ criterion.reason }}</span></span></li>
      </ul>
    </details>
    <p v-if="review.verdict === 'pending'" class="text-xs text-gray-500" data-auto-review-pending>{{ tr('pendingHint') }}</p>
    <p v-if="!compact" class="text-xs text-gray-500">{{ tr('overrideHint') }}</p>
  </section>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TestAutoReview } from '@/types'
defineProps<{ review: TestAutoReview; compact?: boolean }>()
const { t } = useI18n()
const tr = (key: string) => t(`admin.tests.autoReview.${key}`)
const criterionName = (key: string) => ['pelican', 'bicycle', 'riding', 'motion', 'complete'].includes(key) ? tr(`criteria.${key}`) : key
</script>
