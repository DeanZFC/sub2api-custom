import { describe, expect, it } from 'vitest'
import { compareMonitorModels } from '../monitorModels'

describe('monitor model ordering', () => {
  it.each([
    [['gpt-5.3', 'gpt-5', 'gpt-5.4'], ['gpt-5.4', 'gpt-5.3', 'gpt-5']],
    [['claude-sonnet-4-5', 'claude-opus-4-6', 'claude-3-7-sonnet'], ['claude-opus-4-6', 'claude-sonnet-4-5', 'claude-3-7-sonnet']],
    [['gemini-2.5-pro', 'gemini-3.1-pro', 'gemini-3-pro'], ['gemini-3.1-pro', 'gemini-3-pro', 'gemini-2.5-pro']],
    [['gpt-5-20251001', 'gpt-5.4-20261001', 'gpt-5.4-20260901'], ['gpt-5.4-20261001', 'gpt-5.4-20260901', 'gpt-5-20251001']],
  ])('orders recognizable versions and date suffixes newest first', (input, expected) => {
    expect([...input].sort(compareMonitorModels)).toEqual(expected)
  })

  it('keeps a deterministic order for unrelated or unknown models', () => {
    const names = ['custom', 'gpt-5.4', 'gpt-5', 'another', 'claude-opus-4-6', 'gpt-5.4-20261001']
    expect([...names].reverse().sort(compareMonitorModels)).toEqual([...names].sort(compareMonitorModels))
  })
})
