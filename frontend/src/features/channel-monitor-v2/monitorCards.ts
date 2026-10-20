import type { HealthState, MonitorHealth, MonitorMatrixRow, MonitorRecentRequest } from '@/api/channelMonitorV2'
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
