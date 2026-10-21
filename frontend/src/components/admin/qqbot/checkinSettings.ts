import { qqbotAPI, type QQCheckinConfig } from '@/api/qqbot'
import { normalizeGrowthReward } from './growthReward'

export async function loadCheckinConfig(): Promise<QQCheckinConfig> {
  const config = await qqbotAPI.checkinConfig()
  return {
    ...config,
    recharge_window_days: config.recharge_window_days || 30,
    streak_days: config.streak_days || 7,
    streak_chances: config.streak_chances ?? 0,
    streak_reward: normalizeGrowthReward(config.streak_reward, config.streak_chances ?? 0),
    growth: {
      binding_chances: config.growth?.binding_chances ?? 0,
      first_usage_chances: config.growth?.first_usage_chances ?? 0,
      checkin_30_chances: config.growth?.checkin_30_chances ?? 0,
      periodic_tasks: (config.growth?.periodic_tasks ?? []).map(task => ({ ...task, reward: normalizeGrowthReward(task.reward, task.chances) })),
      tasks: (config.growth?.tasks ?? [
        { id: 'binding', name: '完成账号绑定', kind: 'binding', target: 1, chances: config.growth?.binding_chances ?? 0, enabled: (config.growth?.binding_chances ?? 0) > 0 },
        { id: 'first_usage', name: '首次成功 API 调用', kind: 'usage_count', target: 1, chances: config.growth?.first_usage_chances ?? 0, enabled: (config.growth?.first_usage_chances ?? 0) > 0 },
        { id: 'checkin_30', name: '累计签到 30 天', kind: 'checkin_total', target: 30, chances: config.growth?.checkin_30_chances ?? 0, enabled: (config.growth?.checkin_30_chances ?? 0) > 0 }
      ]).map(task => ({ ...task, reward: normalizeGrowthReward('reward' in task ? task.reward : undefined, task.chances) }))
    }
  }
}

// Both tabs use the same full-config endpoint. Serialize saves and refresh the
// other tab's fields so cached drafts and overlapping saves cannot undo them.
let pendingSave = Promise.resolve()
function saveConfigPatch(patch: Partial<QQCheckinConfig> | ((current: QQCheckinConfig) => Partial<QQCheckinConfig>)): Promise<QQCheckinConfig> {
  const saved = pendingSave.then(async () => {
    const current = await loadCheckinConfig()
    return qqbotAPI.saveCheckin({ ...current, ...(typeof patch === 'function' ? patch(current) : patch) })
  })
  pendingSave = saved.then(() => undefined, () => undefined)
  return saved
}

export function saveCheckinSettings(config: QQCheckinConfig): Promise<QQCheckinConfig> {
  return saveConfigPatch({
    enabled: config.enabled,
    min_amount: config.min_amount,
    max_amount: config.max_amount,
    min_recharge_total: config.min_recharge_total,
    min_recharge_30d: config.min_recharge_30d,
    recharge_window_days: config.recharge_window_days,
    streak_days: config.streak_days,
    streak_chances: config.streak_reward.type === 'chances' ? config.streak_reward.quantity : 0,
    streak_reward: { ...config.streak_reward }
  })
}

export function saveGrowthSettings(config: QQCheckinConfig, section: 'tasks' | 'periodic_tasks' = 'tasks'): Promise<QQCheckinConfig> {
  const tasks = config.growth[section].map(task => ({ ...task, reward: { ...task.reward }, chances: task.reward.type === 'chances' ? task.reward.quantity : 0 }))
  return saveConfigPatch(current => ({ growth: { ...current.growth, [section]: tasks } }))
}
