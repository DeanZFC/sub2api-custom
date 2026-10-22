import type { HealthState, MonitorCoverage, MonitorHealth, MonitorMatrixBucket, MonitorMatrixRow, MonitorRecentRequest } from '@/api/channelMonitorV2'
import type { HealthDisplayMode } from './monitorFormat'

export function cardHealth(health: MonitorHealth, mode: HealthDisplayMode): HealthState {
  if (mode === 'success') return health.error_rate
  if (mode === 'ttft') return health.ttft
  if (mode === 'cache') return health.cache || 'unknown'
  return health.overall
}

export function hasMonitorTraffic(row: MonitorMatrixRow): boolean {
  // Keep working when absolute request counts are redacted for ordinary users.
  return row.has_traffic ?? (row.metrics.request_count > 0 || row.buckets.length > 0)
}

export interface MonitorHistorySlot {
  start: number
  end: number
  bucket?: MonitorMatrixBucket
}

/** Keep real aggregate intervals and gaps; never average percentiles or redacted counts. */
export function historyBucketsForCard(buckets: MonitorMatrixBucket[], coverage: MonitorCoverage): Array<MonitorHistorySlot | null> {
  const start = Date.parse(coverage.requested_start)
  const end = Date.parse(coverage.requested_end || coverage.data_through)
  const step = coverage.bucket_seconds * 1000
  if (![start, end, step].every(Number.isFinite) || end <= start || step <= 0) return Array(20).fill(null)

  const first = Math.floor(start / step) * step
  const count = Math.ceil((end - first) / step)
  const visibleCount = Math.min(20, count)
  const byStart = new Map(buckets.map(bucket => [Date.parse(bucket.bucket_start), bucket]))
  const slots: Array<MonitorHistorySlot | null> = Array(20 - visibleCount).fill(null)
  for (let index = count - visibleCount; index < count; index++) {
    const bucketStart = first + index * step
    slots.push({ start: Math.max(start, bucketStart), end: Math.min(end, bucketStart + step), bucket: byStart.get(bucketStart) })
  }
  return slots
}

export function recentRequestsForCard(requests: MonitorRecentRequest[] = []): Array<MonitorRecentRequest | null> {
  const recent = [...requests].sort((a, b) => Date.parse(a.occurred_at) - Date.parse(b.occurred_at)).slice(-20)
  return [...Array<null>(20 - recent.length).fill(null), ...recent]
}

export function requestHealth(request: MonitorRecentRequest | null): HealthState {
  if (!request) return 'unknown'
  if (request.status === 'success') return 'healthy'
  if (request.status === 'error') return 'critical'
  return 'warning'
}
