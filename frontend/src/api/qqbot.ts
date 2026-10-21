import { apiClient } from './client'

export interface QQBotCustomCommand { command: string; reply: string; enabled: boolean }
export interface QQAutonomousConfig {
  enabled: boolean; groups: string[]; names: string[]; topics: string[]
  sample_percent: number; cooldown_seconds: number; min_messages: number
  member_cooldown_seconds: number; hourly_replies: number; daily_replies: number; daily_calls: number
  start_hour: number; end_hour: number; followup_seconds: number; max_chars: number; mention_user: boolean
}
export function defaultAutonomousConfig(): QQAutonomousConfig {
  return { enabled: false, groups: [], names: ['CTAV'], topics: [], sample_percent: 15, cooldown_seconds: 120,
    min_messages: 6, member_cooldown_seconds: 600, hourly_replies: 3, daily_replies: 20, daily_calls: 40,
    start_hour: 9, end_hour: 23, followup_seconds: 60, max_chars: 100, mention_user: false }
}
export interface QQHealthNotificationConfig {
  enabled: boolean; groups: string[]; minimum_samples: number; error_percent: number
  recovery_percent: number; ttft_seconds: number; failure_minutes: number; recovery_minutes: number; cooldown_minutes: number
}
export function defaultHealthNotificationConfig(): QQHealthNotificationConfig {
  return { enabled: false, groups: [], minimum_samples: 50, error_percent: 20, recovery_percent: 5,
    ttft_seconds: 10, failure_minutes: 3, recovery_minutes: 2, cooldown_minutes: 30 }
}
export interface QQBotConfig {
  health_notifications?: QQHealthNotificationConfig
  autonomous?: QQAutonomousConfig
  enabled: boolean; app_id: string; app_secret: string; app_secret_set: boolean; sandbox: boolean
  allowed_groups: string[]; base_url: string; api_key: string; api_key_set: boolean
  custom_commands: QQBotCustomCommand[]
  price_notifications?: { enabled: boolean; scheduled: boolean; groups: string[] }
  chat_model: string; chat_protocol: 'chat_completions' | 'responses'
  image_enabled: boolean; image_base_url: string; image_api_key: string; image_api_key_set: boolean
  image_model: string; image_size: string; system_prompt: string; max_output_tokens: number
  history_messages: number; daily_chat_limit: number; daily_image_limit: number
  total_daily_chat_limit: number; total_daily_image_limit: number; cooldown_seconds: number
  checkin_reward: number; invite_reward: number; invite_daily_limit: number
  invite_min_recharge_total?: number; invite_min_recharge_30d?: number; invite_recharge_window_days?: number
  clear_app_secret?: boolean; clear_api_key?: boolean; clear_image_api_key?: boolean
}
export interface QQBotRuntime {
  autonomous_stats?: { group_id: string; reason: string; count: number }[]
  encryption_ready: boolean; bindings: number
  groups: { id: string; last_event: string; last_seen_at: string }[]
  jobs: { trigger_reason?: string; decision_reason?: string; id: string; group_id: string; member_id: string; kind: string; status: string; error: string; input_tokens: number; output_tokens: number; created_at: string }[]
  rewards: { id: number; user_id: number; kind: string; amount: number; created_at: string }[]
}
export interface QQBindingRequest { code?: string; member_id: string; group_id: string; nickname: string; expires_at: string }
export interface QQEmailBindingPreview { member_id: string; group_id: string; nickname: string; expires_at: string }
export interface QQAdminBinding {
  app_id: string; member_id: string; user_id: number; nickname: string; group_id: string
  created_at: string; email: string; user_status: string; user_deleted: boolean
}
export interface QQAdminBindingsPage { items: QQAdminBinding[]; total: number; page: number; page_size: number }
export interface QQBindingState {
  enabled: boolean
  binding: { member_id: string; group_id: string; nickname: string; created_at: string } | null
  pending: QQBindingRequest | null
}
export type QQGrowthTaskKind = 'binding' | 'usage_count' | 'checkin_total' | 'checkin_streak' | 'recharge_total' | 'usage_tokens' | 'usage_amount'
export interface QQGrowthReward {
  type: QQRewardType; amount: number; quantity: number; prize_name: string; claim_instructions: string
}
export interface QQGrowthTask {
  reward: QQGrowthReward
  id: string; name: string; kind: QQGrowthTaskKind; target: number; chances: number; enabled: boolean
}
export interface QQRankingPrize { rank: number; reward: QQGrowthReward }
export interface QQRankingConfig {
  enabled: boolean; rewards_enabled: boolean; effective_day: string
  token_rewards: QQRankingPrize[]; amount_rewards: QQRankingPrize[]
}
export interface QQCheckinConfig {
  streak_reward: QQGrowthReward
  streak_days: number
  streak_chances: number
  growth: { binding_chances: number; first_usage_chances: number; checkin_30_chances: number; tasks: QQGrowthTask[] }
  enabled: boolean; min_amount: number; max_amount: number
  min_recharge_total: number; min_recharge_30d: number; recharge_window_days: number
}
export interface QQInviteConfig {
  invite_reward: number; invite_daily_limit: number
  min_recharge_total: number; min_recharge_30d: number; recharge_window_days: number
}
export interface QQTransferConfig {
  transfer_enabled: boolean; transfer_fee_percent: number
  transfer_min_amount: number; transfer_max_amount: number
}
export interface QQTransferPreview {
  from_email: string; to_email: string; amount: string; fee: string
  net_amount: string; expires_at: string
}
export interface QQLotteryPrize { name: string; amount: number; probability_bps: number }
export interface QQLotteryConfig {
  enabled: boolean; title: string; recharge_step: number; chances_per_step: number; prizes: QQLotteryPrize[]
  guarantee_enabled: boolean; guarantee_every: number; guarantee_prizes: QQLotteryPrize[]
}
export interface QQLotteryState { user_id: number; recharge_total: number; earned: number; bonus_chances?: number; used: number; remaining: number; next_recharge: number; guarantee_every: number; draws_until_guarantee: number }
export interface QQLotteryDraw { id: number; user_id: number; email: string; member_id: string; prize_name: string; amount: number; remaining: number; created_at: string; is_guaranteed: boolean }
export interface QQLotteryDrawsPage { items: QQLotteryDraw[]; total: number; page: number; page_size: number }
export type QQActivityKind = 'checkin' | 'lottery' | 'invite'
export interface QQActivityParticipant {
  member_id: string; user_id: number; email: string; nickname: string
  bound: boolean; user_status: string; user_deleted: boolean
}
export interface QQActivityRecord {
  id: string; group_id: string; participant: QQActivityParticipant; inviter?: QQActivityParticipant
  status: string; amount: number; prize_name: string; is_guaranteed?: boolean; remaining?: number; created_at: string
  invited_at?: string; joined_at?: string; left_at?: string; rewarded_at?: string
  reward_user_id: number; reward_email: string
}
export interface QQActivityRecordsPage { items: QQActivityRecord[]; total: number; page: number; page_size: number }
export type QQRewardType = 'balance' | 'chances' | 'physical' | 'other'
export interface QQRewardCode {
  batch_name: string; reward_type: QQRewardType; amount: number; quantity: number
  prize_name: string; claim_instructions: string; target_user_id: number | null; expires_at: string | null
  source_kind?: 'code' | 'growth' | 'streak' | 'ranking'
  id: number; app_id: string; code_hint: string; status: 'unused' | 'disabled' | 'expired' | 'pending' | 'fulfilled'
  user_id: number | null; email: string; member_id: string; redeemed_at: string | null
  fulfilled_at: string | null; fulfillment_note: string; created_at: string
}
export interface QQRewardCodesPage { items: QQRewardCode[]; total: number; page: number; page_size: number }
export const qqbotAPI = {
  rankingConfig: async () => (await apiClient.get<QQRankingConfig>('/admin/qqbot/rankings')).data,
  saveRankings: async (config: QQRankingConfig) => (await apiClient.put<QQRankingConfig>('/admin/qqbot/rankings', config)).data,
  rewardCodes: async (search: string, status: string, page: number) => (await apiClient.get<QQRewardCodesPage>('/admin/qqbot/reward-codes', { params: { search, status, page } })).data,
  updateRewardCode: async (id: number, action: 'disable' | 'fulfill', note = '') => (await apiClient.patch(`/admin/qqbot/reward-codes/${id}`, { action, note })).data,
  activityRecords: async (kind: QQActivityKind, search: string, page: number) => (await apiClient.get<QQActivityRecordsPage>(`/admin/qqbot/records/${kind}`, { params: { search, page, page_size: 20 } })).data,
  inviteConfig: async () => (await apiClient.get<QQInviteConfig>('/admin/qqbot/invite')).data,
  saveInvite: async (config: QQInviteConfig) => (await apiClient.put<QQInviteConfig>('/admin/qqbot/invite', config)).data,
  checkinConfig: async () => (await apiClient.get<QQCheckinConfig>('/admin/qqbot/checkin')).data,
  saveCheckin: async (config: QQCheckinConfig) => (await apiClient.put<QQCheckinConfig>('/admin/qqbot/checkin', config)).data,
  transferConfig: async () => (await apiClient.get<QQTransferConfig>('/admin/qqbot/transfer')).data,
  saveTransfer: async (config: QQTransferConfig) => (await apiClient.put<QQTransferConfig>('/admin/qqbot/transfer', config)).data,
  lotteryConfig: async () => (await apiClient.get<QQLotteryConfig>('/admin/qqbot/lottery')).data,
  saveLottery: async (config: QQLotteryConfig) => (await apiClient.put<QQLotteryConfig>('/admin/qqbot/lottery', config)).data,
  lotteryState: async (userID: number) => (await apiClient.get<QQLotteryState>(`/admin/qqbot/lottery/users/${userID}`)).data,
  lotteryDraws: async (search: string, page: number) => (await apiClient.get<QQLotteryDrawsPage>('/admin/qqbot/lottery/draws', { params: { search, page } })).data,
  config: async () => (await apiClient.get<QQBotConfig>('/admin/qqbot/config')).data,
  save: async (config: QQBotConfig) => (await apiClient.put<QQBotConfig>('/admin/qqbot/config', config)).data,
  runtime: async () => (await apiClient.get<QQBotRuntime>('/admin/qqbot/runtime')).data,
  deleteDiscoveredGroup: async (groupID: string) => (await apiClient.delete('/admin/qqbot/groups', { data: { group_id: groupID } })).data,
  bindings: async (search: string, page: number) => (await apiClient.get<QQAdminBindingsPage>('/admin/qqbot/bindings', { params: { search, page, page_size: 20 } })).data,
  unbind: async (binding: QQAdminBinding) => (await apiClient.delete(`/admin/qqbot/bindings/${binding.user_id}`, { data: { app_id: binding.app_id, member_id: binding.member_id, created_at: binding.created_at } })).data,
  probe: async (kind: 'qq' | 'chat' | 'image') => (await apiClient.post<{message: string}>(`/admin/qqbot/test/${kind}`, {}, { timeout: 230000 })).data,
  binding: async () => (await apiClient.get<QQBindingState>('/user/qqbot/binding')).data,
  createCode: async () => (await apiClient.post<QQBindingRequest>('/user/qqbot/binding-code')).data,
  confirm: async (member_id: string) => (await apiClient.post('/user/qqbot/binding-confirm', { member_id })).data,
  previewEmail: async (token: string) => (await apiClient.post<QQEmailBindingPreview>('/qqbot/email-binding/preview', { token })).data,
  confirmEmail: async (token: string, member_id: string) => (await apiClient.post('/qqbot/email-binding/confirm', { token, member_id })).data,
  previewTransfer: async (token: string) => (await apiClient.post<QQTransferPreview>('/qqbot/transfer/preview', { token })).data,
  confirmTransfer: async (token: string) => (await apiClient.post('/qqbot/transfer/confirm', { token })).data
}
