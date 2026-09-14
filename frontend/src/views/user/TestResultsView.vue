<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex items-end justify-between border-b border-gray-200 pb-5 dark:border-dark-700">
        <div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('tests.title') }}</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('tests.description') }}</p></div>
        <button class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="sm" /> {{ t('common.refresh') }}</button>
      </header>
      <p v-if="!loading && !results.length" class="card p-10 text-center text-sm text-gray-500">{{ t('tests.empty') }}</p>
      <div v-else class="grid gap-4 lg:grid-cols-2">
        <article v-for="result in results" :key="result.id" class="card overflow-hidden p-4">
          <div v-if="result.error_message" class="rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300">{{ result.error_message }}</div>
          <div class="mt-2 flex flex-wrap items-center justify-between gap-2">
            <div><h3 class="font-medium text-gray-900 dark:text-white">{{ result.test_name || result.plan_name || result.test_definition?.name || result.plan?.name || `#${result.plan_id ?? result.id}` }}</h3><p class="mt-1 text-xs text-gray-500">{{ result.group_name || '-' }} · {{ result.model_id || result.plan?.model_id || '-' }} · {{ result.latency_ms ?? '-' }}ms · {{ formatDate(result.created_at || result.finished_at) }}</p></div>
            <span :class="result.status === 'success' || result.status === 'passed' ? 'badge badge-success' : 'badge badge-danger'">{{ result.status }}</span>
          </div>
          <TestResultOutput class="mt-4" :result="result" />
        </article>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { testResultsAPI } from '@/api/testResults'
import type { TestResult } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const app = useAppStore()
const loading = ref(false)
const results = ref<TestResult[]>([])
const load = async () => { loading.value = true; try { results.value = await testResultsAPI.list() } catch (error) { app.showError(extractApiErrorMessage(error, t('tests.loadFailed'))) } finally { loading.value = false } }
const formatDate = (value?: string) => value ? new Date(value).toLocaleString() : '-'
onMounted(load)
</script>
