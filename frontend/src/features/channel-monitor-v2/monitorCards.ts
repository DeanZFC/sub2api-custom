import type { HealthState, MonitorCoverage, MonitorHealth, MonitorMatrixRow, MonitorRecentRequest } from '@/api/channelMonitorV2'
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

export function modelsForCard(row: MonitorMatrixRow, models: MonitorMatrixRow[]): MonitorMatrixRow[] {
  return models.filter(model => model.model && hasMonitorTraffic(model)
    && model.platform === row.platform
    && (row.group_id == null || model.group_id === row.group_id)
    && (!row.model || model.model === row.model))
}

export function timelineForCard(row: MonitorMatrixRow, coverage: MonitorCoverage) {
  const step = Math.max(60, coverage.bucket_seconds) * 1000
  const start = Date.parse(coverage.requested_start)
  const end = Date.parse(coverage.requested_end || coverage.data_through)
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return []
  const buckets = new Map(row.buckets.map(bucket => [Date.parse(bucket.bucket_start), bucket]))
  const slots = []
  for (let time = Math.floor(start / step) * step; time < end; time += step) {
    slots.push({ start: time, end: Math.min(time + step, end), bucket: buckets.get(time) })
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
