import { apiClient } from '../client'

export interface ExcelAuthorizationConfig {
  account_id: number
  login_email: string
  credential_mode: 'password_totp'
  engine: 'local_worker'
  proxy_source: 'account' | 'managed_proxy' | 'mihomo'
  proxy_id?: number
  password_configured: boolean
  totp_configured: boolean
  configured: boolean
}
export interface ExcelAuthorizationTask {
  id: number
  status: string
  stage: string
  error?: string
  oauth_profile: string
}
export interface ExcelAuthorizationStatus {
  config?: ExcelAuthorizationConfig
  task?: ExcelAuthorizationTask
}
export interface ExcelAuthorizationInput {
  login_email: string
  credential_mode: 'password_totp'
  engine: 'local_worker'
  proxy_source: ExcelAuthorizationConfig['proxy_source']
  proxy_id?: number
  password?: string
  totp_secret?: string
  clear_password?: boolean
  clear_totp?: boolean
}
export interface CredentialEncryptionStatus { configured: boolean; source: string }
export interface ReauthRuntimeStatus { mode: string; state: string; reason?: string }
export const getExcelAuthorization = async (id: number) => (await apiClient.get<ExcelAuthorizationStatus>(`/admin/accounts/${id}/openai-reauth`)).data
export const saveExcelAuthorization = async (id: number, input: ExcelAuthorizationInput) => (await apiClient.put<ExcelAuthorizationConfig>(`/admin/accounts/${id}/openai-reauth/config`, input)).data
export const startExcelAuthorization = async (id: number) => (await apiClient.post<ExcelAuthorizationTask>(`/admin/accounts/${id}/openai-reauth`, { oauth_profile: 'excel' })).data
export const getCredentialEncryption = async () => (await apiClient.get<CredentialEncryptionStatus>('/admin/openai/credential-encryption')).data
export const initializeCredentialEncryption = async () => (await apiClient.post<CredentialEncryptionStatus>('/admin/openai/credential-encryption')).data
export const getReauthRuntime = async () => (await apiClient.get<ReauthRuntimeStatus>('/admin/openai/reauth-runtime')).data
