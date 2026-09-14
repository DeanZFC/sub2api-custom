import { shallowMount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PricingEntryCard from '../PricingEntryCard.vue'
import type { PricingFormEntry } from '../types'

const channelsApiMock = vi.hoisted(() => ({
  getModelDefaultPricing: vi.fn(),
}))

vi.mock('@/api/admin/channels', () => ({ default: channelsApiMock }))

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

function createEntry(billingMode: PricingFormEntry['billing_mode'] = 'token'): PricingFormEntry {
  return {
    models: [],
    billing_mode: billingMode,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    fast_multiplier: null,
    flex_multiplier: null,
    max_reasoning_effort_multiplier: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: {
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00', end_time: '12:00', multiplier: '2.00' }],
    },
  }
}

function modelInput(wrapper: ReturnType<typeof shallowMount>) {
  return wrapper.findComponent({ name: 'ModelTagInput' })
}

function lastUpdate(wrapper: ReturnType<typeof shallowMount>): PricingFormEntry | undefined {
  const updates = wrapper.emitted('update')
  return updates?.at(-1)?.[0] as PricingFormEntry | undefined
}

async function flushAsyncWork() {
  await Promise.resolve()
  await Promise.resolve()
}

function defaultPricing(input = 0.000001, output = 0.000002) {
  return {
    found: true,
    input_price: input,
    output_price: output,
    cache_write_price: 0.0000005,
    cache_write_1h_price: null,
    cache_read_price: 0.0000001,
    image_input_price: 0.000003,
    image_output_price: 0.000004,
    max_reasoning_effort_multiplier: 1.2,
  }
}

function createBlankTokenEntry(): PricingFormEntry {
  const entry = createEntry()
  entry.time_pricing.periods = []
  return entry
}

describe('PricingEntryCard time pricing visibility', () => {
  it('is hidden by default', () => {
    const wrapper = shallowMount(PricingEntryCard, {
      props: { entry: createEntry() },
    })

    expect(wrapper.findComponent({ name: 'TimePricingSection' }).exists()).toBe(false)
  })

  it('is shown for token pricing when explicitly enabled', () => {
    const wrapper = shallowMount(PricingEntryCard, {
      props: { entry: createEntry(), enableTimePricing: true },
    })

    expect(wrapper.findComponent({ name: 'TimePricingSection' }).exists()).toBe(true)
  })

  it('is hidden for non-token pricing even when explicitly enabled', () => {
    const wrapper = shallowMount(PricingEntryCard, {
      props: { entry: createEntry('per_request'), enableTimePricing: true },
    })

    expect(wrapper.findComponent({ name: 'TimePricingSection' }).exists()).toBe(false)
  })

  it('clears time periods when changing billing mode', () => {
    const entry = createEntry()
    const wrapper = shallowMount(PricingEntryCard, {
      props: { entry, enableTimePricing: true },
    })

    wrapper.findComponent({ name: 'Select' }).vm.$emit('update:modelValue', 'image')

    expect(wrapper.emitted('update')?.[0]?.[0]).toEqual({
      ...entry,
      billing_mode: 'image',
      intervals: [],
      time_pricing: { timezone: 'Asia/Shanghai', periods: [] },
    })
    expect(entry.time_pricing.periods).toHaveLength(1)
  })
})

describe('PricingEntryCard request multipliers', () => {
  it('shows Fast, Flex, and Max effort controls only when explicitly enabled', () => {
    const hidden = shallowMount(PricingEntryCard, { props: { entry: createEntry() } })
    expect(hidden.text()).not.toContain('admin.channels.form.fastMultiplier')

    const shown = shallowMount(PricingEntryCard, {
      props: { entry: createEntry(), enableTierMultipliers: true },
    })
    expect(shown.text()).toContain('admin.channels.form.fastMultiplier')
    expect(shown.text()).toContain('admin.channels.form.flexMultiplier')
    expect(shown.text()).toContain('admin.channels.form.maxReasoningEffortMultiplier')
  })
})

describe('PricingEntryCard automatic default pricing', () => {
  it('does not use the first model price for a multi-model entry', async () => {
    channelsApiMock.getModelDefaultPricing.mockReset()
    const entry = createBlankTokenEntry()
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })

    modelInput(wrapper).vm.$emit('update:models', ['deepseek-v4', 'deepseek-v4.1-flash'])
    await flushAsyncWork()

    expect(channelsApiMock.getModelDefaultPricing).not.toHaveBeenCalled()
    expect(lastUpdate(wrapper)).toMatchObject({
      models: ['deepseek-v4', 'deepseek-v4.1-flash'],
      input_price: null,
      output_price: null,
    })
  })

  it('does not auto-fill when adding one model to an existing multi-model entry', async () => {
    channelsApiMock.getModelDefaultPricing.mockReset()
    const entry = createBlankTokenEntry()
    entry.models = ['deepseek-v4', 'deepseek-v4.1']
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })

    modelInput(wrapper).vm.$emit('update:models', [...entry.models, 'deepseek-v4.1-flash'])
    await wrapper.setProps({ entry: { ...entry, models: [...entry.models, 'deepseek-v4.1-flash'] } })
    await flushAsyncWork()

    expect(channelsApiMock.getModelDefaultPricing).not.toHaveBeenCalled()
    expect(lastUpdate(wrapper)).toMatchObject({
      models: ['deepseek-v4', 'deepseek-v4.1', 'deepseek-v4.1-flash'],
      input_price: null,
      output_price: null,
    })
  })

  it('fills a blank token entry when exactly one model is added', async () => {
    channelsApiMock.getModelDefaultPricing.mockReset()
    channelsApiMock.getModelDefaultPricing.mockResolvedValue(defaultPricing())
    const entry = createBlankTokenEntry()
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })

    modelInput(wrapper).vm.$emit('update:models', ['deepseek-v4.1-flash'])
    await wrapper.setProps({ entry: { ...entry, models: ['deepseek-v4.1-flash'] } })
    await flushAsyncWork()

    expect(channelsApiMock.getModelDefaultPricing).toHaveBeenCalledWith('deepseek-v4.1-flash')
    expect(lastUpdate(wrapper)).toMatchObject({
      models: ['deepseek-v4.1-flash'],
      input_price: 1,
      output_price: 2,
      cache_write_price: 0.5,
      cache_read_price: 0.1,
      image_input_price: 3,
      image_output_price: 4,
      max_reasoning_effort_multiplier: 1.2,
    })
  })

  it('leaves manually configured values untouched, including media, tiers, time rules, and multipliers', async () => {
    channelsApiMock.getModelDefaultPricing.mockReset()
    const entry = createBlankTokenEntry()
    entry.image_input_price = 8
    entry.per_request_price = 9
    entry.fast_multiplier = 1.1
    entry.intervals = [{
      min_tokens: 0,
      max_tokens: null,
      tier_label: 'custom',
      input_price: 7,
      output_price: null,
      cache_write_price: null,
      cache_read_price: null,
      input_multiplier: null,
      output_multiplier: null,
      cache_write_multiplier: null,
      cache_read_multiplier: null,
      per_request_price: null,
      sort_order: 0,
    }]
    entry.time_pricing.periods = [{ start_time: '09:00', end_time: '12:00', multiplier: '2.00' }]
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })

    modelInput(wrapper).vm.$emit('update:models', ['custom-model'])
    await flushAsyncWork()

    expect(channelsApiMock.getModelDefaultPricing).not.toHaveBeenCalled()
    expect(lastUpdate(wrapper)).toMatchObject({
      models: ['custom-model'],
      image_input_price: 8,
      per_request_price: 9,
      fast_multiplier: 1.1,
      intervals: entry.intervals,
      time_pricing: entry.time_pricing,
    })
  })

  it('ignores an old response after a manual edit while the lookup is pending', async () => {
    channelsApiMock.getModelDefaultPricing.mockReset()
    let resolvePricing!: (value: ReturnType<typeof defaultPricing>) => void
    channelsApiMock.getModelDefaultPricing.mockReturnValue(new Promise(resolve => {
      resolvePricing = resolve
    }))
    const entry = createBlankTokenEntry()
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })

    modelInput(wrapper).vm.$emit('update:models', ['deepseek-v4'])
    await wrapper.setProps({ entry: { ...entry, models: ['deepseek-v4'] } })
    const input = wrapper.find('input[type="number"]')
    await input.setValue('9')
    const updateCountAfterManualEdit = wrapper.emitted('update')?.length ?? 0

    resolvePricing(defaultPricing())
    await flushAsyncWork()

    expect(wrapper.emitted('update')?.length).toBe(updateCountAfterManualEdit)
    expect(lastUpdate(wrapper)).toMatchObject({ models: ['deepseek-v4'], input_price: '9' })
  })

  it('ignores a response for a model that was replaced during lookup', async () => {
    channelsApiMock.getModelDefaultPricing.mockReset()
    let resolveFirst!: (value: ReturnType<typeof defaultPricing>) => void
    let resolveSecond!: (value: ReturnType<typeof defaultPricing>) => void
    channelsApiMock.getModelDefaultPricing
      .mockReturnValueOnce(new Promise(resolve => { resolveFirst = resolve }))
      .mockReturnValueOnce(new Promise(resolve => { resolveSecond = resolve }))
    const entry = createBlankTokenEntry()
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })

    modelInput(wrapper).vm.$emit('update:models', ['model-a'])
    await wrapper.setProps({ entry: { ...entry, models: ['model-a'] } })
    modelInput(wrapper).vm.$emit('update:models', ['model-b'])
    await wrapper.setProps({ entry: { ...entry, models: ['model-b'] } })

    resolveFirst(defaultPricing(1, 2))
    await flushAsyncWork()
    expect(lastUpdate(wrapper)?.input_price).not.toBe(1_000_000)

    resolveSecond(defaultPricing(0.000003, 0.000004))
    await flushAsyncWork()
    expect(lastUpdate(wrapper)).toMatchObject({ models: ['model-b'], input_price: 3, output_price: 4 })
  })

  it('does not emit a stale update after the entry is removed while lookup is pending', async () => {
    channelsApiMock.getModelDefaultPricing.mockReset()
    let resolvePricing!: (value: ReturnType<typeof defaultPricing>) => void
    channelsApiMock.getModelDefaultPricing.mockReturnValue(new Promise(resolve => {
      resolvePricing = resolve
    }))
    const entry = createBlankTokenEntry()
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })

    modelInput(wrapper).vm.$emit('update:models', ['model-to-remove'])
    await wrapper.setProps({ entry: { ...entry, models: ['model-to-remove'] } })
    wrapper.find('button').trigger('click')
    const updateCountAfterRemove = wrapper.emitted('update')?.length ?? 0

    resolvePricing(defaultPricing())
    await flushAsyncWork()

    expect(wrapper.emitted('remove')).toHaveLength(1)
    expect(wrapper.emitted('update')?.length).toBe(updateCountAfterRemove)
  })

  it('does not emit a stale update after unmount while lookup is pending', async () => {
    channelsApiMock.getModelDefaultPricing.mockReset()
    let resolvePricing!: (value: ReturnType<typeof defaultPricing>) => void
    channelsApiMock.getModelDefaultPricing.mockReturnValue(new Promise(resolve => {
      resolvePricing = resolve
    }))
    const entry = createBlankTokenEntry()
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })

    modelInput(wrapper).vm.$emit('update:models', ['model-to-unmount'])
    await wrapper.setProps({ entry: { ...entry, models: ['model-to-unmount'] } })
    wrapper.unmount()
    const updateCountAfterUnmount = wrapper.emitted('update')?.length ?? 0

    resolvePricing(defaultPricing())
    await flushAsyncWork()

    expect(wrapper.emitted('update')?.length ?? 0).toBe(updateCountAfterUnmount)
  })
})
