import { qqbotAPI, type QQCheckinConfig, type QQCheckinRewardDistribution } from '@/api/qqbot'
import { normalizeGrowthReward } from './growthReward'

export function defaultCheckinDistribution(): QQCheckinRewardDistribution {
  return { bands: [
    { end_bps: 5000, probability_bps: 7000 },
    { end_bps: 7500, probability_bps: 2000 },
    { end_bps: 10000, probability_bps: 1000 }
  ] }
}

function copyDistribution(distribution: QQCheckinRewardDistribution): QQCheckinRewardDistribution {
  return { bands: distribution.bands.map(band => ({ ...band })) }
}

export async function loadCheckinConfig(): Promise<QQCheckinConfig> {
  const config = await qqbotAPI.checkinConfig()
  return {
    ...config,
    coupon_template_id: config.coupon_template_id ?? 0,
    coupon_quantity: config.coupon_quantity ?? 0,
    milestones: (config.milestones ?? []).map(milestone => ({ ...milestone, kind: milestone.kind || 'checkin_total', reward: normalizeGrowthReward(milestone.reward) })),
    reward_distribution: config.reward_distribution ? copyDistribution(config.reward_distribution) : defaultCheckinDistribution(),
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
        { id: 'first_usage', name: '首次成功 API 调用', kind: 'usage_count', target: 1, chances: config.growth?.first_usage_chances ?? 0, enabled: (config.growth?.first_usage_chances ?? 0) > 0 }
      ]).map(task => ({ ...task, ...(!('period' in task) || !task.period ? { period: 'once' as const } : {}), reward: normalizeGrowthReward('reward' in task ? task.reward : undefined, task.chances) }))
    }
  }
}

// Refresh unrelated fields, but always retain the draft's original revision.
// A missing draft revision must not inherit one from this refresh.
let pendingSave = Promise.resolve()
function saveConfigPatch(version: string | undefined, section: string, patch: Partial<QQCheckinConfig> | ((current: QQCheckinConfig) => Partial<QQCheckinConfig>)): Promise<QQCheckinConfig> {
  const saved = pendingSave.then(async () => {
    const current = await loadCheckinConfig()
    return qqbotAPI.saveCheckin({ ...current, ...(typeof patch === 'function' ? patch(current) : patch), _version: version, _section: version ? section : undefined })
  })
  pendingSave = saved.then(() => undefined, () => undefined)
  return saved
}

export function saveCheckinSettings(config: QQCheckinConfig): Promise<QQCheckinConfig> {
  return saveConfigPatch(config._sections?.checkin || config._version, 'checkin', {
    enabled: config.enabled,
    coupon_template_id: config.coupon_template_id ?? 0,
    coupon_quantity: config.coupon_quantity ?? 0,
    min_amount: config.min_amount,
    max_amount: config.max_amount,
    ...(config.reward_distribution ? { reward_distribution: copyDistribution(config.reward_distribution) } : {}),
    min_recharge_total: config.min_recharge_total,
    min_recharge_30d: config.min_recharge_30d,
    recharge_window_days: config.recharge_window_days,
    streak_days: config.streak_days,
    streak_chances: config.streak_reward.type === 'chances' ? config.streak_reward.quantity : 0,
    streak_reward: { ...config.streak_reward },
    milestones: (config.milestones ?? []).map(milestone => ({ ...milestone, reward: { ...milestone.reward } }))
  })
}

export function validCheckinDistribution(distribution: QQCheckinRewardDistribution | undefined): boolean {
  if (!distribution) return false
  let start = 0
  let total = 0
  for (const { end_bps, probability_bps } of distribution.bands) {
    if (!Number.isInteger(end_bps) || end_bps <= start || end_bps > 10000) return false
    if (!Number.isInteger(probability_bps) || probability_bps < 0 || probability_bps > 10000) return false
    start = end_bps
    total += probability_bps
  }
  return start === 10000 && total === 10000
}

export function saveGrowthSettings(config: QQCheckinConfig, section: 'tasks' | 'periodic_tasks' = 'tasks'): Promise<QQCheckinConfig> {
  const tasks = config.growth[section].map(task => ({ ...task, reward: { ...task.reward }, chances: task.reward.type === 'chances' ? task.reward.quantity : 0 }))
  return saveConfigPatch(config._sections?.[section] || config._version, section, current => ({ growth: { ...current.growth, [section]: tasks } }))
}
