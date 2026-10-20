import type { TestResult } from '@/types'

export type TestResultsState = 'loading' | 'ready' | 'error'
export interface GroupTestResults {
  candy: TestResult[]
  pelican: TestResult[]
}
export const emptyGroupTests: GroupTestResults = { candy: [], pelican: [] }

function testKind(result: TestResult): 'candy' | 'pelican' | undefined {
  const key = result.test_key || result.test_definition?.key
  if (key) return key === 'candy' || key === 'pelican' ? key : undefined
  // Compatibility with older servers; do not mistake arbitrary HTML/number tests for these checks.
  const name = result.test_name || result.test_definition?.name || ''
  if (result.output_kind === 'number' && /糖果|candy/i.test(name)) return 'candy'
  if (result.output_kind === 'html' && /鹈鹕|pelican/i.test(name)) return 'pelican'
}

function resultTime(result: TestResult) {
  return Date.parse(result.started_at || result.finished_at || result.created_at || '') || 0
}

export function indexGroupTestResults(results: TestResult[]): Map<number, GroupTestResults> {
  const groups = new Map<number, GroupTestResults>()
  const seen = new Set<string>()
  // The API supplies current checks. Deduplicate per series, never fall back to an older pass.
  for (const result of [...results].sort((a, b) => resultTime(b) - resultTime(a) || b.id - a.id)) {
    const kind = testKind(result)
    if (!kind || !result.group_id || !['success', 'passed', 'failed', 'error'].includes(result.status)) continue
    const series = JSON.stringify([result.group_id, result.account_id ?? 'group', result.test_definition_id ?? kind, result.model_id, result.reasoning_effort || ''])
    if (seen.has(series)) continue
    seen.add(series)
    let group = groups.get(result.group_id)
    if (!group) {
      group = { candy: [], pelican: [] }
      groups.set(result.group_id, group)
    }
    group[kind].push(result)
  }
  return groups
}

export function testVerdict(result: TestResult): 'pass' | 'fail' | 'pending' {
  if (result.status === 'failed' || result.status === 'error') return 'fail'
  return result.quality_verdict === 'pass' || result.quality_verdict === 'fail' ? result.quality_verdict : 'pending'
}

export function candySummary(results: TestResult[]) {
  const passed = results.filter(result => testVerdict(result) === 'pass').length
  const unknown = results.filter(result => testVerdict(result) === 'pending').length
  return { passed, total: results.length, unknown,
    percent: results.length && !unknown ? Math.round(passed / results.length * 100) : null }
}

export function pelicanAccountCount(results: TestResult[]): number {
  return new Set(results.flatMap(result => result.account_id == null ? [] : [result.account_id])).size
}
