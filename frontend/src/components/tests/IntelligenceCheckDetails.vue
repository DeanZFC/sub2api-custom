<template>
  <div class="space-y-1.5 text-xs leading-5">
    <p v-if="artworkVeto || artworkMissing">
      <strong :class="artworkVeto ? 'text-rose-700 dark:text-rose-300' : 'text-amber-700 dark:text-amber-300'">
        {{ tr('artwork') }} · {{ tr(artworkVeto ? 'art.veto' : 'art.missing') }}
      </strong>
      <span v-if="reviewReason" class="block whitespace-pre-wrap break-words">{{ tr('reviewNote') }} · {{ reviewReason }}</span>
    </p>
    <dl v-if="issues.length" class="space-y-1.5">
      <div v-for="c in issues" :key="c.key">
        <dt class="font-semibold" :class="c.status === 'fail' ? 'text-rose-700 dark:text-rose-300' : 'text-amber-700 dark:text-amber-300'">
          {{ checkName(c) }} · {{ tr('checkStates.' + c.status) }}
        </dt>
        <dd v-if="c.detail || (c.key === 'candy' && httpError)" class="break-words">
          {{ c.detail }} <span v-if="c.key === 'candy' && httpError" class="font-mono">HTTP {{ httpError }}</span>
        </dd>
      </div>
    </dl>
    <p v-if="!checks?.length && !artworkVeto" class="text-gray-500 dark:text-gray-400">{{ tr('checksUnavailable') }}</p>
    <details v-if="passed.length">
      <summary class="w-fit cursor-pointer text-gray-600 hover:underline dark:text-gray-300">{{ tr('passedChecks', { count: passed.length }) }}</summary>
      <dl class="mt-1 space-y-1 border-l-2 border-emerald-500/40 pl-2">
        <div v-for="c in passed" :key="c.key">
          <dt class="font-medium">{{ checkName(c) }} · {{ tr('checkStates.pass') }}</dt>
          <dd v-if="c.detail" class="break-words text-gray-600 dark:text-gray-300">{{ c.detail }}</dd>
        </div>
      </dl>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IntelligenceCheck } from '@/api/admin/intelligence'

const props = defineProps<{
  checks?: IntelligenceCheck[] | null
  httpStatus?: number
  artworkVeto?: boolean
  artworkMissing?: boolean
  reviewReason?: string
}>()
const { t } = useI18n()
const tr = (key: string, params = {}) => t('channelMonitorV2.intelligence.' + key, params)
const checkName = (c: IntelligenceCheck) => tr('checks.' + (c.key === 'http_429' ? 'rate_limit' : c.key))
const httpError = computed(() => props.httpStatus && props.httpStatus >= 300 ? props.httpStatus : undefined)
const passed = computed(() => props.checks?.filter(c => c.status === 'pass') || [])
const issues = computed(() => [...(props.checks || [])].filter(c => c.status !== 'pass')
  .sort((a, b) => Number(b.status === 'fail') - Number(a.status === 'fail')))
</script>
