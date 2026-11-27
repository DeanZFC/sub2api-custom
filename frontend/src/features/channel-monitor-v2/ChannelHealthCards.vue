<template>
  <section :aria-label="t('channelMonitorV2.cards.title')" class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3 px-1">
      <h2 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('channelMonitorV2.cards.title') }}</h2>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.cards.description') }}</p>
    </div>
    <div v-if="sections.length" class="space-y-5">
      <section v-for="section in sections" :key="section.key" class="space-y-3" data-testid="channel-section">
        <h3 class="flex min-w-0 items-center gap-2.5 px-1 text-lg font-bold text-slate-900 dark:text-white" data-testid="channel-section-title">
          <span class="provider-tile"><ProviderIcon :provider="section.platform" :size="21" /></span>
          <span class="truncate" :title="section.title">{{ section.title }}</span>
          <span class="shrink-0 rounded-full border border-slate-200 bg-white/80 px-2.5 py-0.5 text-xs text-slate-500 dark:border-dark-700 dark:bg-dark-800 dark:text-slate-300">{{ section.rows.length }}</span>
        </h3>
        <div class="grid grid-cols-1 items-stretch gap-4 lg:grid-cols-2 2xl:grid-cols-3">
          <article
            v-for="row in section.rows"
            :key="JSON.stringify([row.platform, row.group_id, row.model])"
            class="flex min-w-0 flex-col rounded-3xl border border-white/90 bg-white/90 p-4 shadow-glass dark:border-dark-700 dark:bg-dark-800/95 sm:p-5"
            data-testid="channel-health-card"
          >
            <header class="flex items-start gap-2.5">
              <span class="provider-tile">
                <ProviderIcon :provider="row.platform" :size="21" />
              </span>
              <div class="min-w-0 flex-1">
                <h3 class="truncate text-base font-bold leading-5 text-slate-900 dark:text-white" :title="cardTitle(row)">{{ cardTitle(row) }}</h3>
                <div class="mt-1.5 flex flex-wrap gap-1.5 text-[11px]">
                  <span class="rounded-md bg-emerald-100 px-1.5 py-0.5 font-medium text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300">{{ platformDisplayName(row.platform) }}</span>
                  <span
                    v-if="row.group_id && row.group_rate_multiplier != null"
                    class="rounded-md bg-teal-50 px-1.5 py-0.5 font-medium tabular-nums text-teal-700 dark:bg-teal-900/30 dark:text-teal-300"
                    data-testid="group-rate-multiplier"
                  >{{ t('channelMonitorV2.cards.groupRateMultiplier', { rate: row.group_rate_multiplier }) }}</span>
                  <span class="rounded-md bg-gray-100 px-1.5 py-0.5 text-gray-500 dark:bg-dark-700 dark:text-gray-400">{{ t('channelMonitorV2.cards.passive') }}</span>
                </div>
              </div>
              <span class="shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold" :class="badgeClass[cardHealth(row.health, healthMode)]">
                {{ healthLabel(cardHealth(row.health, healthMode)) }}
              </span>
            </header>
            <ChannelCardMetrics
              :row="row"
              :models="row.models"
              :show-models="showModelBreakdown"
              :selected-model="row.detail.model"
              @select-model="selectedModels[cardKey(row)] = $event"
            />
            <IntelligenceRounds
              v-if="showIntelligenceRounds && row.group_id"
              :group="intelligenceByGroup.get(row.group_id)"
              :directory-message="intelligenceDirectoryMessage"
              :model="row.detail.model || scopeModel"
              :refresh-key="refreshKey"
            />
            <div class="border-t border-gray-100 pt-3 dark:border-dark-700" data-testid="health-history">
              <div v-if="showModelBreakdown && row.detail.model" class="mb-2 flex min-w-0 items-center justify-between gap-2 text-[11px] text-gray-500 dark:text-gray-400" data-testid="card-detail-scope">
                <span class="truncate" :title="row.detail.model">{{ t('channelMonitorV2.cards.currentScope', { model: row.detail.model }) }}</span>
                <button type="button" class="shrink-0 rounded text-emerald-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-emerald-500 dark:text-emerald-400" @click="selectedModels[cardKey(row)] = undefined">{{ t('channelMonitorV2.cards.backToOverall') }}</button>
              </div>
              <div class="mb-2 flex flex-wrap items-center justify-between gap-2 text-[11px] text-gray-500 dark:text-gray-400">
                <span class="font-medium text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.cards.healthHistory') }}</span>
                <span>{{ t(`channelMonitorV2.healthMode.${healthMode}`) }} · {{ historyIntervalLabel }}</span>
              </div>
              <MonitorStatusStrip :slots="historySlots(row.detail)" :label="t('channelMonitorV2.cards.healthHistory')" compact />
              <div class="mt-1 flex justify-between text-[10px] uppercase tracking-widest text-gray-400">
                <span>{{ t('channelMonitorV2.cards.past') }}</span><span>{{ t('channelMonitorV2.cards.now') }}</span>
              </div>
              <p class="mt-1 text-[10px] leading-4 text-gray-400">{{ t('channelMonitorV2.cards.historyLegend') }}</p>
            </div>

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

  </section>
</template>

<script setup lang="ts">
import { formatMonitorCacheRate, formatMonitorOutputTps } from '@/features/channel-monitor-v2/monitorFormat'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { HealthState, MonitorCoverage, MonitorMatrixGroupBy, MonitorMatrixRow } from '@/api/channelMonitorV2'
import EmptyState from '@/components/common/EmptyState.vue'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import { platformDisplayName } from '@/constants/platformCatalog'
import MonitorStatusStrip from './MonitorStatusStrip.vue'
import ChannelCardMetrics from './ChannelCardMetrics.vue'
import IntelligenceRounds from './IntelligenceRounds.vue'
import type { IntelligenceGroup } from '@/api/intelligence'
import { cardHealth, hasMonitorTraffic, historyBucketsForCard } from './monitorCards'
import { compareMonitorModels } from './monitorModels'
import { formatLatencyPrivacy, formatMonitorSuccessRateFromError, healthModeScore, healthScoreClass, type HealthDisplayMode } from './monitorFormat'

const props = withDefaults(defineProps<{
  rows: MonitorMatrixRow[]; coverage: MonitorCoverage; groupBy: MonitorMatrixGroupBy; healthMode: HealthDisplayMode
  modelRows?: MonitorMatrixRow[]; modelCoverage?: MonitorCoverage
  showIntelligenceRounds?: boolean; intelligenceGroups?: IntelligenceGroup[]; intelligenceDirectoryMessage?: string; refreshKey?: number; scopeModel?: string
}>(), { modelRows: () => [], intelligenceGroups: () => [] })
const intelligenceByGroup = computed(() => new Map(props.intelligenceGroups.filter(g => g.models.length).map(g => [g.id,g])))
const { t, locale } = useI18n()
const historyIntervalLabel = computed(() => {
  const minutes = props.coverage.bucket_seconds / 60
  if (minutes < 60) return t('channelMonitorV2.bucket.minutes', { count: minutes })
  if (minutes < 1440) return t('channelMonitorV2.bucket.hours', { count: minutes / 60 })
  return t('channelMonitorV2.bucket.days', { count: minutes / 1440 })
})
const showModelBreakdown = computed(() => props.groupBy === 'platform' || props.groupBy === 'platform_group')
const selectedModels = ref<Record<string, string | undefined>>({})
function cardKey(row: MonitorMatrixRow) { return JSON.stringify([row.platform, row.group_id ?? null, row.model ?? null]) }
const modelsByCard = computed(() => {
  const index = new Map<string, MonitorMatrixRow[]>()
  for (const row of props.modelRows) {
    if (!row.model || !hasMonitorTraffic(row)) continue
    const key = cardKey({ ...row, model: undefined })
    const models = index.get(key) || []
    models.push(row)
    index.set(key, models)
  }
  for (const models of index.values()) models.sort((a, b) => compareMonitorModels(a.model!, b.model!))
  return index
})
watch([() => props.groupBy, modelsByCard], () => {
  for (const [key, model] of Object.entries(selectedModels.value)) {
    if (!showModelBreakdown.value || !modelsByCard.value.get(key)?.some(row => row.model === model)) delete selectedModels.value[key]
  }
})
type HealthCardRow = MonitorMatrixRow & { models: MonitorMatrixRow[]; detail: MonitorMatrixRow }
const sections = computed(() => {
  const byModel = props.groupBy === 'platform_model' || props.groupBy === 'platform_group_model'
  const groups = new Map<string, { key: string; platform: string; model?: string; title: string; rows: HealthCardRow[] }>()
  for (const row of props.rows) {
    if (byModel && (!row.model || !hasMonitorTraffic(row))) continue
    const key = JSON.stringify([row.platform, byModel ? row.model : null])
    let section = groups.get(key)
    if (!section) {
      section = { key, platform: row.platform, model: byModel ? row.model : undefined,
        title: byModel ? row.model! : platformDisplayName(row.platform), rows: [] }
      groups.set(key, section)
    }
    const models = showModelBreakdown.value ? modelsByCard.value.get(cardKey(row)) || [] : []
    const detail = models.find(model => model.model === selectedModels.value[cardKey(row)]) || row
    section.rows.push({ ...row, models, detail })
  }
  return [...groups.values()].sort((a, b) => a.platform.localeCompare(b.platform)
    || compareMonitorModels(a.model || '', b.model || ''))
})
const states: HealthState[] = ['healthy', 'warning', 'critical', 'unknown']
const badgeClass = {
  healthy: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300',
  warning: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
  critical: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300',
  unknown: 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400',
}
const dotClass = {
  healthy: 'bg-emerald-500', warning: 'bg-amber-400',
  critical: 'bg-red-500', unknown: 'bg-gray-300 dark:bg-dark-600',
}
function healthLabel(state: HealthState) { return t(`channelMonitorV2.cards.health.${state}`) }
function cardTitle(row: MonitorMatrixRow) { return row.group_name || (row.group_id ? `#${row.group_id}` : platformDisplayName(row.platform)) }
function timeLabel(time: number | string) { return new Intl.DateTimeFormat(locale.value || undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(time)) }
function historySlots(row: MonitorMatrixRow) {
  return historyBucketsForCard(row.buckets, showModelBreakdown.value && row.model ? props.modelCoverage || props.coverage : props.coverage).map(slot => {
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
        t('channelMonitorV2.metrics.outputTpsValue', { value: formatMonitorOutputTps(metrics.output_tps) }),
        t('channelMonitorV2.metrics.cacheRateValue', { value: formatMonitorCacheRate(metrics) }),
        t('channelMonitorV2.metrics.durationValue', { value: formatLatencyPrivacy(metrics.duration.p50_ms, metrics.duration.p90_ms, metrics.duration.avg_ms, metrics.duration.p95_ms) }),
      ].join('\n'),
    }
  })
}
</script>

<style scoped>
.provider-tile {
  @apply inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-emerald-100 bg-gradient-to-br from-emerald-50 to-emerald-100 text-slate-900 dark:border-emerald-800/60 dark:from-emerald-950 dark:to-emerald-900 dark:text-emerald-200;
}
</style>
