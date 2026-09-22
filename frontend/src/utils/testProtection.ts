import type { TestOutcomeAction, TestProtectionConfig, TestProtectionMetric, TestProtectionRecovery, TestProtectionRule, TestProtectionVote, TestType } from '@/types'

export const protectionMetrics = (kind: string): TestProtectionMetric[] => kind === 'statistics'
  ? ['success_rate', 'cache_rate', 'avg_first_token_ms']
  : kind === 'number' ? ['output_numeric', 'latency_ms'] : ['latency_ms']

const copyAction = (value?: TestOutcomeAction): TestOutcomeAction | undefined => value
  ? { ...value, group_ids: value.group_ids ? [...value.group_ids] : undefined } : undefined
export const defaultTestOutcomeAction = (outcome: 'pass' | 'fail'): TestOutcomeAction => ({
  scheduling: outcome === 'pass' ? 'resume' : 'pause', group_mode: 'keep',
})

export const copyTestProtection = (value?: TestProtectionConfig): TestProtectionConfig => value
  ? { enabled: value.enabled, rules: (value.rules || []).map(rule => ({ ...rule, priority: rule.priority ?? 0, required_pass: rule.required_pass ?? false, thresholds: rule.thresholds?.map(item => ({ ...item })), vote: rule.vote ? { ...rule.vote } : undefined, on_pass: copyAction(rule.on_pass), on_fail: copyAction(rule.on_fail), recovery: rule.recovery ? { ...rule.recovery } : undefined })) }
  : { enabled: false, rules: [] }

function validTestVote(vote: TestProtectionVote): boolean {
  return (vote.public_enabled == null || typeof vote.public_enabled === 'boolean')
    && (!vote.public_enabled || vote.enabled)
    && (!vote.enabled || (Number.isInteger(vote.reject_above) && vote.reject_above >= 0 && vote.reject_above <= 1_000_000
      && Number.isInteger(vote.pass_at_least) && vote.pass_at_least >= 1 && vote.pass_at_least <= 1_000_000))
}

export const defaultProtectionRule = (type: TestType): TestProtectionRule => ({
  test_definition_id: type.id,
  priority: 0,
  required_pass: false,
  pause_on_failure: true,
  thresholds: [],
  on_pass: defaultTestOutcomeAction('pass'),
  on_fail: defaultTestOutcomeAction('fail'),
  ...(type.output_kind === 'statistics' ? { min_samples: 10 }
    : type.output_kind === 'model_check' ? { model_match: 'exact' }
      : { answer_match: type.output_kind === 'number' ? 'numeric' : 'exact' }),
})

export function cacheRecoveryThreshold(rule: TestProtectionRule): number {
  return Math.max(0, ...(rule.thresholds || []).filter(item => item.metric === 'cache_rate' && item.operator === 'lt' && Number.isFinite(item.value)).map(item => item.value))
}

export function canEnableCacheRecovery(rule: TestProtectionRule, kind: string): boolean {
  const cacheThresholds = (rule.thresholds || []).filter(item => item.metric === 'cache_rate')
  return kind === 'statistics' && cacheThresholds.length > 0
    && cacheThresholds.every(item => item.operator === 'lt') && !rule.vote?.enabled
    && (rule.on_fail?.scheduling ?? 'keep') === 'pause'
    && (rule.on_pass?.scheduling ?? 'keep') === 'resume'
    && (rule.on_fail?.group_mode !== 'assign' || Boolean(rule.on_fail.group_ids?.length))
}

export const defaultTestProtectionRecovery = (rule: TestProtectionRule): TestProtectionRecovery => ({
  enabled: false,
  cooldown_seconds: 300,
  trial_seconds: 300,
  max_requests: 20,
  min_samples: 10,
  recover_rate: Math.min(100, cacheRecoveryThreshold(rule) + 5),
})

function validCacheRecovery(rule: TestProtectionRule, kind: string): boolean {
  const recovery = rule.recovery
  if (!recovery?.enabled) return true
  const integerInRange = (value: number, min: number, max: number) => Number.isInteger(value) && value >= min && value <= max
  return canEnableCacheRecovery(rule, kind)
    && integerInRange(recovery.cooldown_seconds, 60, 86400)
    && integerInRange(recovery.trial_seconds, 60, 3600)
    && integerInRange(recovery.max_requests, 1, 1000)
    && integerInRange(recovery.min_samples, 1, recovery.max_requests)
    && Number.isFinite(recovery.recover_rate) && recovery.recover_rate >= cacheRecoveryThreshold(rule) && recovery.recover_rate <= 100
}

function validAction(action: TestOutcomeAction | undefined, groups?: readonly { id: number }[]): boolean {
  if (!action) return true
  if (!['keep', 'pause', 'resume'].includes(action.scheduling) || !['keep', 'assign'].includes(action.group_mode)) return false
  const ids = action.group_ids || []
  if (action.group_mode === 'keep') return !ids.length
  return ids.length >= 1 && ids.length <= 100 && new Set(ids).size === ids.length
    && ids.every(id => Number.isSafeInteger(id) && id > 0 && (!groups || groups.some(group => group.id === id)))
}

export function validTestProtection(value: TestProtectionConfig | undefined, types: TestType[], groups?: readonly { id: number }[]): boolean {
  if (!value?.enabled) return true
  if (!value.rules.length || value.rules.length > 32) return false
  if (new Set(value.rules.map(rule => rule.test_definition_id)).size !== value.rules.length) return false
  return value.rules.every(rule => {
    const type = types.find(item => item.id === rule.test_definition_id)
    if (!type?.enabled) return false
    if (!Number.isInteger(rule.priority) || rule.priority < 0 || rule.priority > 1000 || typeof rule.required_pass !== 'boolean') return false
    if (rule.required_pass && rule.vote?.enabled) return false
    if (!validCacheRecovery(rule, type.output_kind)) return false
    if (!validAction(rule.on_pass, groups) || !validAction(rule.on_fail, groups)) return false
    const assigned = [rule.on_pass, rule.on_fail].filter(action => action?.group_mode === 'assign')
    if (assigned.length && !assigned.some(action => action?.group_ids?.length)) return false
    const thresholds = rule.thresholds || []
    if (thresholds.length > 20 || thresholds.some(item => !protectionMetrics(type.output_kind).includes(item.metric) || !['lt', 'gt'].includes(item.operator) || !Number.isFinite(item.value)
      || (item.metric.endsWith('_rate') && (item.value < 0 || item.value > 100))
      || (item.metric.endsWith('_ms') && item.value < 0))) return false
    if (rule.min_samples != null && (!Number.isInteger(rule.min_samples) || rule.min_samples < 0 || rule.min_samples > 1_000_000_000)) return false
    const voting = rule.vote?.enabled
    if (rule.vote && !validTestVote(rule.vote)) return false
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
