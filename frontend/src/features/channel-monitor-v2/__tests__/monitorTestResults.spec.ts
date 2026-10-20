import { describe, expect, it } from 'vitest'
import { candySummary, indexGroupTestResults, pelicanAccountCount } from '../monitorTestResults'
import type { TestResult } from '@/types'

const result = (id: number, extra: Partial<TestResult> = {}): TestResult => ({
  id, group_id: 7, account_id: id, test_key: 'candy', output_kind: 'number', status: 'success', ...extra,
})

describe('monitor saved tests', () => {
  it('keeps groups isolated, uses check identity, and selects the newest result even when it fails', () => {
    const indexed = indexGroupTestResults([
      result(1, { account_id: 10, quality_verdict: 'pass' }),
      result(2, { account_id: 10, status: 'error' }),
      result(3, { group_id: 8 }),
      result(4, { group_id: null }),
      result(5, { test_key: 'other', test_name: '糖果数字测试' }),
      result(6, { test_key: '', test_name: '糖果数字测试' }),
      result(7, { status: 'running' }),
      result(8, { test_key: 'pelican', output_kind: 'html' }),
      result(9, { test_key: 'pelican', output_kind: 'html', model_id: 'model-b' }),
    ])
    expect(indexed.get(7)?.candy.map(row => row.id)).toEqual([6, 2])
    expect(indexed.get(8)?.candy.map(row => row.id)).toEqual([3])
    expect(indexed.get(7)?.pelican.map(row => row.id)).toEqual([9, 8])
  })

  it('never treats a successful HTTP request or an unknown verdict as a correct candy answer', () => {
    expect(candySummary([result(1, { quality_verdict: 'pass' }), result(2)])).toEqual({ passed: 1, total: 2, unknown: 1, percent: null })
    expect(candySummary([result(1, { quality_verdict: 'pass' }), result(2, { quality_verdict: 'pass', status: 'failed' })])).toEqual({ passed: 1, total: 2, unknown: 0, percent: 50 })
    expect(candySummary([]).percent).toBeNull()
  })

  it('counts distinct accounts across models and keeps group-only results separate', () => {
    expect(pelicanAccountCount([result(1), result(2, { account_id: 1 }), result(3, { account_id: null })])).toBe(1)
  })
})
