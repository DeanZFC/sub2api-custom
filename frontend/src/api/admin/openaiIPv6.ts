import { apiClient } from '../client'
export interface IPv6Binding { account_id: number; lease_id: string; address: string; status: string; last_error: string }
export interface IPv6State { binding: IPv6Binding | null; eligible: boolean; configured: boolean }
const path = (id: number) => '/admin/accounts/' + id + '/ipv6-egress'
export const openaiIPv6API = {
 async status(id: number): Promise<IPv6State> { return (await apiClient.get<IPv6State>(path(id), { timeout: 70_000 })).data },
 async bind(id: number) { return (await apiClient.post<IPv6Binding>(path(id), undefined, { timeout: 70_000 })).data },
 async detach(id: number) { await apiClient.delete(path(id), { timeout: 70_000 }) }
}
