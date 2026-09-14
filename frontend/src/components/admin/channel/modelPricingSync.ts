import type { ModelDefaultPricing } from '@/api/admin/channels'
import { createDefaultTimePricingForm, perTokenToMTok, type PricingFormEntry } from './types'

type DefaultPrices = Map<string, ModelDefaultPricing | null>

// Limit requests because a provider catalog can contain hundreds of models.
export async function loadModelDefaultPrices(
  models: string[],
  fetchPricing: (model: string) => Promise<ModelDefaultPricing>,
): Promise<DefaultPrices> {
  const pending = [...new Set(models)]
  const prices: DefaultPrices = new Map()
  let next = 0
  await Promise.all(Array.from({ length: Math.min(6, pending.length) }, async () => {
    while (next < pending.length) {
      const model = pending[next++]
      const pricing = await fetchPricing(model)
      prices.set(model, pricing.found ? pricing : null)
    }
  }))
  return prices
}

function defaultPriceFields(pricing?: ModelDefaultPricing | null) {
  return {
    input_price: perTokenToMTok(pricing?.input_price),
    output_price: perTokenToMTok(pricing?.output_price),
    cache_write_price: perTokenToMTok(pricing?.cache_write_price),
    cache_write_1h_price: perTokenToMTok(pricing?.cache_write_1h_price),
    cache_read_price: perTokenToMTok(pricing?.cache_read_price),
    image_input_price: perTokenToMTok(pricing?.image_input_price),
    image_output_price: perTokenToMTok(pricing?.image_output_price),
    max_reasoning_effort_multiplier: pricing?.max_reasoning_effort_multiplier ?? null,
  }
}

export function createPricingEntry(models: string[], pricing?: ModelDefaultPricing | null): PricingFormEntry {
  return {
    models: [...models],
    billing_mode: 'token',
    ...defaultPriceFields(pricing),
    fast_multiplier: null,
    flex_multiplier: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm(),
  }
}

function groupByDefaultPrice(models: string[], prices: DefaultPrices): string[][] {
  const groups = new Map<string, string[]>()
  for (const model of models) {
    const pricing = prices.get(model)
    // Unknown models stay independent; an eventual autofill must not assign
    // one newly recognised model's prices to every unknown model.
    const signature = pricing ? JSON.stringify(defaultPriceFields(pricing)) : `unknown:${model}`
    const group = groups.get(signature)
    if (group) group.push(model)
    else groups.set(signature, [model])
  }
  return [...groups.values()]
}

export function createModelPricingEntries(models: string[], prices: DefaultPrices): PricingFormEntry[] {
  return groupByDefaultPrice(models, prices).map(group => createPricingEntry(group, prices.get(group[0])))
}

export function isSharedDeepSeekEntry(entry: PricingFormEntry): boolean {
  return entry.billing_mode === 'token' && entry.models.length > 1 && entry.models.every(model => {
    const name = model.trim().toLowerCase().split('/').pop() || ''
    return name.startsWith('deepseek-') && !name.includes('*')
  })
}

function cloneEntry(entry: PricingFormEntry): PricingFormEntry {
  return {
    ...entry,
    models: [...entry.models],
    intervals: entry.intervals.map(interval => ({ ...interval })),
    time_pricing: {
      ...entry.time_pricing,
      periods: entry.time_pricing.periods.map(period => ({ ...period })),
    },
  }
}

// Called only by the explicit split action. Opening a channel or syncing new
// model names must not change an administrator's existing selling prices.
export function splitDeepSeekPricingEntries(entries: PricingFormEntry[], prices: DefaultPrices) {
  let splitCount = 0
  const result = entries.flatMap(entry => {
    if (!isSharedDeepSeekEntry(entry)) return [entry]
    const known = entry.models.filter(model => prices.get(model))
    const groups = groupByDefaultPrice(known, prices)
    if (groups.length < 2) return [entry]
    splitCount++
    const split: PricingFormEntry[] = groups.map(models => ({
      ...cloneEntry(entry),
      models,
      ...defaultPriceFields(prices.get(models[0])),
    }))
    const unknown = entry.models.filter(model => !prices.get(model))
    if (unknown.length) split.push({ ...cloneEntry(entry), models: unknown })
    return split
  })
  return { entries: result, splitCount }
}
