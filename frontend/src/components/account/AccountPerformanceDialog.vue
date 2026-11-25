<template>
  <BaseDialog :show="true" :title="t('accountPerformance.title')" width="wide" @close="emit('close')">
    <div class="space-y-5 text-sm">
      <p v-if="loading && !snapshot" role="status">{{ t('accountPerformance.loading') }}</p>
      <template v-if="snapshot">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0">
            <span class="break-all font-medium" dir="auto">{{ accountName }}</span>
            <span v-if="dirty" class="ml-2 text-xs text-amber-700 dark:text-amber-300">{{ t('accountPerformance.unsaved') }}</span>
          </div>
          <div role="group" :aria-label="t('accountPerformance.title')" class="inline-flex rounded-lg bg-gray-100 p-1 dark:bg-dark-900">
            <button v-for="item in views" :key="item" type="button" :data-testid="'performance-' + item + '-view'"
              :aria-pressed="view === item" :disabled="busy" class="rounded-md px-3 py-1.5 text-sm focus-visible:ring-2 focus-visible:ring-primary-500"
              :class="view === item ? 'bg-white font-semibold text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-600 dark:text-dark-300'"
              @click="view = item">{{ t(item === 'status' ? 'accountPerformance.statusView' : 'accountPerformance.rulesView') }}</button>
          </div>
        </div>
        <p v-if="!snapshot.monitoring_available" class="text-amber-700 dark:text-amber-300">{{ t('accountPerformance.monitoringDisabled') }}</p>
        <section v-if="view === 'status'" class="space-y-2" :aria-label="t('accountPerformance.status')">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h4 class="font-semibold" data-testid="performance-phase">
              {{ t('accountPerformance.' + phaseLabel) }}
            </h4>
            <span v-if="phase === 'trial'" class="tabular-nums text-gray-600 dark:text-dark-300">
              {{ t('accountPerformance.trialAdmissions', { used: snapshot.trial_requests, limit: snapshot.trial_limit ?? snapshot.config.recovery_samples * 3 }) }}
            </span>
          </div>
          <p v-if="snapshot.paused && snapshot.reason" class="text-amber-700 dark:text-amber-300">{{ t('accountPerformance.pauseReason', { reason: reasonLabel(snapshot.reason) }) }}</p>
          <p v-if="phase === 'paused' && snapshot.recover_after">{{ t('accountPerformance.nextTrial', { time: dateTime(snapshot.recover_after) }) }}</p>
          <p v-if="phase === 'paused' && !snapshot.config.auto_recover">{{ t('accountPerformance.manualRecovery') }}</p>
          <div v-if="phase === 'trial'" class="flex flex-wrap gap-x-3 gap-y-1">
            <span v-if="snapshot.monitoring_available" data-testid="trial-progress">{{ t('accountPerformance.' + trialProgress) }}</span>
            <span v-if="snapshot.trial_ends_at">{{ t('accountPerformance.trialDeadline', { time: dateTime(snapshot.trial_ends_at) }) }}</span>
          </div>
          <p v-if="snapshot.monitoring_available && snapshot.metrics.window_start" class="text-xs text-gray-600 dark:text-dark-300">
            {{ t('accountPerformance.sampleWindow', { start: dateTime(snapshot.metrics.window_start), end: dateTime(snapshot.metrics.window_end) }) }}
          </p>
          <dl class="grid grid-cols-2 gap-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-800 sm:grid-cols-4">
            <div v-for="{ metric, target } in displayedMetrics" :key="metric" :data-metric="metric" class="min-w-0">
              <dt class="text-xs text-gray-600 dark:text-dark-300">{{ t('accountPerformance.' + metric) }}</dt>
              <dd class="font-semibold tabular-nums">{{ currentValue(metric) }}</dd>
              <dd v-if="target" class="text-xs font-medium tabular-nums">{{ t('accountPerformance.recoveryTarget', { target }) }}</dd>
              <dd v-if="snapshot.monitoring_available" class="text-xs text-gray-600 dark:text-dark-300">
                {{ target
                  ? t('accountPerformance.sampleProgress', { count: sampleCount(metric), required: snapshot.config.recovery_samples })
                  : t('accountPerformance.samples', { count: sampleCount(metric) }) }}
              </dd>
            </div>
          </dl>
        </section>

        <fieldset v-else :disabled="busy || loading" class="min-w-0 space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <label class="flex items-center gap-2 font-semibold">
              <input v-model="draft.enabled" type="checkbox" data-testid="monitor-enabled" :disabled="!snapshot.monitoring_available && !draft.enabled">
              {{ t('accountPerformance.enabled') }}
            </label>
            <button type="button" class="text-sm font-medium text-primary-700 hover:underline dark:text-primary-300"
              :aria-expanded="advanced" aria-controls="performance-thresholds" data-testid="performance-advanced" @click="advanced = !advanced">
              {{ t(advanced ? 'accountPerformance.fewerBounds' : 'accountPerformance.moreBounds') }}
            </button>
          </div>
          <p class="text-xs text-gray-600 dark:text-dark-300">{{ t('accountPerformance.boundsNote') }}</p>
          <div id="performance-thresholds" class="grid gap-3" :class="!advanced && 'sm:grid-cols-2 sm:gap-x-6'">
            <div v-for="metric in metricKeys" :key="metric" :data-threshold="metric" class="grid items-center gap-2"
              :class="advanced && 'border-t border-gray-100 py-2 dark:border-dark-700 sm:grid-cols-[8rem_1fr]'">
              <span v-if="advanced" class="font-medium">{{ t('accountPerformance.' + metric) }} ({{ unit(metric) }})</span>
              <div class="grid gap-2" :class="advanced && 'grid-cols-2 sm:grid-cols-4'">
                <label v-for="column in visibleColumns(metric)" :key="column.key" class="min-w-0"
                  :class="advanced ? 'text-xs text-gray-600 dark:text-dark-300' : 'grid grid-cols-[minmax(0,1fr)_7rem] items-center gap-2'">
                  <span>{{ advanced ? t('accountPerformance.' + column.key) : t('accountPerformance.quickBounds.' + metric) + ' (' + unit(metric) + ')' }}</span>
                  <input type="number" class="input" :class="advanced && 'mt-1'" min="0" :max="maxValue(metric)" step="any"
                    :aria-label="advanced ? t('accountPerformance.' + metric) + ' ' + t('accountPerformance.' + column.key) + ' (' + unit(metric) + ')' : undefined"
                    :placeholder="t(column.group === 'pause' ? 'accountPerformance.noBound' : 'accountPerformance.inherit')"
                    :data-testid="metric + '-' + column.key" :value="draft[metric][column.group][column.bound]"
                    @input="setBound(metric, column, $event)">
                </label>
              </div>
            </div>
          </div>
          <label class="grid items-center gap-2 sm:grid-cols-[minmax(12rem,1fr)_7rem]">
            {{ t('accountPerformance.minSamples') }}
            <input v-model.number="draft.min_samples" class="input" type="number" min="1" max="10000" data-testid="min-samples">
          </label>
          <div class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
            <label class="flex items-center gap-2 font-semibold">
              <input v-model="draft.auto_recover" type="checkbox" data-testid="auto-recover">
              {{ t('accountPerformance.autoRecover') }}
            </label>
            <template v-if="draft.auto_recover">
              <div class="grid gap-3 sm:grid-cols-2">
                <label>{{ t('accountPerformance.cooldown') }}
                  <input v-model.number="draft.cooldown_minutes" class="input mt-1" type="number" min="1" max="1440" data-testid="cooldown-minutes">
                </label>
                <label>{{ t('accountPerformance.recoverySamples') }}
                  <input v-model.number="draft.recovery_samples" class="input mt-1" type="number" min="1" max="100" data-testid="recovery-samples">
                </label>
              </div>
              <p v-if="Number.isInteger(draft.recovery_samples) && draft.recovery_samples >= 1 && draft.recovery_samples <= 100"
                class="text-xs text-gray-600 dark:text-dark-300">{{ t('accountPerformance.recoveryNote', { limit: draft.recovery_samples * 3 }) }}</p>
              <p v-if="hasBounds(draft.ttft.pause) || hasBounds(draft.cache_rate.pause)" class="text-xs text-gray-600 dark:text-dark-300">
                {{ t('accountPerformance.streamingSamples') }}
              </p>
            </template>
          </div>
        </fieldset>

        <details v-if="view === 'status'" class="border-t border-gray-200 pt-3 dark:border-dark-700">
          <summary class="cursor-pointer font-medium">{{ t('accountPerformance.audit') }}</summary>
          <p v-if="auditError" class="mt-2 text-red-600">{{ t('accountPerformance.auditUnavailable') }}</p>
          <p v-else-if="!events.length" class="mt-2 text-gray-500">{{ t('accountPerformance.noEvents') }}</p>
          <ul v-else class="mt-2 max-h-48 space-y-1 overflow-y-auto">
            <li v-for="event in events" :key="event.id" class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
              <time :datetime="event.created_at" class="text-xs tabular-nums text-gray-600 dark:text-dark-300">{{ dateTime(event.created_at) }}</time>
              <span>{{ t('accountPerformance.' + event.action + 'Event') }}<template v-if="event.reason"> · {{ reasonLabel(event.reason) }}</template></span>
            </li>
          </ul>
        </details>
        <p v-if="view === 'rules' && snapshot.paused && dirty" class="text-sm text-amber-700 dark:text-amber-300">{{ t('accountPerformance.saveNote') }}</p>
      </template>
    </div>
    <template #footer>
      <div class="flex w-full flex-wrap items-center justify-end gap-2">
        <p v-if="error" role="alert" class="w-full text-sm text-red-700 dark:text-red-300">{{ error }}</p>
        <button v-if="view === 'status' && snapshot?.paused" type="button" class="btn btn-secondary mr-auto" :disabled="busy || loading"
          data-testid="performance-release" @click="releaseTarget = snapshot">{{ t('accountPerformance.release') }}</button>
        <button v-if="view === 'status' || !snapshot" type="button" class="btn btn-secondary"
          data-testid="performance-refresh" :disabled="busy || loading" @click="reload('refresh')">{{ t('accountPerformance.refresh') }}</button>
        <template v-else>
          <button v-if="dirty || error" type="button" class="btn btn-secondary" :disabled="busy || loading"
            @click="reload()">{{ t(dirty ? 'accountPerformance.discardReload' : 'accountPerformance.refresh') }}</button>
          <button type="button" class="btn btn-primary" :disabled="busy || loading || !dirty || (!snapshot.monitoring_available && draft.enabled)"
            data-testid="performance-save" @click="save">{{ t('accountPerformance.save') }}</button>
        </template>
      </div>
    </template>
  </BaseDialog>
  <ConfirmDialog v-if="releaseTarget" :show="true" :title="t('accountPerformance.release')" :message="t('accountPerformance.releaseNote', { name: accountName })"
    :confirm-text="t('accountPerformance.release')" @cancel="releaseTarget = null" @confirm="release" />
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import {
  emptyPerformanceConfig, metricKeys, performancePhase,
  type AccountPerformance, type PerformanceBounds, type PerformanceEvent, type PerformanceMetric,
} from '@/types/accountPerformance'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{ accountId: number; accountName: string }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { t, te, locale } = useI18n()
const views = ['status', 'rules'] as const
const view = ref<typeof views[number]>('status')
const snapshot = ref<AccountPerformance | null>(null)
const editing = shallowRef<AccountPerformance | null>(null)
const draft = ref(emptyPerformanceConfig())
const events = ref<PerformanceEvent[]>([])
const advanced = ref(false)
const releaseTarget = ref<AccountPerformance | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const auditError = ref(false)
const dirty = computed(() => editing.value !== null && JSON.stringify(draft.value) !== JSON.stringify(editing.value.config))
const phase = computed(() => snapshot.value && performancePhase(snapshot.value))
const phaseLabel = computed(() => phase.value === 'monitoring' ? 'active' : phase.value ?? 'disabled')
const columns = [
  { key: 'pauseMin', group: 'pause', bound: 'min' }, { key: 'pauseMax', group: 'pause', bound: 'max' },
  { key: 'recoveryMin', group: 'recovery', bound: 'min' }, { key: 'recoveryMax', group: 'recovery', bound: 'max' },
] as const
type Column = typeof columns[number]

const isRate = (metric: PerformanceMetric) => metric === 'success_rate' || metric === 'cache_rate'
const unit = (metric: PerformanceMetric) => isRate(metric) ? '%' : 'ms'
const maxValue = (metric: PerformanceMetric) => isRate(metric) ? 100 : 1e9
const hasBounds = (bounds: PerformanceBounds) => bounds.min !== null || bounds.max !== null
const primaryColumn = (metric: PerformanceMetric) => isRate(metric) ? columns[0] : columns[1]
const visibleColumns = (metric: PerformanceMetric) => advanced.value ? columns : [primaryColumn(metric)]
const dateTime = (time: string) => formatDateTime(time, undefined, locale.value)
const trialProgress = computed(() => {
  const state = snapshot.value
  if (!state?.trial_requests) return 'waitingForTraffic'
  const needsSamples = metricKeys.some(metric => hasBounds(state.config[metric].pause) && sampleCount(metric) < state.config.recovery_samples)
  return needsSamples ? 'waitingForSamples' : 'checkingRecovery'
})

function reasonLabel(reason: string) {
  return reason.split(',').filter(Boolean).map(code => {
    const key = 'accountPerformance.' + code
    return te(key) ? t(key) : code
  }).join(locale.value.startsWith('zh') ? '、' : ', ')
}

const displayedMetrics = computed(() => {
  const state = snapshot.value
  return metricKeys.map(metric => {
    const rule = state?.config[metric]
    if (phase.value !== 'trial' || !rule || !hasBounds(rule.pause)) return { metric, target: '' }
    const min = rule.recovery.min ?? rule.pause.min
    const max = rule.recovery.max ?? rule.pause.max
    const { format } = new Intl.NumberFormat(locale.value, {
      style: 'unit', unit: isRate(metric) ? 'percent' : 'millisecond',
      unitDisplay: isRate(metric) ? 'narrow' : 'short', maximumSignificantDigits: 21,
    })
    let target = ''
    if (min !== null && max !== null) target = t('accountPerformance.recoveryRange', { min: format(min), max: format(max) })
    else if (min !== null) target = '≥ ' + format(min)
    else if (max !== null) target = '≤ ' + format(max)
    return { metric, target }
  }).sort((a, b) => Number(Boolean(b.target)) - Number(Boolean(a.target)))
})

function currentValue(metric: PerformanceMetric) {
  const stats = snapshot.value?.metrics
  const value = metric === 'ttft' ? stats?.avg_first_token_ms : metric === 'duration' ? stats?.avg_duration_ms : stats?.[metric]
  if (value == null || !snapshot.value?.monitoring_available) return '—'
  return isRate(metric) ? (value * 100).toFixed(1) + '%' : Math.round(value).toLocaleString(locale.value) + ' ms'
}

function sampleCount(metric: PerformanceMetric) {
  const stats = snapshot.value?.metrics
  if (!stats) return 0
  return { success_rate: stats.request_samples, cache_rate: stats.cache_samples, ttft: stats.first_token_samples, duration: stats.duration_samples }[metric]
}

function setBound(metric: PerformanceMetric, column: Column, event: Event) {
  const input = event.target as HTMLInputElement
  draft.value[metric][column.group][column.bound] = input.value === '' && !input.validity.badInput ? null : input.valueAsNumber
}

function validationError() {
  const config = draft.value
  if (config.enabled && !metricKeys.some(metric => hasBounds(config[metric].pause))) return t('accountPerformance.selectMetric')
  for (const metric of metricKeys) {
    const rule = config[metric]
    const name = t('accountPerformance.' + metric)
    for (const { group, bound } of columns) {
      const value = rule[group][bound]
      if (value !== null && (!Number.isFinite(value) || value < 0 || value > maxValue(metric))) {
        return t('accountPerformance.invalidValue', { metric: name, max: maxValue(metric), unit: unit(metric) })
      }
    }
    const { pause, recovery } = rule
    if (pause.min !== null && pause.max !== null && pause.min > pause.max) return t('accountPerformance.invalidBounds', { metric: name })
    const min = recovery.min ?? pause.min
    const max = recovery.max ?? pause.max
    if ((!hasBounds(pause) && hasBounds(recovery)) || (min !== null && max !== null && min > max) ||
        (min !== null && pause.min !== null && min < pause.min) || (max !== null && pause.max !== null && max > pause.max)) {
      return t('accountPerformance.invalidRecovery', { metric: name })
    }
  }
  for (const [label, value, max] of [
    ['minSamples', config.min_samples, 10000], ['cooldown', config.cooldown_minutes, 1440], ['recoverySamples', config.recovery_samples, 100],
  ] as const) {
    if (!Number.isInteger(value) || value < 1 || value > max) return t('accountPerformance.invalidInteger', { field: t('accountPerformance.' + label), max })
  }
  return ''
}

let requestVersion = 0
let refreshing = false
async function reload(mode: 'replace' | 'refresh' | 'background' = 'replace') {
  const background = mode === 'background'
  const version = ++requestVersion
  refreshing = true
  if (!background) {
    loading.value = true
    error.value = ''
  }
  try {
    const response = await adminAPI.accounts.getPerformance([props.accountId])
    const data = response[props.accountId]
    if (version !== requestVersion) return
    if (data?.config.version !== 2) throw new Error('Missing metric rules')
    const previous = snapshot.value
    if (!previous) view.value = performancePhase(data) === 'disabled' ? 'rules' : 'status'
    if (!previous || mode === 'replace' || (!dirty.value && data.revision !== editing.value?.revision)) {
      editing.value = data
      draft.value = structuredClone(data.config)
      advanced.value = metricKeys.some(metric => columns.some(column =>
        column !== primaryColumn(metric) && data.config[metric][column.group][column.bound] !== null
      ))
    }
    snapshot.value = data
    if (background && previous && previous.revision === data.revision && previous.pause_generation === data.pause_generation &&
        performancePhase(previous) === performancePhase(data) && !auditError.value) return
    try {
      const latest = await adminAPI.accounts.getPerformanceEvents(props.accountId)
      if (version !== requestVersion) return
      events.value = latest
      auditError.value = false
    } catch {
      if (version !== requestVersion) return
      events.value = []
      auditError.value = true
    }
  } catch {
    if (version === requestVersion && !background) error.value = t('accountPerformance.unavailable')
  } finally {
    if (version === requestVersion) {
      loading.value = false
      refreshing = false
    }
  }
}

async function save() {
  if (!editing.value || busy.value || !dirty.value) return
  error.value = validationError()
  if (error.value) return
  busy.value = true
  try {
    await adminAPI.accounts.savePerformance(props.accountId, draft.value, editing.value.revision)
    emit('updated')
    await reload()
    if (!error.value) view.value = 'status'
  } catch {
    error.value = t('accountPerformance.failed')
  } finally {
    busy.value = false
  }
}

async function release() {
  const target = releaseTarget.value
  releaseTarget.value = null
  if (!target || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await adminAPI.accounts.releasePerformance(props.accountId, target.revision, target.pause_generation)
    emit('updated')
    await reload()
  } catch {
    error.value = t('accountPerformance.failed')
  } finally {
    busy.value = false
  }
}

let refreshTimer: ReturnType<typeof setInterval>
onMounted(() => {
  void reload()
  refreshTimer = setInterval(() => {
    if (snapshot.value?.paused && !document.hidden && !busy.value && !refreshing && !releaseTarget.value) void reload('background')
  }, 30_000)
})
onUnmounted(() => {
  clearInterval(refreshTimer)
  requestVersion++
})
</script>
