import { describe, expect, it } from 'vitest'
import { readExcelBPSConfig, writeExcelBPSConfig } from '../excelBps'

describe('BPS account policy', () => {
  it('distinguishes legacy all-model routing, an empty scope, and explicit models', () => {
    expect(readExcelBPSConfig({ openai_excel_bps: true }).allModels).toBe(true)
    expect(readExcelBPSConfig({ openai_excel_bps: true, openai_excel_bps_models: [] })).toMatchObject({ allModels: false, models: [] })
    const config = readExcelBPSConfig({ openai_excel_bps: true, openai_excel_bps_models: [' one ', 'one', '', 'two'] })
    expect(writeExcelBPSConfig(config).openai_excel_bps_models).toEqual(['one', 'two'])
  })

  it('preserves unrelated account fields while removing disabled BPS policy', () => {
    const base = { codex_cli_only: true, model_mapping: { a: 'b' }, openai_excel_bps: true, openai_excel_bps_models: ['a'], openai_excel_bps_ignore_images: true }
    expect(writeExcelBPSConfig(readExcelBPSConfig(), base)).toEqual({ codex_cli_only: true, model_mapping: { a: 'b' } })
    expect(base.openai_excel_bps).toBe(true)
  })

  it('uses explicit clearing values for server-side merged bulk updates', () => {
    const config = readExcelBPSConfig({ openai_excel_bps: true })
    expect(writeExcelBPSConfig(config, {}, true)).toMatchObject({ openai_excel_bps: true, openai_excel_bps_models: null, openai_excel_bps_ignore_images: false, openai_excel_bps_403_target_group_id: null })
    config.enabled = false
    expect(writeExcelBPSConfig(config, {}, true)).toMatchObject({ openai_excel_bps: false, openai_excel_bps_models: null, openai_excel_bps_auto_disable_on_403: false })
  })

  it('requires an explicit destination for automatic 403 group changes', () => {
    const config = readExcelBPSConfig({ openai_excel_bps: true, openai_excel_bps_auto_move_on_403: true })
    expect(() => writeExcelBPSConfig(config)).toThrow()
    config.targetGroupId = 0
    expect(writeExcelBPSConfig(config).openai_excel_bps_403_target_group_id).toBe(0)
  })
})
