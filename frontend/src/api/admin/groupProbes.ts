import { apiClient } from '../client'

const base = '/admin/channel-monitor-v2/group-probes'

export interface GroupProbeRule {
  idle_window_minutes: number
  min_requests: number
  duration_minutes: number
  interval_seconds: number
}

export interface GroupProbe extends GroupProbeRule {
  group_id: number
  group_name: string
  model: string
  enabled: boolean
  pending: boolean
  last_user_at: string | null
  last_probe_at: string | null
  round_until: string | null
  status: 'unknown' | 'disabled' | 'healthy' | 'confirming' | 'failed' | 'recovered' | 'stale'
  deferred_reason?: string
  result: {
    success: boolean
    detail?: string
    switched: boolean
    first_token_ms: number | null
    account_id?: number
    estimated_cost_usd: number | null
    tokens: number | null
    partial_usage: boolean
  } | null
}

export async function listGroupProbes(signal?: AbortSignal): Promise<GroupProbe[]> {
  const { data } = await apiClient.get<GroupProbe[]>(base, { signal })
  return data
}

export async function saveGroupProbe(group: number, model: string, enabled: boolean, rule?: GroupProbeRule) {
  await apiClient.put(`${base}/${group}`, { model, enabled, ...rule })
}

export async function runGroupProbe(group: number) {
  await apiClient.post(`${base}/${group}/run`)
}
