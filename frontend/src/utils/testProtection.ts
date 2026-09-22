import type { TestGroupWorkflow, TestOutcomeAction, TestProtectionConfig, TestProtectionMetric, TestProtectionRecovery, TestProtectionRule, TestType } from '@/types'

export const protectionMetrics = (kind: string): TestProtectionMetric[] => kind === 'statistics'
  ? ['success_rate', 'cache_rate', 'avg_first_token_ms']
  : kind === 'number' ? ['output_numeric', 'latency_ms'] : ['latency_ms']

const copyAction = (value?: TestOutcomeAction): TestOutcomeAction | undefined => value
  ? { ...value, group_ids: value.group_ids ? [...value.group_ids] : undefined } : undefined

export const defaultTestOutcomeAction = (outcome: 'pass' | 'fail'): TestOutcomeAction => ({
  scheduling: outcome === 'pass' ? 'resume' : 'pause', group_mode: 'keep',
})

export const copyTestProtection = (value?: TestProtectionConfig): TestProtectionConfig => value
  ? { enabled: value.enabled, rules: (value.rules || []).map(rule => ({ ...rule, thresholds: rule.thresholds?.map(item => ({ ...item })), vote: rule.vote ? { ...rule.vote } : undefined, on_pass: copyAction(rule.on_pass), on_fail: copyAction(rule.on_fail), recovery: rule.recovery ? { ...rule.recovery } : undefined })), ...(value.group_workflow ? { group_workflow: { ...value.group_workflow } } : {}) }
  : { enabled: false, rules: [] }

export function groupWorkflowProtection(workflow: TestGroupWorkflow): TestProtectionConfig {
  const actions = () => ({
    on_pass: { scheduling: 'keep' as const, group_mode: 'assign' as const, group_ids: [workflow.pass_group_id] },
    on_fail: { scheduling: 'keep' as const, group_mode: 'assign' as const, group_ids: [workflow.fail_group_id] },
  })
  return {
    enabled: true,
    group_workflow: { ...workflow },
    rules: [
      { test_definition_id: workflow.automatic_test_id, pause_on_failure: true, expected_answer: '21', answer_match: 'numeric', ...actions() },
      { test_definition_id: workflow.review_test_id, pause_on_failure: true, answer_match: 'exact', vote: { enabled: true, reject_above: 0, pass_at_least: 1 }, ...actions() },
    ],
  }
}

export function validTestGroupWorkflow(workflow: TestGroupWorkflow, types: TestType[], groups?: readonly { id: number }[]): boolean {
  return Object.values(workflow).every(id => Number.isSafeInteger(id) && id > 0)
    && workflow.automatic_test_id !== workflow.review_test_id && workflow.pass_group_id !== workflow.fail_group_id
    && types.some(type => type.id === workflow.automatic_test_id && type.enabled && type.output_kind === 'number')
    && types.some(type => type.id === workflow.review_test_id && type.enabled && type.output_kind === 'html')
    && (!groups || [workflow.pass_group_id, workflow.fail_group_id].every(id => groups.some(group => group.id === id)))
}

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

export function cacheRecoveryThreshold(rule: TestProtectionRule): number {
  return Math.max(0, ...(rule.thresholds || []).filter(item => item.metric === 'cache_rate' && item.operator === 'lt' && Number.isFinite(item.value)).map(item => item.value))
}

export function canEnableCacheRecovery(rule: TestProtectionRule, kind: string): boolean {
  const cacheThresholds = (rule.thresholds || []).filter(item => item.metric === 'cache_rate')
  return kind === 'statistics' && cacheThresholds.length > 0
    && cacheThresholds.every(item => item.operator === 'lt') && !rule.vote?.enabled
    && (rule.on_fail?.scheduling ?? 'pause') === 'pause'
    && (rule.on_pass?.scheduling ?? 'resume') === 'resume'
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
  return ids.length <= 100 && new Set(ids).size === ids.length
    && ids.every(id => Number.isSafeInteger(id) && id > 0 && (!groups || groups.some(group => group.id === id)))
}

export function validTestProtection(value: TestProtectionConfig | undefined, target: string | undefined, types: TestType[], groups?: readonly { id: number }[]): boolean {
  if (value?.group_workflow) return value.enabled && target === 'all_accounts' && validTestGroupWorkflow(value.group_workflow, types, groups)
  if (!value?.enabled) return true
  if (target === 'group' || !value.rules.length || value.rules.length > 32) return false
  if (new Set(value.rules.map(rule => rule.test_definition_id)).size !== value.rules.length) return false
  return value.rules.every(rule => {
    const type = types.find(item => item.id === rule.test_definition_id)
    if (!type?.enabled) return false
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
