import type { PassivePolicy, PassiveCacheProtection } from '@/api/admin/passiveQuality'

// Historical policy shapes for read-only event and upgrade regression tests.
export function defaultPassivePolicy(): PassivePolicy {
  return { schema_version: 2, tps_action: 'limit', tps_concurrency_limit: 2, tps_priority_penalty: 50, ttft_action: 'limit', ttft_concurrency_limit: 2, ttft_priority_penalty: 50, cache_protection: defaultPassiveCacheProtection(), id: 0, version: 0, name: '', enabled: true, mode: 'observe', group_ids: [], excluded_account_ids: [], model: '', scope: 'model', action: 'pause', concurrency_limit: 2, priority_penalty: 10,
    window_seconds: 600, max_samples: 100, min_samples: 30, confirmations: 2, new_samples: 5,
    tps: { enabled: false, pause: 10, recover: 15 }, error_rate: { enabled: true, pause: 20, recover: 5 }, ttft: { enabled: false, pause: 10000, recover: 5000 },
    cache_rate: { enabled: false, pause: 10, recover: 20 }, consecutive_failures: 0,
    cooldown_seconds: 120, max_cooldown_seconds: 3600, trial_seconds: 300, trial_requests: 50, trial_concurrency: 1, recovery_samples: 20 }
}

export function defaultPassiveCacheProtection(): PassiveCacheProtection {
  return {
    action: 'reduce',
    quick: { window_seconds: 300, max_samples: 50, min_samples: 15, confirmations: 2, new_samples: 3, min_tokens: 32768, min_sessions: 3, max_session_share_percent: 60 },
    action_window: { window_seconds: 900, max_samples: 200, min_samples: 30, confirmations: 3, new_samples: 5, min_tokens: 65536, min_sessions: 3, max_session_share_percent: 60 },
    allocation_percent: 25, recovery_step_percent: 25, recovery_confirmations: 2, min_hold_seconds: 300, recovery_hold_seconds: 300,
    max_reduced_percent: 50, min_serving_accounts: 1, reuse_window_seconds: 300, warmup_requests: 1,
  }
}
