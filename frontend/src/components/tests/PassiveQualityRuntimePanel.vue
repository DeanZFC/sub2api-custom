<template>
  <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-quality-runtime>
    <summary class="cursor-pointer text-sm font-semibold">{{ tr('runtimeTitle') }}</summary>
    <p class="my-3 text-xs text-gray-500">{{ tr('runtimeScope') }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }} <button class="underline" @click="refresh">{{ t('common.retry') }}</button></p>
    <div v-if="diagnostic" class="grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-3">
      <p>{{ tr('runtimeQueue') }}: {{ diagnostic.queue_depth }} / {{ diagnostic.queue_capacity }} · {{ tr('runtimePending') }}: {{ diagnostic.pending_records }}</p>
      <p>{{ tr('runtimeDropped') }}: {{ diagnostic.dropped_records }} · {{ tr('runtimeGaps') }}: {{ diagnostic.pending_gap_scopes }}</p>
      <p>{{ tr('runtimeLag') }}: {{ diagnostic.collection_lag_seconds.toFixed(1) }} s</p>
      <p>{{ tr('runtimePersisted') }}: {{ date(diagnostic.last_persisted_at || undefined) }}</p>
      <p>{{ tr('runtimeEvaluated') }}: {{ date(diagnostic.last_sweep_succeeded_at || undefined) }} · {{ diagnostic.last_sweep_duration_ms }} ms</p>
      <p>{{ tr('runtimeSkipped') }}: {{ diagnostic.sweep_skipped_backlog }}</p>
      <p>{{ tr('runtimeCleanup') }}: {{ date(diagnostic.last_cleanup_succeeded_at || undefined) }}</p>
    </div>
    <p v-for="(message, index) in errors" :key="index" class="mt-2 break-words text-xs text-amber-700">{{ message }}</p>
  </details>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { passiveQualityAPI, type PassiveRuntime } from '@/api/admin/passiveQuality'
import { useQualityLabels } from '@/composables/useQualityLabels'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ visible: boolean }>()
const { t } = useI18n(), { date } = useQualityLabels()
const tr = (key: string) => t('admin.tests.experience.' + key)
const diagnostic = ref<PassiveRuntime | null>(null), error = ref('')
const errors = computed(() => [diagnostic.value?.last_persist_error, diagnostic.value?.last_sweep_error, diagnostic.value?.last_cleanup_error].filter(Boolean))
let request = 0, loading = false, timer: ReturnType<typeof setInterval> | undefined
async function refresh() {
  if (loading) return
  const current = ++request; loading = true
  try { const value = await passiveQualityAPI.runtime(); if (current === request) { diagnostic.value = value; error.value = '' } } catch (e) { if (current === request) error.value = extractApiErrorMessage(e, t('admin.tests.passive.loadFailed')) } finally { loading = false }
}
watch(() => props.visible, visible => { if (visible) void refresh() })
onMounted(() => { if (props.visible) void refresh(); timer = setInterval(() => { if (props.visible && !document.hidden) void refresh() }, 15000) })
onUnmounted(() => { request++; clearInterval(timer) })
</script>
