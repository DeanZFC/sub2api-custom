import { adminAPI } from '@/api/admin'
import type { AccountListItem } from '@/types'

// Include every status so exclusions can be configured before an account is
// re-enabled. Only schedulable accounts constrain strategy reasoning options.
export async function loadTestAccounts(): Promise<AccountListItem[]> {
  const pageSize = 1000
  const first = await adminAPI.accounts.list(1, pageSize, { lite: '1' })
  const items = [...(first.items || [])]
  const pages = Math.max(first.pages || 1, Math.ceil((first.total || items.length) / pageSize))
  const rest = await Promise.all(Array.from({ length: pages - 1 }, (_, index) => adminAPI.accounts.list(index + 2, pageSize, { lite: '1' })))
  for (const page of rest) items.push(...(page.items || []))
  return [...new Map(items.map(account => [account.id, account])).values()]
}
