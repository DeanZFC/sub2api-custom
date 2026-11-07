export default {
 monitoringDisabled: 'Ops monitoring is off; pause and recovery evaluation is stopped. Enable monitoring first. You can still disable or release this rule.',
 title: 'Request metrics and automatic pause', configure: 'Configure request metrics and automatic pause',
 success_rate: 'Success', cache_rate: 'Cache', ttft: 'Avg. TTFT', duration: 'Avg. duration',
 metric: 'Metric', current: 'Current', pauseMin: 'Pause below', pauseMax: 'Pause above', recoveryMin: 'Recovery minimum', recoveryMax: 'Recovery maximum',
 noBound: 'No limit', inherit: 'Use pause bound', enabled: 'Enable metric rules (evaluated every 30 seconds)', active: 'Monitoring metrics',
 paused: 'Paused by metrics', trial: 'Recovery trial', trial_timeout: 'Trial timed out or insufficient samples',
 minSamples: 'Minimum valid samples per metric to pause', autoRecover: 'Enable automatic recovery', cooldown: 'Minutes before a recovery trial', recoverySamples: 'New samples required per metric to recover',
 semantics: 'The normal window is the past hour. Success rate = recorded successes / (successes + failures), deduplicated by account and API key. Terminal errors override partial output; WS connection errors do not erase completed turns. Unrecorded outcomes remain unknown. Cache is weighted by valid streaming/WS text prompt tokens; latency uses successful requests.',
 boundsNote: 'Blank metrics are excluded. Any metric strictly outside its bounds with enough samples pauses the account; equality does not. Recovery bounds must be inside the normal range; blank recovery bounds inherit pause bounds. Missing samples show — and are never zero.',
 recoveryNote: 'After cooldown, admit at most one request every 5 seconds, up to 3 times the recovery sample count per trial. All selected metrics must pass on new samples to recover. Failing metrics or insufficient samples after 10 minutes start another cooldown. No traffic means no recovery. Evaluation during and after recovery starts at the trial baseline, looking back at most one hour.',
 saveNote: 'Saving clears this rule’s pause and starts evaluation again. Manual disables, rate limits and other pause sources still apply. Automatic recovery only clears this rule’s pause. New rules default to off.',
 samples: '{count} samples', save: 'Save rules', release: 'Release pause and disable rules', refresh: 'Reload',
 loading: 'Loading…', unavailable: 'Metric configuration unavailable', failed: 'Update failed. Reload before saving after a version conflict.',
 invalid: 'Select at least one enabled metric. Percentages must be 0–100 and latency nonnegative milliseconds. Minimum must not exceed maximum; recovery must be at least as strict. Sample counts and cooldown must be integers within their limits.',
 audit: 'Recent rule events', noEvents: 'No events', auditUnavailable: 'Events unavailable',
 configureEvent: 'Rules saved', pauseEvent: 'Paused by metrics', releaseEvent: 'Released and disabled', trialEvent: 'Recovery trial started', recoverEvent: 'Metrics passed; recovered', retireEvent: 'Retired HTTP protection'
}
