<template>
  <BaseDialog :show="show" :title="t('channelMonitorV2.tests.galleryTitle', { group: groupName })" width="extra-wide" @close="$emit('close')">
    <template v-if="show">
      <button v-if="selected" type="button" class="btn btn-secondary mb-4" data-testid="pelican-back" @click="selectedId = null">{{ t('channelMonitorV2.tests.backToAll') }}</button>
      <div v-if="selected" data-testid="pelican-expanded">
        <p class="mb-3 text-sm text-gray-500">{{ accountLabel(selected.account_id) }} · {{ selected.model_id || '—' }}</p>
        <TestResultOutput :result="selected" />
      </div>
      <div v-else class="space-y-5" data-testid="pelican-accounts">
        <p class="text-xs text-gray-500">{{ t('channelMonitorV2.tests.galleryDescription') }}</p>
        <section v-for="account in accounts" :key="account.key" data-testid="pelican-account">
          <h4 class="mb-2 flex flex-wrap items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
            {{ accountLabel(account.id) }}
            <span v-if="account.paused" class="rounded bg-amber-50 px-1.5 py-0.5 text-xs font-normal text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">{{ t('channelMonitorV2.tests.paused') }}</span>
          </h4>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            <article v-for="result in account.results" :key="result.id" class="min-w-0 rounded-xl border border-gray-200 p-3 dark:border-dark-700" data-testid="pelican-result">
              <div class="mb-2 flex items-start justify-between gap-2 text-xs">
                <div class="min-w-0">
                  <p class="break-words font-medium text-gray-800 dark:text-gray-100">{{ result.model_id || '—' }} <span v-if="result.reasoning_effort" class="text-gray-400">{{ result.reasoning_effort }}</span></p>
                  <time class="text-gray-400">{{ resultTime(result) }}</time>
                </div>
                <span v-if="result.status === 'failed' || result.status === 'error'" class="shrink-0 text-rose-500">{{ t('channelMonitorV2.tests.failed') }}</span>
              </div>
              <div class="relative min-h-24 overflow-hidden rounded-lg bg-gray-50 dark:bg-dark-900/30">
                <div class="pointer-events-none max-h-48 overflow-hidden" aria-hidden="true" inert>
                  <TestResultOutput :result="result" compact :max-preview-height="190" />
                </div>
                <button type="button" class="absolute inset-0 rounded-lg focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :aria-label="t('channelMonitorV2.tests.enlarge', { account: accountLabel(account.id), model: result.model_id || '—' })" data-testid="pelican-enlarge" @click="selectedId = result.id" />
              </div>
            </article>
          </div>
        </section>
        <p v-if="!accounts.length" class="text-sm text-gray-400">{{ t('channelMonitorV2.tests.empty') }}</p>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import type { TestResult } from '@/types'

const props = defineProps<{ show: boolean; groupName: string; results: TestResult[] }>()
defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const selectedId = ref<number | null>(null)
watch(() => props.show, () => { selectedId.value = null })
const selected = computed(() => props.results.find(result => result.id === selectedId.value))
const accounts = computed(() => {
  const grouped = new Map<number | 'group', { key: number | 'group'; id?: number | null; paused: boolean; results: TestResult[] }>()
  for (const result of props.results) {
    const key = result.account_id ?? 'group'
    let account = grouped.get(key)
    if (!account) {
      account = { key, id: result.account_id, paused: result.account_schedulable === false, results: [] }
      grouped.set(key, account)
    }
    account.results.push(result)
  }
  return [...grouped.values()]
})
function accountLabel(id?: number | null) {
  return id == null ? t('channelMonitorV2.tests.groupResult') : t('channelMonitorV2.tests.account', { id })
}
function resultTime(result: TestResult) {
  const time = Date.parse(result.finished_at || result.started_at || result.created_at || '')
  return Number.isFinite(time) ? new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }).format(time) : '—'
}
</script>
