import type { PassivePolicy } from '@/api/admin/passiveQuality'

// Profiles only tune sampling and recovery. Targets, metrics and enforcement
// always remain an explicit administrator choice. Existing policies are read
// as-is; matching a profile never writes or normalizes their values. V2 cache
// windows and per-metric actions intentionally remain independent of profiles.
export const qualityProfileKeys = ['responsive', 'balanced', 'steady'] as const
export type QualityProfile = typeof qualityProfileKeys[number]
const profiles = {
  responsive: { window_seconds: 300, max_samples: 50, min_samples: 15, confirmations: 2, new_samples: 3, cooldown_seconds: 60, max_cooldown_seconds: 1800, trial_seconds: 180, trial_requests: 30, trial_concurrency: 1, recovery_samples: 10 },
  balanced: { window_seconds: 600, max_samples: 100, min_samples: 30, confirmations: 2, new_samples: 5, cooldown_seconds: 120, max_cooldown_seconds: 3600, trial_seconds: 300, trial_requests: 50, trial_concurrency: 1, recovery_samples: 20 },
  steady: { window_seconds: 900, max_samples: 200, min_samples: 60, confirmations: 3, new_samples: 10, cooldown_seconds: 300, max_cooldown_seconds: 3600, trial_seconds: 600, trial_requests: 100, trial_concurrency: 1, recovery_samples: 40 },
} satisfies Record<QualityProfile, Partial<PassivePolicy>>

export function applyQualityProfile(policy: PassivePolicy, profile: QualityProfile): PassivePolicy {
  return { ...policy, ...profiles[profile] }
}

export function matchingQualityProfile(policy: PassivePolicy): QualityProfile | 'custom' {
  return qualityProfileKeys.find(key => Object.entries(profiles[key]).every(([field, value]) => policy[field as keyof PassivePolicy] === value)) || 'custom'
}

export type QualityResponse = 'observe' | 'limit' | 'pause'
export function policyResponse(policy: PassivePolicy): QualityResponse {
  return policy.mode === 'observe' ? 'observe' : policy.action === 'limit' ? 'limit' : 'pause'
}

export function setPolicyResponse(policy: PassivePolicy, response: QualityResponse): PassivePolicy {
  return { ...policy, mode: response === 'observe' ? 'observe' : 'enforce', ...(response === 'observe' ? {} : { action: response }) }
}
