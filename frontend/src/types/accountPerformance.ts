import type { OpsAccountRequestStats } from '@/api/admin/ops'
export interface PerformanceBounds { min: number | null; max: number | null }
export interface PerformanceRule { pause: PerformanceBounds; recovery: PerformanceBounds }
export const metricKeys = ['success_rate', 'cache_rate', 'ttft', 'duration'] as const
export type PerformanceMetric = typeof metricKeys[number]
export type PerformancePhase = 'disabled' | 'monitoring' | 'paused' | 'trial'
export interface PerformanceConfig {
 version: 2
 enabled: boolean
 success_rate: PerformanceRule
 cache_rate: PerformanceRule
 ttft: PerformanceRule
 duration: PerformanceRule
 min_samples: number
 auto_recover: boolean
 cooldown_minutes: number
 recovery_samples: number
}
export interface AccountPerformance {
 phase?: PerformancePhase
 recover_after?: string | null
 trial_ends_at?: string | null
 trial_limit?: number
 monitoring_available: boolean
 account_id: number
 metrics: OpsAccountRequestStats
 config: PerformanceConfig
 revision: number
 paused: boolean
 pause_generation: number
 paused_at: string | null
 trial_started_at: string | null
 trial_requests: number
 metrics_since: string | null
 reason: string
}
export type AccountPerformanceState = Omit<AccountPerformance, 'metrics'>
export function performancePhase(state: Pick<AccountPerformance, 'phase' | 'paused' | 'trial_started_at' | 'config'>): PerformancePhase {
 if (state.phase) return state.phase
 if (state.paused) return state.trial_started_at ? 'trial' : 'paused'
 return state.config.enabled ? 'monitoring' : 'disabled'
}
export interface PerformanceEvent {
 reason?: string
 id: number
 action: 'configure' | 'pause' | 'release' | 'trial' | 'recover' | 'retire'
 actor_id: number | null
 revision: number
 pause_generation: number
 created_at: string
 evidence: unknown
}
const rule = (): PerformanceRule => ({ pause: { min: null, max: null }, recovery: { min: null, max: null } })
export const emptyPerformanceConfig = (): PerformanceConfig => ({
 version: 2, enabled: false, success_rate: rule(), cache_rate: rule(), ttft: rule(), duration: rule(),
 min_samples: 20, auto_recover: true, cooldown_minutes: 5, recovery_samples: 3
})
