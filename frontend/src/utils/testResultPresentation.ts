import type { TestResult } from '@/types'

export type TestResultState = 'pass' | 'fail' | 'pending' | 'running' | 'skipped' | 'error'

const verdict = (value?: string | null): 'pass' | 'fail' | 'pending' | undefined =>
  value === 'pass' || value === 'fail' || value === 'pending' ? value : undefined

/** Execution, the saved final decision, and review evidence have distinct roles. */
export function testResultState(result: TestResult, reviewVerdict?: string): TestResultState {
  if (result.status === 'running' || result.status === 'pending') return 'running'
  if (result.execution_skipped || result.execution_info?.failure_kind === 'quality_deferred'
    || result.protection_decision?.status === 'skipped'
    || (result.status === 'failed' && (result.error_message || '').startsWith('检测跳过：'))) return 'skipped'
  if (result.status !== 'success' && result.status !== 'passed') return 'error'
  if (result.protection_decision?.status === 'error') return 'pending'

  const finalVerdict = verdict(result.quality_verdict)
    ?? verdict(result.protection_decision?.verdict)
    ?? verdict(reviewVerdict)
  if (finalVerdict) return finalVerdict

  if (result.output_kind === 'model_check') return verdict(result.output_model_check?.verdict) ?? 'pending'
  if (result.output_kind === 'capability') return verdict(result.execution_info?.capability?.verdict) ?? 'pending'
  // A visual pass can still await voting or a duration decision. A visual
  // failure remains a failure until the saved final decision overrides it.
  if (result.execution_info?.auto_review?.verdict === 'fail') return 'fail'
  return 'pending'
}
