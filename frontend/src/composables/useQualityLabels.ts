import { useI18n } from 'vue-i18n'

export function useQualityLabels() {
  const { t, te } = useI18n()
  const passive = (key: string, args: Record<string, unknown> = {}) => t(`admin.tests.passive.${key}`, args)
  function reason(value: string): string {
    if (te('admin.tests.groupTargets.reasons.' + value)) return t('admin.tests.groupTargets.reasons.' + value)
    if (te('admin.tests.passive.v2.reasons.' + value)) return passive('v2.reasons.' + value)
    if (te(`admin.tests.passive.reasons.${value}`)) return passive(`reasons.${value}`)
    if (te(`admin.tests.quality.reasons.${value}`)) return t(`admin.tests.quality.reasons.${value}`)
    for (const prefix of ['trial_expired', 'trial_failed', 'waiting_for_recovery']) {
      if (value.startsWith(`${prefix}: `)) return `${passive(`reasons.${prefix}`)}: ${reason(value.slice(prefix.length + 2))}`
    }
    const metric = /^(error_rate|ttft_p90|cache_rate): ([\d.]+) \/ ([\d.]+) \((\d+)\)$/.exec(value)
    if (metric) return passive('metricReason', { metric: passive(metric[1] === 'error_rate' ? 'errorRate' : metric[1] === 'ttft_p90' ? 'ttft' : 'cacheRate'), observed: metric[2], threshold: metric[3], samples: metric[4] })
    const consecutive = /^consecutive_failures: (\d+)$/.exec(value)
    return consecutive ? passive('consecutiveReason', { count: consecutive[1] }) : value
  }
  return { reason, date: (value?: string) => !value || value.startsWith('0001') ? '—' : new Date(value).toLocaleString() }
}
