import { apiClient } from './client'
import { createSharedListing } from './sharedPool'
import type { adminAPI } from './admin'
import type { CreateAccountRequest, CodexSessionImportResult } from '@/types'
import { getGrokSSOImportTimeout, type GrokSSOToOAuthResponse } from './admin/grok'

type AdminAPI = typeof adminAPI
type Result<F extends (...args: never[]) => unknown> = Awaited<ReturnType<F>>
type ResolvedCredentials = {
  index?: number
  name?: string
  credentials: Record<string, unknown>
  extra?: Record<string, unknown>
  expires_at?: string
  error?: string
}

// Keep authentication data while excluding the admin form's optional routing,
// quota, retry and model defaults from shared account submissions.
const credentialKeys = new Set([
  'api_key', 'base_url', 'access_token', 'refresh_token', 'id_token', 'token_type',
  'expires_at', 'expires_in', 'scope', 'client_id', 'auth_mode', 'openai_auth_mode',
  'email', 'email_address', 'org_uuid', 'account_uuid', 'chatgpt_account_id',
  'chatgpt_user_id', 'chatgpt_account_is_fedramp', 'organization_id', 'plan_type',
  'subscription_expires_at', 'agent_runtime_id', 'agent_private_key', 'agent_task_id', 'task_id',
  'project_id', 'oauth_type', 'tier_id', 'sub', 'team_id', 'subscription_tier',
  'entitlement_status', 'service_account_json', 'client_email', 'location',
  'aws_region', 'aws_access_key_id', 'aws_secret_access_key', 'aws_session_token',
  'account_mode', 'api_protocol', 'api_base_urls', 'model_mapping'
])

export function createSharedAccountAPI(proxyURL: () => string) {
  async function post<T>(platform: string, action: string, payload: object = {}): Promise<T> {
    const url = '/user/shared-pool/oauth/' + platform + '/' + action
    const body = { ...payload, proxy_url: proxyURL().trim() }
    // SSO conversion can take longer than the normal request timeout.
    const { data } = action === 'validate-sso'
      ? await apiClient.post<T>(url, body, { timeout: getGrokSSOImportTimeout(1) })
      : await apiClient.post<T>(url, body)
    return data
  }
  function authEndpoint(endpoint: string) {
    const match = /^\/admin\/(accounts|openai)\/([a-z-]+)$/.exec(endpoint)
    if (!match) throw new Error('Unsupported shared authorization operation')
    return { platform: match[1] === 'accounts' ? 'anthropic' : 'openai', action: match[2] }
  }
  async function create(payload: Pick<CreateAccountRequest, 'name' | 'platform' | 'type' | 'credentials' | 'extra' | 'concurrency' | 'rate_multiplier'>, expiresAt?: string) {
    const listing = await createSharedListing({
      name: payload.name,
      platform: payload.platform,
      type: payload.type,
      credentials: Object.fromEntries(Object.entries(payload.credentials).filter(([key]) => credentialKeys.has(key))),
      extra: payload.extra ? Object.fromEntries(Object.entries(payload.extra).filter(([key]) => ['email', 'name', 'privacy_mode', 'subscription_tier', 'project_id', 'codex_fingerprint_mode', 'openai_passthrough_enabled', 'openai_flatten_namespaces'].includes(key))) : undefined,
      expires_at: expiresAt,
      concurrency: payload.concurrency,
      sell_rate: payload.rate_multiplier ?? 1,
      proxy_url: proxyURL().trim()
    })
    return { id: listing.account_id }
  }
  function message(error: unknown) {
    const apiError = error as { response?: { data?: { message?: string; detail?: string } }; message?: string }
    return apiError.response?.data?.message || apiError.response?.data?.detail || apiError.message || '创建失败'
  }
  const accounts = {
    create,
    generateAuthUrl: (async (endpoint, payload) => {
      const { platform, action } = authEndpoint(endpoint)
      return post(platform, action, payload)
    }) as AdminAPI['accounts']['generateAuthUrl'],
    exchangeCode: (async (endpoint, payload) => {
      const { platform, action } = authEndpoint(endpoint)
      return post(platform, action, payload)
    }) as AdminAPI['accounts']['exchangeCode'],
    refreshOpenAIToken: (async (refreshToken, _proxyId, _endpoint, clientId) =>
      post('openai', 'refresh-token', { refresh_token: refreshToken, client_id: clientId })
    ) as AdminAPI['accounts']['refreshOpenAIToken'],
    async importCodexSession(payload: Parameters<AdminAPI['accounts']['importCodexSession']>[0]): Promise<CodexSessionImportResult> {
      const entries = await post<ResolvedCredentials[]>('openai', 'parse-session', { content: payload.content, name: payload.name })
      const result: CodexSessionImportResult = { total: entries.length, created: 0, updated: 0, skipped: 0, failed: 0, errors: [], warnings: [] }
      for (const [index, entry] of entries.entries()) {
        try {
          if (entry.error) throw new Error(entry.error)
          await create({ name: entry.name || payload.name || 'OpenAI', platform: 'openai', type: 'oauth', credentials: entry.credentials, extra: entry.extra, concurrency: payload.concurrency, rate_multiplier: payload.rate_multiplier }, entry.expires_at)
          result.created++
        } catch (error) {
          result.failed++
          result.errors!.push({ index: index + 1, name: entry.name, message: message(error) })
        }
      }
      return result
    },
    async createOpenAICodexPAT(payload: Parameters<AdminAPI['accounts']['createOpenAICodexPAT']>[0]) {
      const resolved = await post<ResolvedCredentials>('openai', 'validate-pat', { access_token: payload.access_token })
      return create({ name: payload.name || 'OpenAI', platform: 'openai', type: 'oauth', credentials: resolved.credentials, extra: resolved.extra, concurrency: payload.concurrency, rate_multiplier: payload.rate_multiplier })
    }
  }
  const gemini: AdminAPI['gemini'] = {
    generateAuthUrl: payload => post('gemini', 'generate-auth-url', payload),
    exchangeCode: payload => post('gemini', 'exchange-code', payload),
    async getCapabilities() {
      const { data } = await apiClient.get<Result<AdminAPI['gemini']['getCapabilities']>>('/user/shared-pool/oauth/gemini/capabilities')
      return data
    }
  }
  const antigravity: AdminAPI['antigravity'] = {
    generateAuthUrl: payload => post('antigravity', 'generate-auth-url', payload),
    exchangeCode: payload => post('antigravity', 'exchange-code', payload),
    refreshAntigravityToken: refreshToken => post('antigravity', 'refresh-token', { refresh_token: refreshToken })
  }
  const grok = {
    generateAuthUrl: ((payload) => post('grok', 'generate-auth-url', payload)) as AdminAPI['grok']['generateAuthUrl'],
    exchangeCode: ((payload) => post('grok', 'exchange-code', payload)) as AdminAPI['grok']['exchangeCode'],
    refreshGrokToken: ((refreshToken) => post('grok', 'refresh-token', { refresh_token: refreshToken })) as AdminAPI['grok']['refreshGrokToken'],
    validateSSOToken: (async (ssoToken) => {
      const resolved = await post<ResolvedCredentials>('grok', 'validate-sso', { sso_token: ssoToken })
      return resolved.credentials
    }) as AdminAPI['grok']['validateSSOToken'],
    authorizePassword: (async () => { throw new Error('Password authorization is unavailable') }) as AdminAPI['grok']['authorizePassword'],
    async createFromSSO(payload: Parameters<AdminAPI['grok']['createFromSSO']>[0]): Promise<GrokSSOToOAuthResponse> {
      const result: GrokSSOToOAuthResponse = { created: [], failed: [] }
      for (const [index, token] of payload.sso_tokens.entries()) {
        const name = payload.sso_tokens.length > 1 ? (payload.name || 'Grok') + '-' + (index + 1) : payload.name || 'Grok'
        try {
          const resolved = await post<ResolvedCredentials>('grok', 'validate-sso', { sso_token: token })
          const account = await create({ name, platform: 'grok', type: 'oauth', credentials: resolved.credentials, extra: resolved.extra, concurrency: payload.concurrency, rate_multiplier: payload.rate_multiplier })
          result.created.push({ index: index + 1, name, account })
        } catch (error) {
          result.failed.push({ index: index + 1, name, error: message(error) })
        }
      }
      return result
    }
  }
  return { accounts, gemini, antigravity, grok }
}
