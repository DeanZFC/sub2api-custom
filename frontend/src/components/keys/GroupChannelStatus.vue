<template>
  <span
    data-group-channel-status
    class="group-channel-status"
    role="img"
    :aria-label="description"
    :aria-busy="loading && !unavailable ? 'true' : undefined"
    :title="description"
  >
    <span class="group-channel-status-track" :class="{ 'is-loading': loading && !unavailable }">
      <span
        v-for="(slot, index) in slots"
        :key="slot.start ?? index"
        data-status-cell
        class="group-channel-status-cell"
        :class="slot.color"
        :data-bucket-start="slot.start == null ? undefined : new Date(slot.start).toISOString()"
        :title="slotTitle(slot)"
        aria-hidden="true"
      />
    </span>
    <span v-if="stateLabel" class="group-channel-status-label" aria-hidden="true">{{ stateLabel }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorCoverage, MonitorMatrixBucket, MonitorMatrixRow } from '@/api/channelMonitorV2'
import { healthScoreClass } from '@/features/channel-monitor-v2/monitorFormat'

const props = withDefaults(defineProps<{
  rows: MonitorMatrixRow[]
  coverage: MonitorCoverage | null
  loading?: boolean
  unavailable?: boolean
}>(), { loading: false, unavailable: false })

const { t, locale } = useI18n()
const slotCount = 10
type StatusSlot = { start: number | null; color: string; bucket?: MonitorMatrixBucket }

// Match the matrix's selected-range alignment, retaining empty intervals.
// Generate only its last ten buckets rather than expanding the whole range.
const bucketStarts = computed<Array<number | null>>(() => {
  const coverage = props.coverage
  const empty = () => Array<number | null>(slotCount).fill(null)
  if (!coverage) return empty()
  const step = Math.max(60, coverage.bucket_seconds) * 1000
  const start = Date.parse(coverage.requested_start)
  const requestedEnd = Date.parse(coverage.requested_end ?? '')
  const end = Number.isFinite(requestedEnd) && requestedEnd > start
    ? requestedEnd
    : Date.parse(coverage.data_through)
  if (![step, start, end].every(Number.isFinite) || end <= start) return empty()
  const first = Math.floor(start / step) * step
  const last = (Math.ceil(end / step) - 1) * step
  return Array.from({ length: slotCount }, (_, index) => {
    const cursor = last - (slotCount - 1 - index) * step
    return cursor >= first ? cursor : null
  })
})

function knownRank(bucket: MonitorMatrixBucket, color: string): number | null {
  if (color === 'health-unknown') return null
  const score = bucket.health.score
  if (score != null && Number.isFinite(score)) return Math.max(0, Math.min(100, score))
  // Use each coarse state's lower bound when older payloads have no score.
  switch (color) {
    case 'health-critical': return 0
    case 'health-warning': return 50
    case 'health-healthy': return 80
    default: return null
  }
}

const slots = computed<StatusSlot[]>(() => {
  const result = bucketStarts.value.map((start): StatusSlot => ({ start, color: 'health-unknown' }))
  if (props.loading || props.unavailable) return result
  const indexes = new Map(result.flatMap((slot, index) => slot.start == null ? [] : [[slot.start, index] as const]))
  const ranks = new Map<number, number>()
  for (const row of props.rows) {
    for (const bucket of row.buckets ?? []) {
      const index = indexes.get(Date.parse(bucket.bucket_start))
      if (index == null) continue
      const color = healthScoreClass(bucket.health, 'overall', bucket.metrics.request_count)
      const rank = knownRank(bucket, color)
      if (rank == null || rank >= (ranks.get(index) ?? Infinity)) continue
      ranks.set(index, rank)
      result[index] = { start: result[index].start, color, bucket }
    }
  }
  return result
})

const stateLabel = computed(() => {
  if (props.unavailable) return t('keys.groupChannelStatus.unavailable')
  if (props.loading) return t('common.loading')
  if (!slots.value.some((slot) => slot.bucket)) return t('common.noData')
  return ''
})

const description = computed(() => [
  t('keys.groupChannelStatus.recent'),
  props.rows.length > 1 ? t('keys.groupChannelStatus.composite') : '',
  stateLabel.value,
].filter(Boolean).join(' · '))

function slotTitle(slot: StatusSlot): string {
  if (props.loading || props.unavailable || slot.start == null) return description.value
  const time = new Date(slot.start).toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit' })
  if (!slot.bucket) return t('channelMonitorV2.matrix.noTrafficAt', { time })
  const score = slot.bucket.health.score
  if (score != null && Number.isFinite(score)) {
    return `${time} · ${t('channelMonitorV2.matrix.scoreLine', { score: Math.round(score) })}`
  }
  const key = {
    healthy: 'healthyLegend', warning: 'warningLegend', critical: 'criticalLegend', unknown: 'unknownLegend',
  }[slot.bucket.health.overall]
  return `${time} · ${t(`channelMonitorV2.matrix.${key}`)}`
}
</script>

<style scoped src="../../features/channel-monitor-v2/monitorHealthColors.css"></style>

<style scoped>
.group-channel-status {
  display: inline-flex;
  width: 112px;
  flex: 0 0 112px;
  flex-direction: column;
  justify-content: center;
  gap: 3px;
  vertical-align: middle;
}
.group-channel-status-track {
  display: grid;
  grid-template-columns: repeat(10, minmax(0, 1fr));
  gap: 2px;
  height: 11px;
}
.group-channel-status-cell { border-radius: 2px; }
.group-channel-status-label {
  color: #6b7280;
  font-size: 10px;
  line-height: 12px;
  text-align: center;
}
:global(.dark) .group-channel-status-label { color: #9ca3af; }
.is-loading { opacity: 0.45; }
</style>
