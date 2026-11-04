/** Editable BPS policy; an absent model scope means legacy all-model routing. */
export interface ExcelBPSConfig {
  enabled: boolean
  allModels: boolean
  models: string[]
  sessionProxy: boolean
  proxySource: 'mihomo' | 'ip_pool'
  cacheCreationAsInput: boolean
  autoDisableOn403: boolean
  autoRecoverOn403: boolean
  recoveryIntervalMinutes: number
  recoveryPending: boolean
  omitUnsupportedTools: boolean
  ignoreImages: boolean
  ignoreEncryptedContent: boolean
  autoMoveOn403: boolean
  targetGroupId: number | ''
}

export const EXCEL_BPS_EXTRA_KEYS = [
  'openai_excel_bps', 'openai_excel_bps_models', 'openai_excel_bps_mihomo',
  'openai_excel_bps_proxy_source', 'openai_excel_bps_cache_creation_as_input',
  'openai_excel_bps_auto_recover_on_403', 'openai_excel_bps_403_recovery_interval_minutes',
  'openai_excel_bps_auto_disable_on_403', 'openai_excel_bps_omit_unsupported_tools',
  'openai_excel_bps_ignore_images', 'openai_excel_bps_ignore_encrypted_content',
  'openai_excel_bps_auto_move_on_403', 'openai_excel_bps_403_target_group_id'
] as const

export function readExcelBPSConfig(extra: Record<string, unknown> = {}): ExcelBPSConfig {
  const enabled = extra.openai_excel_bps === true
  const recoveryPending = !enabled && typeof extra.openai_excel_bps_403_disabled_at === 'string'
  return {
    enabled,
    recoveryPending,
    allModels: (enabled || recoveryPending) && !Object.prototype.hasOwnProperty.call(extra, 'openai_excel_bps_models'),
    models: Array.isArray(extra.openai_excel_bps_models)
      ? extra.openai_excel_bps_models.filter((value): value is string => typeof value === 'string')
      : ['gpt-6-astra', 'gpt-5.6-sol', 'gpt-5.6-terra'],
    sessionProxy: extra.openai_excel_bps_mihomo === true,
    proxySource: extra.openai_excel_bps_proxy_source === 'ip_pool' ? 'ip_pool' : 'mihomo',
    cacheCreationAsInput: extra.openai_excel_bps_cache_creation_as_input === true,
    autoDisableOn403: extra.openai_excel_bps_auto_disable_on_403 === true,
    autoRecoverOn403: extra.openai_excel_bps_auto_recover_on_403 === true,
    recoveryIntervalMinutes: validBPSRecoveryInterval(extra.openai_excel_bps_403_recovery_interval_minutes) ? extra.openai_excel_bps_403_recovery_interval_minutes : 60,
    omitUnsupportedTools: extra.openai_excel_bps_omit_unsupported_tools === true,
    ignoreImages: extra.openai_excel_bps_ignore_images === true,
    ignoreEncryptedContent: extra.openai_excel_bps_ignore_encrypted_content === true,
    autoMoveOn403: extra.openai_excel_bps_auto_move_on_403 === true,
    targetGroupId: typeof extra.openai_excel_bps_403_target_group_id === 'number'
      && Number.isSafeInteger(extra.openai_excel_bps_403_target_group_id)
      && extra.openai_excel_bps_403_target_group_id >= 0 ? extra.openai_excel_bps_403_target_group_id : ''
  }
}

export function validBPSRecoveryInterval(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= 10080
}

/** A 403 action may leave every group or choose a compatible, currently available group. */
export function hasValidExcelBPS403Target(config: ExcelBPSConfig, groups: { id: number; platform?: string }[], allowComposite: boolean): boolean {
  if (!config.enabled || !config.autoMoveOn403) return true
  if (config.targetGroupId === 0) return true
  return typeof config.targetGroupId === 'number' && Number.isSafeInteger(config.targetGroupId)
    && config.targetGroupId > 0 && groups.some(group => group.id === config.targetGroupId
      && (group.platform === 'openai' || (allowComposite && group.platform === 'composite')))
}

/** Batch updates must send null to delete scopes because extra is merged server-side. */
export function writeExcelBPSConfig(config: ExcelBPSConfig, base: Record<string, unknown> = {}, batch = false): Record<string, unknown> {
  const extra = { ...base }
  for (const key of EXCEL_BPS_EXTRA_KEYS) {
    if (batch) extra[key] = null
    else delete extra[key]
  }
  if (batch) {
    for (const key of EXCEL_BPS_EXTRA_KEYS) {
      if (!['openai_excel_bps_models', 'openai_excel_bps_403_target_group_id', 'openai_excel_bps_proxy_source', 'openai_excel_bps_403_recovery_interval_minutes'].includes(key)) extra[key] = false
    }
    extra.openai_excel_bps_proxy_source = 'mihomo'
  }
  const preserveDisabled = !batch && base.openai_excel_bps !== true && typeof base.openai_excel_bps_403_disabled_at === 'string'
  if (!config.enabled && !preserveDisabled) {
    if (batch) extra.openai_excel_bps = false
    return extra
  }
  extra.openai_excel_bps = config.enabled
  if (!config.allModels) extra.openai_excel_bps_models = [...new Set(config.models.map(model => model.trim()).filter(Boolean))]
  if (config.autoRecoverOn403 && config.autoDisableOn403 && !validBPSRecoveryInterval(config.recoveryIntervalMinutes)) {
    throw new Error('BPS recovery interval must be an integer between 1 and 10080 minutes')
  }
  extra.openai_excel_bps_403_recovery_interval_minutes = validBPSRecoveryInterval(config.recoveryIntervalMinutes) ? config.recoveryIntervalMinutes : 60
  if (config.autoDisableOn403 && config.autoRecoverOn403) extra.openai_excel_bps_auto_recover_on_403 = true
  const flags: [keyof ExcelBPSConfig, string][] = [
    ['sessionProxy', 'openai_excel_bps_mihomo'], ['cacheCreationAsInput', 'openai_excel_bps_cache_creation_as_input'],
    ['autoDisableOn403', 'openai_excel_bps_auto_disable_on_403'], ['omitUnsupportedTools', 'openai_excel_bps_omit_unsupported_tools'],
    ['ignoreImages', 'openai_excel_bps_ignore_images'], ['ignoreEncryptedContent', 'openai_excel_bps_ignore_encrypted_content'],
    ['autoMoveOn403', 'openai_excel_bps_auto_move_on_403']
  ]
  for (const [key, target] of flags) if (config[key] === true) extra[target] = true
  if (config.sessionProxy) extra.openai_excel_bps_proxy_source = config.proxySource
  if (config.autoMoveOn403) {
    if (config.targetGroupId === '' || !Number.isSafeInteger(config.targetGroupId) || config.targetGroupId < 0) {
      throw new Error('Select a BPS 403 target group')
    }
    extra.openai_excel_bps_403_target_group_id = config.targetGroupId
  }
  return extra
}
