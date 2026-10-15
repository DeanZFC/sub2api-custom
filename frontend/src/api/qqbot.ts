import { apiClient } from './client'

export interface QQBotConfig {
  enabled: boolean; app_id: string; app_secret: string; app_secret_set: boolean; sandbox: boolean
  allowed_groups: string[]; base_url: string; api_key: string; api_key_set: boolean
  chat_model: string; chat_protocol: 'chat_completions' | 'responses'
  image_enabled: boolean; image_base_url: string; image_api_key: string; image_api_key_set: boolean
  image_model: string; image_size: string; system_prompt: string; max_output_tokens: number
  history_turns: number; daily_chat_limit: number; daily_image_limit: number
  total_daily_chat_limit: number; total_daily_image_limit: number; cooldown_seconds: number
  checkin_reward: number; invite_reward: number; invite_daily_limit: number
  invite_min_recharge_total?: number; invite_min_recharge_30d?: number
  clear_app_secret?: boolean; clear_api_key?: boolean; clear_image_api_key?: boolean
}
export interface QQBotRuntime {
  encryption_ready: boolean; bindings: number
  groups: { id: string; last_event: string; last_seen_at: string }[]
  jobs: { id: string; group_id: string; member_id: string; kind: string; status: string; error: string; input_tokens: number; output_tokens: number; created_at: string }[]
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
export interface QQCheckinConfig {
  enabled: boolean; min_amount: number; max_amount: number
  min_recharge_total: number; min_recharge_30d: number
}
export interface QQInviteConfig {
  invite_reward: number; invite_daily_limit: number
  min_recharge_total: number; min_recharge_30d: number
}
export interface QQLotteryPrize { name: string; amount: number; probability_bps: number }
export interface QQLotteryConfig { enabled: boolean; title: string; recharge_step: number; chances_per_step: number; prizes: QQLotteryPrize[] }
export interface QQLotteryState { user_id: number; recharge_total: number; earned: number; used: number; remaining: number; next_recharge: number }
export interface QQLotteryDraw { id: number; user_id: number; email: string; member_id: string; prize_name: string; amount: number; remaining: number; created_at: string }
export interface QQLotteryDrawsPage { items: QQLotteryDraw[]; total: number; page: number; page_size: number }
export type QQActivityKind = 'checkin' | 'lottery' | 'invite'
export interface QQActivityParticipant {
  member_id: string; user_id: number; email: string; nickname: string
  bound: boolean; user_status: string; user_deleted: boolean
}
export interface QQActivityRecord {
  id: string; group_id: string; participant: QQActivityParticipant; inviter?: QQActivityParticipant
  status: string; amount: number; prize_name: string; remaining?: number; created_at: string
  invited_at?: string; joined_at?: string; left_at?: string; rewarded_at?: string
  reward_user_id: number; reward_email: string
}
export interface QQActivityRecordsPage { items: QQActivityRecord[]; total: number; page: number; page_size: number }
export const qqbotAPI = {
  activityRecords: async (kind: QQActivityKind, search: string, page: number) => (await apiClient.get<QQActivityRecordsPage>(`/admin/qqbot/records/${kind}`, { params: { search, page, page_size: 20 } })).data,
  inviteConfig: async () => (await apiClient.get<QQInviteConfig>('/admin/qqbot/invite')).data,
  saveInvite: async (config: QQInviteConfig) => (await apiClient.put<QQInviteConfig>('/admin/qqbot/invite', config)).data,
  checkinConfig: async () => (await apiClient.get<QQCheckinConfig>('/admin/qqbot/checkin')).data,
  saveCheckin: async (config: QQCheckinConfig) => (await apiClient.put<QQCheckinConfig>('/admin/qqbot/checkin', config)).data,
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
  confirmEmail: async (token: string, member_id: string) => (await apiClient.post('/qqbot/email-binding/confirm', { token, member_id })).data
}
