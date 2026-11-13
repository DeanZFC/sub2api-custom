import type { AccountProxyMode, Proxy } from '@/types'

/** Automatic modes are never forwarded as numeric OAuth proxy IDs. */
export function accountProxyPayload(mode: AccountProxyMode, ids: number[], update = false) {
  if (mode === 'ovh_ipv6' || mode === 'random' || mode === 'least_used') {
    return { proxy_mode: mode, proxy_id: null }
  }
  const selected = mode === 'direct' ? [] : ids
  return { ...(mode ? { proxy_mode: mode } : {}), proxy_id: selected[0] ?? (update ? 0 : null), proxy_ids: [...selected] }
}

/** Both providers reserve their proxy credentials for one managed lease. */
export function isManagedIPv6Proxy(proxy: Pick<Proxy, 'username' | 'managed_ipv6'>): boolean {
  return !!proxy.managed_ipv6 || !!proxy.username?.startsWith('ovh6_') || !!proxy.username?.startsWith('v6-sub2-')
}
