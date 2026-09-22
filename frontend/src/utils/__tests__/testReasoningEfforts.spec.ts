import { describe, expect, it } from 'vitest'
import { reasoningEffortsForTestModel } from '@/utils/testReasoningEfforts'

describe('reasoningEffortsForTestModel', () => {
  it('uses synced account metadata and intersects group capabilities', () => {
    const accounts = [
      { id: 1, group_ids: [7], extra: { upstream_model_metadata: { models: { 'gpt-6-astra': { supported_reasoning_levels: [{ effort: 'medium' }, { effort: 'high' }, { effort: 'ultra' }] } } } } },
      { id: 2, group_ids: [7], extra: { upstream_model_metadata: { models: { 'gpt-6-astra': { supported_reasoning_levels: ['high', 'ultra'] } } } } },
    ]
    expect(reasoningEffortsForTestModel('gpt-6-astra', accounts, 7)).toEqual(['high', 'ultra'])
  })

  it('falls back to common Codex choices when metadata is unavailable', () => {
    expect(reasoningEffortsForTestModel('gpt-6-astra')).toEqual(['low', 'medium', 'high', 'xhigh', 'max', 'ultra'])
    expect(reasoningEffortsForTestModel('gpt-5.6-codex')).toEqual(['low', 'medium', 'high', 'xhigh', 'max'])
    expect(reasoningEffortsForTestModel('gpt-4o')).toEqual([])
  })

  it('selects only the requested account when an account target is set', () => {
    const accounts = [
      { id: 1, group_ids: [7], extra: { upstream_model_metadata: { models: { o3: { supported_reasoning_levels: ['low', 'high'] } } } } },
      { id: 2, group_ids: [7], extra: { upstream_model_metadata: { models: { o3: { supported_reasoning_levels: ['high', 'xhigh'] } } } } },
    ]
    expect(reasoningEffortsForTestModel('o3', accounts, 7, 2)).toEqual(['high', 'xhigh'])
  })
})


it('does not replace an explicit no-reasoning capability with the model-family fallback', () => {
  const accounts = [{ id: 1, group_ids: [7], extra: { upstream_model_metadata: { models: { 'gpt-6-astra': { reasoning: false } } } } }]
  expect(reasoningEffortsForTestModel('gpt-6-astra', accounts, 7)).toEqual([])
})

it('intersects known capabilities with the fallback for accounts whose metadata is missing', () => {
  const accounts = [
    { id: 1, group_ids: [7], extra: { upstream_model_metadata: { models: { 'gpt-6-astra': { supported_reasoning_levels: ['high'] } } } } },
    { id: 2, group_ids: [7] },
  ]
  expect(reasoningEffortsForTestModel('gpt-6-astra', accounts, 7)).toEqual(['high'])
})
