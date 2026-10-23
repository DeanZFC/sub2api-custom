<template>
  <section :aria-label="t('channelMonitorV2.cards.title')" class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3 px-1">
      <h2 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('channelMonitorV2.cards.title') }}</h2>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.cards.description') }}</p>
    </div>
    <div v-if="sections.length" class="space-y-5">
      <section v-for="section in sections" :key="section.key" class="space-y-3" data-testid="channel-section">
        <h3 class="flex min-w-0 items-center gap-2 px-1 text-base font-bold text-gray-900 dark:text-white" data-testid="channel-section-title">
          <ProviderIcon :provider="section.platform" :size="24" />
          <span class="truncate" :title="section.title">{{ section.title }}</span>
          <span class="shrink-0 rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400">{{ section.rows.length }}</span>
        </h3>
        <div class="grid auto-rows-fr grid-cols-1 items-stretch gap-4 lg:grid-cols-2 2xl:grid-cols-3">
          <article
            v-for="row in section.rows"
            :key="JSON.stringify([row.platform, row.group_id, row.model])"
            class="card flex min-w-0 flex-col !rounded-2xl !border-0 !p-4 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700"
            data-testid="channel-health-card"
          >
            <header class="flex h-12 items-start gap-2.5">
              <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400">
                <ProviderIcon :provider="row.platform" :size="25" />
              </span>
              <div class="min-w-0 flex-1">
                <h3 class="truncate text-base font-bold leading-5 text-gray-900 dark:text-white" :title="cardTitle(row)">{{ cardTitle(row) }}</h3>
                <div class="mt-1 flex gap-1.5 overflow-hidden whitespace-nowrap text-[11px]">
                  <span class="rounded-md bg-emerald-50 px-1.5 py-0.5 font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">{{ platformDisplayName(row.platform) }}</span>
                  <span class="rounded-md bg-gray-100 px-1.5 py-0.5 text-gray-500 dark:bg-dark-700 dark:text-gray-400">{{ t('channelMonitorV2.cards.passive') }}</span>
                </div>
              </div>
              <span class="shrink-0 rounded-full px-2 py-1 text-[11px] font-semibold" :class="badgeClass[cardHealth(row.health, healthMode)]">
                {{ healthLabel(cardHealth(row.health, healthMode)) }}
              </span>
            </header>
            <dl class="my-3 grid grid-cols-3 gap-2">
              <div class="min-w-0 rounded-xl bg-gray-50 px-2 py-2 ring-1 ring-gray-100 dark:bg-dark-900/40 dark:ring-dark-700">
                <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.cacheRate')">{{ t('channelMonitorV2.metrics.cacheRate') }}</dt>
                <dd class="mt-1 font-mono text-base font-bold tabular-nums text-gray-800 dark:text-gray-100 sm:text-xl">{{ formatMonitorCacheRate(row.metrics) }}</dd>
              </div>
              <div class="min-w-0 rounded-xl bg-gray-50 px-2 py-2 ring-1 ring-gray-100 dark:bg-dark-900/40 dark:ring-dark-700">
                <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.cards.availability')">{{ t('channelMonitorV2.cards.availability') }}</dt>
                <dd class="mt-1 font-mono text-base font-bold tabular-nums sm:text-xl" :class="textClass[row.health.error_rate]">{{ formatMonitorSuccessRateFromError(row.metrics.error_rate, row.health.error_rate) }}</dd>
              </div>
              <div class="min-w-0 rounded-xl bg-gray-50 px-2 py-2 ring-1 ring-gray-100 dark:bg-dark-900/40 dark:ring-dark-700">
                <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.ttft')">{{ t('channelMonitorV2.metrics.ttft') }}</dt>
                <dd class="mt-1 font-mono text-base font-bold tabular-nums text-gray-800 dark:text-gray-100 sm:text-xl" :title="latencyDetail(row)">{{ formatMonitorMs(row.metrics.ttft.p50_ms) }}</dd>
              </div>
            </dl>
            <div class="mb-3 border-t border-gray-100 pt-3 dark:border-dark-700" data-testid="health-history">
              <div class="mb-2 flex flex-wrap items-center justify-between gap-2 text-[11px] text-gray-500 dark:text-gray-400">
                <span class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.cards.healthHistory') }}</span>
                <span>{{ t(`channelMonitorV2.healthMode.${healthMode}`) }} · {{ historyIntervalLabel }}</span>
              </div>
              <MonitorStatusStrip :slots="historySlots(row)" :label="t('channelMonitorV2.cards.healthHistory')" compact />
              <div class="mt-1 flex justify-between text-[10px] uppercase tracking-widest text-gray-400">
                <span>{{ t('channelMonitorV2.cards.past') }}</span><span>{{ t('channelMonitorV2.cards.now') }}</span>
              </div>
              <p class="mt-1 text-[10px] leading-4 text-gray-400">{{ t('channelMonitorV2.cards.historyLegend') }}</p>
            </div>
            <div class="border-t border-gray-100 pt-3 dark:border-dark-700" data-testid="recent-requests">
              <div class="mb-2 flex items-center justify-between gap-2 text-[11px] text-gray-500 dark:text-gray-400">
                <span class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.cards.recentRequests') }}</span>
                <span class="shrink-0" data-testid="latest-request-time">{{ t('channelMonitorV2.cards.latestRequest', { time: latestRequestTime(row) }) }}</span>
              </div>
              <MonitorStatusStrip :slots="requestSlots(row)" :label="t('channelMonitorV2.cards.recentRequests')" compact />
              <div class="mt-1 flex justify-between text-[10px] uppercase tracking-widest text-gray-400">
                <span>{{ t('channelMonitorV2.cards.past') }}</span><span>{{ t('channelMonitorV2.cards.now') }}</span>
              </div>
              <p class="mt-1 truncate text-[10px] leading-4 text-gray-400" :title="t('channelMonitorV2.cards.requestLegend')">{{ t('channelMonitorV2.cards.requestLegend') }}</p>
            </div>
            <GroupTestFooter
              v-if="row.group_id"
              :results="testsByGroup.get(row.group_id) || emptyGroupTests"
              :state="testResultsState"
              :group-name="cardTitle(row)"
              @open="previewGroup = { id: row.group_id, name: cardTitle(row) }"
            />
          </article>
        </div>
      </section>
    </div>
    <div v-else class="card !rounded-3xl p-8">
      <EmptyState :title="t('channelMonitorV2.empty.title')" :description="t('channelMonitorV2.empty.description')" />
    </div>
    <div class="flex flex-wrap gap-x-4 gap-y-2 px-1 text-xs text-gray-500 dark:text-gray-400">
      <span v-for="state in states" :key="state" class="inline-flex items-center gap-1.5">
        <i class="h-2 w-2 rounded-full" :class="dotClass[state]" />{{ t(`channelMonitorV2.cards.legend.${state}`) }}
      </span>
    </div>
    <GroupPelicanGallery
      :show="previewGroup != null"
      :group-name="previewGroup?.name || ''"
      :results="previewGroup ? testsByGroup.get(previewGroup.id)?.pelican || [] : []"
      @close="previewGroup = null"
    />
  </section>
</template>

<script setup lang="ts">
import { formatMonitorCacheRate } from '@/features/channel-monitor-v2/monitorFormat'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { HealthState, MonitorCoverage, MonitorMatrixGroupBy, MonitorMatrixRow } from '@/api/channelMonitorV2'
import EmptyState from '@/components/common/EmptyState.vue'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import { platformDisplayName } from '@/constants/platformCatalog'
import MonitorStatusStrip from './MonitorStatusStrip.vue'
import GroupTestFooter from './GroupTestFooter.vue'
import GroupPelicanGallery from './GroupPelicanGallery.vue'
import { emptyGroupTests, indexGroupTestResults, type TestResultsState } from './monitorTestResults'
import type { TestResult } from '@/types'
import { cardHealth, hasMonitorTraffic, historyBucketsForCard, recentRequestsForCard, requestHealth } from './monitorCards'
import { compareMonitorModels } from './monitorModels'
import { formatLatencyPrivacy, formatMonitorMs, formatMonitorPercent, formatMonitorSuccessRateFromError, healthModeScore, healthScoreClass, type HealthDisplayMode } from './monitorFormat'

const props = withDefaults(defineProps<{
  rows: MonitorMatrixRow[]; coverage: MonitorCoverage; groupBy: MonitorMatrixGroupBy; healthMode: HealthDisplayMode
  testResults?: TestResult[]; testResultsState?: TestResultsState
}>(), { testResults: () => [], testResultsState: 'ready' })
const testsByGroup = computed(() => indexGroupTestResults(props.testResults))
const previewGroup = ref<{ id: number; name: string } | null>(null)
const { t, locale } = useI18n()
const historyIntervalLabel = computed(() => {
  const minutes = props.coverage.bucket_seconds / 60
  if (minutes < 60) return t('channelMonitorV2.bucket.minutes', { count: minutes })
  if (minutes < 1440) return t('channelMonitorV2.bucket.hours', { count: minutes / 60 })
  return t('channelMonitorV2.bucket.days', { count: minutes / 1440 })
})
const sections = computed(() => {
  const byModel = props.groupBy === 'platform_model' || props.groupBy === 'platform_group_model'
  const groups = new Map<string, { key: string; platform: string; model?: string; title: string; rows: MonitorMatrixRow[] }>()
  for (const row of props.rows) {
    if (byModel && (!row.model || !hasMonitorTraffic(row))) continue
    const key = JSON.stringify([row.platform, byModel ? row.model : null])
    let section = groups.get(key)
    if (!section) {
      section = { key, platform: row.platform, model: byModel ? row.model : undefined,
        title: byModel ? row.model! : platformDisplayName(row.platform), rows: [] }
      groups.set(key, section)
    }
    section.rows.push(row)
  }
  return [...groups.values()].sort((a, b) => a.platform.localeCompare(b.platform)
    || compareMonitorModels(a.model || '', b.model || ''))
})
const states: HealthState[] = ['healthy', 'warning', 'critical', 'unknown']
const textClass = { healthy: 'text-emerald-600 dark:text-emerald-400', warning: 'text-amber-600 dark:text-amber-400', critical: 'text-rose-500 dark:text-rose-400', unknown: 'text-gray-400' }
const badgeClass = { healthy: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300', warning: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300', critical: 'bg-rose-50 text-rose-700 dark:bg-rose-900/30 dark:text-rose-300', unknown: 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400' }
const dotClass = { healthy: 'bg-emerald-400', warning: 'bg-amber-400', critical: 'bg-rose-400', unknown: 'bg-gray-300 dark:bg-dark-600' }
function healthLabel(state: HealthState) { return t(`channelMonitorV2.cards.health.${state}`) }
function modelName(model?: string) { return model || '-' }
function cardTitle(row: MonitorMatrixRow) { return row.group_name || (row.group_id ? `#${row.group_id}` : platformDisplayName(row.platform)) }
function latencyDetail(row: MonitorMatrixRow) { const m = row.metrics.ttft; return formatLatencyPrivacy(m.p50_ms, m.p90_ms, m.avg_ms, m.p95_ms) }
function timeLabel(time: number | string) { return new Intl.DateTimeFormat(locale.value || undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(time)) }
function latestRequestTime(row: MonitorMatrixRow) {
  const latest = recentRequestsForCard(row.recent_requests)[19]
  return latest ? timeLabel(latest.occurred_at) : '-'
}
function historySlots(row: MonitorMatrixRow) {
  return historyBucketsForCard(row.buckets, props.coverage).map(slot => {
    if (!slot) return { state: 'unknown' as const, label: t('channelMonitorV2.cards.noHistory') }
    const range = `${timeLabel(slot.start)} – ${timeLabel(slot.end)}`
    if (!slot.bucket) return { state: 'unknown' as const, label: `${range}\n${t('channelMonitorV2.cards.noIntervalData')}` }

    const { metrics, health } = slot.bucket
    const score = healthModeScore(health, props.healthMode)
    // A returned bucket is evidence of traffic, even when its count is redacted.
    const colorClass = healthScoreClass(health, props.healthMode, 1)
    const state = colorClass === 'health-unknown' ? 'unknown' : cardHealth(health, props.healthMode)
    return {
      state, colorClass,
      label: [
        range,
        `${t(`channelMonitorV2.healthMode.${props.healthMode}`)} · ${healthLabel(state)}`,
        t('channelMonitorV2.matrix.scoreLine', { score: score == null ? '-' : Math.round(score) }),
        t('channelMonitorV2.metrics.successRateValue', { value: formatMonitorSuccessRateFromError(metrics.error_rate, health.error_rate) }),
        t('channelMonitorV2.metrics.ttftValue', { value: formatLatencyPrivacy(metrics.ttft.p50_ms, metrics.ttft.p90_ms, metrics.ttft.avg_ms, metrics.ttft.p95_ms) }),
        t('channelMonitorV2.metrics.cacheRateValue', { value: formatMonitorCacheRate(metrics) }),
        t('channelMonitorV2.metrics.durationValue', { value: formatLatencyPrivacy(metrics.duration.p50_ms, metrics.duration.p90_ms, metrics.duration.avg_ms, metrics.duration.p95_ms) }),
      ].join('\n'),
    }
  })
}
function requestSlots(row: MonitorMatrixRow) {
  return recentRequestsForCard(row.recent_requests).map(request => ({
    state: requestHealth(request),
    label: request ? [
      `${modelName(request.model)} · ${t(`channelMonitorV2.cards.requests.${request.status}`)}`,
      t('channelMonitorV2.cards.requestTime', { value: timeLabel(request.occurred_at) }),
      t('channelMonitorV2.cards.requestTTFT', { value: formatMonitorMs(request.ttft_ms) }),
      t('channelMonitorV2.metrics.cacheRateValue', { value: request.cache_rate == null ? '-' : formatMonitorPercent(request.cache_rate) }),
      t('channelMonitorV2.cards.requestDuration', { value: formatMonitorMs(request.duration_ms) }),
      request.error_category ? t(`channelMonitorV2.errorCategories.${request.error_category}`) : '',
    ].filter(Boolean).join('\n') : t('channelMonitorV2.cards.noRequest'),
  }))
}
</script>
