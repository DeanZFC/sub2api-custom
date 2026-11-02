export type AccountQualityModelStatus = {
  model: string
  status: 'normal' | 'blocked' | 'pending'
  reason: string
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

// Holds remain authoritative while older accounts or scheduler snapshots have
// not yet received the richer status projection. Missing evidence is pending.
export function accountQualityModels(extra?: Record<string, unknown>): AccountQualityModelStatus[] {
  const models = new Map<string, AccountQualityModelStatus>()
  for (const [model, raw] of Object.entries(record(extra?.active_test_model_states) || {})) {
    if (!model) continue
    const state = record(raw)
    const status = state?.status
    models.set(model, {
      model,
      status: status === 'normal' || status === 'blocked' ? status : 'pending',
      reason: typeof state?.reason === 'string' ? state.reason : '',
    })
  }
  for (const field of ['active_test_model_holds', 'active_pass_rate_holds']) {
    for (const [model, value] of Object.entries(record(extra?.[field]) || {})) {
      if (!model || value === false || record(value)?.blocked === false) continue
      const reason = typeof value === 'string' ? value : record(value)?.reason
      models.set(model, { model, status: 'blocked', reason: typeof reason === 'string' ? reason : models.get(model)?.reason || '' })
    }
  }
  return Array.from(models.values()).sort((a, b) => a.model.localeCompare(b.model))
}
