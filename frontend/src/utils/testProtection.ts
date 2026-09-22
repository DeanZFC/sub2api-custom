import type { TestCombinationCondition, TestCombinationRule, TestOutcomeAction, TestProtectionConfig, TestProtectionMetric, TestProtectionRecovery, TestProtectionRule, TestProtectionVote, TestType } from '@/types'

export const protectionMetrics = (kind: string): TestProtectionMetric[] => kind === 'statistics'
  ? ['success_rate', 'cache_rate', 'avg_first_token_ms']
  : kind === 'number' ? ['output_numeric', 'latency_ms'] : ['latency_ms']

const copyAction = (value?: TestOutcomeAction): TestOutcomeAction | undefined => value
  ? { ...value, group_ids: value.group_ids ? [...value.group_ids] : undefined } : undefined
export const defaultTestOutcomeAction = (outcome: 'pass' | 'fail'): TestOutcomeAction => ({
  scheduling: outcome === 'pass' ? 'resume' : 'pause', group_mode: 'keep',
})

export const copyTestProtection = (value?: TestProtectionConfig): TestProtectionConfig => value
  ? { enabled: value.enabled, ...(value.mode ? { mode: value.mode } : {}), ...(value.combinations ? { combinations: value.combinations.map(copyTestCombination) } : {}), rules: (value.rules || []).map(rule => ({ ...rule, priority: rule.priority ?? 0, required_pass: rule.required_pass ?? false, thresholds: rule.thresholds?.map(item => ({ ...item })), vote: rule.vote ? { ...rule.vote } : undefined, on_pass: copyAction(rule.on_pass), on_fail: copyAction(rule.on_fail), recovery: rule.recovery ? { ...rule.recovery } : undefined })) }
  : { enabled: false, rules: [] }

export const copyTestCombinationCondition = (condition: TestCombinationCondition): TestCombinationCondition => ({
  ...condition, ...(condition.conditions ? { conditions: condition.conditions.map(copyTestCombinationCondition) } : {}),
})
export const copyTestCombination = (rule: TestCombinationRule): TestCombinationRule => ({
  ...rule, condition: copyTestCombinationCondition(rule.condition), action: copyAction(rule.action)!,
})
export const newTestCombinationID = (): string => globalThis.crypto?.randomUUID?.()
  ?? `combined-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
export const combinationNodeCount = (condition: TestCombinationCondition): number =>
  1 + (condition.conditions || []).reduce((count, child) => count + combinationNodeCount(child), 0)

export function combinationConflictsWithRecovery(rule: TestCombinationRule, recoveryTestIDs: readonly number[], combinations: readonly TestCombinationRule[] = []): boolean {
  const referencesRecovery = (condition: TestCombinationCondition): boolean => condition.operator === 'test'
    ? recoveryTestIDs.includes(condition.test_definition_id ?? 0)
    : !!condition.conditions?.some(referencesRecovery)
  const needsIndependentVerdict = rule.action.scheduling === 'pause'
    || (rule.action.scheduling === 'resume' && combinations.some(item => item.action.scheduling === 'pause'))
  // A combined pause must be releasable before trial traffic can produce fresh
  // cache statistics, even when the resume condition is in a separate rule.
  return needsIndependentVerdict && referencesRecovery(rule.condition)
}

export function validTestCombinations(value: TestProtectionConfig, groups?: readonly { id: number }[]): boolean {
  const combinations = value.combinations || []
  if (!combinations.length || combinations.length > 32 || new Set(combinations.map(rule => rule.id)).size !== combinations.length) return false
  return combinations.every(rule => {
    if (!rule.id?.trim() || [...rule.id].length > 64 || !rule.name?.trim() || [...rule.name].length > 100
      || !Number.isInteger(rule.priority) || rule.priority < 0 || rule.priority > 1000 || !rule.action || !validAction(rule.action, groups)) return false
    if (combinationConflictsWithRecovery(rule, value.rules.filter(source => source.recovery?.enabled).map(source => source.test_definition_id), combinations)) return false
    let nodes = 0
    const validCondition = (condition: TestCombinationCondition, depth: number): boolean => {
      if (!condition || depth > 6 || ++nodes > 128) return false
      if (condition.operator === 'test') return !condition.conditions?.length
        && ['pass', 'fail'].includes(condition.verdict || '')
        && value.rules.some(item => item.test_definition_id === condition.test_definition_id)
      return ['all', 'any'].includes(condition.operator) && condition.test_definition_id == null && condition.verdict == null
        && !!condition.conditions?.length && condition.conditions.every(child => validCondition(child, depth + 1))
    }
    return validCondition(rule.condition, 1)
  })
}

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
  if (value?.mode && !['per_test', 'combined'].includes(value.mode)) return false
  if (!value?.enabled) return true
  const combined = value.mode === 'combined'
  if (combined ? !validTestCombinations(value, groups) : !!value.combinations?.length) return false
  if (!value.rules.length || value.rules.length > 32) return false
  if (new Set(value.rules.map(rule => rule.test_definition_id)).size !== value.rules.length) return false
  return value.rules.every(rule => {
    const type = types.find(item => item.id === rule.test_definition_id)
    if (!type?.enabled) return false
    if (!combined && (!Number.isInteger(rule.priority) || rule.priority < 0 || rule.priority > 1000 || typeof rule.required_pass !== 'boolean')) return false
    if (!combined && rule.required_pass && rule.vote?.enabled) return false
    if (!validCacheRecovery(rule, type.output_kind)) return false
    if ((!combined || rule.recovery?.enabled) && (!validAction(rule.on_pass, combined ? undefined : groups) || !validAction(rule.on_fail, combined ? undefined : groups))) return false
    const assigned = [rule.on_pass, rule.on_fail].filter(action => action?.group_mode === 'assign')
    if (!combined && assigned.length && !assigned.some(action => action?.group_ids?.length)) return false
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
