import { useI18n } from 'vue-i18n'

export function useQualityLabels() {
  const { t, te } = useI18n()
  const passive = (key: string, args: Record<string, unknown> = {}) => t(`admin.tests.passive.${key}`, args)
  function reason(value: string): string {
    if (te('admin.tests.groupTargets.reasons.' + value)) return t('admin.tests.groupTargets.reasons.' + value)
    if (te('admin.tests.passive.v2.reasons.' + value)) return passive('v2.reasons.' + value)
    if (te(`admin.tests.passive.reasons.${value}`)) return passive(`reasons.${value}`)
    if (te(`admin.tests.quality.reasons.${value}`)) return t(`admin.tests.quality.reasons.${value}`)
    if (value.includes('; ')) return value.split('; ').map(reason).join('; ')
    for (const prefix of ['active_recovery_failed', 'active_recovery_pending', 'active_recovery_running', 'active_probe_metrics_unsupported', 'trial_expired', 'trial_failed', 'waiting_for_recovery', 'waiting_for_fresh_failure', 'cache_allocation_reduced']) {
      if (value.startsWith(`${prefix}: `)) return `${reason(prefix)}: ${reason(value.slice(prefix.length + 2))}`
    }
    if (value.startsWith('fresh_')) return t('admin.tests.groupTargets.freshEvidenceReason', { reason: reason(value.slice(6)) })
    const metric = /^(error_rate|ttft_p90|cache_rate|output_tps): ([\d.]+) \/ ([\d.]+) \((\d+)\)$/.exec(value)
    if (metric) return passive('metricReason', { metric: passive(metric[1] === 'error_rate' ? 'errorRate' : metric[1] === 'ttft_p90' ? 'ttft' : metric[1] === 'output_tps' ? 'tps' : 'cacheRate'), observed: metric[2], threshold: metric[3], samples: metric[4] })
    const burst = /^consecutive_failures: (\d+) within 60s$/.exec(value)
    if (burst) return t('admin.tests.groupTargets.activeConsecutiveFailures', { count: burst[1] })
    if (/^(?:active_recovery_|recovery_|missing_recovery_|insufficient_active_|invalid_active_)[a-z_]+$/.test(value)) return t('admin.tests.groupTargets.activeReasonUnknown', { reason: value })
    const consecutive = /^consecutive_failures: (\d+)$/.exec(value)
    return consecutive ? passive('consecutiveReason', { count: consecutive[1] }) : value
  }
  return { reason, date: (value?: string) => !value || value.startsWith('0001') ? '—' : new Date(value).toLocaleString() }
}
