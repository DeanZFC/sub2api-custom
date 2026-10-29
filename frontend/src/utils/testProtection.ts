import type { TestCombinationCondition, TestCombinationRule, TestOutcomeAction, TestProtectionConfig, TestProtectionMetric, TestProtectionRule, TestProtectionVote, TestType } from '@/types'
import { validTestPassRate } from './testPassRate'

export const protectionMetrics = (kind: string): TestProtectionMetric[] => kind === 'number' ? ['output_numeric', 'latency_ms'] : ['latency_ms']

const copyAction = (value?: TestOutcomeAction): TestOutcomeAction | undefined => value
  ? { ...value, group_ids: value.group_ids ? [...value.group_ids] : undefined } : undefined
export const defaultTestOutcomeAction = (outcome: 'pass' | 'fail'): TestOutcomeAction => ({
  scheduling: outcome === 'pass' ? 'resume' : 'pause', group_mode: 'keep',
})

export function allPassOutcomeAction(value: TestProtectionConfig, outcome: 'pass' | 'fail'): TestOutcomeAction {
  const configured = copyAction(outcome === 'pass' ? value.on_pass : value.on_fail)
  if (configured) return configured
  const action = defaultTestOutcomeAction(outcome)
  if (outcome === 'pass' && value.pass_group_ids !== undefined) {
    action.group_mode = 'assign'
    action.group_ids = [...value.pass_group_ids]
  }
  return action
}

export const copyTestProtection = (value?: TestProtectionConfig): TestProtectionConfig => value
  ? { enabled: value.enabled, ...(value.mode ? { mode: value.mode } : {}), ...(value.pass_group_ids ? { pass_group_ids: [...value.pass_group_ids] } : {}),
    ...(value.on_pass ? { on_pass: copyAction(value.on_pass) } : {}), ...(value.on_fail ? { on_fail: copyAction(value.on_fail) } : {}),
    ...(value.combinations ? { combinations: value.combinations.map(copyTestCombination) } : {}), rules: (value.rules || []).map(rule => ({ ...rule, priority: rule.priority ?? 0, required_pass: rule.required_pass ?? false, thresholds: rule.thresholds?.map(item => ({ ...item })), vote: rule.vote ? { ...rule.vote } : undefined, auto_review: rule.auto_review ? { ...rule.auto_review } : undefined, on_pass: copyAction(rule.on_pass), on_fail: copyAction(rule.on_fail), pass_rate: rule.pass_rate ? { ...rule.pass_rate } : undefined })) }
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

export function validTestCombinations(value: TestProtectionConfig, groups?: readonly { id: number }[]): boolean {
  const combinations = value.combinations || []
  if (!combinations.length || combinations.length > 32 || new Set(combinations.map(rule => rule.id)).size !== combinations.length) return false
  return combinations.every(rule => {
    if (!rule.id?.trim() || [...rule.id].length > 64 || !rule.name?.trim() || [...rule.name].length > 100
      || !Number.isInteger(rule.priority) || rule.priority < 0 || rule.priority > 1000 || !rule.action || !validAction(rule.action, groups)) return false
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
  ...(type.output_kind === 'model_check' ? { model_match: 'exact' }
      : { answer_match: type.output_kind === 'number' ? 'numeric' : 'exact' }),
})

function validAction(action: TestOutcomeAction | undefined, groups?: readonly { id: number }[]): boolean {
  if (!action) return true
  if (!['keep', 'pause', 'resume'].includes(action.scheduling) || !['keep', 'assign'].includes(action.group_mode)) return false
  const ids = action.group_ids || []
  if (action.group_mode === 'keep') return !ids.length
  return ids.length >= 1 && ids.length <= 100 && new Set(ids).size === ids.length
    && ids.every(id => Number.isSafeInteger(id) && id > 0 && (!groups || groups.some(group => group.id === id)))
}

export function validTestProtection(value: TestProtectionConfig | undefined, types: TestType[], groups?: readonly { id: number }[]): boolean {
  if (value?.mode && !['per_test', 'combined', 'all_pass'].includes(value.mode)) return false
  if (!value?.enabled) return true
  const combined = value.mode === 'combined'
  const allPass = value.mode === 'all_pass'
  if (!allPass && (value.pass_group_ids !== undefined || value.on_pass || value.on_fail)) return false
  if (allPass && (!validAction(allPassOutcomeAction(value, 'pass'), groups) || !validAction(allPassOutcomeAction(value, 'fail'), groups))) return false
  if (allPass && (!value.rules.some(rule => !rule.vote?.enabled || rule.auto_review?.enabled || rule.thresholds?.some(item => item.metric === 'latency_ms')) || value.rules.some(rule => rule.vote?.public_enabled))) return false
  if (combined ? !validTestCombinations(value, groups) : !!value.combinations?.length) return false
  if (!value.rules.length || value.rules.length > 32) return false
  if (new Set(value.rules.map(rule => rule.test_definition_id)).size !== value.rules.length) return false
  return value.rules.every(rule => {
    const type = types.find(item => item.id === rule.test_definition_id)
    if (!type?.enabled || type.output_kind === 'statistics') return false
    if (!validTestPassRate(rule.pass_rate, type.output_kind)) return false
    if (rule.auto_review && (typeof rule.auto_review.enabled !== 'boolean' || (rule.auto_review.enabled && type.output_kind !== 'html'))) return false
    if (!combined && (!Number.isInteger(rule.priority) || rule.priority < 0 || rule.priority > 1000 || typeof rule.required_pass !== 'boolean')) return false
    if (!combined && rule.required_pass && rule.vote?.enabled) return false
    if (!combined && (!validAction(rule.on_pass, combined ? undefined : groups) || !validAction(rule.on_fail, combined ? undefined : groups))) return false
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
    if (rule.pass_rate?.enabled && !['model_check', 'capability'].includes(type.output_kind)
      && !answer && !voting && !rule.auto_review?.enabled && !thresholds.some(item => item.metric === 'output_numeric')) return false
    if (rule.answer_match && !['exact', 'contains', 'numeric'].includes(rule.answer_match)) return false
    if (['model_check', 'capability'].includes(type.output_kind) && (voting || answer)) return false
    if (rule.model_match && (type.output_kind !== 'model_check' || !['exact', 'snapshot'].includes(rule.model_match))) return false
    if (!voting && answer && rule.answer_match === 'numeric'
      && (!/^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/.test(answer) || !Number.isFinite(Number(answer)))) return false
    return Boolean(['model_check', 'capability'].includes(type.output_kind) || rule.pause_on_failure || thresholds.length || voting || rule.auto_review?.enabled || answer || rule.on_fail)
  })
}

// Only the aggregate policy executes actions.
export function normalizeAllPassProtection(value: TestProtectionConfig): void {
  if (value.mode !== 'all_pass') return
  delete value.combinations
  if (value.on_pass) delete value.pass_group_ids
  const pass = allPassOutcomeAction(value, 'pass')
  const fail = allPassOutcomeAction(value, 'fail')
  for (const rule of value.rules) {
    rule.priority = 0
    rule.required_pass = false
    rule.pause_on_failure = true
    rule.on_pass = { scheduling: pass.scheduling, group_mode: 'keep' }
    rule.on_fail = { scheduling: fail.scheduling, group_mode: 'keep' }
    if (rule.pass_rate?.enabled) rule.pass_rate = { ...rule.pass_rate, window_size: 1, recover_consecutive: 1, pause_below: 100, recover_at_least: 100, pause_on_single_failure: true }
    if (rule.vote) rule.vote.public_enabled = false
  }
}
