import { describe, expect, it } from 'vitest'
import type { TestResult } from '@/types'
import { testResultState } from '../testResultPresentation'

const result = (extra: Partial<TestResult> = {}): TestResult => ({
  id: 1, status: 'success', output_kind: 'number', ...extra,
})

describe('testResultState', () => {
  it('does not infer quality from transport, scheduling or recent observations', () => {
    expect(testResultState(result())).toBe('pending')
    expect(testResultState(result({ model_schedulable: true, account_schedulable: true }))).toBe('pending')
    expect(testResultState(result({ pass_rate: { latest_verdict: 'pass' } as TestResult['pass_rate'] }))).toBe('pending')
  })

  it.each(['running', 'pending'])('keeps %s execution ahead of an older final verdict', status => {
    expect(testResultState(result({ status, quality_verdict: 'pass' }))).toBe('running')
  })

  it('keeps skipped detection separate from execution and quality failures', () => {
    expect(testResultState(result({ status: 'failed', execution_skipped: true, quality_verdict: 'fail' }))).toBe('skipped')
    expect(testResultState(result({ status: 'failed', error_message: '检测跳过：上轮失败' }))).toBe('skipped')
    expect(testResultState(result({ status: 'failed', execution_info: { failure_kind: 'quality_deferred' } }))).toBe('skipped')
    expect(testResultState(result({ protection_decision: { status: 'skipped', verdict: 'fail' } }))).toBe('skipped')
  })

  it.each(['failed', 'error'])('reports %s execution as an execution failure', status => {
    expect(testResultState(result({ status, quality_verdict: 'pass' }), 'pass')).toBe('error')
  })

  it('does not publish a preliminary pass when committing the decision failed', () => {
    expect(testResultState(result({ quality_verdict: 'pass', protection_decision: { status: 'error', verdict: 'pass' } }), 'pass')).toBe('pending')
  })

  it.each(['pass', 'fail', 'pending'] as const)('prefers the final public %s verdict over review evidence', value => {
    expect(testResultState(result({ quality_verdict: value, protection_decision: { status: 'unchanged', verdict: 'pass' } }), 'pass')).toBe(value)
  })

  it('preserves duration vetoes and administrator final decisions over visual evidence', () => {
    const visual = { auto_review: { verdict: 'pass' } } as TestResult['execution_info']
    expect(testResultState(result({ protection_decision: { status: 'unchanged', verdict: 'fail' }, execution_info: visual }), 'pass')).toBe('fail')
    expect(testResultState(result({ execution_info: { auto_review: { verdict: 'fail' } } as TestResult['execution_info'] }), 'pass')).toBe('pass')
  })

  it('uses intrinsic capability and model verdicts when there is no final decision', () => {
    expect(testResultState(result({ output_kind: 'model_check', output_model_check: { verdict: 'fail' } as TestResult['output_model_check'] }))).toBe('fail')
    expect(testResultState(result({ output_kind: 'capability', execution_info: { capability: { verdict: 'pass' } } as TestResult['execution_info'] }))).toBe('pass')
  })

  it('keeps visual passes pending without a complete final decision', () => {
    expect(testResultState(result({ execution_info: { auto_review: { verdict: 'pass' } } as TestResult['execution_info'] }))).toBe('pending')
    expect(testResultState(result({ execution_info: { auto_review: { verdict: 'fail' } } as TestResult['execution_info'] }))).toBe('fail')
  })
})
