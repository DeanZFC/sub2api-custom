<template>
  <BaseDialog :show="true" :title="t('accountPerformance.title') + ' · ' + accountName" width="wide" @close="emit('close')">
    <div class="space-y-4">
      <p v-if="loading">{{ t('accountPerformance.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <template v-if="metrics">
        <p v-if="!metrics.monitoring_available" class="text-sm text-amber-600">{{ t('accountPerformance.monitoringDisabled') }}</p>
        <p class="text-sm text-gray-500">{{ t('accountPerformance.semantics') }}</p>
        <p v-if="metrics.paused" class="text-sm text-amber-600">{{ t(metrics.trial_started_at ? 'accountPerformance.trial' : 'accountPerformance.paused') }} · {{ reasonLabel }}</p>
        <label class="flex items-center gap-2 text-sm"><input v-model="config.enabled" :disabled="!metrics.monitoring_available" type="checkbox" data-testid="monitor-enabled">{{ t('accountPerformance.enabled') }}</label>
        <div class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead><tr><th>{{ t('accountPerformance.metric') }}</th><th>{{ t('accountPerformance.current') }}</th><th v-for="column in columns" :key="column.key">{{ t('accountPerformance.' + column.key) }}</th></tr></thead>
            <tbody>
              <tr v-for="metric in metricKeys" :key="metric" :data-threshold="metric">
                <th class="py-3 pr-2">{{ t('accountPerformance.' + metric) }} ({{ isPercentage(metric) ? '%' : 'ms' }})</th>
                <td class="pr-2 tabular-nums">{{ currentValue(metric) }}<span class="block text-xs text-gray-400">{{ t('accountPerformance.samples', { count: sampleCount(metric) }) }}</span></td>
                <td v-for="column in columns" :key="column.key" class="p-1">
                  <input v-model="fields[metric][column.key]" type="number" min="0" :max="isPercentage(metric) ? 100 : 1e9" step="any"
                    :aria-label="t('accountPerformance.' + metric) + ' ' + t('accountPerformance.' + column.key)"
                    :placeholder="t(column.kind === 'pause' ? 'accountPerformance.noBound' : 'accountPerformance.inherit')"
                    class="input w-full min-w-20" :data-testid="metric + '-' + column.key" :disabled="busy">
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <label class="flex items-center justify-between gap-3 text-sm">{{ t('accountPerformance.minSamples') }}<input v-model.number="config.min_samples" class="input w-28" type="number" min="1" max="10000" data-testid="min-samples"></label>
        <label class="flex items-center gap-2 text-sm"><input v-model="config.auto_recover" type="checkbox" data-testid="auto-recover">{{ t('accountPerformance.autoRecover') }}</label>
        <div v-if="config.auto_recover" class="space-y-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
          <label class="flex items-center justify-between gap-3 text-sm">{{ t('accountPerformance.cooldown') }}<input v-model.number="config.cooldown_minutes" class="input w-28" type="number" min="1" max="1440" data-testid="cooldown-minutes"></label>
          <label class="flex items-center justify-between gap-3 text-sm">{{ t('accountPerformance.recoverySamples') }}<input v-model.number="config.recovery_samples" class="input w-28" type="number" min="1" max="100" data-testid="recovery-samples"></label>
          <p class="text-xs text-gray-500">{{ t('accountPerformance.recoveryNote') }}</p>
        </div>
        <p class="text-xs text-gray-500">{{ t('accountPerformance.boundsNote') }}</p>
        <p class="rounded-lg bg-amber-50 p-3 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">{{ t('accountPerformance.saveNote') }}</p>
        <h4 class="text-sm font-semibold">{{ t('accountPerformance.audit') }}</h4>
        <p v-if="auditError" class="text-xs text-red-600">{{ t('accountPerformance.auditUnavailable') }}</p>
        <p v-else-if="!events.length" class="text-xs text-gray-500">{{ t('accountPerformance.noEvents') }}</p>
        <ul v-else class="max-h-32 overflow-auto text-xs text-gray-500"><li v-for="event in events" :key="event.id">{{ event.created_at }} · {{ t('accountPerformance.' + event.action + 'Event') }}</li></ul>
      </template>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="busy || loading" @click="reload">{{ t('accountPerformance.refresh') }}</button>
      <button v-if="metrics?.paused" type="button" class="btn btn-secondary" :disabled="busy || loading" data-testid="performance-release" @click="release">{{ t('accountPerformance.release') }}</button>
      <button type="button" class="btn btn-primary" :disabled="busy || loading || !metrics" data-testid="performance-save" @click="save">{{ t('accountPerformance.save') }}</button>
    </template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, onMounted, ref, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { emptyPerformanceConfig, metricKeys, type AccountPerformance, type PerformanceMetric, type PerformanceEvent } from '@/types/accountPerformance'
import BaseDialog from '@/components/common/BaseDialog.vue'
const props = defineProps<{ accountId: number; accountName: string }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { t } = useI18n()
const metrics = ref<AccountPerformance | null>(null)
const events = ref<PerformanceEvent[]>([])
const config = ref(emptyPerformanceConfig())
const loading = ref(false), busy = ref(false), error = ref(''), auditError = ref(false)
const columns = [
  { key: 'pauseMin', kind: 'pause', bound: 'min' }, { key: 'pauseMax', kind: 'pause', bound: 'max' },
  { key: 'recoveryMin', kind: 'recovery', bound: 'min' }, { key: 'recoveryMax', kind: 'recovery', bound: 'max' }
] as const
const fields = reactive(Object.fromEntries(metricKeys.map(key => [key, { pauseMin: '', pauseMax: '', recoveryMin: '', recoveryMax: '' }])) as Record<PerformanceMetric, Record<typeof columns[number]['key'], string | number>>)
const isPercentage = (key: PerformanceMetric) => key === 'success_rate' || key === 'cache_rate'
const reasonLabel = computed(() => metrics.value?.reason.split(',').filter(Boolean).map(key => t('accountPerformance.' + key)).join('、'))
function currentValue(key: PerformanceMetric) {
  const stats = metrics.value?.metrics
  const value = key === 'ttft' ? stats?.avg_first_token_ms : key === 'duration' ? stats?.avg_duration_ms : stats?.[key]
  return value == null ? '—' : (value * (isPercentage(key) ? 100 : 1)).toFixed(1)
}
function sampleCount(key: PerformanceMetric) {
  const stats = metrics.value?.metrics
  return (key === 'ttft' ? stats?.first_token_samples : key === 'duration' ? stats?.duration_samples : key === 'cache_rate' ? stats?.cache_samples : stats?.request_samples) ?? 0
}
async function reload() {
  loading.value = true; error.value = ''
  try {
    const data = await adminAPI.accounts.getPerformance([props.accountId])
    const m = data[props.accountId]
    if (!m || m.config.version !== 2) throw new Error('missing metric configuration')
    metrics.value = m; config.value = { ...emptyPerformanceConfig(), ...m.config }
    for (const key of metricKeys) for (const column of columns) fields[key][column.key] = m.config[key]?.[column.kind]?.[column.bound] ?? ''
    auditError.value = false
    try { events.value = await adminAPI.accounts.getPerformanceEvents(props.accountId) } catch { auditError.value = true }
  } catch { metrics.value = null; error.value = t('accountPerformance.unavailable') } finally { loading.value = false }
}
async function save() {
  const next = { ...emptyPerformanceConfig(), ...config.value }
  let enabled = false
  for (const key of metricKeys) {
    next[key] = { pause: { min: null, max: null }, recovery: { min: null, max: null } }
    for (const column of columns) {
      const raw = fields[key][column.key]
      const value = raw === '' ? null : Number(raw)
      if (value !== null && (!Number.isFinite(value) || value < 0 || value > (isPercentage(key) ? 100 : 1e9))) { error.value = t('accountPerformance.invalid'); return }
      next[key][column.kind][column.bound] = value
    }
    const { pause, recovery } = next[key]
    const selected = pause.min !== null || pause.max !== null
    enabled ||= selected
    const min = recovery.min ?? pause.min, max = recovery.max ?? pause.max
    if ((!selected && (recovery.min !== null || recovery.max !== null)) ||
      (pause.min !== null && pause.max !== null && pause.min > pause.max) ||
      (min !== null && max !== null && min > max) ||
      (pause.min !== null && min !== null && min < pause.min) || (pause.max !== null && max !== null && max > pause.max)) { error.value = t('accountPerformance.invalid'); return }
  }
  if ((next.enabled && !enabled) || !Number.isInteger(next.min_samples) || next.min_samples < 1 || next.min_samples > 10000 ||
    !Number.isInteger(next.cooldown_minutes) || next.cooldown_minutes < 1 || next.cooldown_minutes > 1440 ||
    !Number.isInteger(next.recovery_samples) || next.recovery_samples < 1 || next.recovery_samples > 100) { error.value = t('accountPerformance.invalid'); return }
  if (!metrics.value) return
  busy.value = true; error.value = ''
  try { await adminAPI.accounts.savePerformance(props.accountId, next, metrics.value.revision); emit('updated'); await reload() } catch { error.value = t('accountPerformance.failed') } finally { busy.value = false }
}
async function release() {
  if (!metrics.value) return
  busy.value = true; error.value = ''
  try { await adminAPI.accounts.releasePerformance(props.accountId, metrics.value.revision, metrics.value.pause_generation); emit('updated'); await reload() } catch { error.value = t('accountPerformance.failed') } finally { busy.value = false }
}
onMounted(reload)
</script>
