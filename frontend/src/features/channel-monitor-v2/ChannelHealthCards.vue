<template>
  <section :aria-label="t('channelMonitorV2.cards.title')" class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3 px-1">
      <h2 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('channelMonitorV2.cards.title') }}</h2>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.cards.description') }}</p>
    </div>
    <div v-if="rows.length" class="grid grid-cols-1 items-start gap-5 xl:grid-cols-2 2xl:grid-cols-3">
      <article
        v-for="row in rows"
        :key="`${row.platform}:${row.group_id || ''}:${row.model || ''}`"
        class="card min-w-0 !rounded-3xl !border-0 !p-5 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700 sm:!p-6"
        data-testid="channel-health-card"
      >
        <header class="flex items-start gap-3">
          <span class="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400">
            <ProviderIcon :provider="row.platform" :size="30" />
          </span>
          <div class="min-w-0 flex-1">
            <h3 class="break-words text-lg font-bold leading-snug text-gray-900 dark:text-white">{{ cardTitle(row) }}</h3>
            <div class="mt-2 flex flex-wrap gap-1.5 text-xs">
              <span class="rounded-md bg-emerald-50 px-2 py-1 font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">{{ platformDisplayName(row.platform) }}</span>
              <span class="rounded-md bg-gray-100 px-2 py-1 text-gray-500 dark:bg-dark-700 dark:text-gray-400">{{ t('channelMonitorV2.cards.passive') }}</span>
            </div>
          </div>
          <span class="shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold" :class="badgeClass[cardHealth(row.health, healthMode)]">
            {{ healthLabel(cardHealth(row.health, healthMode)) }}
          </span>
        </header>
        <dl class="my-6 grid grid-cols-3 gap-2 sm:gap-3">
          <div class="min-w-0 rounded-2xl bg-gray-50 p-2 ring-1 ring-gray-100 dark:bg-dark-900/40 dark:ring-dark-700 sm:p-3">
            <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.metrics.cacheRate') }}</dt>
            <dd class="mt-2 font-mono text-base font-bold tabular-nums text-gray-800 dark:text-gray-100 sm:text-2xl">{{ hasMonitorTraffic(row) ? formatMonitorPercent(row.metrics.cache_rate) : '-' }}</dd>
          </div>
          <div class="min-w-0 rounded-2xl bg-gray-50 p-2 ring-1 ring-gray-100 dark:bg-dark-900/40 dark:ring-dark-700 sm:p-3">
            <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.cards.availability') }}</dt>
            <dd class="mt-2 font-mono text-base font-bold tabular-nums sm:text-2xl" :class="textClass[row.health.error_rate]">{{ formatMonitorSuccessRateFromError(row.metrics.error_rate, row.health.error_rate) }}</dd>
          </div>
          <div class="min-w-0 rounded-2xl bg-gray-50 p-2 ring-1 ring-gray-100 dark:bg-dark-900/40 dark:ring-dark-700 sm:p-3">
            <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.metrics.ttft') }}</dt>
            <dd class="mt-2 font-mono text-base font-bold tabular-nums text-gray-800 dark:text-gray-100 sm:text-2xl" :title="latencyDetail(row)">{{ formatMonitorMs(row.metrics.ttft.p50_ms) }}</dd>
          </div>
        </dl>
        <div class="border-t border-gray-100 pt-5 dark:border-dark-700" data-testid="recent-requests">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.cards.recentRequests') }}</span>
            <span>{{ refreshing ? t('channelMonitorV2.updating') : t('channelMonitorV2.cards.refreshIn', { seconds: refreshSeconds }) }}</span>
          </div>
          <MonitorStatusStrip :slots="requestSlots(row)" :label="t('channelMonitorV2.cards.recentRequests')" />
          <div class="mt-2 flex justify-between text-[10px] uppercase tracking-widest text-gray-400">
            <span>{{ t('channelMonitorV2.cards.past') }}</span><span>{{ t('channelMonitorV2.cards.now') }}</span>
          </div>
          <p class="mt-2 text-[10px] leading-relaxed text-gray-400">{{ t('channelMonitorV2.cards.requestLegend') }}</p>
        </div>
        <div class="mt-5 border-t border-gray-100 pt-5 dark:border-dark-700">
          <h4 class="text-sm font-semibold text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.cards.models') }}</h4>
          <p class="mt-1 text-xs text-gray-400">{{ t('channelMonitorV2.cards.modelsHint') }}</p>
          <div class="mt-4 space-y-4">
            <div v-for="model in modelsForCard(row, modelRows)" :key="model.model" data-testid="model-health">
              <div class="mb-1.5 flex items-start justify-between gap-3 text-xs">
                <span class="min-w-0 break-all font-medium text-gray-700 dark:text-gray-200">{{ modelName(model.model) }}</span>
                <span class="shrink-0" :class="textClass[cardHealth(model.health, healthMode)]">{{ healthLabel(cardHealth(model.health, healthMode)) }}</span>
              </div>
              <div class="mb-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-gray-400">
                <span>{{ t('channelMonitorV2.metrics.successRateValue', { value: formatMonitorSuccessRateFromError(model.metrics.error_rate, model.health.error_rate) }) }}</span>
                <span>{{ t('channelMonitorV2.metrics.ttftValue', { value: formatMonitorMs(model.metrics.ttft.p50_ms) }) }}</span>
                <span>{{ t('channelMonitorV2.metrics.cacheRateValue', { value: formatMonitorPercent(model.metrics.cache_rate) }) }}</span>
              </div>
              <MonitorStatusStrip :slots="timelineSlots(model)" :label="modelName(model.model)" compact />
            </div>
            <p v-if="!modelsForCard(row, modelRows).length" class="py-2 text-xs text-gray-400">{{ t('channelMonitorV2.cards.noModels') }}</p>
          </div>
        </div>
      </article>
    </div>
    <div v-else class="card !rounded-3xl p-8">
      <EmptyState :title="t('channelMonitorV2.empty.title')" :description="t('channelMonitorV2.empty.description')" />
    </div>
    <div class="flex flex-wrap gap-x-4 gap-y-2 px-1 text-xs text-gray-500 dark:text-gray-400">
      <span v-for="state in states" :key="state" class="inline-flex items-center gap-1.5">
        <i class="h-2 w-2 rounded-full" :class="dotClass[state]" />{{ t(`channelMonitorV2.cards.legend.${state}`) }}
      </span>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { HealthState, MonitorCoverage, MonitorMatrixRow } from '@/api/channelMonitorV2'
import EmptyState from '@/components/common/EmptyState.vue'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import { platformDisplayName } from '@/constants/platformCatalog'
import MonitorStatusStrip from './MonitorStatusStrip.vue'
import { cardHealth, hasMonitorTraffic, modelsForCard, recentRequestsForCard, requestHealth, timelineForCard } from './monitorCards'
import { formatLatencyPrivacy, formatMonitorMs, formatMonitorPercent, formatMonitorSuccessRateFromError, type HealthDisplayMode } from './monitorFormat'

const props = defineProps<{ rows: MonitorMatrixRow[]; modelRows: MonitorMatrixRow[]; coverage: MonitorCoverage; healthMode: HealthDisplayMode; nextRefreshAt: number; refreshing: boolean }>()
const { t, locale } = useI18n()
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { timer = setInterval(() => { now.value = Date.now() }, 1000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
const refreshSeconds = computed(() => Math.max(0, Math.ceil((props.nextRefreshAt - now.value) / 1000)))
const states: HealthState[] = ['healthy', 'warning', 'critical', 'unknown']
const textClass = { healthy: 'text-emerald-600 dark:text-emerald-400', warning: 'text-amber-600 dark:text-amber-400', critical: 'text-rose-500 dark:text-rose-400', unknown: 'text-gray-400' }
const badgeClass = { healthy: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300', warning: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300', critical: 'bg-rose-50 text-rose-700 dark:bg-rose-900/30 dark:text-rose-300', unknown: 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400' }
const dotClass = { healthy: 'bg-emerald-400', warning: 'bg-amber-400', critical: 'bg-rose-400', unknown: 'bg-gray-300 dark:bg-dark-600' }
function healthLabel(state: HealthState) { return t(`channelMonitorV2.cards.health.${state}`) }
function modelName(model?: string) { return model === '__other__' ? t('channelMonitorV2.otherModels') : model || '-' }
function cardTitle(row: MonitorMatrixRow) { return [row.group_name || (row.group_id ? `#${row.group_id}` : ''), row.model ? modelName(row.model) : ''].filter(Boolean).join(' / ') || platformDisplayName(row.platform) }
function latencyDetail(row: MonitorMatrixRow) { const m = row.metrics.ttft; return formatLatencyPrivacy(m.p50_ms, m.p90_ms, m.avg_ms, m.p95_ms) }
function timeLabel(time: number | string) { return new Intl.DateTimeFormat(locale.value || undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(time)) }
function timelineSlots(row: MonitorMatrixRow) {
  return timelineForCard(row, props.coverage).map(slot => {
    const state = slot.bucket ? cardHealth(slot.bucket.health, props.healthMode) : 'unknown'
    const lines = [`${timeLabel(slot.start)} – ${timeLabel(slot.end)}`, healthLabel(state)]
    if (slot.bucket) {
      lines.push(t('channelMonitorV2.metrics.successRateValue', { value: formatMonitorSuccessRateFromError(slot.bucket.metrics.error_rate, slot.bucket.health.error_rate) }), t('channelMonitorV2.metrics.ttftValue', { value: formatMonitorMs(slot.bucket.metrics.ttft.p50_ms) }), t('channelMonitorV2.metrics.cacheRateValue', { value: formatMonitorPercent(slot.bucket.metrics.cache_rate) }))
    }
    return { state, label: lines.join(' · ') }
  })
}
function requestSlots(row: MonitorMatrixRow) {
  return recentRequestsForCard(row.recent_requests).map(request => ({
    state: requestHealth(request),
    label: request ? [timeLabel(request.occurred_at), modelName(request.model), t(`channelMonitorV2.cards.requests.${request.status}`), request.ttft_ms != null ? t('channelMonitorV2.metrics.ttftValue', { value: formatMonitorMs(request.ttft_ms) }) : '', request.error_category ? t(`channelMonitorV2.errorCategories.${request.error_category}`) : ''].filter(Boolean).join(' · ') : t('channelMonitorV2.cards.noRequest'),
  }))
}
</script>
