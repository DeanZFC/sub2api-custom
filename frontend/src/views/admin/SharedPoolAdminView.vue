<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <div class="flex-1 sm:max-w-72"><input v-model="search" class="input" placeholder="搜索账号或上传用户" @keyup.enter="loadAccounts" /></div>
          <Select v-model="status" class="w-36" :options="[{ value: '', label: '全部状态' }, { value: 'active', label: '运行中' }, { value: 'paused', label: '已暂停' }, { value: 'suspended', label: '已禁用' }, { value: 'invalid', label: '不可用' }]" @change="loadAccounts" />
          <div class="ml-auto flex gap-2"><button class="btn btn-secondary" :disabled="loading" @click="loadAccounts"><Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" /></button></div>
        </div>
      </template>
      <template #table>
        <div class="mb-4 flex items-center gap-2 border-b border-gray-200 dark:border-dark-700">
          <button v-for="tab in tabs" :key="tab.key" class="border-b-2 px-4 py-3 text-sm font-medium" :class="tabKey === tab.key ? 'border-primary-500 text-primary-600' : 'border-transparent text-gray-500'" @click="tabKey = tab.key">{{ tab.label }}</button>
        </div>
        <DataTable v-if="tabKey === 'accounts'" :columns="accountColumns" :data="accounts" :loading="loading">
          <template #cell-name="{ row }"><div class="font-medium">{{ row.name || row.display_name || `#${row.id}` }}</div><div class="text-xs text-gray-500">{{ row.platform }} · {{ row.type || 'oauth' }}</div></template>
          <template #cell-uploader="{ row }"><div>{{ row.uploader_name || row.uploader_email || `用户 #${row.uploader_id || '-'}` }}</div></template>
          <template #cell-status="{ row }"><span class="badge" :class="row.status !== 'active' || row.listed === false ? 'badge-warning' : 'badge-success'">{{ row.status === 'suspended' ? '已禁用' : row.status === 'invalid' ? '无效' : row.listed === false ? '已下架' : '运行中' }}</span></template>
          <template #cell-metrics="{ row }"><div>{{ row.total_call_count ?? 0 }} 次调用</div><div class="text-xs text-gray-500">{{ averageLatency(row) ? `${averageLatency(row)}ms` : '—' }}</div></template>
          <template #cell-actions="{ row }"><div class="flex gap-2"><button class="btn btn-secondary btn-sm" @click="toggleAccount(row)">{{ row.status === 'suspended' ? '恢复' : '禁用' }}</button><button class="btn btn-secondary btn-sm" @click="toggleListed(row)">{{ row.listed === false ? '上架' : '下架' }}</button></div></template>
        </DataTable>
        <DataTable v-else :columns="userColumns" :data="users" :loading="loadingUsers">
          <template #cell-user="{ row }"><div class="font-medium">{{ row.username || row.email || `用户 #${row.user_id}` }}</div><div class="text-xs text-gray-500">{{ row.shared_account_count ?? 0 }} 个共享账号</div></template>
          <template #cell-permission="{ row }"><span class="badge" :class="row.publish_enabled ? 'badge-success' : 'badge-warning'">{{ row.publish_enabled ? '允许发布' : '已禁止' }}</span></template>
          <template #cell-actions="{ row }"><button class="btn btn-secondary btn-sm" @click="toggleUser(row)">{{ row.publish_enabled ? '禁止发布' : '恢复发布' }}</button></template>
        </DataTable>
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { listSharedAccounts, listSharedUsers, setSharedAccountListed, setSharedAccountStatus, setSharedUserPublishPermission, type AdminSharedAccount, type AdminSharedUser } from '@/api/admin/sharedPool'

const tabs = [{ key: 'accounts', label: '共享账号' }, { key: 'users', label: '发布权限' }]
const tabKey = ref('accounts'); const search = ref(''); const status = ref(''); const loading = ref(false); const loadingUsers = ref(false)
const accounts = ref<AdminSharedAccount[]>([]); const users = ref<AdminSharedUser[]>([])
function averageLatency(row: AdminSharedAccount) {
  const values = (row.recent_calls || []).map(call => Number(call.duration_ms || 0)).filter(value => value > 0)
  return values.length ? Math.round(values.reduce((sum, value) => sum + value, 0) / values.length) : 0
}
const accountColumns = [{ key: 'name', label: '账号' }, { key: 'uploader', label: '上传用户' }, { key: 'status', label: '状态' }, { key: 'metrics', label: '使用情况' }, { key: 'actions', label: '操作' }]
const userColumns = [{ key: 'user', label: '用户' }, { key: 'permission', label: '发布权限' }, { key: 'actions', label: '操作' }]
async function loadAccounts() { loading.value = true; try { const result = await listSharedAccounts({ search: search.value || undefined, status: status.value || undefined }); accounts.value = result.items || [] } finally { loading.value = false } }
async function loadUsers() { loadingUsers.value = true; try { const result = await listSharedUsers({ search: search.value || undefined }); users.value = result.items || [] } finally { loadingUsers.value = false } }
async function toggleAccount(row: AdminSharedAccount) {
  const suspending = row.status !== 'suspended'
  await setSharedAccountStatus(row.id, suspending ? 'suspended' : 'active')
  await loadAccounts()
}
async function toggleListed(row: AdminSharedAccount) { await setSharedAccountListed(row.id, row.listed === false); await loadAccounts() }
async function toggleUser(row: AdminSharedUser) {
  const enabled = !row.publish_enabled
  const reason = !enabled ? (window.prompt('请输入禁止发布原因（可选）', row.block_reason || '') || undefined) : undefined
  await setSharedUserPublishPermission(row.user_id, enabled, reason)
  await loadUsers()
}
watch(tabKey, value => value === 'accounts' ? loadAccounts() : loadUsers()); onMounted(loadAccounts)
</script>
