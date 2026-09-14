<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <div class="flex-1 sm:max-w-72"><input v-model="search" class="input" :placeholder="tabKey === 'accounts' ? '搜索账号或上传用户' : tabKey === 'users' ? '搜索用户' : '搜索用户、账号或模型'" @keyup.enter="handleSearchSubmit" /></div>
          <Select v-if="tabKey === 'accounts'" v-model="status" class="w-36" :options="[{ value: '', label: '全部状态' }, { value: 'active', label: '运行中' }, { value: 'paused', label: '已暂停' }, { value: 'suspended', label: '已禁用' }, { value: 'invalid', label: '不可用' }]" @change="loadAccounts" />
          <Select v-else-if="tabKey === 'users'" v-model="publishFilter" class="w-36" :options="[{ value: '', label: '全部权限' }, { value: 'true', label: '允许发布' }, { value: 'false', label: '已禁止' }]" @change="handleUserFilterChange" />
          <button v-if="tabKey === 'revenue' && revenueOwnerId" class="text-xs text-primary-600 hover:underline" @click="clearRevenueOwnerFilter">已按用户筛选 · 清除</button>
          <button v-if="tabKey === 'records' && recordOwnerId" class="text-xs text-primary-600 hover:underline" @click="clearRecordOwnerFilter">已按用户筛选 · 清除</button>
          <div class="ml-auto flex gap-2"><RouterLink v-if="tabKey === 'records'" class="btn btn-secondary" to="/admin/usage?shared_only=true">查看全部共享请求</RouterLink><button class="btn btn-secondary" :disabled="loading || loadingUsers || loadingRevenue || loadingRecords" @click="refreshCurrent"><Icon name="refresh" size="md" :class="loading || loadingUsers || loadingRevenue || loadingRecords ? 'animate-spin' : ''" /></button></div>
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
          <template #cell-actions="{ row }"><div class="flex gap-2"><button class="btn btn-secondary btn-sm" @click="openEdit(row)">编辑</button><button class="btn btn-secondary btn-sm" @click="openTest(row)">测试</button><button class="btn btn-secondary btn-sm" @click="toggleAccount(row)">{{ row.status === 'suspended' ? '恢复' : '禁用' }}</button><button class="btn btn-secondary btn-sm" @click="toggleListed(row)">{{ row.listed === false ? '上架' : '下架' }}</button><button class="btn btn-secondary btn-sm text-red-600" @click="deleteAccount(row)">删除</button></div></template>
        </DataTable>
        <DataTable v-else-if="tabKey === 'users'" :columns="userColumns" :data="users" :loading="loadingUsers">
          <template #cell-user="{ row }"><div class="font-medium">{{ row.username || row.email || `用户 #${row.user_id}` }}</div><div class="text-xs text-gray-500">{{ row.shared_account_count ?? 0 }} 个共享账号</div></template>
          <template #cell-permission="{ row }"><span class="badge" :class="row.publish_enabled ? 'badge-success' : 'badge-warning'">{{ row.publish_enabled ? '允许发布' : '已禁止' }}</span></template>
          <template #cell-actions="{ row }"><div class="flex gap-2"><button class="btn btn-secondary btn-sm" @click="viewUserRevenue(row)">查看收益</button><button class="btn btn-secondary btn-sm" @click="toggleUser(row)">{{ row.publish_enabled ? '禁止发布' : '恢复发布' }}</button></div></template>
        </DataTable>
        <DataTable v-else-if="tabKey === 'revenue'" :columns="revenueColumns" :data="revenue" :loading="loadingRevenue">
          <template #cell-user="{ row }"><div class="font-medium">{{ row.username || row.email || `用户 #${row.user_id}` }}</div><div class="text-xs text-gray-500">{{ row.listing_count }} 个账号 · {{ row.request_count }} 次请求</div></template>
          <template #cell-amounts="{ row }"><div class="text-emerald-600">收益 {{ row.owner_amount }}</div><div class="text-xs text-gray-500">消费 {{ row.gross_amount }} · 抽成 {{ row.platform_fee }}</div></template>
          <template #cell-wallet="{ row }"><div>可提 {{ row.available }}</div><div class="text-xs text-gray-500">冻结 {{ row.pending }}</div></template>
          <template #cell-actions="{ row }"><button class="btn btn-secondary btn-sm" @click="viewUserRecords(row)">查看请求</button></template>
        </DataTable>
        <DataTable v-else :columns="recordColumns" :data="records" :loading="loadingRecords">
          <template #cell-user="{ row }"><div class="font-medium">{{ row.owner_name || row.owner_email || `用户 #${row.owner_user_id}` }}</div><div class="text-xs text-gray-500">消费者 #{{ row.consumer_user_id }}</div></template>
          <template #cell-account="{ row }"><div>{{ row.listing_name }}</div><div class="text-xs text-gray-500">{{ row.platform }} · {{ row.model || '—' }}</div></template>
          <template #cell-amount="{ row }"><div class="text-emerald-600">{{ row.owner_amount }}</div><div class="text-xs text-gray-500">消费 {{ row.gross_cost }} · 抽成 {{ row.platform_fee }}</div></template>
          <template #cell-result="{ row }"><div>{{ row.result_status || '已结算' }}</div><div class="text-xs text-gray-500">{{ row.duration_ms ? `${row.duration_ms}ms` : '—' }}</div></template>
        </DataTable>
      </template>
      <template #pagination>
        <Pagination
          v-if="tabKey !== 'accounts' && paginationState.total > 0"
          :page="paginationState.page"
          :total="paginationState.total"
          :page-size="paginationState.pageSize"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>
    <EditAccountModal :show="showEdit" :shared-pool="true" :shared-pool-admin="true" :shared-listing-id="editingListingId" :account="editingAccount" :proxies="[]" :groups="[]" @close="closeEdit" @updated="closeEdit; loadAccounts()" />
    <AccountTestModal :show="showTest" :shared-pool="true" :shared-pool-admin="true" :shared-listing-id="testingListingId" :account="testingAccount" @close="showTest = false; testingAccount = null" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import EditAccountModal from '@/components/account/EditAccountModal.vue'
import AccountTestModal from '@/components/admin/account/AccountTestModal.vue'
import { listSharedAccounts, listSharedUsers, listSharedRevenue, listSharedRevenueRecords, setSharedAccountListed, setSharedAccountStatus, deleteSharedAccount, getSharedAccount, setSharedUserPublishPermission, type AdminSharedAccount, type AdminSharedUser, type AdminSharedRevenueSummary, type AdminSharedRevenueRecord } from '@/api/admin/sharedPool'
import type { Account } from '@/types'

const tabs = [{ key: 'accounts', label: '共享账号' }, { key: 'users', label: '发布权限' }, { key: 'revenue', label: '用户收益' }, { key: 'records', label: '请求记录' }]
const tabKey = ref('accounts'); const search = ref(''); const status = ref(''); const publishFilter = ref(''); const loading = ref(false); const loadingUsers = ref(false); const loadingRevenue = ref(false); const loadingRecords = ref(false)
const accounts = ref<AdminSharedAccount[]>([]); const users = ref<AdminSharedUser[]>([]); const revenue = ref<AdminSharedRevenueSummary[]>([]); const records = ref<AdminSharedRevenueRecord[]>([])
const showEdit = ref(false); const editingAccount = ref<Account | null>(null); const editingListingId = ref<number | undefined>()
const showTest = ref(false); const testingAccount = ref<Account | null>(null); const testingListingId = ref<number | undefined>()
const userPagination = ref({ page: 1, pageSize: 20, total: 0 })
const revenuePagination = ref({ page: 1, pageSize: 20, total: 0 })
const recordPagination = ref({ page: 1, pageSize: 20, total: 0 })
const revenueOwnerId = ref<number | undefined>()
const recordOwnerId = ref<number | undefined>()
const paginationState = computed(() => tabKey.value === 'users' ? userPagination.value : tabKey.value === 'revenue' ? revenuePagination.value : recordPagination.value)
function averageLatency(row: AdminSharedAccount) {
  const values = (row.recent_calls || []).map(call => Number(call.duration_ms || 0)).filter(value => value > 0)
  return values.length ? Math.round(values.reduce((sum, value) => sum + value, 0) / values.length) : 0
}
const accountColumns = [{ key: 'name', label: '账号' }, { key: 'uploader', label: '上传用户' }, { key: 'status', label: '状态' }, { key: 'metrics', label: '使用情况' }, { key: 'actions', label: '操作' }]
const userColumns = [{ key: 'user', label: '用户' }, { key: 'permission', label: '发布权限' }, { key: 'actions', label: '操作' }]
const revenueColumns = [{ key: 'user', label: '发布用户' }, { key: 'amounts', label: '收益汇总' }, { key: 'wallet', label: '钱包状态' }, { key: 'last_request_at', label: '最近请求' }, { key: 'actions', label: '操作' }]
const recordColumns = [{ key: 'user', label: '用户' }, { key: 'account', label: '共享账号' }, { key: 'amount', label: '结算金额' }, { key: 'result', label: '结果' }, { key: 'created_at', label: '时间' }]
async function loadAccounts() { loading.value = true; try { const result = await listSharedAccounts({ search: search.value || undefined, status: status.value || undefined }); accounts.value = result.items || [] } finally { loading.value = false } }
async function loadUsers() {
  loadingUsers.value = true
  try {
    const result = await listSharedUsers({
      search: search.value || undefined,
      page: userPagination.value.page,
      page_size: userPagination.value.pageSize,
      publish_enabled: publishFilter.value || undefined
    })
    users.value = result.items || []
    userPagination.value.total = result.total || 0
    if (result.page) userPagination.value.page = result.page
    if (result.page_size) userPagination.value.pageSize = result.page_size
  } finally { loadingUsers.value = false }
}
async function loadRevenue() {
  loadingRevenue.value = true
  try { const result = await listSharedRevenue({ search: search.value || undefined, owner_id: revenueOwnerId.value, page: revenuePagination.value.page, page_size: revenuePagination.value.pageSize }); revenue.value = result.items || []; revenuePagination.value.total = result.total || 0 } finally { loadingRevenue.value = false }
}
async function loadRecords() {
  loadingRecords.value = true
  try { const result = await listSharedRevenueRecords({ search: search.value || undefined, owner_id: recordOwnerId.value, page: recordPagination.value.page, page_size: recordPagination.value.pageSize }); records.value = result.items || []; recordPagination.value.total = result.total || 0 } finally { loadingRecords.value = false }
}
function refreshCurrent() { if (tabKey.value === 'accounts') return loadAccounts(); if (tabKey.value === 'users') return loadUsers(); if (tabKey.value === 'revenue') return loadRevenue(); return loadRecords() }
function handleUserFilterChange() { userPagination.value.page = 1; loadUsers() }
function viewUserRevenue(row: AdminSharedUser) { revenueOwnerId.value = row.user_id; revenuePagination.value.page = 1; tabKey.value = 'revenue' }
function viewUserRecords(row: AdminSharedRevenueSummary) { recordOwnerId.value = row.user_id; recordPagination.value.page = 1; tabKey.value = 'records' }
function clearRevenueOwnerFilter() { revenueOwnerId.value = undefined; revenuePagination.value.page = 1; loadRevenue() }
function clearRecordOwnerFilter() { recordOwnerId.value = undefined; recordPagination.value.page = 1; loadRecords() }
function handleSearchSubmit() { if (tabKey.value === 'users') { userPagination.value.page = 1; return loadUsers() } if (tabKey.value === 'revenue') { revenuePagination.value.page = 1; return loadRevenue() } if (tabKey.value === 'records') { recordPagination.value.page = 1; return loadRecords() } return loadAccounts() }
function handleUserPageChange(page: number) { userPagination.value.page = page; loadUsers() }
function handleUserPageSizeChange(pageSize: number) { userPagination.value.pageSize = pageSize; userPagination.value.page = 1; loadUsers() }
function handlePageChange(page: number) { if (tabKey.value === 'users') return handleUserPageChange(page); if (tabKey.value === 'revenue') { revenuePagination.value.page = page; return loadRevenue() } recordPagination.value.page = page; return loadRecords() }
function handlePageSizeChange(pageSize: number) { if (tabKey.value === 'users') return handleUserPageSizeChange(pageSize); if (tabKey.value === 'revenue') { revenuePagination.value.pageSize = pageSize; revenuePagination.value.page = 1; return loadRevenue() } recordPagination.value.pageSize = pageSize; recordPagination.value.page = 1; return loadRecords() }
async function toggleAccount(row: AdminSharedAccount) {
  const suspending = row.status !== 'suspended'
  await setSharedAccountStatus(row.id, suspending ? 'suspended' : 'active')
  await loadAccounts()
}
async function toggleListed(row: AdminSharedAccount) { await setSharedAccountListed(row.id, row.listed === false); await loadAccounts() }
async function deleteAccount(row: AdminSharedAccount) {
  if (!window.confirm('确定删除这个共享账号吗？删除后将立即从共享池下线，历史使用记录会保留。')) return
  await deleteSharedAccount(row.id)
  await loadAccounts()
}
async function openEdit(row: AdminSharedAccount) {
  try {
    editingAccount.value = await getSharedAccount(row.id)
    editingListingId.value = row.id
    showEdit.value = true
  } catch (error) { window.alert(error instanceof Error ? error.message : '无法读取账号详情') }
}
async function openTest(row: AdminSharedAccount) {
  try {
    testingAccount.value = await getSharedAccount(row.id)
    testingListingId.value = row.id
    showTest.value = true
  } catch (error) { window.alert(error instanceof Error ? error.message : '无法读取账号详情') }
}
function closeEdit() { showEdit.value = false; editingAccount.value = null; editingListingId.value = undefined }
async function toggleUser(row: AdminSharedUser) {
  const enabled = !row.publish_enabled
  const reason = !enabled ? (window.prompt('请输入禁止发布原因（可选）', row.block_reason || '') || undefined) : undefined
  await setSharedUserPublishPermission(row.user_id, enabled, reason)
  await loadUsers()
}
watch(tabKey, value => value === 'accounts' ? loadAccounts() : value === 'users' ? loadUsers() : value === 'revenue' ? loadRevenue() : loadRecords()); onMounted(loadAccounts)
</script>
