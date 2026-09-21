import type { TestProtectionConfig, TestProtectionMetric, TestProtectionRule, TestType } from '@/types'

export const protectionMetrics = (kind: string): TestProtectionMetric[] => kind === 'statistics'
  ? ['success_rate', 'cache_rate', 'avg_first_token_ms']
  : kind === 'number' ? ['output_numeric', 'latency_ms'] : ['latency_ms']

export const copyTestProtection = (value?: TestProtectionConfig): TestProtectionConfig => value
  ? { enabled: value.enabled, rules: (value.rules || []).map(rule => ({ ...rule, thresholds: rule.thresholds?.map(item => ({ ...item })), vote: rule.vote ? { ...rule.vote } : undefined })) }
  : { enabled: false, rules: [] }

export const defaultProtectionRule = (type: TestType): TestProtectionRule => ({
  test_definition_id: type.id,
  pause_on_failure: true,
  thresholds: [],
  ...(type.output_kind === 'statistics' ? { min_samples: 10 } : { answer_match: type.output_kind === 'number' ? 'numeric' : 'exact' }),
})

export function validTestProtection(value: TestProtectionConfig | undefined, target: string | undefined, types: TestType[]): boolean {
  if (!value?.enabled) return true
  if (target === 'group' || !value.rules.length || value.rules.length > 32) return false
  if (new Set(value.rules.map(rule => rule.test_definition_id)).size !== value.rules.length) return false
  return value.rules.every(rule => {
    const type = types.find(item => item.id === rule.test_definition_id)
    if (!type?.enabled) return false
    const thresholds = rule.thresholds || []
    if (thresholds.length > 20 || thresholds.some(item => !protectionMetrics(type.output_kind).includes(item.metric) || !['lt', 'gt'].includes(item.operator) || !Number.isFinite(item.value)
      || (item.metric.endsWith('_rate') && (item.value < 0 || item.value > 100))
      || (item.metric.endsWith('_ms') && item.value < 0))) return false
    if (rule.min_samples != null && (!Number.isInteger(rule.min_samples) || rule.min_samples < 0 || rule.min_samples > 1_000_000_000)) return false
    const voting = rule.vote?.enabled
    if (voting && (!Number.isInteger(rule.vote!.reject_above) || rule.vote!.reject_above < 0 || rule.vote!.reject_above > 1_000_000
      || !Number.isInteger(rule.vote!.pass_at_least) || rule.vote!.pass_at_least < 1 || rule.vote!.pass_at_least > 1_000_000)) return false
    const answer = rule.expected_answer?.trim()
    if (answer && [...answer].length > 10000) return false
    if (rule.answer_match && !['exact', 'contains', 'numeric'].includes(rule.answer_match)) return false
    if (type.output_kind === 'statistics' && (voting || answer)) return false
    if (!voting && answer && rule.answer_match === 'numeric'
      && (!/^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/.test(answer) || !Number.isFinite(Number(answer)))) return false
    return Boolean(rule.pause_on_failure || thresholds.length || voting || answer)
  })
}
