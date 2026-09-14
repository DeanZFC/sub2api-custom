import { describe, expect, it, vi } from 'vitest'
import type { ModelDefaultPricing } from '@/api/admin/channels'
import { createModelPricingEntries, createPricingEntry, loadModelDefaultPrices, splitDeepSeekPricingEntries } from '../modelPricingSync'

const flash: ModelDefaultPricing = {
  found: true, input_price: 1.5e-7, output_price: 6e-7, cache_read_price: 3e-9,
}
const pro: ModelDefaultPricing = {
  found: true, input_price: 6.6e-7, output_price: 1.98e-6, cache_read_price: 2.2e-8,
}
const prices = new Map([
  ['deepseek-flash', flash],
  ['deepseek-v4.1-flash', flash],
  ['deepseek-v4-pro', pro],
])

describe('channel model default price synchronization', () => {
  it('creates separate Pro and Flash rules, converting per-token prices to USD per million', () => {
    const entries = createModelPricingEntries([...prices.keys()], prices)
    expect(entries).toHaveLength(2)
    expect(entries[0]).toMatchObject({
      models: ['deepseek-flash', 'deepseek-v4.1-flash'], input_price: 0.15, output_price: 0.6, cache_read_price: 0.003,
    })
    expect(entries[1]).toMatchObject({ models: ['deepseek-v4-pro'], input_price: 0.66, output_price: 1.98, cache_read_price: 0.022 })
  })

  it('repairs the shared legacy card while keeping unknown model prices and custom rules', () => {
    const legacy = createPricingEntry([...prices.keys(), 'deepseek-private'], flash)
    legacy.fast_multiplier = 1.5
    legacy.time_pricing.periods = [{ start_time: '09:00:00', end_time: '10:00:00', multiplier: 2 }]
    const manual = createPricingEntry(['deepseek-v4-pro-private'], pro)
    manual.input_price = 9
    const other = createPricingEntry(['gpt-5', 'gpt-6'], flash)
    const original = JSON.stringify([legacy, manual, other])
    const result = splitDeepSeekPricingEntries([legacy, manual, other], prices)
    expect(result.splitCount).toBe(1)
    expect(result.entries).toHaveLength(5)
    expect(result.entries[1]).toMatchObject({ models: ['deepseek-v4-pro'], input_price: 0.66, fast_multiplier: 1.5 })
    expect(result.entries[2]).toMatchObject({ models: ['deepseek-private'], input_price: 0.15 })
    expect(result.entries[3]).toBe(manual)
    expect(result.entries[4]).toBe(other)
    expect(JSON.stringify([legacy, manual, other])).toBe(original)
    result.entries[1].time_pricing.periods[0].multiplier = 4
    expect(result.entries[0].time_pricing.periods[0].multiplier).toBe(2)
    expect(legacy.time_pricing.periods[0].multiplier).toBe(2)
  })

  it('does not rewrite another custom card just because a mixed card needs splitting', () => {
    const mixed = createPricingEntry(['deepseek-flash', 'deepseek-v4-pro'], flash)
    const same = createPricingEntry(['deepseek-flash', 'deepseek-v4.1-flash'], flash)
    same.input_price = 8
    // Both orders guard against a previous card masking the changed flag.
    for (const entries of [[mixed, same], [same, mixed]]) {
      const result = splitDeepSeekPricingEntries(entries, prices)
      expect(result.splitCount).toBe(1)
      expect(result.entries).toContain(same)
      expect(same.input_price).toBe(8)
    }
  })

  it('keeps wildcard, non-token and unknown models untouched', () => {
    const wildcard = createPricingEntry(['deepseek-*', 'deepseek-v4-pro'], flash)
    const image = { ...createPricingEntry(['deepseek-flash', 'deepseek-v4-pro'], flash), billing_mode: 'image' as const }
    const unknown = createPricingEntry(['deepseek-one', 'deepseek-two'], flash)
    const result = splitDeepSeekPricingEntries([wildcard, image, unknown], prices)
    expect(result.splitCount).toBe(0)
    expect(result.entries).toEqual([wildcard, image, unknown])
    expect(createModelPricingEntries(['unknown-a', 'unknown-b'], new Map())).toHaveLength(2)
  })

  it('keeps models with different cache or effort prices in separate rules', () => {
    const defaults = new Map([
      ['a', flash], ['b', { ...flash, cache_read_price: 0 }],
      ['c', { ...flash, max_reasoning_effort_multiplier: 2 }],
    ])
    expect(createModelPricingEntries([...defaults.keys()], defaults)).toHaveLength(3)
  })

  it('deduplicates lookups and bounds concurrent requests', async () => {
    let active = 0
    let maxActive = 0
    const fetch = vi.fn(async () => {
      active++
      maxActive = Math.max(maxActive, active)
      await new Promise(resolve => setTimeout(resolve, 0))
      active--
      return flash
    })
    const models = Array.from({ length: 18 }, (_, index) => `model-${index}`)
    const result = await loadModelDefaultPrices([...models, ...models], fetch)
    expect(result.size).toBe(18)
    expect(fetch).toHaveBeenCalledTimes(18)
    expect(maxActive).toBeLessThanOrEqual(6)
  })

  it('propagates lookup failures so the form is not partially rewritten', async () => {
    await expect(loadModelDefaultPrices(['deepseek-v4-pro'], async () => { throw new Error('offline') })).rejects.toThrow('offline')
    expect(await loadModelDefaultPrices(['missing'], async () => ({ found: false }))).toEqual(new Map([['missing', null]]))
  })
})
