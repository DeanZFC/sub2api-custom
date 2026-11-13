<template>
  <BaseDialog :show="show" :title="t('ovhIPv6.storage.title')" width="wide" @close="emit('close')">
    <div class="space-y-5" :aria-busy="loading">
      <p class="text-sm leading-relaxed text-gray-600 dark:text-gray-300">{{ t('ovhIPv6.storage.description') }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="loading && !stats" role="status" class="py-8 text-center text-sm text-gray-500">{{ t('ovhIPv6.loading') }}</p>
      <template v-if="stats">
        <div class="rounded-xl border border-primary-200 bg-primary-50 p-4 dark:border-primary-800 dark:bg-primary-900/20">
          <p class="break-words text-sm font-semibold text-primary-900 dark:text-primary-200" data-testid="ipv6-issued-range">{{ issuedLabel }}</p>
          <p v-if="stats.prefix" class="mt-1 break-all font-mono text-xs text-primary-700 dark:text-primary-300">{{ stats.prefix }}</p>
          <p class="mt-2 text-xs leading-relaxed text-primary-800 dark:text-primary-300">{{ t('ovhIPv6.storage.rangeHint') }}</p>
          <p v-if="!stats.control_available" role="status" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ stats.control_error || t('ovhIPv6.unavailable') }}</p>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('ovhIPv6.storage.counterTitle') }}</h4>
            <div class="mt-3 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white" data-testid="ipv6-counter-bytes">{{ bytes(stats.files.counter_bytes) }}</div>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('ovhIPv6.storage.diskBytes') }}: {{ bytes(stats.files.counter_disk_bytes) }}</p>
            <p class="mt-3 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('ovhIPv6.storage.counterHint') }}</p>
          </section>
          <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('ovhIPv6.storage.databaseTitle') }}</h4>
            <div class="mt-3 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white" data-testid="ipv6-database-bytes">{{ bytes(stats.database.total_bytes) }}</div>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('ovhIPv6.storage.databaseParts', { table: bytes(stats.database.table_bytes), index: bytes(stats.database.index_bytes) }) }}</p>
            <p class="mt-3 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('ovhIPv6.storage.databaseHint') }}</p>
          </section>
        </div>

        <section>
          <h4 class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">{{ t('ovhIPv6.storage.filesTitle') }}</h4>
          <p v-if="!stats.files.available" role="status" class="mb-2 text-xs text-amber-700 dark:text-amber-300">{{ stats.files.error || t('ovhIPv6.storage.filesUnavailable') }}</p>
          <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
            <table class="w-full text-left text-xs">
              <thead class="bg-gray-50 text-gray-500 dark:bg-dark-700 dark:text-gray-400">
                <tr><th class="px-3 py-2 font-medium">{{ t('ovhIPv6.storage.file') }}</th><th class="px-3 py-2 text-right font-medium">{{ t('ovhIPv6.storage.fileBytes') }}</th><th class="px-3 py-2 text-right font-medium">{{ t('ovhIPv6.storage.diskBytes') }}</th></tr>
              </thead>
              <tbody class="divide-y divide-gray-100 text-gray-700 dark:divide-dark-700 dark:text-gray-300">
                <tr v-for="item in stats.files.items" :key="item.name">
                  <td class="px-3 py-2"><code>{{ item.name }}</code><p v-if="item.error" class="mt-1 text-amber-700 dark:text-amber-300">{{ item.error }}</p></td>
                  <td class="px-3 py-2 text-right tabular-nums">{{ bytes(item.bytes) }}</td>
                  <td class="px-3 py-2 text-right tabular-nums">{{ bytes(item.disk_bytes) }}</td>
                </tr>
                <tr class="font-medium"><td class="px-3 py-2">{{ t('ovhIPv6.storage.filesTotal') }}</td><td class="px-3 py-2 text-right tabular-nums">{{ bytes(stats.files.total_bytes) }}</td><td class="px-3 py-2 text-right tabular-nums">{{ bytes(stats.files.total_disk_bytes) }}</td></tr>
              </tbody>
            </table>
          </div>
        </section>

        <section class="rounded-lg bg-gray-50 p-4 dark:bg-dark-700/50">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('ovhIPv6.storage.leasesTitle') }}</h4>
          <p v-if="!stats.database.available" role="status" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ stats.database.error || t('ovhIPv6.storage.databaseUnavailable') }}</p>
          <dl class="mt-3 grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
            <div v-for="item in leaseCounts" :key="item.key"><dt class="text-gray-500 dark:text-gray-400">{{ t(`ovhIPv6.storage.${item.key}`) }}</dt><dd class="mt-1 font-semibold tabular-nums text-gray-900 dark:text-white">{{ item.value ?? t('ovhIPv6.storage.unknown') }}</dd></div>
          </dl>
          <p class="mt-3 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('ovhIPv6.storage.leasesHint') }}</p>
        </section>
        <p class="text-xs leading-relaxed text-gray-400 dark:text-gray-500">{{ t('ovhIPv6.storage.sampleHint', { time: sampledAt }) }}</p>
      </template>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="loading" data-testid="refresh-ipv6-storage" @click="loadStorage"><Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />{{ t('ovhIPv6.storage.refresh') }}</button>
      <button type="button" class="btn btn-primary" @click="emit('close')">{{ t('common.close') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { ovhIPv6API, type OvhIPv6StorageStats } from '@/api/admin/ovhIPv6'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatBytes } from '@/utils/format'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const stats = ref<OvhIPv6StorageStats | null>(null)
const loading = ref(false)
const error = ref('')
let controller: AbortController | null = null

const issuedLabel = computed(() => {
  const issued = stats.value?.issued
  if (issued == null) return t('ovhIPv6.storage.issuedUnknown')
  return issued === '0' ? t('ovhIPv6.storage.noneIssued') : t('ovhIPv6.storage.issuedRange', { end: issued })
})
const sampledAt = computed(() => {
  if (!stats.value) return ''
  const date = new Date(stats.value.sampled_at)
  return Number.isNaN(date.valueOf()) ? stats.value.sampled_at : date.toLocaleString(locale.value)
})
const leaseCounts = computed(() => {
  const db = stats.value?.database
  return [
    { key: 'rows', value: db?.rows },
    { key: 'reserved', value: db?.reserved },
    { key: 'bound', value: db?.bound },
    { key: 'retiring', value: db?.retiring }
  ]
})

function bytes(value: number | null) {
  if (value == null) return t('ovhIPv6.storage.unknown')
  return value < 1024 ? `${value} B` : `${formatBytes(value)} (${value.toLocaleString(locale.value)} B)`
}

async function loadStorage() {
  if (loading.value || !props.show) return
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = ''
  try {
    const result = await ovhIPv6API.getStorage(request.signal)
    if (controller === request && !request.signal.aborted) stats.value = result
  } catch (cause) {
    if (controller === request && !request.signal.aborted) error.value = extractApiErrorMessage(cause, t('ovhIPv6.storage.loadFailed'))
  } finally {
    if (controller === request) {
      loading.value = false
      controller = null
    }
  }
}

watch(() => props.show, show => {
  if (show) {
    stats.value = null
    void loadStorage()
  } else {
    controller?.abort()
    controller = null
    loading.value = false
  }
}, { immediate: true })
onUnmounted(() => { controller?.abort(); controller = null })
</script>
