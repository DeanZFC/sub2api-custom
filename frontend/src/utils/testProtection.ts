import type { TestOutcomeAction, TestProtectionConfig, TestProtectionMetric, TestProtectionRule, TestType } from '@/types'

export const protectionMetrics = (kind: string): TestProtectionMetric[] => kind === 'statistics'
  ? ['success_rate', 'cache_rate', 'avg_first_token_ms']
  : kind === 'number' ? ['output_numeric', 'latency_ms'] : ['latency_ms']

const copyAction = (value?: TestOutcomeAction): TestOutcomeAction | undefined => value
  ? { ...value, group_ids: value.group_ids ? [...value.group_ids] : undefined } : undefined

export const defaultTestOutcomeAction = (outcome: 'pass' | 'fail'): TestOutcomeAction => ({
  scheduling: outcome === 'pass' ? 'resume' : 'pause', group_mode: 'keep',
})

export const copyTestProtection = (value?: TestProtectionConfig): TestProtectionConfig => value
  ? { enabled: value.enabled, rules: (value.rules || []).map(rule => ({ ...rule, thresholds: rule.thresholds?.map(item => ({ ...item })), vote: rule.vote ? { ...rule.vote } : undefined, on_pass: copyAction(rule.on_pass), on_fail: copyAction(rule.on_fail) })) }
  : { enabled: false, rules: [] }

export const defaultProtectionRule = (type: TestType): TestProtectionRule => ({
  test_definition_id: type.id,
  pause_on_failure: true,
  thresholds: [],
  on_pass: defaultTestOutcomeAction('pass'),
  on_fail: defaultTestOutcomeAction('fail'),
  ...(type.output_kind === 'statistics' ? { min_samples: 10 }
    : type.output_kind === 'model_check' ? { model_match: 'exact' }
      : { answer_match: type.output_kind === 'number' ? 'numeric' : 'exact' }),
})

function validAction(action: TestOutcomeAction | undefined, groups?: readonly { id: number }[]): boolean {
  if (!action) return true
  if (!['keep', 'pause', 'resume'].includes(action.scheduling) || !['keep', 'assign'].includes(action.group_mode)) return false
  const ids = action.group_ids || []
  if (action.group_mode === 'keep') return !ids.length
  return ids.length <= 100 && new Set(ids).size === ids.length
    && ids.every(id => Number.isSafeInteger(id) && id > 0 && (!groups || groups.some(group => group.id === id)))
}

export function validTestProtection(value: TestProtectionConfig | undefined, target: string | undefined, types: TestType[], groups?: readonly { id: number }[]): boolean {
  if (!value?.enabled) return true
  if (target === 'group' || !value.rules.length || value.rules.length > 32) return false
  if (new Set(value.rules.map(rule => rule.test_definition_id)).size !== value.rules.length) return false
  return value.rules.every(rule => {
    const type = types.find(item => item.id === rule.test_definition_id)
    if (!type?.enabled) return false
    if (!validAction(rule.on_pass, groups) || !validAction(rule.on_fail, groups)) return false
    const assigned = [rule.on_pass, rule.on_fail].filter(action => action?.group_mode === 'assign')
    if (assigned.length && !assigned.some(action => action?.group_ids?.length)) return false
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
    if (['statistics', 'model_check'].includes(type.output_kind) && (voting || answer)) return false
    if (rule.model_match && (type.output_kind !== 'model_check' || !['exact', 'snapshot'].includes(rule.model_match))) return false
    if (!voting && answer && rule.answer_match === 'numeric'
      && (!/^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/.test(answer) || !Number.isFinite(Number(answer)))) return false
    return Boolean(type.output_kind === 'model_check' || rule.pause_on_failure || thresholds.length || voting || answer || rule.on_fail)
  })
}
