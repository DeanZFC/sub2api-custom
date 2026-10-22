export interface CapabilityQuestion { key: string; prompt: string; expected: string; match: 'exact' | 'number' }
export interface CapabilityConfig { version: number; samples: number; required_passes: number; families: string[]; questions: CapabilityQuestion[] }
export interface CapabilityResult { version: number; seed: string; samples: number; required_passes: number; passed: number; verdict: string; answers: (CapabilityQuestion & { answer: string; verdict: string })[] }
export const defaultCapabilityConfig = (): CapabilityConfig => ({ version: 1, samples: 3, required_passes: 2, families: ['candy', 'state', 'instructions'], questions: [] })
export function parseCapabilityConfig(raw: string): CapabilityConfig | null {
  try {
    const c = JSON.parse(raw) as CapabilityConfig
    if (!c || c.version !== 1 || !Array.isArray(c.families) || !c.families.every(f => typeof f === 'string') || (c.questions != null && (!Array.isArray(c.questions) || !c.questions.every(q => q && ['key', 'prompt', 'expected', 'match'].every(k => typeof q[k as keyof CapabilityQuestion] === 'string'))))) return null
    return { ...c, questions: c.questions || [] }
  } catch { return null }
}
export function validCapabilityConfig(c: CapabilityConfig | null): boolean {
  return !!c && Number.isInteger(c.samples) && c.samples >= 1 && c.samples <= 5 && Number.isInteger(c.required_passes) && c.required_passes >= 1 && c.required_passes <= c.samples
    && new Set(c.families).size === c.families.length && c.families.every(f => ['candy', 'state', 'instructions'].includes(f))
    && c.questions.length <= 50 && (c.families.length > 0 || c.questions.length >= c.samples) && new Set(c.questions.map(q => q.key.trim())).size === c.questions.length
    && c.questions.every(q => q.key.trim().length > 0 && q.key.length <= 100 && q.prompt.trim().length > 0 && q.prompt.length <= 8000 && q.expected.trim().length > 0 && q.expected.length <= 2000 && ['exact', 'number'].includes(q.match) && (q.match !== 'number' || validInteger(q.expected.trim())))
}

function validInteger(value: string): boolean {
  if (!/^[+-]?\d+$/.test(value)) return false
  const n = BigInt(value)
  return n >= -9223372036854775808n && n <= 9223372036854775807n
}
