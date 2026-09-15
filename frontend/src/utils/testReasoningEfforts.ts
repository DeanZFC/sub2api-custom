/**
 * Resolve reasoning-effort choices for a scheduled test model.
 *
 * Account model syncs persist upstream metadata under
 * `extra.upstream_model_metadata.models`; use that as the source of truth
 * when available. The fallback intentionally mirrors the gateway's common
 * Codex/OpenAI effort catalog so a newly typed model remains usable before
 * metadata has been synced.
 */

export const COMMON_REASONING_EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max', 'ultra'] as const

export interface TestModelMetadata {
  id?: unknown
  reasoning?: unknown
  default_reasoning_level?: unknown
  supported_reasoning_levels?: unknown
}

function normalizeEffort(value: unknown): string {
  if (typeof value !== 'string') return ''
  const normalized = value.trim().toLowerCase().replace(/[-_\s]/g, '')
  if (normalized === 'extrahigh' || normalized === 'veryhigh') return 'xhigh'
  if (normalized === 'ultracode') return 'ultra'
  return normalized
}

function metadataModels(extra: Record<string, unknown> | undefined): TestModelMetadata[] {
  const snapshot = extra?.upstream_model_metadata
  if (!snapshot || typeof snapshot !== 'object') return []
  const models = (snapshot as { models?: unknown }).models
  if (!models || typeof models !== 'object') return []
  return Object.entries(models as Record<string, unknown>).flatMap(([id, raw]) => {
    if (!raw || typeof raw !== 'object') return []
    return [{ ...(raw as TestModelMetadata), id: (raw as TestModelMetadata).id || id }]
  })
}

function metadataForModel(extra: Record<string, unknown> | undefined, modelID: string): TestModelMetadata | undefined {
  const wanted = modelID.trim().toLowerCase()
  if (!wanted) return undefined
  const exact = metadataModels(extra).find((entry) => String(entry.id ?? '').trim().toLowerCase() === wanted)
  if (exact) return exact
  // A model-list may expose a dated model while the plan uses its stable alias.
  return metadataModels(extra).find((entry) => {
    const id = String(entry.id ?? '').trim().toLowerCase()
    return id && (id.startsWith(`${wanted}-`) || wanted.startsWith(`${id}-`))
  })
}

function metadataEfforts(metadata: TestModelMetadata | undefined): string[] {
  if (!metadata) return []
  if (metadata.reasoning === false) return []
  const raw = metadata.supported_reasoning_levels
  if (!Array.isArray(raw)) return []
  const efforts = raw.flatMap((level) => {
    const value = level && typeof level === 'object' && 'effort' in level
      ? (level as { effort?: unknown }).effort
      : level
    const normalized = normalizeEffort(value)
    return normalized ? [normalized] : []
  })
  return Array.from(new Set(efforts))
}

function fallbackEfforts(modelID: string): string[] {
  const model = modelID.trim().toLowerCase()
  if (!model) return []
  // Mirror the configured Codex catalog when no per-account metadata has been
  // synced yet. Ultra is intentionally limited to the models that advertise
  // it; showing an option the backend will reject is worse than hiding it.
  if (/gpt-6(?:-|$)/.test(model) || /gpt-5\.6-(?:sol|terra)(?:-|$)/.test(model)) {
    return [...COMMON_REASONING_EFFORTS]
  }
  if (/gpt-5\.6(?:-|$)/.test(model)) {
    return COMMON_REASONING_EFFORTS.filter((effort) => effort !== 'ultra')
  }
  if (model.startsWith('claude-') || model.includes('/claude-')) {
    if (/haiku/.test(model)) return []
    if (/opus-5/.test(model)) return ['low', 'medium', 'high', 'xhigh', 'max']
    return ['low', 'medium', 'high', 'max']
  }
  if (model.startsWith('grok-') || model.includes('/grok-')) {
    return /grok-4\.6/.test(model) ? ['low', 'medium', 'high', 'xhigh'] : ['low', 'medium', 'high']
  }
  if (model.includes('deepseek')) {
    return ['low', 'high', 'max']
  }
  if (/(?:gpt-5(?:\.|-|$)|o[1-9](?:-|$)|codex)/.test(model) || /(?:reasoning|thinking|deepseek-r1|deepseek-v[3-9]|qwq|glm-5|kimi-k2-thinking)/.test(model)) {
    return COMMON_REASONING_EFFORTS.filter((effort) => effort !== 'max' && effort !== 'ultra')
  }
  return []
}

export interface TestReasoningAccountLike {
  extra?: Record<string, unknown>
  group_ids?: number[]
  id?: number
}

/**
 * Return options in ascending effort order. For a group plan, only efforts
 * advertised by every account with metadata are retained, preventing a
 * choice that one of the scheduled accounts cannot accept. If no account has
 * synced metadata, use the model-family fallback.
 */
export function reasoningEffortsForTestModel(
  modelID: string,
  accounts: TestReasoningAccountLike[] = [],
  groupID?: number | null,
  accountID?: number | null,
): string[] {
  const candidates = accountID
    ? accounts.filter((account) => account.id === accountID)
    : groupID
      ? accounts.filter((account) => account.group_ids?.includes(Number(groupID)))
      : accounts
  const metadataChoices = candidates.map((account) => metadataEfforts(metadataForModel(account.extra, modelID)))

  // A group run must be safe for every selected account. If even one account
  // has no synced capability snapshot, use the model-family fallback instead
  // of showing a level that may fail on that account.
  if (metadataChoices.length > 0 && metadataChoices.every((choices) => choices.length > 0)) {
    const common = metadataChoices.slice(1).reduce((result, choices) => result.filter((effort) => choices.includes(effort)), metadataChoices[0])
    return orderEfforts(common)
  }
  return orderEfforts(fallbackEfforts(modelID))
}

function orderEfforts(efforts: string[]): string[] {
  const rank = new Map<string, number>(COMMON_REASONING_EFFORTS.map((effort, index) => [effort, index]))
  return Array.from(new Set(efforts)).sort((a, b) => (rank.get(a) ?? 999) - (rank.get(b) ?? 999))
}
